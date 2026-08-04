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

//go:build !windows

package varnish

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
			"cache_hit",
			"cache_miss",
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

// TestDifferentiationAndHitRatio checks that cache_hit/cache_miss (lifetime
// totals since Varnish started) are differentiated into per-second rates,
// that hit_ratio is computed from those rates, and that uptime (itself a
// monotonically increasing counter meant to be read as-is) passes through
// untouched.
func TestDifferentiationAndHitRatio(t *testing.T) {
	store := &internal.StoreAccumulator{}
	acc := newAccumulator(store)

	t0 := time.Now()
	t1 := t0.Add(10 * time.Second)

	acc.PrepareGather()
	acc.AddFields("varnish", map[string]any{
		"cache_hit":  uint64(1000),
		"cache_miss": uint64(100),
		"uptime":     uint64(3600),
	}, nil, t0)

	// Discard the first gather: cache_hit/cache_miss have no rate yet (no
	// history).
	store.Measurement = nil

	acc.PrepareGather()
	acc.AddFields("varnish", map[string]any{
		"cache_hit":  uint64(1000 + 900), // rate = 90/s
		"cache_miss": uint64(100 + 100),  // rate = 10/s
		"uptime":     uint64(3610),
	}, nil, t1)

	got := collectFinalMetrics(store)

	// hit_ratio = 90 / (90+10) = 0.9
	assertMetrics(t, got, map[string]float64{
		"varnish_cache_hit":       90,
		"varnish_cache_miss":      10,
		"varnish_cache_hit_ratio": 0.9,
		"varnish_uptime":          3610,
	})
}
