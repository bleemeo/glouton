package collectors

import (
	"glouton/types"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// Collectors merge multiple collector in one.
type Collectors []prometheus.Collector

// Describe implement prometheus.Collector. It call sequentially each Describe method.
func (cs Collectors) Describe(ch chan<- *prometheus.Desc) {
	for _, c := range cs {
		c.Describe(ch)
	}
}

// Collect implement prometheus.Collector. It call concurrently each Collect method.
func (cs Collectors) Collect(ch chan<- prometheus.Metric) {
	wg := sync.WaitGroup{}
	wg.Add(len(cs))

	for _, c := range cs {
		go func(c prometheus.Collector) {
			defer types.ProcessPanic()

			c.Collect(ch)
			wg.Done()
		}(c)
	}

	wg.Wait()
}
