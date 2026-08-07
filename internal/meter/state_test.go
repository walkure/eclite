package meter

import (
	"sync"
	"testing"
	"time"

	"github.com/walkure/eclite/internal/echonet"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(start time.Time) *fakeClock { return &fakeClock{now: start} }

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

var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func frame(esv byte, props ...echonet.Property) *echonet.Frame {
	return &echonet.Frame{ESV: esv, Properties: props}
}

func TestHandleFrame_FullSequence(t *testing.T) {
	clock := newFakeClock(epoch)
	s := New(nil, clock)

	s.HandleFrame(frame(echonet.ESVGetRes,
		echonet.Property{EPC: echonet.EPCCoefficient, EDT: []byte{0x00, 0x00, 0x00, 0x01}},
		echonet.Property{EPC: echonet.EPCUnit, EDT: []byte{0x00}},                                // magnitude 1
		echonet.Property{EPC: echonet.EPCCumulativeEnergy, EDT: []byte{0x00, 0x00, 0x27, 0x10}},  // 10000
		echonet.Property{EPC: echonet.EPCInstantaneousWatt, EDT: []byte{0x00, 0x00, 0x03, 0xE8}}, // 1000
	))

	snap := s.Snapshot()
	if !snap.Ready {
		t.Fatalf("Ready = false, want true after coefficient+unit known")
	}
	if !snap.HaveKWh || snap.KWh != 10000 {
		t.Fatalf("KWh = %v (have=%v), want 10000", snap.KWh, snap.HaveKWh)
	}
	if !snap.HaveWatt || snap.Watt != 1000 {
		t.Fatalf("Watt = %v (have=%v), want 1000", snap.Watt, snap.HaveWatt)
	}
	if snap.LastUpdate != epoch {
		t.Fatalf("LastUpdate = %v, want %v", snap.LastUpdate, epoch)
	}
}

func TestHandleFrame_GetSNA_FirstTime(t *testing.T) {
	s := New(nil, newFakeClock(epoch))

	result := s.HandleFrame(frame(echonet.ESVGetSNA))
	if result.ResendReducedEPCs == nil {
		t.Fatalf("HandleFrame: want ResendReducedEPCs set on first Get_SNA")
	}
	want := []byte{echonet.EPCUnit, echonet.EPCCumulativeEnergy, echonet.EPCInstantaneousWatt, echonet.EPCApplianceVersion}
	if len(result.ResendReducedEPCs) != len(want) {
		t.Fatalf("ResendReducedEPCs = %v, want %v", result.ResendReducedEPCs, want)
	}
	for i, epc := range want {
		if result.ResendReducedEPCs[i] != epc {
			t.Fatalf("ResendReducedEPCs[%d] = %#x, want %#x", i, result.ResendReducedEPCs[i], epc)
		}
	}

	// kwhMag should now be defaulted to 1 and known.
	snap := s.Snapshot()
	if snap.Ready {
		t.Fatalf("Ready = true, want false (unit/mag still unknown)")
	}
}

func TestHandleFrame_GetSNA_IgnoredOnceKnown(t *testing.T) {
	s := New(nil, newFakeClock(epoch))

	// A real 0xD3 arrives first.
	s.HandleFrame(frame(echonet.ESVGetRes,
		echonet.Property{EPC: echonet.EPCCoefficient, EDT: []byte{0x00, 0x00, 0x00, 0x02}},
	))

	// A later stray Get_SNA must be dropped, not overwrite the real value.
	result := s.HandleFrame(frame(echonet.ESVGetSNA))
	if result.ResendReducedEPCs != nil {
		t.Fatalf("HandleFrame: want no resend once coefficient already known, got %v", result.ResendReducedEPCs)
	}
}

func TestHandleFrame_CumulativeEnergyBeforeReady_SkipsNotCrashes(t *testing.T) {
	s := New(nil, newFakeClock(epoch))

	// 0xE0 arrives in the same frame as 0xE7, before coefficient/unit are
	// known. The regression this guards: echonet.pl's `die` here used to
	// also drop the co-arriving 0xE7 (and crash the process). Both must
	// now be handled independently: 0xE0 skipped, 0xE7 still applied.
	s.HandleFrame(frame(echonet.ESVGetRes,
		echonet.Property{EPC: echonet.EPCCumulativeEnergy, EDT: []byte{0x00, 0x00, 0x00, 0x01}},
		echonet.Property{EPC: echonet.EPCInstantaneousWatt, EDT: []byte{0x00, 0x00, 0x00, 0x64}}, // 100
	))

	snap := s.Snapshot()
	if snap.HaveKWh {
		t.Fatalf("HaveKWh = true, want false (0xE0 arrived before ready, should be skipped)")
	}
	if !snap.HaveWatt || snap.Watt != 100 {
		t.Fatalf("Watt = %v (have=%v), want 100 — 0xE7 in the same frame must still apply", snap.Watt, snap.HaveWatt)
	}
}

func TestHandleFrame_UnrecognizedESVIgnored(t *testing.T) {
	s := New(nil, newFakeClock(epoch))
	result := s.HandleFrame(frame(0x71, echonet.Property{EPC: echonet.EPCInstantaneousWatt, EDT: []byte{0, 0, 0, 1}}))
	if result.ResendReducedEPCs != nil {
		t.Fatalf("HandleFrame: unexpected resend for unrelated ESV")
	}
	if s.Snapshot().HaveWatt {
		t.Fatalf("HaveWatt = true, want false — unrecognized ESV must be ignored entirely")
	}
}

func TestMarkConnected_SeedsOnlyOnce(t *testing.T) {
	clock := newFakeClock(epoch)
	s := New(nil, clock)

	s.MarkConnected(clock.Now())
	if got := s.Snapshot().LastUpdate; got != epoch {
		t.Fatalf("LastUpdate = %v, want %v", got, epoch)
	}

	clock.Advance(time.Minute)
	s.MarkConnected(clock.Now()) // must not clobber a value already set
	if got := s.Snapshot().LastUpdate; got != epoch {
		t.Fatalf("LastUpdate = %v after second MarkConnected, want unchanged %v", got, epoch)
	}
}

func TestStaleFor(t *testing.T) {
	clock := newFakeClock(epoch)
	s := New(nil, clock)

	if _, stale := s.StaleFor(time.Minute); stale {
		t.Fatalf("StaleFor before MarkConnected: want not stale")
	}

	s.MarkConnected(clock.Now())
	if _, stale := s.StaleFor(time.Minute); stale {
		t.Fatalf("StaleFor immediately after MarkConnected: want not stale")
	}

	clock.Advance(2 * time.Minute)
	dur, stale := s.StaleFor(time.Minute)
	if !stale {
		t.Fatalf("StaleFor after window elapsed: want stale")
	}
	if dur != 2*time.Minute {
		t.Fatalf("StaleFor duration = %v, want 2m", dur)
	}
}

func TestStaleFor_ResetByWattReading(t *testing.T) {
	clock := newFakeClock(epoch)
	s := New(nil, clock)
	s.MarkConnected(clock.Now())

	clock.Advance(30 * time.Second)
	s.HandleFrame(frame(echonet.ESVGetRes,
		echonet.Property{EPC: echonet.EPCInstantaneousWatt, EDT: []byte{0, 0, 0, 1}},
	))

	clock.Advance(40 * time.Second) // 40s since the reading, 70s since connect
	if _, stale := s.StaleFor(time.Minute); stale {
		t.Fatalf("StaleFor: want not stale — the watt reading should have reset the clock")
	}
}
