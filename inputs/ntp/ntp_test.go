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

package ntp

import (
	"testing"
	"time"

	"github.com/bleemeo/glouton/inputs/internal"
	"github.com/google/go-cmp/cmp"
)

// TestTagsDropped checks that only "remote" -- the peer the metrics are about -- is
// kept. The other tags describe the current selection state, whose value changes
// while ntpd runs, and each change would otherwise start a new metric series.
func TestTagsDropped(t *testing.T) {
	store := &internal.StoreAccumulator{}
	acc := internal.Accumulator{
		RenameGlobal:     renameGlobal,
		TransformMetrics: transformMetrics,
		Accumulator:      store,
	}

	acc.PrepareGather()
	acc.AddFields("ntpq", map[string]any{
		"delay":  1.234,
		"jitter": 0.567,
		"offset": -0.089,
		"reach":  1.0,
	}, map[string]string{
		"remote":       "ntp1.example.com",
		"refid":        "192.168.1.1",
		"stratum":      "2",
		"type":         "u",
		"state_prefix": "*",
	}, time.Now())

	if len(store.Measurement) != 1 {
		t.Fatalf("got %d measurements, want 1: %#v", len(store.Measurement), store.Measurement)
	}

	want := map[string]string{"remote": "ntp1.example.com", "item": "ntp1.example.com"}
	if diff := cmp.Diff(want, store.Measurement[0].Tags); diff != "" {
		t.Errorf("tags of measurement %q (-want +got):\n%s", store.Measurement[0].Name, diff)
	}

	// reach (the fraction of the last 8 polls that succeeded, via the plugin's
	// ReachFormat: "ratio") is converted to a percentage, like every other percentage
	// metric in this codebase.
	if value, _ := store.Measurement[0].Fields["reach_perc"].(float64); value != 100.0 {
		t.Errorf("fields[reach_perc] == %v, want 100.0", store.Measurement[0].Fields["reach_perc"])
	}
}

// TestDurationFieldsConvertedToSeconds checks that delay/jitter/offset -- reported by
// ntpq in milliseconds -- are converted to seconds and renamed accordingly, matching
// every other duration metric in this codebase, and that reach -- reported by the
// plugin as a 0..1 ratio -- is converted to a 0..100 percentage.
func TestDurationFieldsConvertedToSeconds(t *testing.T) {
	store := &internal.StoreAccumulator{}
	acc := internal.Accumulator{
		RenameGlobal:     renameGlobal,
		TransformMetrics: transformMetrics,
		Accumulator:      store,
	}

	acc.PrepareGather()
	acc.AddFields("ntpq", map[string]any{
		"delay":  20.5,
		"jitter": 13.2,
		"offset": -4.8,
		"reach":  0.75,
	}, map[string]string{"remote": "ntp1.example.com"}, time.Now())

	fields := store.Measurement[0].Fields

	want := map[string]float64{
		"delay_seconds":  0.0205,
		"jitter_seconds": 0.0132,
		"offset_seconds": -0.0048,
		"reach_perc":     75.0,
	}

	for name, wantValue := range want {
		got, _ := fields[name].(float64)
		if got != wantValue {
			t.Errorf("fields[%q] == %v, want %v", name, fields[name], wantValue)
		}
	}

	for _, name := range []string{"delay", "jitter", "offset", "reach"} {
		if _, ok := fields[name]; ok {
			t.Errorf("raw field %q should have been renamed, still present", name)
		}
	}
}
