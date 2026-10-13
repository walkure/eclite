package skstack

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

const (
	testID    = "0000009A0512368210005525010E3F50"
	testPass  = "TESTPASSWORD"
	testChan  = "21"
	testPanID = "8888"
	testAddr  = "001D129012345678"
	testIPv6  = "FE80:0000:0000:0000:021D:1290:1234:5678" // 39 chars, like SKLL64's real output
)

// happyPathSteps is the canonical full handshake transcript: SKVER through
// SKJOIN, ending with EVENT 25 (PANA established).
func happyPathSteps() []exchange {
	return []exchange{
		{expect: "SKVER", reply: []string{"OK"}},
		{expect: "SKINFO", reply: []string{"OK"}},
		{expect: "SKRESET", reply: []string{"OK"}},
		{expect: "SKSREG SFE 0", reply: []string{"OK"}},
		{expect: "ROPT", reply: []string{"OK 01"}},
		{expect: "SKSETPWD C " + testPass, reply: []string{"OK"}},
		{expect: "SKSETRBID " + testID, reply: []string{"OK"}},
		{expect: "SKSCAN 2 FFFFFFFF 6", reply: []string{
			"EPANDESC",
			"  Channel:" + testChan,
			"  Channel Page:09",
			"  Pan ID:" + testPanID,
			"  Addr:" + testAddr,
			"  LQI:C1",
			"  PairID:00112233",
			"EVENT 22",
		}},
		{expect: "SKSREG S2 " + testChan, reply: []string{"OK"}},
		{expect: "SKSREG S3 " + testPanID, reply: []string{"OK"}},
		{expect: "SKLL64 " + testAddr, reply: []string{testIPv6}},
		{expect: "SKJOIN " + testIPv6, reply: []string{"OK", "EVENT 25 " + testIPv6}},
	}
}

// happyPathStepsBP35C0 mirrors happyPathSteps but for a BP35C0-generation
// dongle: SKVER replies with "EVER 1.5.2" (crossing the 1.5 threshold in
// hasSideParam), so SKSCAN must carry the resulting <SIDE>=0 argument —
// proving the same client auto-adapts to both module generations purely
// from SKVER, without a config flag.
func happyPathStepsBP35C0() []exchange {
	return []exchange{
		{expect: "SKVER", reply: []string{"EVER 1.5.2", "OK"}},
		{expect: "SKINFO", reply: []string{"OK"}},
		{expect: "SKRESET", reply: []string{"OK"}},
		{expect: "SKSREG SFE 0", reply: []string{"OK"}},
		{expect: "ROPT", reply: []string{"OK 01"}},
		{expect: "SKSETPWD C " + testPass, reply: []string{"OK"}},
		{expect: "SKSETRBID " + testID, reply: []string{"OK"}},
		{expect: "SKSCAN 2 FFFFFFFF 6 0", reply: []string{
			"EPANDESC",
			"  Channel:" + testChan,
			"  Channel Page:09",
			"  Pan ID:" + testPanID,
			"  Addr:" + testAddr,
			"  LQI:C1",
			"  Side:0",
			"  PairID:00112233",
			"EVENT 22",
		}},
		{expect: "SKSREG S2 " + testChan, reply: []string{"OK"}},
		{expect: "SKSREG S3 " + testPanID, reply: []string{"OK"}},
		{expect: "SKLL64 " + testAddr, reply: []string{testIPv6}},
		{expect: "SKJOIN " + testIPv6, reply: []string{"OK", "EVENT 25 " + testIPv6}},
	}
}

// TestConnect_HappyPath_BP35C0 is the BP35C0-generation counterpart to
// TestConnect_HappyPath. scriptedTransport's exact-command assertion on
// every step means this fails loudly if Connect omits <SIDE> anywhere it
// belongs once a 1.5.x SKVER reply is seen.
func TestConnect_HappyPath_BP35C0(t *testing.T) {
	tr := newScriptedTransport(t, happyPathStepsBP35C0())
	c := newTestClient(tr)
	shrinkTimeouts(c)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if !tr.allStepsConsumed() {
		t.Fatalf("Connect returned success without consuming the full BP35C0 scripted transcript")
	}
	if !c.sideParam {
		t.Fatalf("sideParam = false after EVER 1.5.2, want true")
	}
}

