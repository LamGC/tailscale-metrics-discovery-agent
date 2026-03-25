package agent

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/lamgc/tailscale-service-discovery-agent/internal/protocol"
)

// agentCollector implements prometheus.Collector by reading live state from
// the service registry on each scrape.
type agentCollector struct {
	reg          *registry
	servicesDesc *prometheus.Desc
}

func newAgentCollector(reg *registry) *agentCollector {
	return &agentCollector{
		reg: reg,
		servicesDesc: prometheus.NewDesc(
			"tsd_agent_services",
			"Number of registered services grouped by type.",
			[]string{"type"}, nil,
		),
	}
}

func (ac *agentCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- ac.servicesDesc
}

func (ac *agentCollector) Collect(ch chan<- prometheus.Metric) {
	entries := ac.reg.list()
	typeCounts := map[string]float64{
		string(protocol.ServiceTypeStatic): 0,
		string(protocol.ServiceTypeBucket): 0,
		string(protocol.ServiceTypeProxy):  0,
	}
	for _, e := range entries {
		typeCounts[string(e.Type)]++
	}
	for t, n := range typeCounts {
		ch <- prometheus.MustNewConstMetric(ac.servicesDesc, prometheus.GaugeValue, n, t)
	}
}
