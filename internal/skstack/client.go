package skstack

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// Default handshake timing. The Perl original has NO timeout at all on this
// path — a wedged dongle at boot hangs the process forever with no
// supervisor restart. Every step here is bounded so a stuck handshake
// instead returns an error, letting the caller exit and systemd restart the
// container. Kept as Client fields (not consts) so tests can shrink them.
const (
	defaultHandshakeStepTimeout = 10 * time.Second
	defaultScanStepTimeout      = 20 * time.Second
	defaultMaxScanAttempts      = 5
	defaultJoinAckTimeout       = 10 * time.Second
	defaultPANAEstablishTimeout = 60 * time.Second

	// defaultUDPResponseTimeout mirrors SKSock.pm's UDP_TIMEOUT: armed on
	// every SendUDP, cleared by any bare "OK" or ERXUDP line — which in
	// practice means it's cleared by the SKSENDTO command's own immediate
	// ack within milliseconds, not by the meter's actual (much later)
	// reply. It rarely fires; the independent watchdog in the app layer is
	// the real lost-meter detector. Ported as-is for parity, not as the
	// primary safety net.
	defaultUDPResponseTimeout = 50 * time.Second

	timeoutCheckInterval = 250 * time.Millisecond
)

var errUDPResponseTimeout = errors.New("skstack: no response to outstanding UDP send within timeout")

// errWOPTProvisioned is returned by doROPT when WOPT was just written for
// the first time this session. Whether that NVRAM write takes effect
// immediately or only after the next SKRESET is undocumented, so rather
// than gamble on this session's ERXUDP encoding, Connect treats it as a
// restart trigger — the caller exits, and the next session's own
// SKRESET+ROPT confirms hex-ASCII is genuinely active before any ERXUDP
// is ever parsed. Distinct from a WOPT failure so logs/tests can tell
// "just provisioned, restarting to confirm" apart from a real error.
var errWOPTProvisioned = errors.New("skstack: WOPT provisioned dongle for hex-ASCII mode; restarting to ensure it's active this session")

// Client drives one BP35A1 dongle through its SKSTACK command protocol.
type Client struct {
	tr     Transport
	id     string
	pass   string
	logger *slog.Logger

	lines   chan string
	readErr chan error

	erxudpCh chan ERXUDPMessage

	mu        sync.Mutex
	connected bool
	ipv6      string

	timeoutMu  sync.Mutex
	timeoutAt  time.Time
	udpTimeout time.Duration

	// Handshake step timeouts, overridable by tests in this package.
	handshakeStepTimeout time.Duration
	scanStepTimeout      time.Duration
	maxScanAttempts      int
	joinAckTimeout       time.Duration
	panaEstablishTimeout time.Duration
}

// NewClient starts reading tr in the background. id/pass are the B-route
// credentials. logger may be nil (defaults to slog.Default()).
func NewClient(tr Transport, id, pass string, logger *slog.Logger) *Client {
	if logger == nil {
		logger = slog.Default()
	}
	c := &Client{
		tr:                   tr,
		id:                   id,
		pass:                 pass,
		logger:               logger,
		lines:                make(chan string, 32),
		readErr:              make(chan error, 1),
		erxudpCh:             make(chan ERXUDPMessage, 8),
		udpTimeout:           defaultUDPResponseTimeout,
		handshakeStepTimeout: defaultHandshakeStepTimeout,
		scanStepTimeout:      defaultScanStepTimeout,
		maxScanAttempts:      defaultMaxScanAttempts,
		joinAckTimeout:       defaultJoinAckTimeout,
		panaEstablishTimeout: defaultPANAEstablishTimeout,
	}
	go c.readLoop()
	return c
}

// ERXUDP delivers received UDP datagrams once the session is established.
func (c *Client) ERXUDP() <-chan ERXUDPMessage {
	return c.erxudpCh
}

func (c *Client) readLoop() {
	defer close(c.lines)
	var buf []byte
	tmp := make([]byte, 4096)
	for {
		n, err := c.tr.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			for {
				line, rest, ok := splitLine(buf)
				if !ok {
					break
				}
				buf = rest
				c.logger.Debug("skstack: raw line from device", "line", line)
				c.lines <- line
			}
		}
		if err != nil {
			c.readErr <- err
			return
		}
	}
}

