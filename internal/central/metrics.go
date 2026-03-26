package central

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/LamGC/tailscale-metrics-discovery-agent/internal/protocol"
)

// centralCollector implements prometheus.Collector by reading live state from
// the collector on each scrape, ensuring metrics are always up to date.
type centralCollector struct {
	col         *collector
	peersDesc   *prometheus.Desc
	targetsDesc *prometheus.Desc
}

func newCentralCollector(col *collector) *centralCollector {
	return &centralCollector{
		col: col,
		peersDesc: prometheus.NewDesc(
			"tsd_central_peers",
			"Number of Agent peers grouped by health status.",
			[]string{"health"}, nil,
		),
		targetsDesc: prometheus.NewDesc(
			"tsd_central_sd_targets_total",
			"Total number of Prometheus SD targets currently cached by Central.",
			nil, nil,
		),
	}
}

func (cc *centralCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- cc.peersDesc
	ch <- cc.targetsDesc
}

func (cc *centralCollector) Collect(ch chan<- prometheus.Metric) {
	peers := cc.col.Peers()
	healthCounts := map[string]float64{
		string(protocol.AgentHealthOK):           0,
		string(protocol.AgentHealthOffline):      0,
		string(protocol.AgentHealthTimeout):      0,
		string(protocol.AgentHealthUnauthorized): 0,
		string(protocol.AgentHealthUnknown):      0,
	}
	for _, p := range peers {
		healthCounts[string(p.Health)]++
	}
	for h, n := range healthCounts {
		ch <- prometheus.MustNewConstMetric(cc.peersDesc, prometheus.GaugeValue, n, h)
	}

	targets := cc.col.Targets()
	ch <- prometheus.MustNewConstMetric(cc.targetsDesc, prometheus.GaugeValue, float64(len(targets)))
}
