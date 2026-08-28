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

package clickhouse

import (
	"math"
	"testing"
	"time"

	"github.com/bleemeo/glouton/inputs/internal"
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
			"query",
			"select_query",
			"query_time_microseconds",
			"failed_query",
			"mutation_total_milliseconds",
			"network_receive_bytes",
			"network_send_bytes",
			"slow_read",
			"mutation_total_parts",
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

// TestRenamePipelineEvents exercises the "clickhouse_events" measurement,
// which is fully made of cumulative counters converted to per-second rates
// (via DifferentiatedMetrics), then combined by transformMetrics into
// average query/mutation durations. Final metric names are checked against
// the allow-list in agent/metric-filter/metric.go.
func TestRenamePipelineEvents(t *testing.T) {
	store := &internal.StoreAccumulator{}
	acc := newAccumulator(store)

	t0 := time.Now()
	t1 := t0.Add(10 * time.Second)

	acc.PrepareGather()
	acc.AddFields("clickhouse_events", map[string]any{
		"query":                       uint64(1000),
		"select_query":                uint64(500),
		"failed_query":                uint64(100),
		"network_receive_bytes":       uint64(200000),
		"network_send_bytes":          uint64(100000),
		"slow_read":                   uint64(50),
		"query_time_microseconds":     uint64(1000000),
		"mutation_total_milliseconds": uint64(10000),
		"mutation_total_parts":        uint64(200),
	}, nil, t0)

	// Discard the first gather: every field is a differentiated counter, so
	// it has no rate yet (no history).
	store.Measurement = nil

	acc.PrepareGather()
	acc.AddFields("clickhouse_events", map[string]any{
		"query":                       uint64(1000 + 50),          // rate = 5/s
		"select_query":                uint64(500 + 10),           // rate = 1/s
		"failed_query":                uint64(100 + 5),            // rate = 0.5/s
		"network_receive_bytes":       uint64(200000 + 1000),      // rate = 100/s
		"network_send_bytes":          uint64(100000 + 500),       // rate = 50/s
		"slow_read":                   uint64(50 + 20),            // rate = 2/s
		"query_time_microseconds":     uint64(1000000 + 40000000), // rate = 4 000 000/s
		"mutation_total_milliseconds": uint64(10000 + 30000),      // rate = 3 000/s
		"mutation_total_parts":        uint64(200 + 10),           // rate = 1/s
	}, nil, t1)

	got := collectFinalMetrics(store)

	// query_time_seconds = queryTimeRate / queryCountRate / 1e6 = 4 000 000 / 5 / 1e6 = 0.8
	// mutation_time_seconds = mutationTimeRate / mutationCountRate / 1e3 = 3 000 / 1 / 1e3 = 3.0
	assertMetrics(t, got, map[string]float64{
		"clickhouse_events_query":                 5,
		"clickhouse_events_select_query":          1,
		"clickhouse_events_failed_query":          0.5,
		"clickhouse_events_network_receive_bytes": 100,
		"clickhouse_events_network_send_bytes":    50,
		"clickhouse_events_slow_read":             2,
		"clickhouse_events_query_time_seconds":    0.8,
		"clickhouse_events_mutation_time_seconds": 3,
	})

	// The raw duration rates are meaningless by themselves and must not be emitted
	// alongside the averages derived from them.
	for _, name := range []string{
		"clickhouse_events_query_time_microseconds",
		"clickhouse_events_mutation_total_milliseconds",
	} {
		if value, ok := got[name]; ok {
			t.Errorf("raw duration rate %q should have been dropped, got value %v", name, value)
		}
	}
}

// TestRenamePipelineMetrics exercises the "clickhouse_metrics" measurement,
// where the "query" field is renamed to "active_query" by renameGlobal
// before differentiation, so it is treated as an instant gauge and not
// as a rate.
func TestRenamePipelineMetrics(t *testing.T) {
	store := &internal.StoreAccumulator{}
	acc := newAccumulator(store)

	acc.PrepareGather()
	acc.AddFields("clickhouse_metrics", map[string]any{
		"query":           7.0,
		"delayed_inserts": 3.0,
	}, nil, time.Now())

	got := collectFinalMetrics(store)

	assertMetrics(t, got, map[string]float64{
		"clickhouse_metrics_active_query":    7,
		"clickhouse_metrics_delayed_inserts": 3,
	})

	if _, ok := got["clickhouse_metrics_query"]; ok {
		t.Errorf("field %q should have been renamed to %q, got both", "clickhouse_metrics_query", "clickhouse_metrics_active_query")
	}
}

// TestDropWrappedNegativeMetrics exercises the "clickhouse_metrics"
// measurement when Clickhouse reports a negative Int64
// (see https://github.com/ClickHouse/ClickHouse/issues/3143), which Telegraf
// casts to Uint64 and turns into a huge near-2^64 value. Such values must be
// dropped rather than emitted as-is.
func TestDropWrappedNegativeMetrics(t *testing.T) {
	store := &internal.StoreAccumulator{}
	acc := newAccumulator(store)

	acc.PrepareGather()
	acc.AddFields("clickhouse_metrics", map[string]any{
		"memory_tracking": uint64(18400000000000000000), // -46043709551616 wrapped to Uint64, ~18.4 EB.
		"delayed_inserts": 3.0,
	}, nil, time.Now())

	got := collectFinalMetrics(store)

	assertMetrics(t, got, map[string]float64{
		"clickhouse_metrics_delayed_inserts": 3,
	})

	if _, ok := got["clickhouse_metrics_memory_tracking"]; ok {
		t.Errorf("field %q should have been dropped as a wrapped-negative value, got %v", "clickhouse_metrics_memory_tracking", got["clickhouse_metrics_memory_tracking"])
	}
}
