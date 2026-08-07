package echonet

import (
	"encoding/binary"
	"fmt"
)

// EPC codes this codebase reads from the meter.
const (
	EPCCoefficient         = 0xD3 // optional — many meters don't implement it
	EPCUnit                = 0xE1
	EPCCumulativeEnergy    = 0xE0
	EPCInstantaneousWatt   = 0xE7
	EPCInstantaneousAmpere = 0xE8
	EPCApplianceVersion    = 0x82
)

// Magnitude returns the multiplier a 0xE1 unit-code EDT byte represents,
// per the ECHONET Lite low-voltage smart meter class definition. Unknown
// codes default to 1 (matching SKSock.pm's get_mag fallback) — the caller
// is responsible for logging that fallback if it wants parity with the
// Perl original's warning.
func Magnitude(unitCode byte) float64 {
	switch unitCode {
	case 0x00:
		return 1
	case 0x01:
		return 0.1
	case 0x02:
		return 0.01
	case 0x03:
		return 0.001
	case 0x04:
		return 0.0001
	case 0x0A:
		return 10
	case 0x0B:
		return 100
	case 0x0C:
		return 1000
	case 0x0D:
		return 10000
	default:
		return 1
	}
}

// KnownMagnitude reports whether unitCode is one Magnitude has an explicit
// mapping for, so callers can distinguish "meter reported unit code 0x00"
// from "meter reported an unrecognized code and we're defaulting to 1".
func KnownMagnitude(unitCode byte) bool {
	switch unitCode {
	case 0x00, 0x01, 0x02, 0x03, 0x04, 0x0A, 0x0B, 0x0C, 0x0D:
		return true
	default:
		return false
	}
}

// DecodeCoefficient decodes EPC 0xD3 (cumulative-energy coefficient): a
// big-endian uint32.
func DecodeCoefficient(edt []byte) (uint32, error) {
	if len(edt) != 4 {
		return 0, fmt.Errorf("echonet: 0xD3 EDT length = %d, want 4", len(edt))
	}
	return binary.BigEndian.Uint32(edt), nil
}

// DecodeUnitCode decodes EPC 0xE1 (cumulative-energy unit): a single byte,
// looked up via Magnitude.
func DecodeUnitCode(edt []byte) (byte, error) {
	if len(edt) != 1 {
		return 0, fmt.Errorf("echonet: 0xE1 EDT length = %d, want 1", len(edt))
	}
	return edt[0], nil
}

// DecodeCumulativeEnergyRaw decodes EPC 0xE0 (cumulative energy, positive
// direction): a big-endian signed 32-bit raw meter count. Multiply by the
// coefficient (0xD3) and unit magnitude (Magnitude(0xE1 code)) to get kWh.
func DecodeCumulativeEnergyRaw(edt []byte) (int32, error) {
	if len(edt) != 4 {
		return 0, fmt.Errorf("echonet: 0xE0 EDT length = %d, want 4", len(edt))
	}
	return int32(binary.BigEndian.Uint32(edt)), nil
}

// DecodeInstantaneousWatt decodes EPC 0xE7: a big-endian signed 32-bit watt
// value.
func DecodeInstantaneousWatt(edt []byte) (int32, error) {
	if len(edt) != 4 {
		return 0, fmt.Errorf("echonet: 0xE7 EDT length = %d, want 4", len(edt))
	}
	return int32(binary.BigEndian.Uint32(edt)), nil
}

// DecodeInstantaneousAmpere decodes EPC 0xE8: two signed 16-bit values
// (R-phase, T-phase) in 0.1A units; 0x7FFE means "not measurable" and is
// normalized to 0, matching echonet.pl's handling. Returns the sum of both
// phases in amperes. Computed for completeness — per the README this isn't
// currently exposed via Prometheus, since the 0.1A wire resolution doesn't
// match the meter's actual ~1A accuracy.
func DecodeInstantaneousAmpere(edt []byte) (float64, error) {
	if len(edt) != 4 {
		return 0, fmt.Errorf("echonet: 0xE8 EDT length = %d, want 4", len(edt))
	}
	r := int16(binary.BigEndian.Uint16(edt[0:2]))
	t := int16(binary.BigEndian.Uint16(edt[2:4]))
	const notMeasurable = 0x7ffe
	if r == notMeasurable {
		r = 0
	}
	if t == notMeasurable {
		t = 0
	}
	return float64(r+t) / 10.0, nil
}

// DecodeApplianceVersion decodes EPC 0x82 (APPENDIX release): a 4-byte EDT
// of [major][minor][release letter][revision]. Only release+revision are
// used, matching echonet.pl's unpack('x2AC', $edt).
func DecodeApplianceVersion(edt []byte) (release byte, revision byte, err error) {
	if len(edt) != 4 {
		return 0, 0, fmt.Errorf("echonet: 0x82 EDT length = %d, want 4", len(edt))
	}
	return edt[2], edt[3], nil
}
