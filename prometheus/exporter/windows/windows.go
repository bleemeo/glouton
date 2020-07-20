// +build windows

package windows

import (
	"fmt"
	"glouton/logger"
	"sync"
	"time"

	"github.com/prometheus-community/windows_exporter/collector"
	"github.com/prometheus/client_golang/prometheus"
)

const maxScrapeDuration time.Duration = 9500 * time.Millisecond

// this code mainly comes from https://github.com/prometheus-community/windows_exporter/blob/f1384759cb04f8a66cd0954e2eff4bca81b2caa4/exporter.go#L256
type windowsCollector struct {
	collectors map[string]collector.Collector
}

var (
	scrapeDurationDesc = prometheus.NewDesc(
		prometheus.BuildFQName(collector.Namespace, "exporter", "collector_duration_seconds"),
		"windows_exporter: Duration of a collection.",
		[]string{"collector"},
		nil,
	)
	scrapeSuccessDesc = prometheus.NewDesc(
		prometheus.BuildFQName(collector.Namespace, "exporter", "collector_success"),
		"windows_exporter: Whether the collector was successful.",
		[]string{"collector"},
		nil,
	)
	scrapeTimeoutDesc = prometheus.NewDesc(
		prometheus.BuildFQName(collector.Namespace, "exporter", "collector_timeout"),
		"windows_exporter: Whether the collector timed out.",
		[]string{"collector"},
		nil,
	)
	snapshotDuration = prometheus.NewDesc(
		prometheus.BuildFQName(collector.Namespace, "exporter", "perflib_snapshot_duration_seconds"),
		"Duration of perflib snapshot capture",
		nil,
		nil,
	)
)

// Describe implements prometheus.Collector.
func (coll windowsCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- scrapeDurationDesc
	ch <- scrapeSuccessDesc
}

type collectorOutcome int

const (
	pending collectorOutcome = iota
	success
	failed
)

// Collect sends the collected metrics from each of the collectors to
// prometheus.
func (coll windowsCollector) Collect(ch chan<- prometheus.Metric) {
	t := time.Now()
	cs := keys(coll.collectors)
	scrapeContext, err := collector.PrepareScrapeContext(cs)
	ch <- prometheus.MustNewConstMetric(
		snapshotDuration,
		prometheus.GaugeValue,
		time.Since(t).Seconds(),
	)
	if err != nil {
		ch <- prometheus.NewInvalidMetric(scrapeSuccessDesc, fmt.Errorf("failed to prepare scrape: %v", err))
		return
	}

	wg := sync.WaitGroup{}
	wg.Add(len(coll.collectors))
	collectorOutcomes := make(map[string]collectorOutcome)
	for name := range coll.collectors {
		collectorOutcomes[name] = pending
	}

	metricsBuffer := make(chan prometheus.Metric)
	l := sync.Mutex{}
	finished := false
	go func() {
		for m := range metricsBuffer {
			l.Lock()
			if !finished {
				ch <- m
			}
			l.Unlock()
		}
	}()

	for name, c := range coll.collectors {
		go func(name string, c collector.Collector) {
			defer wg.Done()
			outcome := execute(name, c, scrapeContext, metricsBuffer)
			l.Lock()
			if !finished {
				collectorOutcomes[name] = outcome
			}
			l.Unlock()
		}(name, c)
	}

	allDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(allDone)
		close(metricsBuffer)
	}()

	// Wait until either all collectors finish, or timeout expires
	select {
	case <-allDone:
	case <-time.After(maxScrapeDuration):
	}

	l.Lock()
	finished = true

	remainingCollectorNames := make([]string, 0)
	for name, outcome := range collectorOutcomes {
		var successValue, timeoutValue float64
		if outcome == pending {
			timeoutValue = 1.0
			remainingCollectorNames = append(remainingCollectorNames, name)
		}
		if outcome == success {
			successValue = 1.0
		}

		ch <- prometheus.MustNewConstMetric(
			scrapeSuccessDesc,
			prometheus.GaugeValue,
			successValue,
			name,
		)
		ch <- prometheus.MustNewConstMetric(
			scrapeTimeoutDesc,
			prometheus.GaugeValue,
			timeoutValue,
			name,
		)
	}

	if len(remainingCollectorNames) > 0 {
		logger.V(1).Printf("windows_exporter timed out waiting for collectors %v", remainingCollectorNames)
	}

	l.Unlock()
}

func execute(name string, c collector.Collector, ctx *collector.ScrapeContext, ch chan<- prometheus.Metric) collectorOutcome {
	t := time.Now()
	err := c.Collect(ctx, ch)
	duration := time.Since(t).Seconds()
	ch <- prometheus.MustNewConstMetric(
		scrapeDurationDesc,
		prometheus.GaugeValue,
		duration,
		name,
	)

	if err != nil {
		logger.V(2).Printf("collector %s failed after %fs: %s", name, duration, err)
		return failed
	}
	return success
}

func NewCollector(enabledCollectors []string) (prometheus.Collector, error) {
	collectors := map[string]collector.Collector{}

	for _, name := range enabledCollectors {
		c, err := collector.Build(name)
		if err != nil {
			logger.V(0).Printf("windows_exporter: couldn't build the list of collectors: %s", err)
			return nil, err
		}
		collectors[name] = c
	}

	logger.V(2).Printf("windows_exporter: the enabled collectors are %v", keys(collectors))

	return &windowsCollector{collectors: collectors}, nil
}

func keys(m map[string]collector.Collector) []string {
	ret := make([]string, 0, len(m))
	for key := range m {
		ret = append(ret, key)
	}
	return ret
}
