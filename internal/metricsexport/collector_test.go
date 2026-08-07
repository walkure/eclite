package metricsexport

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/walkure/eclite/internal/echonet"
	"github.com/walkure/eclite/internal/meter"
)

func newReadyState(t *testing.T) *meter.State {
	t.Helper()
	s := meter.New(slog.Default(), fixedClock{})
	s.HandleFrame(&echonet.Frame{
		ESV: echonet.ESVGetRes,
		Properties: []echonet.Property{
			{EPC: echonet.EPCCoefficient, EDT: []byte{0x00, 0x00, 0x00, 0x01}},
			{EPC: echonet.EPCUnit, EDT: []byte{0x00}},
			{EPC: echonet.EPCCumulativeEnergy, EDT: []byte{0x00, 0x00, 0x27, 0x10}},  // 10000
			{EPC: echonet.EPCInstantaneousWatt, EDT: []byte{0x00, 0x00, 0x03, 0xE8}}, // 1000
		},
	})
	return s
}

type fixedClock struct{}

func (fixedClock) Now() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }

func TestCollect_EmitsCachedValues(t *testing.T) {
	state := newReadyState(t)
	c := NewCollector(state, nil, nil)

	want := `
		# HELP kwh_total The Integrated Power Consumption
		# TYPE kwh_total counter
		kwh_total 10000
		# HELP watt_now The Current Power Consumption
		# TYPE watt_now gauge
		watt_now 1000
	`
	if err := testutil.CollectAndCompare(c, strings.NewReader(want)); err != nil {
		t.Fatalf("unexpected collecting result:\n%v", err)
	}
}

func TestCollect_WithLabels(t *testing.T) {
	state := newReadyState(t)
	c := NewCollector(state, map[string]string{"place": "home"}, nil)

	want := `
		# HELP kwh_total The Integrated Power Consumption
		# TYPE kwh_total counter
		kwh_total{place="home"} 10000
		# HELP watt_now The Current Power Consumption
		# TYPE watt_now gauge
		watt_now{place="home"} 1000
	`
	if err := testutil.CollectAndCompare(c, strings.NewReader(want)); err != nil {
		t.Fatalf("unexpected collecting result:\n%v", err)
	}
}

func TestCollect_NotYetReady_EmitsNothing(t *testing.T) {
	state := meter.New(slog.Default(), fixedClock{})
	c := NewCollector(state, nil, nil)

	want := `
	`
	if err := testutil.CollectAndCompare(c, strings.NewReader(want)); err != nil {
		t.Fatalf("unexpected collecting result:\n%v", err)
	}
}

func TestCollect_TriggersRefreshOnlyWhenReady(t *testing.T) {
	calls := 0
	notReady := meter.New(slog.Default(), fixedClock{})
	c := NewCollector(notReady, nil, func() { calls++ })
	reg := prometheus.NewPedanticRegistry()
	reg.MustRegister(c)
	if _, err := reg.Gather(); err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if calls != 0 {
		t.Fatalf("request calls = %d, want 0 when not ready", calls)
	}

	ready := newReadyState(t)
	c2 := NewCollector(ready, nil, func() { calls++ })
	reg2 := prometheus.NewPedanticRegistry()
	reg2.MustRegister(c2)
	if _, err := reg2.Gather(); err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if calls != 1 {
		t.Fatalf("request calls = %d, want 1 when ready", calls)
	}
}