// splitLine extracts one line from the front of buf, terminated by
// "\r\n", a bare "\r", or a bare "\n" — matching SKSock.pm's own
// normalization (it regex-replaces any of the three with "\n" before
// splitting). Most SKSTACK responses are CRLF-terminated, but at least
// one (ROPT's reply, confirmed via the command reference) is documented
// as CR-only; splitting on "\n" alone would leave such a reply stuck in
// the buffer forever, indistinguishable from the device never answering.
func splitLine(buf []byte) (line string, rest []byte, ok bool) {
	for i, b := range buf {
		switch b {
		case '\n':
			return string(buf[:i]), buf[i+1:], true
		case '\r':
			if i+1 < len(buf) && buf[i+1] == '\n' {
				return string(buf[:i]), buf[i+2:], true
			}
			return string(buf[:i]), buf[i+1:], true
		}
	}
	return "", buf, false
}

// nextLine blocks for the next line from the device, or returns ctx's error,
// or the terminal read error once the transport is closed/broken.
func (c *Client) nextLine(ctx context.Context) (string, error) {
	select {
	case line, ok := <-c.lines:
		if !ok {
			select {
			case err := <-c.readErr:
				return "", err
			default:
				return "", io.ErrClosedPipe
			}
		}
		return line, nil
	case err := <-c.readErr:
		return "", err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// sendCmd writes a command line, terminated per the SKSTACK command
// reference's own distinction: "product setting" commands (ROPT/WOPT) are
// CR-only; every other ("SK...") command is CRLF.
func (c *Client) sendCmd(mode string, args ...string) error {
	cmd := mode
	for _, a := range args {
		cmd += " " + a
	}
	terminator := "\r\n"
	if mode == "ROPT" || mode == "WOPT" {
		terminator = "\r"
	}
	_, err := c.tr.Write([]byte(cmd + terminator))
	return err
}

// expectOK reads lines until a bare "OK" (success), a "FAIL..." line
// (error), or ctx expires. Anything else is logged and skipped, matching
// SKSock.pm's tolerant dispatch.
func (c *Client) expectOK(ctx context.Context) error {
	for {
		line, err := c.nextLine(ctx)
		if err != nil {
			return err
		}
		switch {
		case line == "OK":
			return nil
		case strings.HasPrefix(line, "FAIL"):
			return fmt.Errorf("device reported failure: %s", line)
		default:
			c.logger.Debug("skstack: ignoring line while awaiting OK", "line", line)
		}
	}
}

func (c *Client) step(ctx context.Context, timeout time.Duration, mode string, args ...string) error {
	if err := c.sendCmd(mode, args...); err != nil {
		return fmt.Errorf("send %s: %w", mode, err)
	}
	stepCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := c.expectOK(stepCtx); err != nil {
		return fmt.Errorf("%s: %w", mode, err)
	}
	return nil
}

// Connect runs the full SKVER..SKJOIN handshake and blocks until the PANA
// session with the meter is established, or ctx/step timeouts expire.
func (c *Client) Connect(ctx context.Context) error {
	steps := []struct {
		mode string
		args []string
	}{
		{"SKVER", nil},
		{"SKINFO", nil},
		{"SKRESET", nil},
		{"SKSREG", []string{"SFE", "0"}},
	}
	for _, s := range steps {
		if err := c.step(ctx, c.handshakeStepTimeout, s.mode, s.args...); err != nil {
			return err
		}
	}

	if err := c.doROPT(ctx); err != nil {
		return err
	}

	if err := c.step(ctx, c.handshakeStepTimeout, "SKSETPWD", "C", c.pass); err != nil {
		return err
	}
	if err := c.step(ctx, c.handshakeStepTimeout, "SKSETRBID", c.id); err != nil {
		return err
	}

	peer, err := c.scan(ctx)
	if err != nil {
		return err
	}

	if err := c.step(ctx, c.handshakeStepTimeout, "SKSREG", "S2", peer.Channel); err != nil {
		return err
	}
	if err := c.step(ctx, c.handshakeStepTimeout, "SKSREG", "S3", peer.PanID); err != nil {
		return err
	}

	ipv6, err := c.ll64(ctx, peer.Addr)
	if err != nil {
		return err
	}

	if err := c.join(ctx, ipv6); err != nil {
		return err
	}

	c.mu.Lock()
	c.connected = true
	c.ipv6 = ipv6
	c.mu.Unlock()
	return nil
}

// doROPT checks whether the dongle reports ERXUDP payloads as hex-ASCII
// text or raw binary bytes embedded in the line, and fixes it via WOPT if
// needed. This gate matters a lot more than the ROPT/WOPT commands alone
// suggest: SKSTACK is a line-oriented protocol, and a binary ERXUDP
// payload can contain arbitrary bytes — including literal CR/LF — that
// would corrupt splitLine's line framing itself, not just this one
// datagram. BP35A1's factory default is binary mode, so a dongle
// answering "OK 00" here is the normal first-boot case, not an edge case.
//
// A reply of exactly "OK 00" means binary mode is currently active, so
// WOPT 01 must be sent to switch to hex-ASCII; any other reply means
// hex-ASCII is already active. WOPT writes NVRAM, so it must never be
// called speculatively — only when ROPT has explicitly confirmed it's
// needed.
//
// Unlike the original (pre-fix) assumption, a ROPT timeout/failure is now
// treated as fatal, not "proceed assuming hex-ASCII": since binary is the
// factory default, an unconfirmed ROPT can't be trusted to mean
// hex-ASCII any more than a confirmed-binary reply can — both get
// refused with the same decisiveness, funneled through the same fatal
// handshake-error path as every other step.
//
// Whether a same-session WOPT write is active immediately or only after
// the next SKRESET is undocumented, so a successful WOPT does not
// continue the handshake either — see errWOPTProvisioned.
//
// Per the SKSTACK command reference, ROPT/WOPT ("product setting"
// commands) reply CR-terminated, unlike every "SK..." command's CRLF —
// sendCmd already accounts for this on the way out.
func (c *Client) doROPT(ctx context.Context) error {
	if err := c.sendCmd("ROPT"); err != nil {
		return fmt.Errorf("ROPT: send: %w", err)
	}
	stepCtx, cancel := context.WithTimeout(ctx, c.handshakeStepTimeout)
	defer cancel()
	for {
		line, err := c.nextLine(stepCtx)
		if err != nil {
			return fmt.Errorf("ROPT: no response within %s: %w", c.handshakeStepTimeout, err)
		}
		if strings.HasPrefix(line, "FAIL") {
			return fmt.Errorf("ROPT failed: %s", line)
		}
		if !strings.HasPrefix(line, "OK") {
			c.logger.Debug("skstack: ignoring line while awaiting ROPT response", "line", line)
			continue
		}
		if line == "OK 00" {
			c.logger.Info("skstack: ROPT reports binary mode, enabling hex-ASCII via WOPT (writes NVRAM)")
			if err := c.step(ctx, c.handshakeStepTimeout, "WOPT", "01"); err != nil {
				return fmt.Errorf("WOPT: %w", err)
			}
			c.logger.Warn("skstack: WOPT provisioned dongle for hex-ASCII mode; restarting to ensure it's active this session")
			return errWOPTProvisioned
		}
		c.logger.Debug("skstack: ROPT reports hex-ASCII already active, not calling WOPT", "line", line)
		return nil
	}
}

// scan runs SKSCAN, retrying (bounded, unlike Perl's unbounded retry) until
// a peer is found or attempts are exhausted.
func (c *Client) scan(ctx context.Context) (peerInfo, error) {
	for attempt := 1; attempt <= c.maxScanAttempts; attempt++ {
		if err := c.sendCmd("SKSCAN", "2", "FFFFFFFF", "6"); err != nil {
			return peerInfo{}, fmt.Errorf("send SKSCAN: %w", err)
		}
		scanCtx, cancel := context.WithTimeout(ctx, c.scanStepTimeout)
		peer, found, err := c.readScanResults(scanCtx)
		cancel()
		if err != nil {
			return peerInfo{}, fmt.Errorf("SKSCAN (attempt %d/%d): %w", attempt, c.maxScanAttempts, err)
		}
		if found {
			c.logger.Info("skstack: found peer via SKSCAN",
				"channel", peer.Channel, "channel_page", peer.ChannelPage,
				"pan_id", peer.PanID, "addr", peer.Addr, "lqi", peer.LQI, "pair_id", peer.PairID)
			return peer, nil
		}
		c.logger.Info("skstack: scan found no peer, retrying", "attempt", attempt)
	}
	return peerInfo{}, fmt.Errorf("SKSCAN: no peer found after %d attempts", c.maxScanAttempts)
}

func (c *Client) readScanResults(ctx context.Context) (peerInfo, bool, error) {
	var peer peerInfo
	found := false
	for {
		line, err := c.nextLine(ctx)
		if err != nil {
			return peerInfo{}, false, err
		}
		switch {
		case line == "EPANDESC":
			peer = peerInfo{}
			found = true
		case strings.HasPrefix(line, "FAIL"):
			return peerInfo{}, false, fmt.Errorf("device reported failure: %s", line)
		default:
			if id, _, ok := parseEventLine(line); ok {
				if id == eventActiveScanComplete {
					return peer, found, nil
				}
				continue
			}
			if found {
				parsePeerField(&peer, line)
			}
		}
	}
}

// ll64 sends SKLL64 and waits for the resulting 39-character IPv6 address
// line (not an "OK" — SKSock.pm special-cases this by line length).
func (c *Client) ll64(ctx context.Context, addr string) (string, error) {
	if err := c.sendCmd("SKLL64", addr); err != nil {
		return "", fmt.Errorf("send SKLL64: %w", err)
	}
	stepCtx, cancel := context.WithTimeout(ctx, c.handshakeStepTimeout)
	defer cancel()
	for {
		line, err := c.nextLine(stepCtx)
		if err != nil {
			return "", fmt.Errorf("SKLL64: %w", err)
		}
		if strings.HasPrefix(line, "FAIL") {
			return "", fmt.Errorf("SKLL64 failed: %s", line)
		}
		if len(line) == 39 {
			return line, nil
		}
	}
}

// join sends SKJOIN, waits for its command ack, then waits for EVENT 25
// (PANA established) or a terminal failure event.
func (c *Client) join(ctx context.Context, ipv6 string) error {
	if err := c.sendCmd("SKJOIN", ipv6); err != nil {
		return fmt.Errorf("send SKJOIN: %w", err)
	}
	ackCtx, cancel := context.WithTimeout(ctx, c.joinAckTimeout)
	err := c.expectOK(ackCtx)
	cancel()
	if err != nil {
		return fmt.Errorf("SKJOIN: %w", err)
	}

	panaCtx, cancel2 := context.WithTimeout(ctx, c.panaEstablishTimeout)
	defer cancel2()
	for {
		line, err := c.nextLine(panaCtx)
		if err != nil {
			return fmt.Errorf("waiting for PANA connection: %w", err)
		}
		id, _, ok := parseEventLine(line)
		if !ok {
			continue
		}
		switch id {
		case eventPANAConnectionEstablished:
			return nil
		case eventSessionTerminating, eventPANAGracefullyTerminated,
			eventPANATerminationTimeout, eventPANASessionTimeout:
			return fmt.Errorf("PANA connection failed (EVENT %d): %s", id, line)
		}
	}
}

// Run processes lines after a successful Connect: delivering ERXUDP
// datagrams on ERXUDP(), and returning a non-nil error the moment any
// fatal condition is observed (read failure, bare FAIL, a session-ending
// EVENT, or the UDP-response watchdog). It never attempts an in-process
// reconnect — that reconnect-from-inside-a-callback behavior is exactly
// what made the Perl original's timeout/restart design silently not work.
// The caller is expected to treat a returned error as fatal: send a
// best-effort Terminate, clean up, and exit the process so the container
// supervisor restarts it.
func (c *Client) Run(ctx context.Context) error {
	ticker := time.NewTicker(timeoutCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if c.checkTimeout() {
				return errUDPResponseTimeout
			}
		case line, ok := <-c.lines:
			if !ok {
				select {
				case err := <-c.readErr:
					return fmt.Errorf("serial read: %w", err)
				default:
					return fmt.Errorf("serial read: %w", io.ErrClosedPipe)
				}
			}
			if err := c.dispatch(line); err != nil {
				return err
			}
		}
	}
}

func (c *Client) dispatch(line string) error {
	switch {
	case strings.HasPrefix(line, "ERXUDP"):
		msg, err := parseERXUDP(line)
		if err != nil {
			c.logger.Warn("skstack: malformed ERXUDP line, ignoring", "err", err, "line", line)
			return nil
		}
		c.clearTimeout()
		select {
		case c.erxudpCh <- msg:
		default:
			c.logger.Warn("skstack: ERXUDP consumer not keeping up, dropping datagram")
		}
	case strings.HasPrefix(line, "EVENT"):
		id, _, ok := parseEventLine(line)
		if !ok {
			return nil
		}
		switch id {
		case eventSessionTerminating, eventPANAGracefullyTerminated,
			eventPANATerminationTimeout, eventPANASessionTimeout:
			return fmt.Errorf("PANA session ended (EVENT %d): %s", id, line)
		default:
			c.logger.Debug("skstack: event", "line", line)
		}
	case strings.HasPrefix(line, "FAIL"):
		return fmt.Errorf("device reported failure: %s", line)
	case line == "OK":
		c.clearTimeout()
	default:
		c.logger.Debug("skstack: unrecognized line", "line", line)
	}
	return nil
}

// SendUDP transmits payload to the connected meter via SKSENDTO. It is a
// silent no-op if the session isn't established yet, matching SKSock.pm's
// send_udp guard.
func (c *Client) SendUDP(payload []byte) error {
	c.mu.Lock()
	connected := c.connected
	ipv6 := c.ipv6
	c.mu.Unlock()
	if !connected {
		return nil
	}

	// SKSENDTO's DATA is raw binary immediately following the ASCII header
	// (the device reads exactly the declared length) — unlike every other
	// SKSTACK command, it is NOT hex-encoded and has NO trailing CRLF.
	header := fmt.Sprintf("SKSENDTO 1 %s 0E1A 1 %04X ", ipv6, len(payload))
	buf := append([]byte(header), payload...)
	if _, err := c.tr.Write(buf); err != nil {
		return fmt.Errorf("SKSENDTO: %w", err)
	}
	c.armTimeout()
	return nil
}

// Terminate sends SKTERM best-effort, without waiting for a reply —
// matching SKSock.pm's terminate(). Callers use this as part of a fatal
// shutdown sequence, not as a request to keep the session alive.
func (c *Client) Terminate() {
	if err := c.sendCmd("SKTERM"); err != nil {
		c.logger.Warn("skstack: failed to send SKTERM during shutdown", "err", err)
	}
}

func (c *Client) armTimeout() {
	c.timeoutMu.Lock()
	c.timeoutAt = time.Now().Add(c.udpTimeout)
	c.timeoutMu.Unlock()
}

func (c *Client) clearTimeout() {
	c.timeoutMu.Lock()
	c.timeoutAt = time.Time{}
	c.timeoutMu.Unlock()
}

func (c *Client) checkTimeout() bool {
	c.timeoutMu.Lock()
	defer c.timeoutMu.Unlock()
	if !c.timeoutAt.IsZero() && !time.Now().Before(c.timeoutAt) {
		c.timeoutAt = time.Time{}
		return true
	}
	return false
}

// hexEncodeUpper is a small helper kept here since both this package's
// tests and callers reason about the wire hex encoding of ERXUDP payloads.
func hexEncodeUpper(b []byte) string {
	return strings.ToUpper(hex.EncodeToString(b))
}
