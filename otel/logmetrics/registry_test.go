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
	"sort"
	"testing"
	"time"

	glmodel "github.com/bleemeo/glouton/prometheus/model"
	"github.com/bleemeo/glouton/types"

	"github.com/google/go-cmp/cmp"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

func TestRegistryResolve(t *testing.T) {
	t.Parallel()

	reg := newMetricsRegistry(0)

	logCounters := []metricSpec{
		{Metric: "apache_errors_count"},
		{Metric: "apache_requests_count"},
	}

	counters := reg.resolve(logCounters, "")

	if len(counters) != 2 {
		t.Fatalf("Expected 2 counters, got %d", len(counters))
	}

	// Resolving an already-registered metric name again must return the same counter.
	again := reg.resolve([]metricSpec{{Metric: "apache_errors_count"}}, "")

	if again[0] != counters[0] {
		t.Fatal("Expected resolve() to return the same *counter for an already-registered metric name")
	}

	names := reg.metricNames()
	sort.Strings(names)

	expected := []string{"apache_errors_count", "apache_requests_count"}
	if diff := cmp.Diff(expected, names); diff != "" {
		t.Fatalf("Unexpected metric names:\n%s", diff)
	}
}

func TestRegistryEmit(t *testing.T) {
	t.Parallel()

	reg := newMetricsRegistry(0)

	counters := reg.resolve([]metricSpec{
		{Metric: "apache_errors_count"},
	}, "")

	// 120 matches over 60 (simulated) elapsed seconds => 2/s.
	now := time.Now()
	counters[0].lastEmitAt = now.Add(-60 * time.Second)

	for range 120 {
		counters[0].add(1)
	}

	app := glmodel.NewBufferAppender()

	if err := reg.emit(app, now); err != nil {
		t.Fatalf("emit returned an error: %v", err)
	}

	mfs, err := app.AsMF()
	if err != nil {
		t.Fatalf("AsMF returned an error: %v", err)
	}

	if len(mfs) != 1 {
		t.Fatalf("Expected exactly 1 metric family, got %d", len(mfs))
	}

	if got := mfs[0].GetName(); got != "apache_errors_count" {
		t.Errorf("Expected metric name %q, got %q", "apache_errors_count", got)
	}

	if len(mfs[0].GetMetric()) != 1 {
		t.Fatalf("Expected exactly 1 sample, got %d", len(mfs[0].GetMetric()))
	}

	if got := mfs[0].GetMetric()[0].GetUntyped().GetValue(); got != 2 {
		t.Errorf("Expected value 2, got %v", got)
	}
}

// makeSumMetrics builds a pmetric.Metrics with a single Sum data point per counts entry.
func makeSumMetrics(counts map[string]int64) pmetric.Metrics {
	md := pmetric.NewMetrics()
	sm := md.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty()

	for name, count := range counts {
		m := sm.Metrics().AppendEmpty()
		m.SetName(name)
		sum := m.SetEmptySum()
		sum.SetIsMonotonic(true)
		sum.SetAggregationTemporality(pmetric.AggregationTemporalityDelta)
		sum.DataPoints().AppendEmpty().SetIntValue(count)
	}

	return md
}

// Test that feeding a pmetric.Metrics through the shared sink only increments matching counters.
func TestMetricsSink(t *testing.T) {
	t.Parallel()

	reg := newMetricsRegistry(0)

	counters := reg.resolve([]metricSpec{
		{Metric: "apache_errors_count"},
		{Metric: "apache_requests_count"},
	}, "")

	sink := reg.metricsSinkForItem("")

	if err := sink.ConsumeMetrics(t.Context(), makeSumMetrics(map[string]int64{
		"apache_errors_count": 2,
		"unregistered_metric": 5, // no matching counter, silently ignored
	})); err != nil {
		t.Fatalf("ConsumeMetrics returned an error: %v", err)
	}

	if err := sink.ConsumeMetrics(t.Context(), makeSumMetrics(map[string]int64{
		"apache_errors_count": 3,
	})); err != nil {
		t.Fatalf("ConsumeMetrics returned an error: %v", err)
	}

	errorsCounter, requestsCounter := counters[0], counters[1]

	if got := errorsCounter.peekSum(); got != 5 {
		t.Errorf("Expected 5 total matches for apache_errors_count (2+3 across two batches), got %d", got)
	}

	if got := requestsCounter.peekSum(); got != 0 {
		t.Errorf("Expected 0 matches for apache_requests_count, got %d", got)
	}
}

