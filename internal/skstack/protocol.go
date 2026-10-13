package skstack

import (
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// EVENT ids reported by the BP35A1, per the behavior SKSock.pm's _event()
// already encodes. Only the ones the Go client acts on are named; anything
// else is logged and otherwise ignored, matching the Perl fallback.
const (
	eventNAReceived                = 2
	eventUDPSendComplete           = 21
	eventActiveScanComplete        = 22
	eventSessionTerminating        = 24 // Perl auto-issues SKTERM on this; Go instead treats it as fatal (no in-process reconnect).
	eventPANAConnectionEstablished = 25
	eventPANATerminationRequested  = 26 // informational only — the terminal outcome arrives as 27/28/29.
	eventPANAGracefullyTerminated  = 27
	eventPANATerminationTimeout    = 28
	eventPANASessionTimeout        = 29
	eventSendLimitExceeded         = 32
	eventSendLimitReset            = 33
)

// ERXUDPMessage is a received UDP datagram as reported by the dongle. Data
// is the hex-ASCII encoding of the payload bytes exactly as the device puts
// it on the wire (SKSTACK notification lines are ASCII text, so binary
// payloads are hex-encoded) — decoding into an ECHONET Lite frame is the
// echonet package's job, not this one.
type ERXUDPMessage struct {
	Src, Dst         string
	SrcPort, DstPort string
	SrcMAC           string
	Secure           string
	Length           int
	Data             string
}

// parseERXUDP parses a hex-ASCII-framed ERXUDP line. The validation here
// is deliberately stricter than "does this look roughly right": a
// correctly hex-ASCII-framed line has no embedded whitespace and exactly
// as many hex characters as its declared length claims, so any deviation
// — extra fields, a length mismatch, non-hex characters — is a strong
// signal the dongle is actually in binary ERXUDP mode (see doROPT) or the
// stream is otherwise corrupted, not a benign firmware quirk to shrug
// off. The caller treats a parse failure as "drop this one datagram,
// keep running" (see dispatch in client.go), not fatal.
//
// sideParam must match hasSideParam(the dongle's SKVER string): BP35C0's
// SKSTACK (unlike BP35A1's) inserts a <SIDE> field between SECURED and
// DATALEN, per the BP35C0 command reference's ERXUDP event — so the field
// count and the index of everything from SECURED onward shifts by one.
func parseERXUDP(line string, sideParam bool) (ERXUDPMessage, error) {
	fields := strings.Fields(line)
	wantFields := 9
	secureIdx, lengthIdx, dataIdx := 6, 7, 8
	if sideParam {
		wantFields = 10
		secureIdx, lengthIdx, dataIdx = 6, 8, 9
	}
	if len(fields) != wantFields {
		return ERXUDPMessage{}, fmt.Errorf("malformed ERXUDP line (want %d fields, got %d): %q", wantFields, len(fields), line)
	}
	// LENGTH is hex (matching SendUDP's own outbound "%04X" length field
	// in client.go's SKSENDTO framing), not decimal.
	length64, err := strconv.ParseInt(fields[lengthIdx], 16, 32)
	if err != nil {
		return ERXUDPMessage{}, fmt.Errorf("malformed ERXUDP length %q: %w", fields[lengthIdx], err)
	}
	length := int(length64)
	data := fields[dataIdx]
	if len(data) != length*2 {
		return ERXUDPMessage{}, fmt.Errorf(
			"ERXUDP data length mismatch: declared %d bytes, got %d hex chars — check ROPT/WOPT (device may be in binary ERXUDP mode)",
			length, len(data))
	}
	if _, err := hex.DecodeString(data); err != nil {
		return ERXUDPMessage{}, fmt.Errorf(
			"ERXUDP data is not valid hex — check ROPT/WOPT (device may be in binary ERXUDP mode): %w", err)
	}
	return ERXUDPMessage{
		Src:     fields[1],
		Dst:     fields[2],
		SrcPort: fields[3],
		DstPort: fields[4],
		SrcMAC:  fields[5],
		Secure:  fields[secureIdx],
		Length:  length,
		Data:    data,
	}, nil
}

// hasSideParam reports whether the dongle's SKSTACK firmware requires the
// <SIDE> parameter on SKSCAN/SKSENDTO and includes it in ERXUDP lines.
//
// Empirically (comparing ROHM's two command references against a live
// EVER reply from each module): BP35A1 (EVER 1.2.10, command reference
// v1.3.2) uses the older 3-argument SKSCAN / no-SIDE ERXUDP form; BP35C0
// (EVER 1.5.2, command reference v1.2.0, Dual Stack Edition) requires
// SIDE everywhere, to disambiguate its B-route/HAN MAC planes. The split
// tracks the SKSTACK firmware generation, not the module model printed on
// the case, so this checks the (major, minor) from SKVER rather than any
// product string — there's no SKSTACK command that reports the module
// model directly.
//
// An unparsable version reply falls back to false (the older, narrower
// protocol): a 3-argument SKSCAN against a dongle that actually demands 4
// fails loudly (FAIL ER05) and is easy to diagnose from the log, whereas
// guessing "new protocol" against an old dongle would misparse every
// ERXUDP as malformed and silently drop all incoming data.
func hasSideParam(version string) bool {
	major, minor, ok := parseVersionMajorMinor(version)
	if !ok {
		return false
	}
	if major != 1 {
		return major > 1
	}
	return minor >= 5
}

// parseVersionMajorMinor extracts the leading "major.minor" from an EVER
// reply's x.x.x version string (e.g. "1.5.2" -> 1, 5, true).
func parseVersionMajorMinor(version string) (major, minor int, ok bool) {
	parts := strings.SplitN(strings.TrimSpace(version), ".", 3)
	if len(parts) < 2 {
		return 0, 0, false
	}
	major, errMajor := strconv.Atoi(parts[0])
	minor, errMinor := strconv.Atoi(parts[1])
	if errMajor != nil || errMinor != nil {
		return 0, 0, false
	}
	return major, minor, true
}

// parseEventLine parses a line of the form "EVENT <id> [addr]". ok is false
// if line isn't an EVENT line at all.
func parseEventLine(line string) (id int, addr string, ok bool) {
	fields := strings.Fields(line)
	if len(fields) < 2 || fields[0] != "EVENT" {
		return 0, "", false
	}
	n, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, "", false
	}
	if len(fields) >= 3 {
		addr = fields[2]
	}
	return n, addr, true
}

