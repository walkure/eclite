// Package echonet decodes and builds ECHONET Lite frames for the B-route
// exchange with a low-voltage smart electric energy meter. It only knows
// about the wire format — deciding what a decoded value means for
// application state (is the coefficient known yet? is this reading stale?)
// is the meter package's job.
package echonet

import (
	"encoding/hex"
	"fmt"
)

// ESV (service) codes this codebase cares about in meter responses.
const (
	ESVGetSNA = 0x52 // "Get" processing failed for one or more requested properties
	ESVInf    = 0x72 // unsolicited property notification
	ESVGetRes = 0x73 // Get response
)

// Object codes for the low-voltage smart electric energy meter B-route
// exchange: the meter is always SEOJ class-group/class 0x02/0x88 (instance
// 1); the controller (us) is always DEOJ 0x05/0xFF (instance 1).
const (
	classGroupMeter      = 0x02
	classMeter           = 0x88
	classGroupController = 0x05
	classController      = 0xFF
	instanceOne          = 0x01
)

// Frame is a decoded incoming ECHONET Lite frame from the meter.
type Frame struct {
	ESV        byte
	Properties []Property
}

// Property is one EPC/EDT pair from a frame's property list.
type Property struct {
	EPC byte
	EDT []byte
}

// Parse decodes hexData — the hex-ASCII payload skstack.ERXUDPMessage
// carries for a received datagram — validating the ECHONET Lite header
// (EHD, and that SEOJ/DEOJ identify a meter-to-controller exchange) and
// splitting the property list. It does not interpret EDT contents, and it
// does not reject frames by ESV — a Get_SNA (0x52) frame parses just as
// well as an INF/Get_Res; deciding which ESVs are meaningful is the
// caller's job.
func Parse(hexData string) (*Frame, error) {
	raw, err := hex.DecodeString(hexData)
	if err != nil {
		return nil, fmt.Errorf("echonet: invalid hex payload: %w", err)
	}
	if len(raw) < 12 {
		return nil, fmt.Errorf("echonet: frame too short (%d bytes)", len(raw))
	}
	if raw[0] != 0x10 || raw[1] != 0x81 {
		return nil, fmt.Errorf("echonet: unexpected EHD %02X%02X", raw[0], raw[1])
	}
	if raw[4] != classGroupMeter || raw[5] != classMeter {
		return nil, fmt.Errorf("echonet: unexpected SEOJ %02X%02X (want meter %02X%02X)",
			raw[4], raw[5], classGroupMeter, classMeter)
	}
	if raw[7] != classGroupController || raw[8] != classController {
		return nil, fmt.Errorf("echonet: unexpected DEOJ %02X%02X (want controller %02X%02X)",
			raw[7], raw[8], classGroupController, classController)
	}

	esv := raw[10]
	opc := int(raw[11])
	props, err := splitProperties(raw[12:], opc)
	if err != nil {
		return nil, err
	}
	return &Frame{ESV: esv, Properties: props}, nil
}

func splitProperties(data []byte, count int) ([]Property, error) {
	props := make([]Property, 0, count)
	for i := 0; i < count; i++ {
		if len(data) < 2 {
			return nil, fmt.Errorf("echonet: truncated property list (property %d/%d)", i+1, count)
		}
		epc := data[0]
		pdc := int(data[1])
		if len(data) < 2+pdc {
			return nil, fmt.Errorf("echonet: truncated EDT for EPC %02X (property %d/%d)", epc, i+1, count)
		}
		edt := data[2 : 2+pdc]
		props = append(props, Property{EPC: epc, EDT: edt})
		data = data[2+pdc:]
	}
	return props, nil
}

// BuildGetRequest builds a Get (0x62) request frame for the given EPCs,
// addressed from the controller (SEOJ 05FF01) to the meter (DEOJ 028801).
// TID is always fixed at 0x0001, matching echonet.pl's hardcoded literal
// requests — nothing in this exchange ever reads the TID back.
func BuildGetRequest(epcs ...byte) []byte {
	buf := make([]byte, 0, 12+len(epcs)*2)
	buf = append(buf,
		0x10, 0x81, // EHD1, EHD2
		0x00, 0x01, // TID
		classGroupController, classController, instanceOne, // SEOJ (us)
		classGroupMeter, classMeter, instanceOne, // DEOJ (meter)
		0x62,            // ESV: Get
		byte(len(epcs)), // OPC
	)
	for _, epc := range epcs {
		buf = append(buf, epc, 0x00) // PDC=0 for a Get request (no EDT)
	}
	return buf
}