// Test that resolving the same metric name under two different items returns distinct counters.
func TestRegistryItemDisambiguation(t *testing.T) {
	t.Parallel()

	reg := newMetricsRegistry(0)

	logCounters := []metricSpec{{Metric: "web_errors_count"}}

	countersA := reg.resolve(logCounters, "container-a")
	countersB := reg.resolve(logCounters, "container-b")

	if countersA[0] == countersB[0] {
		t.Fatal("Expected distinct counters for the same metric name under different items")
	}

	if err := reg.metricsSinkForItem("container-a").ConsumeMetrics(t.Context(), makeSumMetrics(map[string]int64{
		"web_errors_count": 2,
	})); err != nil {
		t.Fatalf("ConsumeMetrics returned an error: %v", err)
	}

	if err := reg.metricsSinkForItem("container-b").ConsumeMetrics(t.Context(), makeSumMetrics(map[string]int64{
		"web_errors_count": 5,
	})); err != nil {
		t.Fatalf("ConsumeMetrics returned an error: %v", err)
	}

	if got := countersA[0].peekSum(); got != 2 {
		t.Errorf("Expected 2 matches for container-a, got %d", got)
	}

	if got := countersB[0].peekSum(); got != 5 {
		t.Errorf("Expected 5 matches for container-b, got %d", got)
	}

	app := glmodel.NewBufferAppender()

	// Safely past each counter's creation-time lastEmitAt, regardless of clock resolution -- this test
	// only checks which series/labels are present, not the emitted rate value.
	if err := reg.emit(app, time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("emit returned an error: %v", err)
	}

	mfs, err := app.AsMF()
	if err != nil {
		t.Fatalf("AsMF returned an error: %v", err)
	}

	if len(mfs) != 1 || mfs[0].GetName() != "web_errors_count" {
		t.Fatalf("Expected exactly 1 metric family named web_errors_count, got %v", mfs)
	}

	if len(mfs[0].GetMetric()) != 2 {
		t.Fatalf("Expected 2 distinctly-labeled samples (one per item), got %d", len(mfs[0].GetMetric()))
	}

	gotItems := make([]string, 0, 2)

	for _, m := range mfs[0].GetMetric() {
		for _, lbl := range m.GetLabel() {
			if lbl.GetName() == types.LabelItem {
				gotItems = append(gotItems, lbl.GetValue())
			}
		}
	}

	sort.Strings(gotItems)

	if diff := cmp.Diff([]string{"container-a", "container-b"}, gotItems); diff != "" {
		t.Fatalf("Unexpected item labels:\n%s", diff)
	}
}

// Test that resolving the same metric name under the same item, but with different static "labels:",
// returns distinct counters instead of the second spec silently reusing the first's -- guards against a
// regression where two metrics: entries sharing a name (e.g. one metric split by status-code range into
// several conditions/labels combinations) collapsed onto a single series.
func TestRegistryResolveSameMetricDifferentLabelsAreDistinctCounters(t *testing.T) {
	t.Parallel()

	reg := newMetricsRegistry(0)

	counters2xx := reg.resolve([]metricSpec{
		{Metric: "log_common_code", Labels: map[string]string{"code": "2xx"}},
	}, "")

	counters3xx := reg.resolve([]metricSpec{
		{Metric: "log_common_code", Labels: map[string]string{"code": "3xx"}},
	}, "")

	if counters2xx[0] == counters3xx[0] {
		t.Fatal("Expected distinct counters for the same metric/item with different static labels")
	}

	if got := counters2xx[0].lbls.Get("code"); got != "2xx" {
		t.Errorf("Expected the first counter's code label to stay 2xx, got %q", got)
	}

	if got := counters3xx[0].lbls.Get("code"); got != "3xx" {
		t.Errorf("Expected the second counter's code label to stay 3xx, got %q", got)
	}

	counters2xx[0].add(1)
	counters3xx[0].add(1)
	counters3xx[0].add(1)

	if got := counters2xx[0].peekSum(); got != 1 {
		t.Errorf("Expected 1 match for code=2xx, got %d", got)
	}

	if got := counters3xx[0].peekSum(); got != 2 {
		t.Errorf("Expected 2 matches for code=3xx, got %d", got)
	}
}

