package echonet

import (
	"encoding/hex"
	"strings"
	"testing"
)

// buildFrameHex assembles a raw ECHONET Lite frame (meter -> controller)
// and hex-encodes it, mirroring the wire format skstack hands to Parse.
func buildFrameHex(t *testing.T, esv byte, props ...Property) string {
	t.Helper()
	buf := []byte{
		0x10, 0x81, // EHD
		0x00, 0x01, // TID
		classGroupMeter, classMeter, instanceOne, // SEOJ (meter)
		classGroupController, classController, instanceOne, // DEOJ (controller)
		esv,
		byte(len(props)),
	}
	for _, p := range props {
		buf = append(buf, p.EPC, byte(len(p.EDT)))
		buf = append(buf, p.EDT...)
	}
	return strings.ToUpper(hex.EncodeToString(buf))
}

func TestParse_HappyPath(t *testing.T) {
	hexData := buildFrameHex(t, ESVGetRes,
		Property{EPC: EPCUnit, EDT: []byte{0x00}},
		Property{EPC: EPCCumulativeEnergy, EDT: []byte{0x00, 0x00, 0x27, 0x10}},
		Property{EPC: EPCInstantaneousWatt, EDT: []byte{0x00, 0x00, 0x03, 0xE8}},
	)
	frame, err := Parse(hexData)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if frame.ESV != ESVGetRes {
		t.Fatalf("ESV = %#x, want %#x", frame.ESV, ESVGetRes)
	}
	if len(frame.Properties) != 3 {
		t.Fatalf("len(Properties) = %d, want 3", len(frame.Properties))
	}
	if frame.Properties[1].EPC != EPCCumulativeEnergy {
		t.Fatalf("Properties[1].EPC = %#x, want %#x", frame.Properties[1].EPC, EPCCumulativeEnergy)
	}
	if got, want := frame.Properties[1].EDT, []byte{0x00, 0x00, 0x27, 0x10}; !bytesEqual(got, want) {
		t.Fatalf("Properties[1].EDT = %x, want %x", got, want)
	}
}

func TestParse_NoProperties(t *testing.T) {
	hexData := buildFrameHex(t, ESVGetSNA)
	frame, err := Parse(hexData)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(frame.Properties) != 0 {
		t.Fatalf("len(Properties) = %d, want 0", len(frame.Properties))
	}
}

func TestParse_RejectsForeignHeader(t *testing.T) {
	valid := buildFrameHex(t, ESVGetRes, Property{EPC: EPCUnit, EDT: []byte{0x00}})
	rawValid, err := hex.DecodeString(valid)
	if err != nil {
		t.Fatalf("test setup: %v", err)
	}

	tests := []struct {
		name   string
		mutate func([]byte)
	}{
		{"bad EHD1", func(b []byte) { b[0] = 0x04 }},
		{"bad EHD2", func(b []byte) { b[1] = 0x00 }},
		{"bad SEOJ class group", func(b []byte) { b[4] = 0x01 }},
		{"bad SEOJ class", func(b []byte) { b[5] = 0x89 }},
		{"bad DEOJ class group", func(b []byte) { b[7] = 0x0E }},
		{"bad DEOJ class", func(b []byte) { b[8] = 0x00 }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw := append([]byte(nil), rawValid...)
			tc.mutate(raw)
			_, err := Parse(strings.ToUpper(hex.EncodeToString(raw)))
			if err == nil {
				t.Fatalf("Parse: want error for %s, got nil", tc.name)
			}
		})
	}
}

func TestParse_RejectsShortOrInvalidHex(t *testing.T) {
	if _, err := Parse("1081000105FF01"); err == nil {
		t.Fatalf("Parse: want error for too-short frame")
	}
	if _, err := Parse("not-hex-at-all"); err == nil {
		t.Fatalf("Parse: want error for invalid hex")
	}
}

func TestParse_RejectsTruncatedPropertyList(t *testing.T) {
	// Claims OPC=2 but only supplies one property's worth of bytes.
	hexData := buildFrameHex(t, ESVGetRes, Property{EPC: EPCUnit, EDT: []byte{0x00}})
	raw, err := hex.DecodeString(hexData)
	if err != nil {
		t.Fatalf("test setup: %v", err)
	}
	raw[11] = 2 // OPC
	_, err = Parse(strings.ToUpper(hex.EncodeToString(raw)))
	if err == nil {
		t.Fatalf("Parse: want error for truncated property list")
	}
}

func TestBuildGetRequest_MatchesKnownWireBytes(t *testing.T) {
	// These three are the exact literal byte strings echonet.pl hardcodes;
	// BuildGetRequest must reproduce them exactly for wire compatibility.
	tests := []struct {
		name string
		epcs []byte
		want []byte
	}{
		{
			name: "initial query (on_connected)",
			epcs: []byte{0xD3, 0xE1, 0xE0, 0xE7, 0x82},
			want: []byte{
				0x10, 0x81, 0x00, 0x01, 0x05, 0xFF, 0x01, 0x02, 0x88, 0x01, 0x62, 0x05,
				0xD3, 0x00, 0xE1, 0x00, 0xE0, 0x00, 0xE7, 0x00, 0x82, 0x00,
			},
		},
		{
			name: "get_watt",
			epcs: []byte{0xE0, 0xE7},
			want: []byte{
				0x10, 0x81, 0x00, 0x01, 0x05, 0xFF, 0x01, 0x02, 0x88, 0x01, 0x62, 0x02,
				0xE0, 0x00, 0xE7, 0x00,
			},
		},
		{
			name: "reduced query after Get_SNA on 0xD3",
			epcs: []byte{0xE1, 0xE0, 0xE7, 0x82},
			want: []byte{
				0x10, 0x81, 0x00, 0x01, 0x05, 0xFF, 0x01, 0x02, 0x88, 0x01, 0x62, 0x04,
				0xE1, 0x00, 0xE0, 0x00, 0xE7, 0x00, 0x82, 0x00,
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := BuildGetRequest(tc.epcs...)
			if !bytesEqual(got, tc.want) {
				t.Fatalf("BuildGetRequest(%v) = % X, want % X", tc.epcs, got, tc.want)
			}
		})
	}
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
