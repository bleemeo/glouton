package collectors

import (
	"sync"
	"time"

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
			c.Collect(ch)
			wg.Done()
		}(c)
	}

	wg.Wait()
}

// TickingCollector is a prometheus collector that collect metrics only once per x calls to Collect(),
// where x is chosen when creating the TickingCollector.
type TickingCollector struct {
	collector prometheus.Collector
	l         *sync.Mutex
	ticker    *time.Ticker
}

// NewTickingCollector creates a collector that only collect metrics once every refreshRateSeconds.
func NewTickingCollector(collector prometheus.Collector, refreshRateSeconds int) TickingCollector {
	res := TickingCollector{
		collector: collector,
		l:         &sync.Mutex{},
		ticker:    time.NewTicker(time.Duration(refreshRateSeconds) * time.Second),
	}

	// TODO: have a different behavior between static probes and remote probes

	return res
}

// Describe implement prometheus.Collector.
func (dc TickingCollector) Describe(ch chan<- *prometheus.Desc) {
	dc.collector.Describe(ch)
}

// Collect implement prometheus.Collector.
func (dc TickingCollector) Collect(ch chan<- prometheus.Metric) {
	dc.l.Lock()
	defer dc.l.Unlock()

	select {
	case <-dc.ticker.C:
		dc.collector.Collect(ch)
	default:
	}
}
