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
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/otel/logsource"
	"github.com/bleemeo/glouton/types"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/storage"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
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

// counterKey identifies one aggregated series. item is part of the identity so the same metric name from
// different containers stays distinguishable. attrs disambiguates further by the exact combination of
// dynamic "attributes:" values a log line produced (see resolveAttrCounterLocked); it's "" for the base
// counter resolve() eagerly declares, since attribute values aren't known until a matching log line arrives.
type counterKey struct {
	metric string
	item   string
	attrs  string
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
	// releaseEpoch increments each time a pending release for an item is cancelled (see
	// cancelPendingReleaseLocked). Timer.Stop() returning false only means the timer's func has
	// already been scheduled to run -- not that it has finished, or even started: the forget()
	// goroutine it fires can still be sitting on reg.l when a resolve() for the same item runs first,
	// cancels the (already-fired) timer, and reuses the counters. Without this, the delayed forget()
	// goroutine would then acquire the lock and delete those just-reused counters anyway. release()
	// captures the epoch at schedule time and forget() compares it before deleting, so a forget() left
	// stale by an intervening cancellation is a no-op instead of purging live data.
	//
	// This is a plain immutable value captured into the forget() closure at schedule time, deliberately
	// not a *time.Timer identity: a self-referential closure (`var t *time.Timer; t = time.AfterFunc(d,
	// func(){ use t })`) reads t from a goroutine that can race the assignment `t = ...` itself when d is
	// short enough (confirmed with `go test -race` using this package's own short-grace-period tests) --
	// there's no synchronization between the AfterFunc call returning and its callback goroutine
	// starting. Capturing an already-computed value sidesteps that: nothing writes to the local `epoch`
	// variable after the closure captures it, only to the map it was read from.
	releaseEpoch map[string]uint64
}

func newMetricsRegistry(gracePeriod time.Duration) *metricsRegistry {
	return &metricsRegistry{
		declaredNames:  make(map[string]bool),
		counters:       make(map[counterKey]*counter),
		gracePeriod:    gracePeriod,
		pendingRelease: make(map[string]*time.Timer),
		releaseEpoch:   make(map[string]uint64),
	}
}

// resolve returns one counter per spec for item, creating it the first time this pair is seen. This is
// the base (attrs-less) counter: it declares the metric at zero even before any log line has matched, and
// its label set is the "item > labels" half of the overall item > labels > attributes precedence -- a
// metrics: entry's dynamic "attributes:" values, only known once a log line actually produces them, are
// merged in later by resolveAttrCounterLocked and can never override what's already set here.
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

	epoch := reg.releaseEpoch[item]

	reg.pendingRelease[item] = time.AfterFunc(reg.gracePeriod, func() {
		reg.forget(item, epoch)
	})
}