// TestSendUDP_SideParam confirms SKSENDTO carries the <SIDE>=0 argument
// once Connect has detected a BP35C0-generation dongle. scriptedTransport
// asserts the exact bytes written, so this fails if SendUDP's BP35C0
// branch regresses to the no-SIDE header.
func TestSendUDP_SideParam(t *testing.T) {
	payload := []byte{0x10, 0x81}
	wantWrite := fmt.Sprintf("SKSENDTO 1 %s 0E1A 1 0 %04X ", testIPv6, len(payload)) + string(payload)
	steps := append(happyPathStepsBP35C0(), exchange{expect: wantWrite})
	tr := newScriptedTransport(t, steps)
	c := newTestClient(tr)
	shrinkTimeouts(c)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	if err := c.Connect(ctx); err != nil {
		cancel()
		t.Fatalf("Connect: %v", err)
	}
	cancel()

	if err := c.SendUDP(payload); err != nil {
		t.Fatalf("SendUDP: %v", err)
	}
	if !tr.allStepsConsumed() {
		t.Fatalf("SendUDP did not write the expected <SIDE>-bearing SKSENDTO header")
	}
}

func newTestClient(tr Transport) *Client {
	return NewClient(tr, testID, testPass, nil)
}

func shrinkTimeouts(c *Client) {
	c.handshakeStepTimeout = 200 * time.Millisecond
	c.scanStepTimeout = 200 * time.Millisecond
	c.joinAckTimeout = 200 * time.Millisecond
	c.panaEstablishTimeout = 200 * time.Millisecond
	c.udpTimeout = 150 * time.Millisecond
}

func TestConnect_HappyPath(t *testing.T) {
	tr := newScriptedTransport(t, happyPathSteps())
	c := newTestClient(tr)
	shrinkTimeouts(c)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if !tr.allStepsConsumed() {
		t.Fatalf("Connect returned success without consuming the full scripted transcript")
	}
	c.mu.Lock()
	connected, ipv6 := c.connected, c.ipv6
	c.mu.Unlock()
	if !connected {
		t.Fatalf("client not marked connected after successful handshake")
	}
	if ipv6 != testIPv6 {
		t.Fatalf("ipv6 = %q, want %q", ipv6, testIPv6)
	}
}

// TestConnect_ROPTAlreadyEnabled_SkipsWOPT: happyPathSteps scripts ROPT
// replying "OK 01" (already enabled). WOPT writes NVRAM, so it must only
// be called when ROPT explicitly reports "OK 00" — scriptedTransport has
// no WOPT step scripted here, so it would Fatal on an unexpected write if
// Connect called WOPT anyway.
func TestConnect_ROPTAlreadyEnabled_SkipsWOPT(t *testing.T) {
	tr := newScriptedTransport(t, happyPathSteps())
	c := newTestClient(tr)
	shrinkTimeouts(c)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if !tr.allStepsConsumed() {
		t.Fatalf("not all steps consumed")
	}
}

// TestConnect_ROPTNoResponse_IsFatal: a silent ROPT used to be treated as
// non-fatal (assume hex-ASCII, proceed) — that turned out to be masking a
// line-splitting bug (fixed, see TestSplitLine), not real firmware
// silence. Now that BP35A1's binary-mode-by-default is understood, an
// unconfirmed ROPT can't be trusted to mean hex-ASCII any more than a
// confirmed-binary reply can, so this is fatal too — and must still never
// trigger a speculative WOPT call.
func TestConnect_ROPTNoResponse_IsFatal(t *testing.T) {
	steps := []exchange{
		{expect: "SKVER", reply: []string{"OK"}},
		{expect: "SKINFO", reply: []string{"OK"}},
		{expect: "SKRESET", reply: []string{"OK"}},
		{expect: "SKSREG SFE 0", reply: []string{"OK"}},
		{expect: "ROPT", reply: nil}, // no reply at all, ever
	}
	tr := newScriptedTransport(t, steps)
	c := newTestClient(tr)
	shrinkTimeouts(c)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := c.Connect(ctx)
	if err == nil {
		t.Fatalf("Connect: want error on silent ROPT, got nil")
	}
	if !strings.Contains(err.Error(), "ROPT") {
		t.Fatalf("Connect error = %v, want it to mention ROPT", err)
	}
	// No steps past ROPT are scripted; if Connect had (incorrectly)
	// proceeded to SKSETPWD (or called WOPT), scriptedTransport would
	// have Fatal'd on the unexpected write.
	if !tr.allStepsConsumed() {
		t.Fatalf("not all steps consumed")
	}
}

