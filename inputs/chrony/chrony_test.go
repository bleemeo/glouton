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
		RenameGlobal:     renameGlobal,
		TransformMetrics: transformMetrics,
		Accumulator:      store,
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

// TestActivityPassesThrough checks that chrony_activity's fields (counts of
// configured sources by reachability) pass through untouched.
func TestActivityPassesThrough(t *testing.T) {
	store := &internal.StoreAccumulator{}
	acc := internal.Accumulator{
		RenameGlobal:     renameGlobal,
		TransformMetrics: transformMetrics,
		Accumulator:      store,
	}

	acc.PrepareGather()
	acc.AddFields("chrony_activity", map[string]any{
		"online":        3,
		"offline":       1,
		"burst_online":  0,
		"burst_offline": 0,
		"unresolved":    0,
	}, map[string]string{"source": "/run/chrony/chronyd.sock"}, time.Now())

	fields := store.Measurement[0].Fields

	want := map[string]float64{"online": 3, "offline": 1}
	for name, wantValue := range want {
		if got, _ := fields[name].(float64); got != wantValue {
			t.Errorf("fields[%q] == %v, want %v", name, fields[name], wantValue)
		}
	}
}

// TestSourcesIPBecomesALabelAndFieldsConverted checks that chrony_sources' "ip" field
// becomes a label of its own -- so each source gets its own series without the address
// being buried in the item next to the container name -- that reachability, the raw
// 0..255 value of the 8-bit reach shift register, is converted into the percentage of
// the last 8 polls that succeeded (a bit count, not the register's numeric value), and
// that latest_measurement (already in seconds) is renamed accordingly.
func TestSourcesIPBecomesALabelAndFieldsConverted(t *testing.T) {
	store := &internal.StoreAccumulator{}
	acc := internal.Accumulator{
		RenameGlobal:     renameGlobal,
		TransformMetrics: transformMetrics,
		Accumulator:      store,
	}

	acc.PrepareGather()
	acc.AddFields("chrony_sources", map[string]any{
		"ip":                 "17.253.108.125",
		"reachability":       uint16(0b0011_1111), // 63: 6 of the last 8 polls succeeded
		"latest_measurement": 0.000048,
		"stratum":            uint16(2),
	}, map[string]string{
		"peer":   "time.apple.com",
		"source": "/run/chrony/chronyd.sock",
	}, time.Now())

	// No item: it is the service instance, set once for the whole input, and the peer
	// name stays as chronyd reported it (several pool members share one).
	wantTags := map[string]string{"peer": "time.apple.com", peerAddressTag: "17.253.108.125"}
	if diff := cmp.Diff(wantTags, store.Measurement[0].Tags); diff != "" {
		t.Errorf("tags of measurement %q (-want +got):\n%s", store.Measurement[0].Name, diff)
	}

	fields := store.Measurement[0].Fields

	want := map[string]float64{
		"reachability_perc":          75, // 6/8 * 100
		"latest_measurement_seconds": 0.000048,
	}
	for name, wantValue := range want {
		if got, _ := fields[name].(float64); got != wantValue {
			t.Errorf("fields[%q] == %v, want %v", name, fields[name], wantValue)
		}
	}

	for _, name := range []string{"reachability", "latest_measurement"} {
		if _, ok := fields[name]; ok {
			t.Errorf("raw field %q should have been renamed, still present", name)
		}
	}
}

// TestSourcesFromSamePoolGetDistinctAddresses checks that two sources resolved from the
// same "pool" directive -- which chronyd reports under the identical "peer" name -- still
// end up as two distinct series, told apart by their (always unique) address.
func TestSourcesFromSamePoolGetDistinctAddresses(t *testing.T) {
	store := &internal.StoreAccumulator{}
	acc := internal.Accumulator{
		RenameGlobal:     renameGlobal,
		TransformMetrics: transformMetrics,
		Accumulator:      store,
	}

	acc.PrepareGather()

	for _, ip := range []string{"17.253.108.125", "17.253.108.253"} {
		acc.AddFields("chrony_sources", map[string]any{
			"ip":           ip,
			"reachability": uint16(255),
		}, map[string]string{"peer": "time.apple.com"}, time.Now())
	}

	if len(store.Measurement) != 2 {
		t.Fatalf("got %d measurements, want 2 (one per IP): %#v", len(store.Measurement), store.Measurement)
	}

	addresses := map[string]bool{}
	for _, m := range store.Measurement {
		addresses[m.Tags[peerAddressTag]] = true
	}

	if !addresses["17.253.108.125"] || !addresses["17.253.108.253"] {
		t.Errorf("%s labels == %v, want both pool IPs represented", peerAddressTag, addresses)
	}
}