// Test that release() with a zero grace period drops the item's counters synchronously (the behavior
// every other registry test relies on).
func TestRegistryReleaseZeroGracePeriodIsSynchronous(t *testing.T) {
	t.Parallel()

	reg := newMetricsRegistry(0)

	reg.resolve([]metricSpec{{Metric: "app_errors_count"}}, "web-1")
	reg.release("web-1")

	if _, found := reg.counters[counterKey{metric: "app_errors_count", item: "web-1"}]; found {
		t.Error("Expected the counter to be gone immediately after release() with a zero grace period")
	}
}

// Test that resolve() for an item cancels its pending release, so a container recreated shortly after the
// old one disappears (new ID, same item) reuses the existing counter instead of resetting to 0.
func TestRegistryResolveCancelsPendingRelease(t *testing.T) {
	t.Parallel()

	reg := newMetricsRegistry(time.Hour) // long enough that the timer never fires during this test

	before := reg.resolve([]metricSpec{{Metric: "app_errors_count"}}, "web-1")
	before[0].add(3)

	reg.release("web-1")

	if _, pending := reg.pendingRelease["web-1"]; !pending {
		t.Fatal("Expected release() to schedule a pending release")
	}

	after := reg.resolve([]metricSpec{{Metric: "app_errors_count"}}, "web-1")

	if _, pending := reg.pendingRelease["web-1"]; pending {
		t.Error("Expected resolve() to cancel the pending release")
	}

	if after[0] != before[0] {
		t.Error("Expected resolve() to return the same counter (reused, not reset) after cancelling the pending release")
	}

	if got := after[0].peekSum(); got != 3 {
		t.Errorf("Expected the counter's prior total to survive (3), got %d", got)
	}
}

// Test that release() eventually drops the item's counters once its grace period elapses with no reuse.
func TestRegistryReleaseForgetsAfterGracePeriodElapses(t *testing.T) {
	t.Parallel()

	const gracePeriod = 20 * time.Millisecond

	reg := newMetricsRegistry(gracePeriod)

	reg.resolve([]metricSpec{{Metric: "app_errors_count"}}, "web-1")
	reg.release("web-1")

	deadline := time.Now().Add(10 * gracePeriod)

	for time.Now().Before(deadline) {
		reg.l.Lock()
		_, found := reg.counters[counterKey{metric: "app_errors_count", item: "web-1"}]
		reg.l.Unlock()

		if !found {
			return // forgotten, as expected
		}

		time.Sleep(gracePeriod / 4)
	}

	t.Errorf("Expected the counter to be forgotten within %s of its grace period elapsing, it never was", 10*gracePeriod)
}

// Test that a forget() call carrying a now-stale epoch (simulating a release() timer that already fired,
// but whose goroutine only acquires reg.l after a concurrent resolve() reused the item -- Timer.Stop()
// returning false in cancelPendingReleaseLocked doesn't stop an already-fired goroutine from still
// running) does not purge the counters the resolve() just reused.
func TestRegistryForgetSkipsStaleEpochAfterReuse(t *testing.T) {
	t.Parallel()

	reg := newMetricsRegistry(time.Hour) // grace period irrelevant: forget() is invoked directly below

	before := reg.resolve([]metricSpec{{Metric: "app_errors_count"}}, "web-1")
	before[0].add(7)

	reg.release("web-1")

	staleEpoch := reg.releaseEpoch["web-1"] // captured as release()'s scheduled forget() would have

	// A same-named replacement is resolved before the grace period elapses.
	after := reg.resolve([]metricSpec{{Metric: "app_errors_count"}}, "web-1")

	if after[0] != before[0] {
		t.Fatal("Expected resolve() to reuse the existing counter")
	}

	// Simulate the delayed forget() goroutine finally running with the epoch it captured before the
	// resolve() above invalidated it.
	reg.forget("web-1", staleEpoch)

	if _, found := reg.counters[counterKey{metric: "app_errors_count", item: "web-1"}]; !found {
		t.Fatal("Expected the reused counter to survive a stale forget() call racing a concurrent resolve()")
	}

	if got := after[0].peekSum(); got != 7 {
		t.Errorf("Expected the counter's prior total to survive (7), got %d", got)
	}
}

