package skstack

import "testing"

// TestSplitLine is a direct regression test for the actual root cause of a
// real-hardware failure: ROPT's reply is documented as terminated by a
// bare "\r" (no trailing "\n"), unlike most other SKSTACK responses which
// use "\r\n". The original implementation split only on "\n", so a
// CR-only reply just sat in the buffer forever — indistinguishable from
// the device never answering at all. SKSock.pm avoided this by
// normalizing "\r\n"/"\r"/"\n" all as line breaks before splitting
// (`s/\x0D\x0A|\x0D|\x0A/\n/g`); splitLine must do the same.
func TestSplitLine(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		wantLine string
		wantRest string
		wantOK   bool
	}{
		{"CRLF", "OK\r\nrest", "OK", "rest", true},
		{"bare CR", "OK\rrest", "OK", "rest", true},
		{"bare LF", "OK\nrest", "OK", "rest", true},
		{"no terminator yet", "OK", "", "OK", false},
		{"empty buffer", "", "", "", false},
		{"terminator at very end, CRLF", "OK\r\n", "OK", "", true},
		{"terminator at very end, bare CR", "OK\r", "OK", "", true},
		{"leading terminator yields empty line", "\r\nOK", "", "OK", true},
		{"CR at end of buffer with no following byte (yet)", "OK\r", "OK", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			line, rest, ok := splitLine([]byte(tc.in))
			if ok != tc.wantOK {
				t.Fatalf("splitLine(%q) ok = %v, want %v", tc.in, ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if line != tc.wantLine {
				t.Errorf("splitLine(%q) line = %q, want %q", tc.in, line, tc.wantLine)
			}
			if string(rest) != tc.wantRest {
				t.Errorf("splitLine(%q) rest = %q, want %q", tc.in, string(rest), tc.wantRest)
			}
		})
	}
}

// TestSplitLine_ReassemblesMultipleLines exercises repeated calls the way
// readLoop actually uses splitLine: draining every complete line from a
// buffer that mixes terminator styles, matching what real BP35A1 traffic
// looks like (mostly CRLF, with at least one CR-only response type).
func TestSplitLine_ReassemblesMultipleLines(t *testing.T) {
	buf := []byte("EVER 1.2.10\r\nOK\r\nOK 01\rOK\r\n")
	var got []string
	for {
		line, rest, ok := splitLine(buf)
		if !ok {
			break
		}
		got = append(got, line)
		buf = rest
	}
	want := []string{"EVER 1.2.10", "OK", "OK 01", "OK"}
	if len(got) != len(want) {
		t.Fatalf("got %d lines %v, want %d lines %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
	if len(buf) != 0 {
		t.Errorf("leftover buffer = %q, want empty", buf)
	}
}

// TestParseERXUDP_HappyPath is a baseline sanity check for the
// well-formed case, alongside the validation-failure tests below.
func TestParseERXUDP_HappyPath(t *testing.T) {
	line := "ERXUDP FE80::1 FE80::2 0E1A 0E1A 001D129012345678 0 0004 DEADBEEF"
	msg, err := parseERXUDP(line)
	if err != nil {
		t.Fatalf("parseERXUDP: %v", err)
	}
	if msg.Src != "FE80::1" || msg.Dst != "FE80::2" {
		t.Errorf("Src/Dst = %q/%q, want FE80::1/FE80::2", msg.Src, msg.Dst)
	}
	if msg.Length != 4 {
		t.Errorf("Length = %d, want 4", msg.Length)
	}
	if msg.Data != "DEADBEEF" {
		t.Errorf("Data = %q, want DEADBEEF", msg.Data)
	}
}

// TestParseERXUDP_ValidationFailures covers the checks added specifically
// to catch a dongle unexpectedly in binary ERXUDP mode (see doROPT):
// under correct hex-ASCII framing, DATA has no embedded whitespace and
// exactly 2*LENGTH hex characters — any deviation is a strong signal of
// binary-mode corruption, not a benign quirk.
func TestParseERXUDP_ValidationFailures(t *testing.T) {
	tests := []struct {
		name string
		line string
	}{
		{
			name: "length mismatch (declared 4 bytes, got 2)",
			line: "ERXUDP FE80::1 FE80::2 0E1A 0E1A 001D129012345678 0 0004 DEAD",
		},
		{
			name: "invalid hex characters",
			line: "ERXUDP FE80::1 FE80::2 0E1A 0E1A 001D129012345678 0 0004 ZZZZZZZZ",
		},
		{
			name: "extra trailing field (embedded whitespace in binary data)",
			line: "ERXUDP FE80::1 FE80::2 0E1A 0E1A 001D129012345678 0 0004 DEAD BEEF",
		},
		{
			name: "too few fields",
			line: "ERXUDP FE80::1 FE80::2 0E1A 0E1A 001D129012345678 0 0004",
		},
		{
			name: "non-numeric length",
			line: "ERXUDP FE80::1 FE80::2 0E1A 0E1A 001D129012345678 0 XXXX DEADBEEF",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseERXUDP(tc.line); err == nil {
				t.Fatalf("parseERXUDP(%q): want error, got nil", tc.line)
			}
		})
	}
}
