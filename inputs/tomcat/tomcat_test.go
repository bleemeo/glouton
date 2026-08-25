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

package tomcat

import (
	"math"
	"testing"
	"time"

	"github.com/bleemeo/glouton/inputs/internal"
	"github.com/google/go-cmp/cmp"
)

// collectFinalMetrics replicates the measurement/field -> final metric name
// convention applied downstream (inputs.Accumulator.addMetrics): the metric
// name is the field name alone when the measurement was renamed to "", or
// "<measurement>_<field>" otherwise.
func collectFinalMetrics(store *internal.StoreAccumulator) map[string]float64 {
	got := make(map[string]float64)

	for _, m := range store.Measurement {
		for field, value := range m.Fields {
			name := field
			if m.Name != "" {
				name = m.Name + "_" + field
			}

			switch v := value.(type) {
			case float64:
				got[name] = v
			case uint64:
				got[name] = float64(v)
			}
		}
	}

	return got
}

func newAccumulator(store *internal.StoreAccumulator) internal.Accumulator {
	return internal.Accumulator{
		RenameGlobal:     renameGlobal,
		TransformMetrics: transformMetrics,
		DifferentiatedMetrics: []string{
			"bytes_received",
			"bytes_sent",
			"error_count",
			"processing_time",
			"request_count",
		},
		Accumulator: store,
	}
}

func assertMetrics(t *testing.T, got map[string]float64, want map[string]float64) {
	t.Helper()

	for name, value := range want {
		gotValue, ok := got[name]
		if !ok {
			t.Errorf("metric %q not emitted, got metrics: %v", name, got)

			continue
		}

		if math.Abs(gotValue-value) > 0.0001 {
			t.Errorf("metric %q == %v, want %v", name, gotValue, value)
		}
	}
}

// assertTags checks the tags kept on every emitted measurement.
func assertTags(t *testing.T, store *internal.StoreAccumulator, want map[string]string) {
	t.Helper()

	for _, m := range store.Measurement {
		if diff := cmp.Diff(want, m.Tags); diff != "" {
			t.Errorf("tags of measurement %q (-want +got):\n%s", m.Name, diff)
		}
	}
}

// TestRenamePipelineConnector exercises the "tomcat_connector" measurement's
// cumulative counters, converted to per-second rates (via
// DifferentiatedMetrics), with processing_time further combined with
// request_count by transformMetrics into an average processing time per
// request -- same pattern as inputs/clickhouse's query_time_seconds.
func TestRenamePipelineConnector(t *testing.T) {
	store := &internal.StoreAccumulator{}
	acc := newAccumulator(store)

	t0 := time.Now()
	t1 := t0.Add(10 * time.Second)

	acc.PrepareGather()
	acc.AddFields("tomcat_connector", map[string]any{
		"bytes_received":       uint64(100000),
		"bytes_sent":           uint64(500000),
		"error_count":          uint64(10),
		"processing_time":      uint64(20000),
		"request_count":        uint64(1000),
		"current_threads_busy": 5.0,
		"current_thread_count": 20.0,
		"max_threads":          200.0,
		"max_time":             300.0,
	}, map[string]string{
		"name":   "http-nio-8080",
		"source": "http://127.0.0.1:8080/manager/status/all?XML=true",
	}, t0)

	// Discard the first gather: every differentiated field has no rate yet
	// (no history).
	store.Measurement = nil

	acc.PrepareGather()
	acc.AddFields("tomcat_connector", map[string]any{
		"bytes_received":       uint64(100000 + 20000),  // rate = 2000/s
		"bytes_sent":           uint64(500000 + 100000), // rate = 10000/s
		"error_count":          uint64(10 + 2),          // rate = 0.2/s
		"processing_time":      uint64(20000 + 4000),    // rate = 400/s
		"request_count":        uint64(1000 + 100),      // rate = 10/s
		"current_threads_busy": 8.0,
		"current_thread_count": 20.0,
		"max_threads":          200.0,
		"max_time":             450.0,
	}, map[string]string{
		"name":   "http-nio-8080",
		"source": "http://127.0.0.1:8080/manager/status/all?XML=true",
	}, t1)

	got := collectFinalMetrics(store)

	// processing_time_seconds = processingTimeRate / requestCountRate / 1000 = 400 / 10 / 1000 = 0.04
	assertMetrics(t, got, map[string]float64{
		"tomcat_connector_bytes_received":          2000,
		"tomcat_connector_bytes_sent":              10000,
		"tomcat_connector_error_count":             0.2,
		"tomcat_connector_request_count":           10,
		"tomcat_connector_processing_time_seconds": 0.04,
		// Gauges must pass through untouched, not differentiated.
		"tomcat_connector_current_threads_busy": 8,
		"tomcat_connector_max_time":             450,
	})

	if _, ok := got["tomcat_connector_processing_time"]; ok {
		t.Errorf("raw processing_time rate should have been dropped, got value %v", got["tomcat_connector_processing_time"])
	}

	// The status URL is redundant with the labels already set on service metrics,
	// while "name" tells which connector this is.
	assertTags(t, store, map[string]string{"name": "http-nio-8080", "item": "http-nio-8080"})
}