// TestRegistryStaleForgetDoesNotStealLivePendingRelease guards against a regression where forget()
// unconditionally deleted reg.pendingRelease[item] before checking its epoch: a stale forget() call
// (its timer already fired, but the goroutine only acquires reg.l after a newer release() cycle already
// installed a fresh, live timer for the same item) would erase that live timer's only bookkeeping entry.
// A later resolve() checking pendingRelease would then find nothing to cancel, so it wouldn't bump the
// epoch or stop the still-armed live timer -- which would go on to fire and wrongly forgetLocked an item
// back in active use. Complements TestRegistryForgetSkipsStaleEpochAfterReuse's "stale forget must not
// delete the reused counters" invariant with the pendingRelease bookkeeping side of the same fix.
func TestRegistryStaleForgetDoesNotStealLivePendingRelease(t *testing.T) {
	t.Parallel()

	reg := newMetricsRegistry(time.Hour) // grace period irrelevant: forget()/release() called directly below

	reg.resolve([]metricSpec{{Metric: "app_errors_count"}}, "web-1")

	reg.release("web-1") // schedules the first (soon-to-be-stale) pending release, epoch e0
	staleEpoch := reg.releaseEpoch["web-1"]

	// A same-named replacement reuses the item, cancelling the first pending release (bumps the epoch).
	reg.resolve([]metricSpec{{Metric: "app_errors_count"}}, "web-1")

	// The item is released again: a new, live pending release is installed under the bumped epoch.
	reg.release("web-1")

	liveTimer, pending := reg.pendingRelease["web-1"]
	if !pending {
		t.Fatal("Expected the second release() to install a live pending release")
	}

	// Simulate the first release()'s delayed forget() goroutine finally running now, with the epoch it
	// captured before either of the above events invalidated it.
	reg.forget("web-1", staleEpoch)

	if got, pending := reg.pendingRelease["web-1"]; !pending || got != liveTimer {
		t.Fatal("Expected the stale forget() call to leave the live pending release untouched")
	}

	// A resolve() now must still be able to see and cancel that live pending release.
	reg.resolve([]metricSpec{{Metric: "app_errors_count"}}, "web-1")

	if _, pending := reg.pendingRelease["web-1"]; pending {
		t.Error("Expected resolve() to cancel the live pending release the stale forget() left behind")
	}
}

// Test that custom static labels merge in but can never override the reserved __name__/item labels.
func TestRegistryLabels(t *testing.T) {
	t.Parallel()

	reg := newMetricsRegistry(0)

	staticCounters := reg.resolve([]metricSpec{
		{Metric: "app_errors_count", Labels: map[string]string{"env": "prod"}},
	}, "")

	if got := staticCounters[0].lbls.Get("env"); got != "prod" {
		t.Errorf("Expected custom label env=prod, got %q", got)
	}

	if got := staticCounters[0].lbls.Get(types.LabelName); got != "app_errors_count" {
		t.Errorf("Expected __name__=app_errors_count, got %q", got)
	}

	spoofedCounters := reg.resolve([]metricSpec{
		{Metric: "container_errors_count", Labels: map[string]string{"item": "spoofed"}},
	}, "real-container")

	if got := spoofedCounters[0].lbls.Get(types.LabelItem); got != "real-container" {
		t.Errorf("Expected the real auto-derived item to win over a user labels:{item:...} entry, got %q", got)
	}
}

// sumDataPoint is one data point to feed makeSumMetricWithPoints, optionally carrying attributes (as a
// metrics: entry's "attributes:" list would produce via the countconnector).
type sumDataPoint struct {
	Attrs map[string]string
	Count int64
}

