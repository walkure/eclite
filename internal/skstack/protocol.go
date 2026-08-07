package skstack

import (
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

func parseERXUDP(line string) (ERXUDPMessage, error) {
	fields := strings.Fields(line)
	if len(fields) < 9 {
		return ERXUDPMessage{}, fmt.Errorf("malformed ERXUDP line: %q", line)
	}
	length, err := strconv.Atoi(fields[7])
	if err != nil {
		return ERXUDPMessage{}, fmt.Errorf("malformed ERXUDP length %q: %w", fields[7], err)
	}
	return ERXUDPMessage{
		Src:     fields[1],
		Dst:     fields[2],
		SrcPort: fields[3],
		DstPort: fields[4],
		SrcMAC:  fields[5],
		Secure:  fields[6],
		Length:  length,
		Data:    fields[8],
	}, nil
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
