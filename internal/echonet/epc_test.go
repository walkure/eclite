package echonet

import "testing"

func TestMagnitude(t *testing.T) {
	tests := []struct {
		code byte
		want float64
	}{
		{0x00, 1}, {0x01, 0.1}, {0x02, 0.01}, {0x03, 0.001}, {0x04, 0.0001},
		{0x0A, 10}, {0x0B, 100}, {0x0C, 1000}, {0x0D, 10000},
		{0xFF, 1}, // unknown code -> default fallback, matches get_mag's behavior
	}
	for _, tc := range tests {
		if got := Magnitude(tc.code); got != tc.want {
			t.Errorf("Magnitude(%#x) = %v, want %v", tc.code, got, tc.want)
		}
	}
}

func TestKnownMagnitude(t *testing.T) {
	for _, code := range []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x0A, 0x0B, 0x0C, 0x0D} {
		if !KnownMagnitude(code) {
			t.Errorf("KnownMagnitude(%#x) = false, want true", code)
		}
	}
	for _, code := range []byte{0x05, 0x09, 0xFF} {
		if KnownMagnitude(code) {
			t.Errorf("KnownMagnitude(%#x) = true, want false", code)
		}
	}
}

func TestDecodeCoefficient(t *testing.T) {
	v, err := DecodeCoefficient([]byte{0x00, 0x00, 0x00, 0x01})
	if err != nil {
		t.Fatalf("DecodeCoefficient: %v", err)
	}
	if v != 1 {
		t.Fatalf("DecodeCoefficient = %d, want 1", v)
	}
	if _, err := DecodeCoefficient([]byte{0x00}); err == nil {
		t.Fatalf("DecodeCoefficient: want error for short EDT")
	}
}

func TestDecodeUnitCode(t *testing.T) {
	v, err := DecodeUnitCode([]byte{0x01})
	if err != nil {
		t.Fatalf("DecodeUnitCode: %v", err)
	}
	if v != 0x01 {
		t.Fatalf("DecodeUnitCode = %#x, want 0x01", v)
	}
	if _, err := DecodeUnitCode([]byte{0x01, 0x02}); err == nil {
		t.Fatalf("DecodeUnitCode: want error for wrong-length EDT")
	}
}

func TestDecodeCumulativeEnergyRaw(t *testing.T) {
	v, err := DecodeCumulativeEnergyRaw([]byte{0x00, 0x00, 0x27, 0x10})
	if err != nil {
		t.Fatalf("DecodeCumulativeEnergyRaw: %v", err)
	}
	if v != 10000 {
		t.Fatalf("DecodeCumulativeEnergyRaw = %d, want 10000", v)
	}
}

func TestDecodeInstantaneousWatt(t *testing.T) {
	v, err := DecodeInstantaneousWatt([]byte{0xFF, 0xFF, 0xFF, 0xFF}) // -1
	if err != nil {
		t.Fatalf("DecodeInstantaneousWatt: %v", err)
	}
	if v != -1 {
		t.Fatalf("DecodeInstantaneousWatt = %d, want -1 (signed decode)", v)
	}
}

func TestDecodeInstantaneousAmpere(t *testing.T) {
	tests := []struct {
		name string
		edt  []byte
		want float64
	}{
		{"both measurable", []byte{0x00, 0x0A, 0x00, 0x05}, 1.5},  // (10+5)/10
		{"R not measurable", []byte{0x7F, 0xFE, 0x00, 0x05}, 0.5}, // 0x7ffe -> 0, +5 -> 0.5
		{"T not measurable", []byte{0x00, 0x0A, 0x7F, 0xFE}, 1.0}, // 10 -> 1.0
		{"both not measurable", []byte{0x7F, 0xFE, 0x7F, 0xFE}, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DecodeInstantaneousAmpere(tc.edt)
			if err != nil {
				t.Fatalf("DecodeInstantaneousAmpere: %v", err)
			}
			if got != tc.want {
				t.Fatalf("DecodeInstantaneousAmpere(% X) = %v, want %v", tc.edt, got, tc.want)
			}
		})
	}
}

func TestDecodeApplianceVersion(t *testing.T) {
	release, revision, err := DecodeApplianceVersion([]byte{0x01, 0x00, 'F', 0x03})
	if err != nil {
		t.Fatalf("DecodeApplianceVersion: %v", err)
	}
	if release != 'F' || revision != 0x03 {
		t.Fatalf("DecodeApplianceVersion = (%q, %d), want ('F', 3)", release, revision)
	}
}
