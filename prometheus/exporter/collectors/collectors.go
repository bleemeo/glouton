package collectors

import "github.com/prometheus/client_golang/prometheus"

// Collectors merge multiple collector in one.
type Collectors []prometheus.Collector

// Describe implement prometheus.Collector. It call sequentially each Describe method.
func (cs Collectors) Describe(ch chan<- *prometheus.Desc) {
	for _, c := range cs {
		c.Describe(ch)
	}
}

// Collect implement prometheus.Collector. It call sequentially each Collect method.
func (cs Collectors) Collect(ch chan<- prometheus.Metric) {
	for _, c := range cs {
		c.Collect(ch)
	}
}
