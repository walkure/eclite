// Package meter holds the mutable readings this daemon has learned from the
// smart meter and the business rules for updating them from decoded
// ECHONET Lite frames — the equivalent of echonet.pl's $mag/$kwh_mag/$watt/
// $kwh/$update globals and the per-EPC branches of its parse() function.
package meter

import (
	"log/slog"
	"sync"
	"time"

	"github.com/walkure/eclite/internal/echonet"
)

// Clock abstracts time.Now so WDT staleness can be tested without real
// sleeps.
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// State holds the readings, guarded by an internal mutex since it's
// updated from the ERXUDP-consuming goroutine and read from HTTP handler
// goroutines concurrently.
//
// magKnown/kwhMagKnown are explicit flags rather than SKSock.pm's "0 means
// unknown" overload on $mag/$kwh_mag — that overload made a genuine future
// 0xD3 EDT value of 1 indistinguishable from the Get_SNA fallback sentinel.
type State struct {
	mu sync.Mutex

	logger *slog.Logger
	clock  Clock

	magKnown bool
	mag      float64 // unit multiplier, from EPC 0xE1 via echonet.Magnitude

	kwhMagKnown bool
	kwhMag      uint32 // coefficient, from EPC 0xD3 (or 1, defaulted on Get_SNA)

	watt     int32
	haveWatt bool

	kwh     float64
	haveKWh bool

	lastUpdate time.Time
}

// New creates a State. logger/clock may be nil (default to slog.Default()
// and the real wall clock respectively).
func New(logger *slog.Logger, clock Clock) *State {
	if logger == nil {
		logger = slog.Default()
	}
	if clock == nil {
		clock = realClock{}
	}
	return &State{logger: logger, clock: clock}
}

// Now returns the current time per this State's clock, so callers don't
// need to know whether a real or fake clock is in use.
func (s *State) Now() time.Time { return s.clock.Now() }

// MarkConnected seeds the staleness clock at connection time. SKSock.pm's
// WDT can't trip until $update is set by a first successful watt reading —
// so a meter that establishes a PANA session but then never answers a
// single Get request hangs forever undetected there. Seeding lastUpdate
// here means the WDT window starts counting from connection, closing that
// gap: "connected but silent" now looks the same as "was fine, then went
// quiet" to the watchdog.
func (s *State) MarkConnected(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastUpdate.IsZero() {
		s.lastUpdate = now
	}
}

// Snapshot is a consistent, lock-free-to-read copy of the current readings.
type Snapshot struct {
	Watt     int32
	HaveWatt bool
	KWh      float64
	HaveKWh  bool
	// Ready mirrors echonet.pl's "$mag > 0 && $kwh_mag > 0" /metrics gate:
	// both the unit magnitude and the coefficient are known.
	Ready      bool
	LastUpdate time.Time
}

func (s *State) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Snapshot{
		Watt:       s.watt,
		HaveWatt:   s.haveWatt,
		KWh:        s.kwh,
		HaveKWh:    s.haveKWh,
		Ready:      s.magKnown && s.kwhMagKnown,
		LastUpdate: s.lastUpdate,
	}
}

// StaleFor reports how long it's been since the last watt reading (or
// since MarkConnected, if no reading has arrived yet) and whether that
// exceeds window. If lastUpdate is still zero (MarkConnected was never
// called), it reports not-stale — that's a defensive fallback, not the
// primary gate; production code always calls MarkConnected right after a
// successful handshake.
func (s *State) StaleFor(window time.Duration) (dur time.Duration, stale bool) {
	s.mu.Lock()
	last := s.lastUpdate
	s.mu.Unlock()
	if last.IsZero() {
		return 0, false
	}
	dur = s.clock.Now().Sub(last)
	return dur, dur > window
}

// FrameResult tells the caller what further I/O action (if any) is needed
// after HandleFrame's business logic ran. Sending anything is the caller's
// job (it holds the skstack.Client) — this package never touches the wire.
type FrameResult struct {
	// ResendReducedEPCs is non-nil the first time a Get_SNA response for
	// the (optional) 0xD3 coefficient property is seen: the caller should
	// re-issue a Get for these EPCs, omitting 0xD3.
	ResendReducedEPCs []byte
}