// cancelPendingReleaseLocked stops any release() scheduled for item, since it's back in use. Bumping
// releaseEpoch invalidates that release's forget() call even if its timer already fired: see
// releaseEpoch's doc comment. Caller must hold reg.l.
func (reg *metricsRegistry) cancelPendingReleaseLocked(item string) {
	if timer, pending := reg.pendingRelease[item]; pending {
		reg.releaseEpoch[item]++

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

// forget is forgetLocked's entry point for a fired release() timer, which runs without reg.l held. epoch
// is the value releaseEpoch[item] had when this release() call scheduled it; if a resolve() reused item
// in the meantime, cancelPendingReleaseLocked already bumped releaseEpoch, so this call is stale and must
// not delete the counters that resolve() just reused (see releaseEpoch's doc comment).
func (reg *metricsRegistry) forget(item string, epoch uint64) {
	reg.l.Lock()
	defer reg.l.Unlock()

	delete(reg.pendingRelease, item)

	if reg.releaseEpoch[item] != epoch {
		return
	}

	reg.forgetLocked(item)
}

// metricNames returns every declared metric name.
func (reg *metricsRegistry) metricNames() []string {
	reg.l.Lock()
	defer reg.l.Unlock()

	return slices.Collect(maps.Keys(reg.declaredNames))
}

// emit appends one "matches per second" sample per registered metric into app. The base (attrs=="")
// counter for a metric that also has at least one real attrs-variant sibling is skipped once it's at
// zero: a metric using "attributes:" never adds to its own base counter (every real match always carries
// the full configured attribute set, see resolveAttrCounterLocked), so once a sibling exists, the base is
// a permanent phantom that would otherwise emit a spurious always-zero {__name__, item} series with no
// attribute labels forever, indistinguishable from a legitimately-idle metric.
func (reg *metricsRegistry) emit(app storage.Appender) error {
	reg.l.Lock()
	counters := maps.Clone(reg.counters)
	reg.l.Unlock()

	hasAttrSibling := make(map[counterKey]bool, len(counters))

	for key := range counters {
		if key.attrs != "" {
			hasAttrSibling[counterKey{metric: key.metric, item: key.item}] = true
		}
	}

	for key, c := range counters {
		total := c.counter.Total()

		if key.attrs == "" && total == 0 && hasAttrSibling[key] {
			continue
		}

		rate := float64(total) / float64(windowSecs)

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

// addSumDataPoints adds m's data points to the matching (metric, item) counter(s). A data point with no
// attributes (the common case: no "attributes:" configured, or the connector produced one attrs-less
// point) goes straight to the base counter resolve() already declared. A data point carrying attributes
// (from a metrics: entry's "attributes:" list) is routed to a sibling counter specific to that exact
// combination of attribute values, created lazily since those values are only known once a log line
// actually produces them -- see resolveAttrCounterLocked for how they're merged with item/labels.
// Caller must hold reg.l.
func (reg *metricsRegistry) addSumDataPoints(m pmetric.Metric, item string) {
	if m.Type() != pmetric.MetricTypeSum {
		return
	}

	base, found := reg.counters[counterKey{metric: m.Name(), item: item}]
	if !found {
		return
	}

	dataPoints := m.Sum().DataPoints()

	for i := range dataPoints.Len() {
		dp := dataPoints.At(i)

		attrs := dp.Attributes()
		if attrs.Len() == 0 {
			base.counter.Add(int(dp.IntValue()))

			continue
		}

		reg.resolveAttrCounterLocked(base, attrs).counter.Add(int(dp.IntValue()))
	}
}

// resolveAttrCounterLocked returns (creating it the first time this exact combination is seen) the
// counter for base's (metric, item) further split by attrs, the dynamic values a metrics: entry's
// "attributes:" list extracted from one log line. Precedence for the resulting label set is
// item > labels > attributes: base.lbls already has the reserved __name__/item keys and any static
// "labels:" baked in (see resolve()), so an attribute whose key collides with one already claimed there
// is dropped instead of overriding it. Caller must hold reg.l.
func (reg *metricsRegistry) resolveAttrCounterLocked(base *counter, attrs pcommon.Map) *counter {
	claimed := base.lbls.Map()

	merged := make(map[string]string, len(claimed)+attrs.Len())
	maps.Copy(merged, claimed)

	extraKeys := make([]string, 0, attrs.Len())

	var shadowed []string

	attrs.Range(func(k string, v pcommon.Value) bool {
		if _, exists := claimed[k]; exists {
			shadowed = append(shadowed, k)

			return true
		}

		merged[k] = v.AsString()
		extraKeys = append(extraKeys, k)

		return true
	})

	sort.Strings(extraKeys) // deterministic key regardless of pcommon.Map iteration order

	var attrsID strings.Builder

	for _, k := range extraKeys {
		// %q (not %s) so a key or value containing '=', ',', or '"' can't make two distinct
		// combinations collide on the same encoded key -- Go's quoting is injective and never
		// leaves an unescaped '"' inside its own output, so concatenating quoted pairs keeps the
		// whole sequence unambiguous.
		fmt.Fprintf(&attrsID, "%q=%q,", k, merged[k])
	}

	key := counterKey{metric: base.metric, item: base.item, attrs: attrsID.String()}

	c, found := reg.counters[key]
	if found {
		return c
	}

	if len(shadowed) > 0 {
		logger.V(2).Printf("logmetrics: metric %q: attribute(s) %v shadowed by item/labels, dropping their value(s)", base.metric, shadowed)
	}

	c = &counter{
		metric:  base.metric,
		item:    base.item,
		counter: logsource.NewRingCounter(windowSecs),
		lbls:    labels.FromMap(merged),
	}
	reg.counters[key] = c

	return c
}
