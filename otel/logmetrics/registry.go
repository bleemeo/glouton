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

	"github.com/bleemeo/glouton/otel/logsource"
	"github.com/bleemeo/glouton/types"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/storage"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

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
}

func newMetricsRegistry() *metricsRegistry {
	return &metricsRegistry{
		declaredNames: make(map[string]bool),
		counters:      make(map[counterKey]*counter),
	}
}

// resolve returns one counter per spec for item, creating it the first time this pair is seen.
func (reg *metricsRegistry) resolve(specs []metricSpec, item string) []*counter {
	reg.l.Lock()
	defer reg.l.Unlock()

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
