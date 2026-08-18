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

package chrony

import (
	"testing"
	"time"

	"github.com/bleemeo/glouton/inputs/internal"
	"github.com/google/go-cmp/cmp"
)

// TestTagsDropped checks that the tags describing the current synchronization state
// are dropped: their value changes while chronyd runs, and each change would
// otherwise start a new metric series.
func TestTagsDropped(t *testing.T) {
	store := &internal.StoreAccumulator{}
	acc := internal.Accumulator{
		RenameGlobal: renameGlobal,
		Accumulator:  store,
	}

	acc.PrepareGather()
	acc.AddFields("chrony", map[string]any{
		"frequency":       -1.5,
		"system_time":     0.000012,
		"last_offset":     0.000001,
		"rms_offset":      0.000003,
		"root_delay":      0.02,
		"root_dispersion": 0.001,
		"skew":            0.05,
	}, map[string]string{
		"leap_status":  "normal",
		"reference_id": "C0248F97",
		"stratum":      "3",
		"source":       "/run/chrony/chronyd.sock",
	}, time.Now())

	if len(store.Measurement) != 1 {
		t.Fatalf("got %d measurements, want 1: %#v", len(store.Measurement), store.Measurement)
	}

	if diff := cmp.Diff(map[string]string{}, store.Measurement[0].Tags); diff != "" {
		t.Errorf("tags of measurement %q (-want +got):\n%s", store.Measurement[0].Name, diff)
	}

	// The metrics themselves must pass through untouched: they are all gauges.
	if value, _ := store.Measurement[0].Fields["skew"].(float64); value != 0.05 {
		t.Errorf("fields[skew] == %v, want 0.05", store.Measurement[0].Fields["skew"])
	}
}
