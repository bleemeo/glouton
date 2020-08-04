// This code mainly comes from https://github.com/prometheus-community/windows_exporter/blob/f1384759cb04f8a66cd0954e2eff4bca81b2caa4/exporter.go.
// Please refer to its license at https://github.com/prometheus-community/windows_exporter/blob/master/LICENSE.

// +build windows

package windows

import (
	"fmt"
	"glouton/inputs"
	"glouton/logger"
	"regexp"
	"sync"
	"time"

	// necessary for the go:linkname annotation
	_ "unsafe"

	"github.com/prometheus-community/windows_exporter/collector"
	"github.com/prometheus/client_golang/prometheus"
	"gopkg.in/alecthomas/kingpin.v2"
)

const maxScrapeDuration time.Duration = 9500 * time.Millisecond

type windowsCollector struct {
	collectors map[string]collector.Collector
}

//nolint: gochecknoglobals
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

// the good news is that '^(^value$)$' will match 'value', so we can merge all those regexps together
// (if this property was false, it would have been difficult to merge them, givent the way the
// regexps are processed in windows_exporter: https://github.com/prometheus-community/windows_exporter/blob/21a02c4fbec4304f883ed7957bd81045d2f0c133/collector/logical_disk.go#L153)
func mergeRegexps(regexps []*regexp.Regexp) string {
	var result string

	for _, re := range regexps {
		if result != "" {
			result += "|"
		}

		result += fmt.Sprintf("(%s)", re.String())
	}

	return result
}

func NewCollector(enabledCollectors []string, options inputs.CollectorConfig) (prometheus.Collector, error) {
	var args []string

	if len(options.IODiskWhitelist) > 0 {
		args = append(args, fmt.Sprintf("--collector.logical_disk.volume-whitelist=%s", mergeRegexps(options.IODiskWhitelist)))
	}

	if len(options.IODiskBlacklist) > 0 {
		args = append(args, fmt.Sprintf("--collector.logical_disk.volume-blacklist=%s", mergeRegexps(options.IODiskBlacklist)))
	}

	if len(options.NetIfBlacklist) > 0 {
		var blacklist string

		for _, inter := range options.NetIfBlacklist {
			if blacklist != "" {
				blacklist += "|"
			}

			blacklist += fmt.Sprintf("(%s)", inter)
		}

		args = append(args, fmt.Sprintf("--collector.net.nic-blacklist=%s", blacklist))
	}

	if _, err := kingpin.CommandLine.Parse(args); err != nil {
		return nil, fmt.Errorf("windows_exporter: kingpin initialization failed: %v", err)
	}

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
