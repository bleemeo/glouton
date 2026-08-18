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

package bind

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
		RenameGlobal:               renameGlobal,
		RenameMetrics:              renameMetrics,
		ShouldDifferentiateMetrics: shouldDifferentiateMetrics,
		Accumulator:                store,
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

// TestCounterDifferentiation exercises "bind_counter": every field (however
// dynamically named, e.g. per DNS record type) is a running total since
// named started, so it gets differentiated into a per-second rate, on top of
// the usual ALL_CAPS/CamelCase -> snake_case rename.
func TestCounterDifferentiation(t *testing.T) {
	store := &internal.StoreAccumulator{}
	acc := newAccumulator(store)

	t0 := time.Now()
	t1 := t0.Add(10 * time.Second)

	acc.PrepareGather()
	acc.AddFields("bind_counter", map[string]any{
		"QUERY":       uint64(1000),
		"NXDOMAIN":    uint64(50),
		"SERVFAIL":    uint64(10),
		"QrySuccess":  uint64(900),
		"QryNXDOMAIN": uint64(50),
	}, map[string]string{"type": "nsstat", "url": "http://127.0.0.1:8053/xml/v3"}, t0)

	// Discard the first gather: every field is a differentiated counter, so
	// it has no rate yet (no history).
	store.Measurement = nil

	acc.PrepareGather()
	acc.AddFields("bind_counter", map[string]any{
		"QUERY":       uint64(1000 + 100), // rate = 10/s
		"NXDOMAIN":    uint64(50 + 10),    // rate = 1/s
		"SERVFAIL":    uint64(10 + 5),     // rate = 0.5/s
		"QrySuccess":  uint64(900 + 80),   // rate = 8/s
		"QryNXDOMAIN": uint64(50 + 10),    // rate = 1/s
	}, map[string]string{"type": "nsstat", "url": "http://127.0.0.1:8053/xml/v3"}, t1)

	got := collectFinalMetrics(store)

	assertMetrics(t, got, map[string]float64{
		"bind_counter_query":        10,
		"bind_counter_nxdomain":     1,
		"bind_counter_servfail":     0.5,
		"bind_counter_qry_success":  8,
		"bind_counter_qry_nxdomain": 1,
	})

	// The statistics-channel URL is redundant with the labels already set on
	// service metrics, while "type" tells which counter set this is.
	assertTags(t, store, map[string]string{"type": "nsstat"})
}

// TestMemoryNotDifferentiated exercises "bind_memory": total_use/in_use are
// current memory usage (gauges), not counters, and must pass through
// untouched.
func TestMemoryNotDifferentiated(t *testing.T) {
	store := &internal.StoreAccumulator{}
	acc := newAccumulator(store)

	acc.PrepareGather()
	acc.AddFields("bind_memory", map[string]any{
		"total_use": uint64(16663252),
		"in_use":    uint64(4113717),
	}, map[string]string{
		"url":    "http://127.0.0.1:8053/xml/v3",
		"source": "127.0.0.1",
		"port":   "8053",
	}, time.Now())

	got := collectFinalMetrics(store)

	assertMetrics(t, got, map[string]float64{
		"bind_memory_total_use": 16663252,
		"bind_memory_in_use":    4113717,
	})

	assertTags(t, store, map[string]string{})
}