func TestConnect_ROPTFails_IsFatal(t *testing.T) {
	steps := []exchange{
		{expect: "SKVER", reply: []string{"OK"}},
		{expect: "SKINFO", reply: []string{"OK"}},
		{expect: "SKRESET", reply: []string{"OK"}},
		{expect: "SKSREG SFE 0", reply: []string{"OK"}},
		{expect: "ROPT", reply: []string{"FAIL ER10"}},
	}
	tr := newScriptedTransport(t, steps)
	c := newTestClient(tr)
	shrinkTimeouts(c)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := c.Connect(ctx)
	if err == nil {
		t.Fatalf("Connect: want error on ROPT FAIL, got nil")
	}
	if !strings.Contains(err.Error(), "ER10") {
		t.Fatalf("Connect error = %v, want it to mention the FAIL line", err)
	}
}

// TestConnect_ROPTConfirmsBinary_WOPTSucceeds_TriggersRestart: BP35A1's
// factory default is binary ERXUDP mode, so ROPT replying "OK 00" is the
// normal first-boot case, not an edge case. doROPT self-heals it via
// WOPT, but since whether that NVRAM write is active for *this* session
// is undocumented, Connect must not proceed on the same handshake — it
// returns errWOPTProvisioned so the caller restarts and the next
// session's own SKRESET+ROPT confirms hex-ASCII before anything is
// parsed.
func TestConnect_ROPTConfirmsBinary_WOPTSucceeds_TriggersRestart(t *testing.T) {
	steps := []exchange{
		{expect: "SKVER", reply: []string{"OK"}},
		{expect: "SKINFO", reply: []string{"OK"}},
		{expect: "SKRESET", reply: []string{"OK"}},
		{expect: "SKSREG SFE 0", reply: []string{"OK"}},
		{expect: "ROPT", reply: []string{"OK 00"}},
		{expect: "WOPT 01", reply: []string{"OK"}},
	}
	tr := newScriptedTransport(t, steps)
	c := newTestClient(tr)
	shrinkTimeouts(c)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := c.Connect(ctx)
	if !errors.Is(err, errWOPTProvisioned) {
		t.Fatalf("Connect error = %v, want errWOPTProvisioned", err)
	}
	// No steps past WOPT are scripted; if Connect had (incorrectly)
	// proceeded to SKSETPWD, scriptedTransport would have Fatal'd.
	if !tr.allStepsConsumed() {
		t.Fatalf("not all steps consumed")
	}
}

func TestConnect_ROPTConfirmsBinary_WOPTFails_IsFatal(t *testing.T) {
	steps := []exchange{
		{expect: "SKVER", reply: []string{"OK"}},
		{expect: "SKINFO", reply: []string{"OK"}},
		{expect: "SKRESET", reply: []string{"OK"}},
		{expect: "SKSREG SFE 0", reply: []string{"OK"}},
		{expect: "ROPT", reply: []string{"OK 00"}},
		{expect: "WOPT 01", reply: []string{"FAIL ER10"}},
	}
	tr := newScriptedTransport(t, steps)
	c := newTestClient(tr)
	shrinkTimeouts(c)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := c.Connect(ctx)
	if err == nil {
		t.Fatalf("Connect: want error on WOPT failure, got nil")
	}
	if errors.Is(err, errWOPTProvisioned) {
		t.Fatalf("Connect error = %v, want a real WOPT failure, not errWOPTProvisioned", err)
	}
	if !strings.Contains(err.Error(), "WOPT") {
		t.Fatalf("Connect error = %v, want it to mention WOPT", err)
	}
}

func TestConnect_FailOutsideSKTERM(t *testing.T) {
	steps := []exchange{
		{expect: "SKVER", reply: []string{"OK"}},
		{expect: "SKINFO", reply: []string{"FAIL ER04"}},
	}
	tr := newScriptedTransport(t, steps)
	c := newTestClient(tr)
	shrinkTimeouts(c)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := c.Connect(ctx)
	if err == nil {
		t.Fatalf("Connect: want error on FAIL, got nil")
	}
	if !strings.Contains(err.Error(), "FAIL ER04") {
		t.Fatalf("Connect error = %v, want it to mention the FAIL line", err)
	}
}

