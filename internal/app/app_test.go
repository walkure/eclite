package app

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/walkure/eclite/internal/config"
	"github.com/walkure/eclite/internal/echonet"
	"github.com/walkure/eclite/internal/skstack"
)

// fakeTransport is a minimal scripted serial fake: it answers the fixed
// SKSTACK handshake sequence, then lets the test inject further lines
// (ERXUDP datagrams) asynchronously. It's a smaller cousin of
// internal/skstack's scriptedTransport — skstack's version isn't exported,
// and this package only needs to get through one fixed handshake, not
// exercise every protocol edge case (that's what internal/skstack's own
// tests are for).
type fakeTransport struct {
	t *testing.T

	mu     sync.Mutex
	toRead []byte
	closed bool
	notify chan struct{}
}

func newFakeTransport(t *testing.T) *fakeTransport {
	return &fakeTransport{t: t, notify: make(chan struct{}, 1)}
}

const testIPv6 = "FE80:0000:0000:0000:021D:1290:1234:5678"

func (f *fakeTransport) Write(p []byte) (int, error) {
	// ROPT/WOPT are CR-only terminated; every other command is CRLF.
	cmd := strings.TrimSuffix(strings.TrimSuffix(string(p), "\r\n"), "\r")
	var reply []string
	switch {
	case cmd == "SKVER", cmd == "SKINFO", cmd == "SKRESET", cmd == "SKSREG SFE 0",
		strings.HasPrefix(cmd, "SKSETPWD"), strings.HasPrefix(cmd, "SKSETRBID"),
		cmd == "SKSREG S2 21", cmd == "SKSREG S3 8888":
		reply = []string{"OK"}
	case cmd == "ROPT":
		// Already hex-ASCII (steady state, matching real production
		// hardware) — WOPT must NOT be called. ROPT/WOPT edge cases
		// (binary mode, WOPT-triggers-restart, etc.) are covered at the
		// skstack package level (client_test.go), not here.
		reply = []string{"OK 01"}
	case cmd == "SKSCAN 2 FFFFFFFF 6":
		reply = []string{"EPANDESC", "  Channel:21", "  Pan ID:8888", "  Addr:001D129012345678", "EVENT 22"}
	case cmd == "SKLL64 001D129012345678":
		reply = []string{testIPv6}
	case cmd == "SKJOIN "+testIPv6:
		reply = []string{"OK", "EVENT 25 " + testIPv6}
	case cmd == "SKTERM":
		// best-effort shutdown command; no reply needed for this test.
	case strings.HasPrefix(cmd, "SKSENDTO"):
		// SendUDP traffic — accepted silently, no scripted reply.
	default:
		f.t.Fatalf("fakeTransport: unexpected write %q", cmd)
	}
	f.push(reply)
	return len(p), nil
}

func (f *fakeTransport) push(lines []string) {
	if len(lines) == 0 {
		return
	}
	f.mu.Lock()
	for _, line := range lines {
		f.toRead = append(f.toRead, []byte(line+"\r\n")...)
	}
	f.mu.Unlock()
	f.wake()
}

func (f *fakeTransport) wake() {
	select {
	case f.notify <- struct{}{}:
	default:
	}
}

func (f *fakeTransport) Read(p []byte) (int, error) {
	for {
		f.mu.Lock()
		if len(f.toRead) > 0 {
			n := copy(p, f.toRead)
			f.toRead = f.toRead[n:]
			f.mu.Unlock()
			return n, nil
		}
		if f.closed {
			f.mu.Unlock()
			return 0, io.EOF
		}
		f.mu.Unlock()
		<-f.notify
	}
}

func (f *fakeTransport) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	f.wake()
	return nil
}

// injectERXUDP builds and injects a fully-formed ERXUDP line carrying an
// ECHONET Lite frame, as if the meter had just answered a query.
func (f *fakeTransport) injectERXUDP(payload []byte) {
	hexPayload := strings.ToUpper(hex.EncodeToString(payload))
	line := "ERXUDP " + testIPv6 + " FE80:0000:0000:0000:1234:5678:9ABC:DEF0 0E1A 0E1A 001D129012345678 0 " +
		hex4(len(payload)) + " " + hexPayload
	f.push([]string{line})
}