// makeSumMetricWithPoints builds a pmetric.Metrics with a single Sum metric named name, one data point per
// entry in points.
func makeSumMetricWithPoints(name string, points ...sumDataPoint) pmetric.Metrics {
	md := pmetric.NewMetrics()
	sm := md.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty()

	m := sm.Metrics().AppendEmpty()
	m.SetName(name)
	sum := m.SetEmptySum()
	sum.SetIsMonotonic(true)
	sum.SetAggregationTemporality(pmetric.AggregationTemporalityDelta)

	for _, p := range points {
		dp := sum.DataPoints().AppendEmpty()
		dp.SetIntValue(p.Count)

		for k, v := range p.Attrs {
			dp.Attributes().PutStr(k, v)
		}
	}

	return md
}

// Test that data points carrying attribute values (a metrics: entry's "attributes:" list) create distinct
// series per combination actually observed, instead of collapsing into the item's base counter, and that
// the same combination seen again across batches accumulates into that same series.
func TestRegistryAttributesCreateDistinctSeries(t *testing.T) {
	t.Parallel()

	reg := newMetricsRegistry(0)

	reg.resolve([]metricSpec{{Metric: "http_requests_count"}}, "")

	sink := reg.metricsSinkForItem("")

	if err := sink.ConsumeMetrics(t.Context(), makeSumMetricWithPoints("http_requests_count",
		sumDataPoint{Attrs: map[string]string{"status": "200"}, Count: 3},
		sumDataPoint{Attrs: map[string]string{"status": "500"}, Count: 1},
	)); err != nil {
		t.Fatalf("ConsumeMetrics returned an error: %v", err)
	}

	// A later batch with the same combo must accumulate into the same series, not create a third one.
	if err := sink.ConsumeMetrics(t.Context(), makeSumMetricWithPoints("http_requests_count",
		sumDataPoint{Attrs: map[string]string{"status": "200"}, Count: 2},
	)); err != nil {
		t.Fatalf("ConsumeMetrics returned an error: %v", err)
	}

	base, found := reg.counters[counterKey{metric: "http_requests_count", item: ""}]
	if !found {
		t.Fatal("Expected the base counter to exist")
	}

	if got := base.peekSum(); got != 0 {
		t.Errorf("Expected the base (no-attrs) counter to stay untouched, got %d", got)
	}

	var status200, status500 *counter

	for key, c := range reg.counters {
		if key.metric != "http_requests_count" || key.attrs == "" {
			continue
		}

		switch c.lbls.Get("status") {
		case "200":
			status200 = c
		case "500":
			status500 = c
		}
	}

	if status200 == nil || status500 == nil {
		t.Fatalf("Expected distinct counters for status=200 and status=500, got counters=%+v", reg.counters)
	}

	if got := status200.peekSum(); got != 5 {
		t.Errorf("Expected status=200 total 5 (3+2 across two batches), got %d", got)
	}

	if got := status500.peekSum(); got != 1 {
		t.Errorf("Expected status=500 total 1, got %d", got)
	}
}

// Test that once a metric using "attributes:" has at least one real attrs-variant sibling, its base
// (attrs="") counter -- which can never receive an Add() itself, see resolveAttrCounterLocked -- is no
// longer emitted as a spurious always-zero series alongside the real ones.
func TestRegistryEmitSkipsPhantomBaseSeriesOnceAttributedSiblingExists(t *testing.T) {
	t.Parallel()

	reg := newMetricsRegistry(0)

	reg.resolve([]metricSpec{{Metric: "http_requests_count"}}, "")

	sink := reg.metricsSinkForItem("")

	if err := sink.ConsumeMetrics(t.Context(), makeSumMetricWithPoints("http_requests_count",
		sumDataPoint{Attrs: map[string]string{"status": "200"}, Count: 3},
	)); err != nil {
		t.Fatalf("ConsumeMetrics returned an error: %v", err)
	}

	app := glmodel.NewBufferAppender()

	// Safely past each counter's creation-time lastEmitAt; this test only checks which series survive,
	// not the emitted rate value.
	if err := reg.emit(app, time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("emit returned an error: %v", err)
	}

	mfs, err := app.AsMF()
	if err != nil {
		t.Fatalf("AsMF returned an error: %v", err)
	}

	if len(mfs) != 1 {
		t.Fatalf("Expected exactly 1 metric family, got %d", len(mfs))
	}

	if got := len(mfs[0].GetMetric()); got != 1 {
		t.Fatalf("Expected exactly 1 sample (the real status=200 series, base series skipped), got %d", got)
	}

	for _, lbl := range mfs[0].GetMetric()[0].GetLabel() {
		if lbl.GetName() == "status" && lbl.GetValue() != "200" {
			t.Errorf("Expected the surviving sample to be the status=200 series, got status=%q", lbl.GetValue())
		}
	}
}

