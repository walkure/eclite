package skstack

import (
	"io"
	"strings"
	"sync"
	"testing"
)

// exchange is one scripted request/response pair for scriptedTransport.
// expect, if non-empty, is the exact bytes (after stripping a trailing
// "\r\n", since nearly every SKSTACK command is CRLF-terminated by
// sendCmd) the client must Write before reply is made available for
// reading. Leave expect empty to skip the check (useful when the exact
// bytes don't matter, e.g. SKSENDTO payloads in other tests).
type exchange struct {
	expect string
	reply  []string
}

// scriptedTransport is an in-memory Transport that asserts the client sends
// exactly the expected command at each step before releasing the next
// scripted response lines. This is the highest-value test fixture for this
// package: a wrong command or argument is the failure mode most likely to
// hang silently against real hardware.
type scriptedTransport struct {
	t     *testing.T
	steps []exchange
	idx   int

	mu     sync.Mutex
	toRead []byte
	closed bool
	notify chan struct{}
}

func newScriptedTransport(t *testing.T, steps []exchange) *scriptedTransport {
	return &scriptedTransport{t: t, steps: steps, notify: make(chan struct{}, 1)}
}

func (s *scriptedTransport) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.idx >= len(s.steps) {
		s.t.Fatalf("unexpected write with no more scripted steps: %q", p)
		return 0, io.ErrClosedPipe
	}
	step := s.steps[s.idx]
	// ROPT/WOPT are CR-only terminated; every other command is CRLF.
	got := strings.TrimSuffix(strings.TrimSuffix(string(p), "\r\n"), "\r")
	if step.expect != "" && got != step.expect {
		s.t.Fatalf("write %d: got %q, want %q", s.idx, got, step.expect)
		return 0, io.ErrClosedPipe
	}
	s.idx++
	for _, line := range step.reply {
		s.toRead = append(s.toRead, []byte(line+"\r\n")...)
	}
	s.wakeLocked()
	return len(p), nil
}

func (s *scriptedTransport) wakeLocked() {
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

func (s *scriptedTransport) Read(p []byte) (int, error) {
	for {
		s.mu.Lock()
		if len(s.toRead) > 0 {
			n := copy(p, s.toRead)
			s.toRead = s.toRead[n:]
			s.mu.Unlock()
			return n, nil
		}
		if s.closed {
			s.mu.Unlock()
			return 0, io.EOF
		}
		s.mu.Unlock()
		<-s.notify
	}
}

func (s *scriptedTransport) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.wakeSignal()
	return nil
}

func (s *scriptedTransport) wakeSignal() {
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

// injectLine pushes a line not tied to any particular Write, e.g. an
// asynchronous ERXUDP or EVENT notification arriving after the handshake.
func (s *scriptedTransport) injectLine(line string) {
	s.mu.Lock()
	s.toRead = append(s.toRead, []byte(line+"\r\n")...)
	s.mu.Unlock()
	s.wakeSignal()
}

// allStepsConsumed reports whether every scripted exchange was used —
// call at the end of a happy-path test to catch a handshake that returned
// success without actually going through every expected step.
func (s *scriptedTransport) allStepsConsumed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.idx == len(s.steps)
}