func hex4(n int) string {
	const digits = "0123456789ABCDEF"
	b := [4]byte{}
	for i := 3; i >= 0; i-- {
		b[i] = digits[n&0xF]
		n >>= 4
	}
	return string(b[:])
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func testConfig(t *testing.T, wdtMinutes int) *config.Config {
	t.Helper()
	return &config.Config{
		BP35A1: config.BP35A1{ID: "test-id", Pass: "test-pass", Device: "/dev/fake"},
		Prometheus: config.Prometheus{
			WDT:    wdtMinutes,
			HTTP:   0, // unused: opts.Listen is overridden in tests
			Labels: map[string]string{"place": "test"},
		},
	}
}

func fullMeterResponse() []byte {
	return echonetResponse(echonet.ESVGetRes,
		echonet.Property{EPC: echonet.EPCCoefficient, EDT: []byte{0x00, 0x00, 0x00, 0x01}},
		echonet.Property{EPC: echonet.EPCUnit, EDT: []byte{0x00}},
		echonet.Property{EPC: echonet.EPCCumulativeEnergy, EDT: []byte{0x00, 0x00, 0x27, 0x10}},
		echonet.Property{EPC: echonet.EPCInstantaneousWatt, EDT: []byte{0x00, 0x00, 0x03, 0xE8}},
	)
}

// echonetResponse builds a raw meter->controller frame byte-for-byte, for
// injecting as if the meter had answered.
func echonetResponse(esv byte, props ...echonet.Property) []byte {
	buf := []byte{0x10, 0x81, 0x00, 0x01, 0x02, 0x88, 0x01, 0x05, 0xFF, 0x01, esv, byte(len(props))}
	for _, p := range props {
		buf = append(buf, p.EPC, byte(len(p.EDT)))
		buf = append(buf, p.EDT...)
	}
	return buf
}

// runOnListener wires opts.Listen to hand back a listener the test already
// created via net.Listen("tcp", "127.0.0.1:0"), so the test knows the real
// address without needing the OS-assigned port from cfg.
func runOnListener(ln net.Listener) func(addr string) (net.Listener, error) {
	used := false
	return func(addr string) (net.Listener, error) {
		if used {
			return nil, errors.New("runOnListener: Listen called more than once")
		}
		used = true
		return ln, nil
	}
}

func TestRun_EndToEnd(t *testing.T) {
	tr := newFakeTransport(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}

	exitCalls := make(chan int, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runDone := make(chan error, 1)
	go func() {
		runDone <- Run(ctx, testConfig(t, 60), Options{
			NewTransport: func(string) (skstack.Transport, error) { return tr, nil },
			Listen:       runOnListener(ln),
			Exit:         func(code int) { exitCalls <- code },
			Clock:        &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
		})
	}()

	// Wait for the handshake to complete and the initial query to be sent,
	// then answer it as the meter would.
	time.Sleep(100 * time.Millisecond)
	tr.injectERXUDP(fullMeterResponse())
	time.Sleep(100 * time.Millisecond)

	url := "http://" + ln.Addr().String() + "/metrics"
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /metrics status = %d, want 200", resp.StatusCode)
	}

	cancel()
	select {
	case err := <-runDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run() error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("Run did not return after context cancellation")
	}
	select {
	case code := <-exitCalls:
		if code != 1 {
			t.Fatalf("Exit called with %d, want 1", code)
		}
	default:
		t.Fatalf("Exit was never called")
	}
}

func TestRun_WDTTripCausesFatalExit(t *testing.T) {
	tr := newFakeTransport(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}

	clock := &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	exitCalls := make(chan int, 1)

	runDone := make(chan error, 1)
	go func() {
		runDone <- Run(context.Background(), testConfig(t, 1), Options{ // 1-minute WDT window
			NewTransport:     func(string) (skstack.Transport, error) { return tr, nil },
			Listen:           runOnListener(ln),
			Exit:             func(code int) { exitCalls <- code },
			Clock:            clock,
			shutdownGrace:    200 * time.Millisecond,
			wdtCheckInterval: 50 * time.Millisecond,
		})
	}()

	time.Sleep(50 * time.Millisecond) // let handshake + MarkConnected land
	clock.Advance(2 * time.Minute)    // exceed the 1-minute WDT window

	select {
	case err := <-runDone:
		if err == nil || !strings.Contains(err.Error(), "WDT") {
			t.Fatalf("Run() error = %v, want it to mention WDT", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("Run did not exit after WDT should have tripped")
	}
	select {
	case code := <-exitCalls:
		if code != 1 {
			t.Fatalf("Exit called with %d, want 1", code)
		}
	default:
		t.Fatalf("Exit was never called")
	}
}

func TestRun_NotYetInitialized_Returns500(t *testing.T) {
	tr := newFakeTransport(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go Run(ctx, testConfig(t, 60), Options{
		NewTransport: func(string) (skstack.Transport, error) { return tr, nil },
		Listen:       runOnListener(ln),
		Exit:         func(int) {},
		Clock:        &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
	})

	time.Sleep(100 * time.Millisecond) // handshake completes, but no reading yet

	resp, err := http.Get("http://" + ln.Addr().String() + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("GET /metrics status = %d, want 500", resp.StatusCode)
	}
}
