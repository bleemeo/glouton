// Copyright 2015-2025 Bleemeo
//
// bleemeo.com an infrastructure monitoring solution in the Cloud
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// This code comes from https://github.com/prometheus-community/windows_exporter/blob/f1384759cb04f8a66cd0954e2eff4bca81b2caa4/exporter.go.
// To see its license, please refer to https://github.com/prometheus-community/windows_exporter/blob/master/LICENSE.
// The only modification is switching the (deprecated) log provider from prometheus/common/log (backed by
// logrus) to our own logger.

//go:build windows

package windows

import (
	"fmt"
	"sync"
	"time"

	"github.com/bleemeo/glouton/crashreport"
	"github.com/bleemeo/glouton/logger"

	"github.com/prometheus-community/windows_exporter/collector"
	"github.com/prometheus/client_golang/prometheus"
)

type windowsCollector struct {
	maxScrapeDuration time.Duration
	collectors        map[string]collector.Collector
}

//nolint:gochecknoglobals
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

	// This can be removed when client_golang exposes this on Windows
	// (See https://github.com/prometheus/client_golang/issues/376)
	startTime     = float64(time.Now().Unix())
	startTimeDesc = prometheus.NewDesc(
		"process_start_time_seconds",
		"Start time of the process since unix epoch in seconds.",
		nil,
		nil,
	)
)

// Describe sends all the descriptors of the collectors included to
// the provided channel.
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
	//nolint:promlinter // counter metrics should have "_total" suffix
	ch <- prometheus.MustNewConstMetric(
		startTimeDesc,
		prometheus.CounterValue,
		startTime,
	)

	t := time.Now()

	cs := make([]string, 0, len(coll.collectors))
	for name := range coll.collectors {
		cs = append(cs, name)
	}

	scrapeContext, err := collector.PrepareScrapeContext(cs)
	ch <- prometheus.MustNewConstMetric(
		snapshotDuration,
		prometheus.GaugeValue,
		time.Since(t).Seconds(),
	)

	if err != nil {
		ch <- prometheus.NewInvalidMetric(scrapeSuccessDesc, fmt.Errorf("failed to prepare scrape: %w", err))

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
		defer crashreport.ProcessPanic()

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
			defer crashreport.ProcessPanic()
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
		defer crashreport.ProcessPanic()

		wg.Wait()
		close(allDone)
		close(metricsBuffer)
	}()

	// Wait until either all collectors finish, or timeout expires
	select {
	case <-allDone:
	case <-time.After(coll.maxScrapeDuration):
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
		logger.V(1).Printf("Collection timed out, still waiting for %v", remainingCollectorNames)
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
		logger.V(1).Printf("collector %s failed after %fs: %s", name, duration, err)

		return failed
	}

	logger.V(2).Printf("collector %s succeeded after %fs.", name, duration)

	return success
}