// Test that an attribute whose key collides with the reserved item key or a static "labels:" entry is
// dropped instead of overriding it: precedence is item > labels > attributes.
func TestRegistryAttributesShadowedByItemAndLabels(t *testing.T) {
	t.Parallel()

	reg := newMetricsRegistry(0)

	reg.resolve([]metricSpec{
		{Metric: "web_requests_count", Labels: map[string]string{"env": "prod"}},
	}, "web-1")

	sink := reg.metricsSinkForEntry("web-1", types.LabelsToText(map[string]string{"env": "prod"}))

	if err := sink.ConsumeMetrics(t.Context(), makeSumMetricWithPoints("web_requests_count",
		sumDataPoint{Attrs: map[string]string{
			"item":   "spoofed-item", // shadowed: reserved key
			"env":    "spoofed-env",  // shadowed: static "labels:" already claims it
			"region": "eu",           // unclaimed: becomes a real label
		}, Count: 1},
	)); err != nil {
		t.Fatalf("ConsumeMetrics returned an error: %v", err)
	}

	var realCounter *counter

	for key, c := range reg.counters {
		if key.metric == "web_requests_count" && key.attrs != "" {
			realCounter = c
		}
	}

	if realCounter == nil {
		t.Fatalf("Expected a real attribute-derived counter, got counters=%+v", reg.counters)
	}

	if got := realCounter.lbls.Get(types.LabelItem); got != "web-1" {
		t.Errorf("Expected the real item to win over the spoofed \"item\" attribute, got %q", got)
	}

	if got := realCounter.lbls.Get("env"); got != "prod" {
		t.Errorf("Expected the static labels: value to win over the spoofed \"env\" attribute, got %q", got)
	}

	if got := realCounter.lbls.Get("region"); got != "eu" {
		t.Errorf("Expected the unclaimed \"region\" attribute to surface as a label, got %q", got)
	}

	if got := realCounter.peekSum(); got != 1 {
		t.Errorf("Expected 1 match, got %d", got)
	}
}

// Test that two genuinely distinct attribute-value combinations never collide onto the same counter, even
// when a value contains the raw separator characters ('=', ',') the canonical key is built from -- guards
// against a regression where an unescaped "key=value," encoding let combination A ({"a": "1,b=2", "b":
// "3"}) and combination B ({"a": "1", "b": "2,b=3"}) both produce the literal string "a=1,b=2,b=3," and
// merge into one series with whichever combination's labels happened to be created first.
func TestRegistryAttributesWithSeparatorCharsDontCollide(t *testing.T) {
	t.Parallel()

	reg := newMetricsRegistry(0)

	reg.resolve([]metricSpec{{Metric: "tricky_count"}}, "")

	sink := reg.metricsSinkForItem("")

	if err := sink.ConsumeMetrics(t.Context(), makeSumMetricWithPoints("tricky_count",
		sumDataPoint{Attrs: map[string]string{"a": "1,b=2", "b": "3"}, Count: 1},
		sumDataPoint{Attrs: map[string]string{"a": "1", "b": "2,b=3"}, Count: 1},
	)); err != nil {
		t.Fatalf("ConsumeMetrics returned an error: %v", err)
	}

	var combos []counterKey

	for key := range reg.counters {
		if key.metric == "tricky_count" && key.attrs != "" {
			combos = append(combos, key)
		}
	}

	if len(combos) != 2 {
		t.Fatalf("Expected 2 distinct attribute-combo counters, got %d: %+v", len(combos), combos)
	}

	for _, key := range combos {
		c := reg.counters[key]

		if got := c.peekSum(); got != 1 {
			t.Errorf("Expected each distinct combo to total 1 (no cross-contamination), got %d for %+v", got, key)
		}
	}
}
