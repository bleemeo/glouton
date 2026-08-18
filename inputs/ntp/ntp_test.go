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
		RenameGlobal: renameGlobal,
		Accumulator:  store,
	}

	acc.PrepareGather()
	acc.AddFields("ntpq", map[string]any{
		"delay":  1.234,
		"jitter": 0.567,
		"offset": -0.089,
		"reach":  int64(377),
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

	// The metrics themselves must pass through untouched: ntpq reports the current
	// state of each peer, not cumulative counters.
	if value, _ := store.Measurement[0].Fields["reach"].(float64); value != 377 {
		t.Errorf("fields[reach] == %v, want 377", store.Measurement[0].Fields["reach"])
	}
}
