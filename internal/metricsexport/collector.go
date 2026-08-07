// Package metricsexport implements the Prometheus /metrics surface,
// equivalent to echonet.pl's makeHttpResult and the scrape branch of its
// main loop.
package metricsexport

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/walkure/eclite/internal/meter"
)

// Requester issues the "give me a fresh reading" query for the next
// scrape — skstack.Client.SendUDP(echonet.BuildGetRequest(...)) in
// production. Kept as a narrow function type so this package doesn't need
// to depend on skstack.
type Requester func()

// Collector implements prometheus.Collector over a meter.State. Collect
// emits whatever readings are currently cached, then fires an async
// refresh request for the *next* scrape — reproducing echonet.pl's
// documented "instantaneous but actually last-known" /metrics behavior
// (see README) through the Collector's natural extension point instead of
// hand-rolled HTTP handling.
type Collector struct {
	state   *meter.State
	request Requester

	wattDesc *prometheus.Desc
	kwhDesc  *prometheus.Desc
}

// NewCollector builds a Collector. labels become ConstLabels on both
// metrics, matching config.yaml's prometheus.labels. request may be nil
// (Collect simply won't trigger a refresh).
func NewCollector(state *meter.State, labels map[string]string, request Requester) *Collector {
	return &Collector{
		state:    state,
		request:  request,
		wattDesc: prometheus.NewDesc("watt_now", "The Current Power Consumption", nil, labels),
		kwhDesc:  prometheus.NewDesc("kwh_total", "The Integrated Power Consumption", nil, labels),
	}
}

func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.wattDesc
	ch <- c.kwhDesc
}

func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	snap := c.state.Snapshot()
	if snap.HaveWatt {
		ch <- prometheus.MustNewConstMetric(c.wattDesc, prometheus.GaugeValue, float64(snap.Watt))
	}
	if snap.HaveKWh {
		ch <- prometheus.MustNewConstMetric(c.kwhDesc, prometheus.CounterValue, snap.KWh)
	}
	if snap.Ready && c.request != nil {
		c.request()
	}
}