// peerInfo is what SKSCAN's EPANDESC block reports about a discovered
// coordinator (the meter's Wi-SUN adapter), mirroring SKSock.pm's
// sk_peerinfo fields.
type peerInfo struct {
	Channel     string
	ChannelPage string
	PanID       string
	Addr        string
	LQI         string
	PairID      string
}

var (
	reChannelPage = regexp.MustCompile(`Channel Page:(\w+)`)
	reChannel     = regexp.MustCompile(`Channel:(\w+)`)
	rePanID       = regexp.MustCompile(`Pan ID:(\w+)`)
	reAddr        = regexp.MustCompile(`Addr:(\w+)`)
	reLQI         = regexp.MustCompile(`LQI:(\w+)`)
	rePairID      = regexp.MustCompile(`PairID:(\w+)`)
)

func parsePeerField(p *peerInfo, line string) {
	if m := reChannelPage.FindStringSubmatch(line); m != nil {
		p.ChannelPage = m[1]
	}
	if m := reChannel.FindStringSubmatch(line); m != nil {
		p.Channel = m[1]
	}
	if m := rePanID.FindStringSubmatch(line); m != nil {
		p.PanID = m[1]
	}
	if m := reAddr.FindStringSubmatch(line); m != nil {
		p.Addr = m[1]
	}
	if m := reLQI.FindStringSubmatch(line); m != nil {
		p.LQI = m[1]
	}
	if m := rePairID.FindStringSubmatch(line); m != nil {
		p.PairID = m[1]
	}
}
