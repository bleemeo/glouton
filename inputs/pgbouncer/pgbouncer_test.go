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

package pgbouncer

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
		TransformMetrics: transformMetrics,
		DifferentiatedMetrics: []string{
			"total_query_count",
			"total_query_time",
			"total_received",
			"total_sent",
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

// TestRenamePipelineStats exercises the "pgbouncer" measurement (SHOW
// STATS), where cumulative counters are converted to per-second rates then
// renamed (total_query_count -> query, total_received -> received_bytes,
// total_sent -> sent_bytes) and total_query_time is combined with
// total_query_count into an average query_time_seconds.
func TestRenamePipelineStats(t *testing.T) {
	store := &internal.StoreAccumulator{}
	acc := newAccumulator(store)

	t0 := time.Now()
	t1 := t0.Add(10 * time.Second)

	acc.PrepareGather()
	acc.AddFields("pgbouncer", map[string]any{
		"total_query_count": int64(1000),
		"total_query_time":  int64(5000000),
		"total_received":    int64(200000),
		"total_sent":        int64(100000),
	}, nil, t0)

	// Discard the first gather: every field is a differentiated counter, so
	// it has no rate yet (no history).
	store.Measurement = nil

	acc.PrepareGather()
	acc.AddFields("pgbouncer", map[string]any{
		"total_query_count": int64(1000 + 50),         // rate = 5/s
		"total_query_time":  int64(5000000 + 5000000), // rate = 500 000/s
		"total_received":    int64(200000 + 1000),     // rate = 100/s
		"total_sent":        int64(100000 + 500),      // rate = 50/s
	}, nil, t1)

	got := collectFinalMetrics(store)

	// query_time_seconds = queryTimeRate / queryCountRate / 1e6 = 500 000 / 5 / 1e6 = 0.1
	assertMetrics(t, got, map[string]float64{
		"pgbouncer_query":              5,
		"pgbouncer_received_bytes":     100,
		"pgbouncer_sent_bytes":         50,
		"pgbouncer_query_time_seconds": 0.1,
	})
}

// TestRenamePipelinePools exercises the "pgbouncer_pools" measurement (SHOW
// POOLS), which is untouched by transformMetrics: fields are emitted as-is.
func TestRenamePipelinePools(t *testing.T) {
	store := &internal.StoreAccumulator{}
	acc := newAccumulator(store)

	acc.PrepareGather()
	acc.AddFields("pgbouncer_pools", map[string]any{
		"cl_active":  int64(3),
		"cl_waiting": int64(1),
		"sv_active":  int64(2),
		"sv_idle":    int64(4),
		"maxwait":    int64(7),
	}, nil, time.Now())

	got := collectFinalMetrics(store)

	assertMetrics(t, got, map[string]float64{
		"pgbouncer_pools_cl_active":  3,
		"pgbouncer_pools_cl_waiting": 1,
		"pgbouncer_pools_sv_active":  2,
		"pgbouncer_pools_sv_idle":    4,
		"pgbouncer_pools_maxwait":    7,
	})
}