func TestConnect_ScanRetryExceeded(t *testing.T) {
	var steps []exchange
	steps = append(steps,
		exchange{expect: "SKVER", reply: []string{"OK"}},
		exchange{expect: "SKINFO", reply: []string{"OK"}},
		exchange{expect: "SKRESET", reply: []string{"OK"}},
		exchange{expect: "SKSREG SFE 0", reply: []string{"OK"}},
		exchange{expect: "ROPT", reply: []string{"OK 01"}},
		exchange{expect: "SKSETPWD C " + testPass, reply: []string{"OK"}},
		exchange{expect: "SKSETRBID " + testID, reply: []string{"OK"}},
	)
	for i := 0; i < defaultMaxScanAttempts; i++ {
		// no EPANDESC block — scan finds nothing, every attempt.
		steps = append(steps, exchange{expect: "SKSCAN 2 FFFFFFFF 6", reply: []string{"EVENT 22"}})
	}
	tr := newScriptedTransport(t, steps)
	c := newTestClient(tr)
	shrinkTimeouts(c)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := c.Connect(ctx)
	if err == nil {
		t.Fatalf("Connect: want error after exhausting scan attempts, got nil")
	}
	if !strings.Contains(err.Error(), "no peer found") {
		t.Fatalf("Connect error = %v, want it to mention scan exhaustion", err)
	}
	if !tr.allStepsConsumed() {
		t.Fatalf("expected all %d scan attempts to be used", defaultMaxScanAttempts)
	}
}

func TestConnect_SessionEventDuringHandshake(t *testing.T) {
	steps := happyPathSteps()
	// Replace the final SKJOIN reply: instead of EVENT 25, the session
	// ends (EVENT 27). The client must surface this as an error and must
	// NOT attempt any in-process reconnect (SKTERM/SKRESET) — scriptedTransport
	// has no further steps scripted, so any extra write would fail the test.
	steps[len(steps)-1] = exchange{
		expect: "SKJOIN " + testIPv6,
		reply:  []string{"OK", "EVENT 27 " + testIPv6},
	}
	tr := newScriptedTransport(t, steps)
	c := newTestClient(tr)
	shrinkTimeouts(c)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := c.Connect(ctx)
	if err == nil {
		t.Fatalf("Connect: want error when session ends mid-handshake, got nil")
	}
	if !strings.Contains(err.Error(), "EVENT 27") {
		t.Fatalf("Connect error = %v, want it to mention EVENT 27", err)
	}
	// Give any (incorrect) reconnect goroutine a moment to misbehave.
	time.Sleep(50 * time.Millisecond)
}

func connectHappyPath(t *testing.T) (*Client, *scriptedTransport) {
	t.Helper()
	tr := newScriptedTransport(t, happyPathSteps())
	c := newTestClient(tr)
	shrinkTimeouts(c)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	return c, tr
}

func TestRun_ERXUDPDelivery(t *testing.T) {
	c, tr := connectHappyPath(t)

	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- c.Run(ctx) }()

	payload := []byte{0x10, 0x81, 0x00, 0x01, 0x02, 0x88, 0x01, 0x05, 0xFF, 0x01, 0x72, 0x01, 0xE7, 0x04, 0x00, 0x00, 0x01, 0xF4}
	line := "ERXUDP " + testIPv6 + " FE80:0000:0000:0000:1234:5678:9ABC:DEF0 0E1A 0E1A 001D129012345678 0 " +
		itoaHex4(len(payload)) + " " + hexEncodeUpper(payload)
	tr.injectLine(line)

	select {
	case msg := <-c.ERXUDP():
		if msg.Data != hexEncodeUpper(payload) {
			t.Fatalf("ERXUDP data = %q, want %q", msg.Data, hexEncodeUpper(payload))
		}
	case err := <-runErr:
		t.Fatalf("Run exited unexpectedly: %v", err)
	case <-time.After(1 * time.Second):
		t.Fatalf("timed out waiting for ERXUDP delivery")
	}

	cancel()
	select {
	case err := <-runErr:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run() error = %v, want context.Canceled", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("Run did not exit after context cancellation")
	}
}

