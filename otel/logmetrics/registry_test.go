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

	// 120 matches over the 60s window => 2/s.
	for range 120 {
		counters[0].counter.Add(1)
	}

	app := glmodel.NewBufferAppender()

	if err := reg.emit(app); err != nil {
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

	if got := errorsCounter.counter.Total(); got != 5 {
		t.Errorf("Expected 5 total matches for apache_errors_count (2+3 across two batches), got %d", got)
	}

	if got := requestsCounter.counter.Total(); got != 0 {
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

	if got := countersA[0].counter.Total(); got != 2 {
		t.Errorf("Expected 2 matches for container-a, got %d", got)
	}

	if got := countersB[0].counter.Total(); got != 5 {
		t.Errorf("Expected 5 matches for container-b, got %d", got)
	}

	app := glmodel.NewBufferAppender()

	if err := reg.emit(app); err != nil {
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
	before[0].counter.Add(3)

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

	if got := after[0].counter.Total(); got != 3 {
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