// HandleFrame applies a decoded frame's effect to the state, mirroring
// echonet.pl's parse(). Frames with an ESV other than Get_SNA/INF/Get_Res
// are ignored, matching the Perl original's `return unless 72 or 73`
// (after its Get_SNA special case).
func (s *State) HandleFrame(frame *echonet.Frame) FrameResult {
	switch frame.ESV {
	case echonet.ESVGetSNA:
		return s.handleGetSNA()
	case echonet.ESVInf, echonet.ESVGetRes:
		s.handleProperties(frame.Properties)
	default:
		s.logger.Debug("meter: ignoring frame with unhandled ESV", "esv", frame.ESV)
	}
	return FrameResult{}
}

func (s *State) handleGetSNA() FrameResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.kwhMagKnown {
		// Already known (a real 0xD3 answer, or this same fallback taken
		// earlier) — a later stray Get_SNA is dropped, matching
		// echonet.pl's `$kwh_mag == 0` guard.
		return FrameResult{}
	}
	s.kwhMag = 1 // ECHONET Lite default coefficient when the property is unimplemented
	s.kwhMagKnown = true
	s.logger.Info("meter: coefficient (0xD3) not implemented by meter, defaulting to 1")
	return FrameResult{ResendReducedEPCs: []byte{
		echonet.EPCUnit, echonet.EPCCumulativeEnergy, echonet.EPCInstantaneousWatt, echonet.EPCApplianceVersion,
	}}
}

func (s *State) handleProperties(props []echonet.Property) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range props {
		switch p.EPC {
		case echonet.EPCCoefficient:
			v, err := echonet.DecodeCoefficient(p.EDT)
			if err != nil {
				s.logger.Warn("meter: malformed 0xD3, ignoring", "err", err)
				continue
			}
			s.kwhMag = v
			s.kwhMagKnown = true
			s.logger.Info("meter: coefficient known", "kwh_mag", v)

		case echonet.EPCUnit:
			code, err := echonet.DecodeUnitCode(p.EDT)
			if err != nil {
				s.logger.Warn("meter: malformed 0xE1, ignoring", "err", err)
				continue
			}
			if !echonet.KnownMagnitude(code) {
				s.logger.Warn("meter: unrecognized unit code, defaulting magnitude to 1", "code", code)
			}
			s.mag = echonet.Magnitude(code)
			s.magKnown = true
			s.logger.Info("meter: magnitude known", "mag", s.mag)

		case echonet.EPCCumulativeEnergy:
			if !s.kwhMagKnown || !s.magKnown {
				// Benign startup-ordering race (a kWh reply arriving
				// before the unit/coefficient replies) — not a stuck
				// meter. echonet.pl used to `die` the whole process
				// here, which also silently dropped any 0xE7 co-arriving
				// in the same frame (both are requested together); skip
				// just this property and keep processing the rest.
				s.logger.Warn("meter: 0xE0 received before coefficient/unit known, skipping this reading")
				continue
			}
			raw, err := echonet.DecodeCumulativeEnergyRaw(p.EDT)
			if err != nil {
				s.logger.Warn("meter: malformed 0xE0, ignoring", "err", err)
				continue
			}
			s.kwh = float64(raw) * float64(s.kwhMag) * s.mag
			s.haveKWh = true

		case echonet.EPCInstantaneousWatt:
			v, err := echonet.DecodeInstantaneousWatt(p.EDT)
			if err != nil {
				s.logger.Warn("meter: malformed 0xE7, ignoring", "err", err)
				continue
			}
			s.watt = v
			s.haveWatt = true
			// The only place lastUpdate advances — WDT staleness is
			// watt-reading-driven only, matching echonet.pl (a
			// kWh-only update doesn't reset it).
			s.lastUpdate = s.clock.Now()

		case echonet.EPCApplianceVersion:
			release, revision, err := echonet.DecodeApplianceVersion(p.EDT)
			if err != nil {
				s.logger.Warn("meter: malformed 0x82, ignoring", "err", err)
				continue
			}
			s.logger.Info("meter: appliance version", "release", string(release), "revision", revision)

		case echonet.EPCInstantaneousAmpere:
			amp, err := echonet.DecodeInstantaneousAmpere(p.EDT)
			if err != nil {
				s.logger.Warn("meter: malformed 0xE8, ignoring", "err", err)
				continue
			}
			// Computed but not persisted/exported — see README: the
			// 0.1A wire resolution doesn't match the meter's actual
			// ~1A accuracy, so it was judged not worth recording.
			s.logger.Debug("meter: instantaneous ampere (not exported)", "amperes", amp)

		default:
			s.logger.Debug("meter: ignoring unrecognized EPC", "epc", p.EPC)
		}
	}
}
