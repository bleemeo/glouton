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

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/types"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/storage"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

// windowSecs is the sliding window used for the "matches per second" rate,
// mirroring the old Fluent Bit rate(...[1m]).
const windowSecs = 60

// counter aggregates the delta counts reported by the countconnector for one
// metric over a sliding window (matching itself happens via OTTL).
type counter struct {
	metric  string
	counter *ringCounter
	lbls    labels.Labels // precomputed once: never changes after creation, no need to rebuild it on every emit
}

// metricsRegistry holds one counter per metric name, shared across every source
// that references it, so same-named matches from different sources aggregate.
type metricsRegistry struct {
	l        sync.Mutex
	counters map[string]*counter
}

func newMetricsRegistry() *metricsRegistry {
	return &metricsRegistry{counters: make(map[string]*counter)}
}

// resolve returns one counter per filter, creating it the first time its metric
// name is seen; repeated names share the same counter.
func (reg *metricsRegistry) resolve(filters []config.LogFilter) []*counter {
	reg.l.Lock()
	defer reg.l.Unlock()

	counters := make([]*counter, 0, len(filters))

	for _, filter := range filters {
		c, found := reg.counters[filter.Metric]
		if !found {
			c = &counter{
				metric:  filter.Metric,
				counter: newRingCounter(windowSecs),
				lbls:    labels.FromMap(map[string]string{types.LabelName: filter.Metric}),
			}
			reg.counters[filter.Metric] = c
		}

		counters = append(counters, c)
	}

	return counters
}

// metricNames returns every registered metric name (fed into the metric
// allow-list, see agent.rebuildDynamicMetricAllowDenyList).
func (reg *metricsRegistry) metricNames() []string {
	reg.l.Lock()
	defer reg.l.Unlock()

	return slices.Collect(maps.Keys(reg.counters))
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

// metricsSink is the shared "next" consumer for every source's countconnector,
// adding each Sum data point's delta into the matching counter.
func (reg *metricsRegistry) metricsSink() consumer.Metrics {
	sink, err := consumer.NewMetrics(func(_ context.Context, md pmetric.Metrics) error {
		reg.l.Lock()
		defer reg.l.Unlock()

		resourceMetrics := md.ResourceMetrics()
		for i := range resourceMetrics.Len() {
			scopeMetrics := resourceMetrics.At(i).ScopeMetrics()
			for j := range scopeMetrics.Len() {
				metrics := scopeMetrics.At(j).Metrics()
				for k := range metrics.Len() {
					reg.addSumDataPoints(metrics.At(k))
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

// addSumDataPoints adds m's data points (Sum only, all countconnector emits) to
// the matching counter. Caller must hold reg.l.
func (reg *metricsRegistry) addSumDataPoints(m pmetric.Metric) {
	if m.Type() != pmetric.MetricTypeSum {
		return
	}

	c, found := reg.counters[m.Name()]
	if !found {
		return
	}

	dataPoints := m.Sum().DataPoints()

	for i := range dataPoints.Len() {
		c.counter.Add(int(dataPoints.At(i).IntValue()))
	}
}
