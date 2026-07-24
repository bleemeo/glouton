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

	"github.com/bleemeo/glouton/config"
	glmodel "github.com/bleemeo/glouton/prometheus/model"

	"github.com/google/go-cmp/cmp"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

func TestRegistryResolve(t *testing.T) {
	t.Parallel()

	reg := newMetricsRegistry()

	logCounters := []config.LogCounter{
		{Metric: "apache_errors_count", Regex: `\[error\]`},
		{Metric: "apache_requests_count", Regex: "GET /"},
	}

	counters := reg.resolve(logCounters)

	if len(counters) != 2 {
		t.Fatalf("Expected 2 counters, got %d", len(counters))
	}

	// Resolving an already-registered metric name again must return the exact same
	// counter, so that matches from multiple sources reporting under the same
	// metric name are aggregated.
	again := reg.resolve([]config.LogCounter{{Metric: "apache_errors_count", Regex: "unused"}})

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

	reg := newMetricsRegistry()

	counters := reg.resolve([]config.LogCounter{
		{Metric: "apache_errors_count", Regex: `\[error\]`},
	})

	// 120 matches over the windowSecs (60s) window => 2/s.
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

// makeSumMetrics builds a pmetric.Metrics with a single Sum data point per
// counts entry, mirroring what countconnector.appendMetricsTo produces.
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

// TestMetricsSink is the regression test for the OTel-native design: it feeds a
// pmetric.Metrics (as the countconnector would produce) through the shared sink and
// checks the matching counters — and only those — got incremented by the delta.
func TestMetricsSink(t *testing.T) {
	t.Parallel()

	reg := newMetricsRegistry()

	counters := reg.resolve([]config.LogCounter{
		{Metric: "apache_errors_count", Regex: `\[error\]`},
		{Metric: "apache_requests_count", Regex: "GET /"},
	})

	sink := reg.metricsSink()

	if err := sink.ConsumeMetrics(t.Context(), makeSumMetrics(map[string]int64{
		"apache_errors_count": 2,
		"unregistered_metric": 5, // must be silently ignored: no matching counter
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
