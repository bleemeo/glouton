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

package dovecot

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
		DifferentiatedMetrics: []string{
			"num_logins",
			"num_cmds",
			"mail_cache_hits",
			"disk_input",
			"disk_output",
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

// TestDifferentiation checks that num_logins/num_cmds/mail_cache_hits/
// disk_input/disk_output (cumulative since reset_timestamp) are
// differentiated into per-second rates, while num_connected_sessions (the
// live count of currently open IMAP sessions, per Dovecot's own docs -- a
// gauge, not a counter) passes through untouched.
func TestDifferentiation(t *testing.T) {
	store := &internal.StoreAccumulator{}
	acc := newAccumulator(store)

	t0 := time.Now()
	t1 := t0.Add(10 * time.Second)

	acc.PrepareGather()
	acc.AddFields("dovecot", map[string]any{
		"num_logins":             uint64(174827),
		"num_cmds":               uint64(917469),
		"num_connected_sessions": uint64(1204),
		"mail_cache_hits":        uint64(68192209),
		"disk_input":             uint64(6493168218112),
		"disk_output":            uint64(17978638815232),
	}, nil, t0)

	// Discard the first gather: every differentiated field has no rate yet
	// (no history).
	store.Measurement = nil

	acc.PrepareGather()
	acc.AddFields("dovecot", map[string]any{
		"num_logins":             uint64(174827 + 100),            // rate = 10/s
		"num_cmds":               uint64(917469 + 500),            // rate = 50/s
		"num_connected_sessions": uint64(1300),                    // live gauge, new value
		"mail_cache_hits":        uint64(68192209 + 2000),         // rate = 200/s
		"disk_input":             uint64(6493168218112 + 100000),  // rate = 10000/s
		"disk_output":            uint64(17978638815232 + 200000), // rate = 20000/s
	}, nil, t1)

	got := collectFinalMetrics(store)

	assertMetrics(t, got, map[string]float64{
		"dovecot_num_logins":             10,
		"dovecot_num_cmds":               50,
		"dovecot_mail_cache_hits":        200,
		"dovecot_disk_input":             10000,
		"dovecot_disk_output":            20000,
		"dovecot_num_connected_sessions": 1300,
	})
}
