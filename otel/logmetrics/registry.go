// Copyright 2015-2026 Bleemeo
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

package logmetrics

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/bleemeo/glouton/otel/logsource"
	"github.com/bleemeo/glouton/types"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/storage"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

// releaseGracePeriod bridges a container recreation (new ID, same item, e.g. a Kubernetes pod restart):
// release() delays actually dropping an item's counters by this long, so a same-item replacement resolved
// shortly after (in a later scan than the one that noticed the old container gone) reuses the existing
// counter -- a continuous series -- instead of resetting to 0.
const releaseGracePeriod = 90 * time.Second

// metricSpec is a (name, labels) pair used for registry declare/resolve.
type metricSpec struct {
	Metric string
	Labels map[string]string
}

// windowSecs is the sliding window, in seconds, for the "matches per second" rate.
const windowSecs = 60

// counter aggregates the delta counts for one metric over a sliding window.
// item is "" for non-container sources, or the container name otherwise.
type counter struct {
	metric  string
	item    string
	counter *logsource.RingCounter
	lbls    labels.Labels // precomputed once, never rebuilt
}

// counterKey identifies one aggregated series; item is part of the identity so
// the same metric name from different containers stays distinguishable.
type counterKey struct {
	metric string
	item   string
}

// metricsRegistry holds one counter per (metric, item), shared across sources so matches aggregate.
type metricsRegistry struct {
	l             sync.Mutex
	declaredNames map[string]bool // every name ever resolved, feeds MetricNames()
	counters      map[counterKey]*counter
	// gracePeriod is how long release() waits before actually dropping an item's counters. Zero means
	// immediate/synchronous (used by tests).
	gracePeriod time.Duration
	// pendingRelease holds a scheduled-but-not-yet-fired release() timer per item, so a resolve() for
	// that item before it fires can cancel it (the item is back in use).
	pendingRelease map[string]*time.Timer
}

func newMetricsRegistry(gracePeriod time.Duration) *metricsRegistry {
	return &metricsRegistry{
		declaredNames:  make(map[string]bool),
		counters:       make(map[counterKey]*counter),
		gracePeriod:    gracePeriod,
		pendingRelease: make(map[string]*time.Timer),
	}
}

// resolve returns one counter per spec for item, creating it the first time this pair is seen.
func (reg *metricsRegistry) resolve(specs []metricSpec, item string) []*counter {
	reg.l.Lock()
	defer reg.l.Unlock()

	reg.cancelPendingReleaseLocked(item)

	resolved := make([]*counter, 0, len(specs))

	for _, spec := range specs {
		reg.declaredNames[spec.Metric] = true

		key := counterKey{metric: spec.Metric, item: item}

		c, found := reg.counters[key]
		if !found {
			lblMap := maps.Clone(spec.Labels)
			if lblMap == nil {
				lblMap = make(map[string]string, 2) //nolint:mnd
			}

			// Reserved keys set last so they win over a same-name user label.
			lblMap[types.LabelName] = spec.Metric

			if item != "" {
				lblMap[types.LabelItem] = item
			}

			c = &counter{
				metric:  spec.Metric,
				item:    item,
				counter: logsource.NewRingCounter(windowSecs),
				lbls:    labels.FromMap(lblMap),
			}
			reg.counters[key] = c
		}

		resolved = append(resolved, c)
	}

	return resolved
}

// release drops every counter registered for item, after gracePeriod, so a removed container's series stop
// being emitted (as a permanent 0) instead of accumulating in the registry for the process lifetime. The
// delay bridges a container recreation racing this release: see releaseGracePeriod. declaredNames is left
// untouched: the metric name itself may still be in use by other items.
func (reg *metricsRegistry) release(item string) {
	if item == "" {
		return
	}

	reg.l.Lock()
	defer reg.l.Unlock()

	reg.cancelPendingReleaseLocked(item)

	if reg.gracePeriod <= 0 {
		reg.forgetLocked(item)

		return
	}

	reg.pendingRelease[item] = time.AfterFunc(reg.gracePeriod, func() {
		reg.forget(item)
	})
}

// cancelPendingReleaseLocked stops and forgets any release() scheduled for item, since it's back in use.
// Caller must hold reg.l.
func (reg *metricsRegistry) cancelPendingReleaseLocked(item string) {
	if timer, pending := reg.pendingRelease[item]; pending {
		timer.Stop()
		delete(reg.pendingRelease, item)
	}
}

// forgetLocked drops every counter registered for item. Caller must hold reg.l.
func (reg *metricsRegistry) forgetLocked(item string) {
	for key := range reg.counters {
		if key.item == item {
			delete(reg.counters, key)
		}
	}
}

// forget is forgetLocked's entry point for a fired release() timer, which runs without reg.l held.
func (reg *metricsRegistry) forget(item string) {
	reg.l.Lock()
	defer reg.l.Unlock()

	delete(reg.pendingRelease, item)
	reg.forgetLocked(item)
}

// metricNames returns every declared metric name.
func (reg *metricsRegistry) metricNames() []string {
	reg.l.Lock()
	defer reg.l.Unlock()

	return slices.Collect(maps.Keys(reg.declaredNames))
}

// emit appends one "matches per second" sample per registered metric into app.
func (reg *metricsRegistry) emit(app storage.Appender) error {
	reg.l.Lock()
	counters := slices.Collect(maps.Values(reg.counters))
	reg.l.Unlock()

	for _, c := range counters {
		rate := float64(c.counter.Total()) / float64(windowSecs)

		_, err := app.Append(
			0,
			c.lbls,
			0,
			rate,
		)
		if err != nil {
			return fmt.Errorf("append log metric %q: %w", c.metric, err)
		}
	}

	return app.Commit()
}

// metricsSinkForItem returns the "next" consumer for one source's countconnector,
// adding each Sum data point's delta into the matching (metric, item) counter.
func (reg *metricsRegistry) metricsSinkForItem(item string) consumer.Metrics {
	sink, err := consumer.NewMetrics(func(_ context.Context, md pmetric.Metrics) error {
		reg.l.Lock()
		defer reg.l.Unlock()

		resourceMetrics := md.ResourceMetrics()
		for i := range resourceMetrics.Len() {
			scopeMetrics := resourceMetrics.At(i).ScopeMetrics()
			for j := range scopeMetrics.Len() {
				metrics := scopeMetrics.At(j).Metrics()
				for k := range metrics.Len() {
					reg.addSumDataPoints(metrics.At(k), item)
				}
			}
		}

		return nil
	})
	if err != nil {
		panic(err)
	}

	return sink
}

// addSumDataPoints adds m's data points to the matching (metric, item) counter. Caller must hold reg.l.
func (reg *metricsRegistry) addSumDataPoints(m pmetric.Metric, item string) {
	if m.Type() != pmetric.MetricTypeSum {
		return
	}

	c, found := reg.counters[counterKey{metric: m.Name(), item: item}]
	if !found {
		return
	}

	dataPoints := m.Sum().DataPoints()

	for i := range dataPoints.Len() {
		c.counter.Add(int(dataPoints.At(i).IntValue()))
	}
}