// TestRun_MalformedERXUDPIsNotFatal: a length-mismatched (or otherwise
// invalid-hex) ERXUDP line — e.g. from a dongle unexpectedly in binary
// ERXUDP mode — must be dropped and logged, not take down the whole
// session; a subsequent well-formed ERXUDP must still be delivered
// normally.
func TestRun_MalformedERXUDPIsNotFatal(t *testing.T) {
	c, tr := connectHappyPath(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- c.Run(ctx) }()

	// Declares length 4 but only supplies 2 bytes (4 hex chars) of data.
	badLine := "ERXUDP " + testIPv6 + " FE80:0000:0000:0000:1234:5678:9ABC:DEF0 0E1A 0E1A 001D129012345678 0 0004 DEAD"
	tr.injectLine(badLine)

	payload := []byte{0x10, 0x81, 0x00, 0x01, 0x02, 0x88, 0x01, 0x05, 0xFF, 0x01, 0x72, 0x01, 0xE7, 0x04, 0x00, 0x00, 0x01, 0xF4}
	goodLine := "ERXUDP " + testIPv6 + " FE80:0000:0000:0000:1234:5678:9ABC:DEF0 0E1A 0E1A 001D129012345678 0 " +
		itoaHex4(len(payload)) + " " + hexEncodeUpper(payload)
	tr.injectLine(goodLine)

	select {
	case msg := <-c.ERXUDP():
		if msg.Data != hexEncodeUpper(payload) {
			t.Fatalf("ERXUDP data = %q, want %q", msg.Data, hexEncodeUpper(payload))
		}
	case err := <-runErr:
		t.Fatalf("Run exited unexpectedly (malformed ERXUDP should be dropped, not fatal): %v", err)
	case <-time.After(1 * time.Second):
		t.Fatalf("timed out waiting for the well-formed ERXUDP after a malformed one")
	}

	cancel()
	select {
	case err := <-runErr:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run() error = %v, want context.Canceled", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("Run did not exit after context cancellation")
	}
}

func TestRun_SessionEndingEventIsFatal(t *testing.T) {
	c, tr := connectHappyPath(t)

	ctx := context.Background()
	runErr := make(chan error, 1)
	go func() { runErr <- c.Run(ctx) }()

	tr.injectLine("EVENT 29")

	select {
	case err := <-runErr:
		if err == nil || !strings.Contains(err.Error(), "EVENT 29") {
			t.Fatalf("Run() error = %v, want it to mention EVENT 29", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("Run did not exit after session-ending EVENT")
	}
	// No further scripted steps exist; if Run had tried to auto-reconnect
	// (SKTERM/SKRESET/...), the next Write would already have failed the
	// test via scriptedTransport.
}

func TestRun_BareFailIsFatal(t *testing.T) {
	c, tr := connectHappyPath(t)

	ctx := context.Background()
	runErr := make(chan error, 1)
	go func() { runErr <- c.Run(ctx) }()

	tr.injectLine("FAIL ER10")

	select {
	case err := <-runErr:
		if err == nil || !strings.Contains(err.Error(), "ER10") {
			t.Fatalf("Run() error = %v, want it to mention the FAIL line", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("Run did not exit after bare FAIL")
	}
}

func TestRun_UDPResponseTimeout(t *testing.T) {
	steps := happyPathSteps()
	steps = append(steps, exchange{}) // absorb the SendUDP write; expect="" skips content check
	tr := newScriptedTransport(t, steps)
	c := newTestClient(tr)
	shrinkTimeouts(c)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	if err := c.Connect(ctx); err != nil {
		cancel()
		t.Fatalf("Connect: %v", err)
	}
	cancel()

	if err := c.SendUDP([]byte{0x10, 0x81}); err != nil {
		t.Fatalf("SendUDP: %v", err)
	}

	runCtx, runCancel := context.WithCancel(context.Background())
	defer runCancel()
	runErr := make(chan error, 1)
	go func() { runErr <- c.Run(runCtx) }()

	select {
	case err := <-runErr:
		if !errors.Is(err, errUDPResponseTimeout) {
			t.Fatalf("Run() error = %v, want errUDPResponseTimeout", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("Run did not fire the UDP response watchdog in time")
	}
}

func TestSendUDP_NotConnectedIsNoop(t *testing.T) {
	// Zero scripted steps: any Write at all fails the test.
	tr := newScriptedTransport(t, nil)
	c := newTestClient(tr)
	shrinkTimeouts(c)

	if err := c.SendUDP([]byte{0x10, 0x81}); err != nil {
		t.Fatalf("SendUDP before connect: %v", err)
	}
}

func itoaHex4(n int) string {
	const hexdigits = "0123456789ABCDEF"
	b := [4]byte{}
	for i := 3; i >= 0; i-- {
		b[i] = hexdigits[n&0xF]
		n >>= 4
	}
	return string(b[:])
}
