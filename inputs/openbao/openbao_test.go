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

package openbao

import (
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

// TestRenamePipeline exercises the full renameGlobal -> transformMetrics ->
// renameMetrics chain and checks the final metric name against what the
// metric allow-list in agent/metric-filter/metric.go expects.
func TestRenamePipeline(t *testing.T) {
	cases := []struct {
		name        string
		measurement string
		fields      map[string]any
		wantMetric  string
		wantValue   float64
	}{
		{
			name:        "leadership_lost is renamed to leadership_losses",
			measurement: "vault.core.leadership_lost",
			fields:      map[string]any{"rate": 2.0, "count": uint64(100)},
			wantMetric:  "bao_core_leadership_losses",
			wantValue:   2,
		},
		{
			name:        "handle_request rate is promoted to count then pluralized",
			measurement: "vault.core.handle_request",
			fields:      map[string]any{"rate": 5.0, "count": uint64(50)},
			wantMetric:  "bao_core_handle_requests",
			wantValue:   5,
		},
		{
			name:        "handle_login_request rate is promoted to count then pluralized",
			measurement: "vault.core.handle_login_request",
			fields:      map[string]any{"rate": 1.0, "count": uint64(10)},
			wantMetric:  "bao_core_handle_login_requests",
			wantValue:   1,
		},
		{
			name:        "check_token rate is promoted to count then pluralized",
			measurement: "vault.core.check_token",
			fields:      map[string]any{"rate": 3.0, "count": uint64(30)},
			wantMetric:  "bao_core_check_tokens",
			wantValue:   3,
		},
		{
			name:        "gauge value keeps the measurement name as-is",
			measurement: "vault.core.active",
			fields:      map[string]any{"value": 1.0},
			wantMetric:  "bao_core_active",
			wantValue:   1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &internal.StoreAccumulator{}
			acc := internal.Accumulator{
				RenameGlobal:     renameGlobal,
				TransformMetrics: transformMetrics,
				RenameMetrics:    renameMetrics,
				Accumulator:      store,
			}

			acc.PrepareGather()
			acc.AddFields(tc.measurement, tc.fields, nil, time.Now())

			got := collectFinalMetrics(store)

			value, ok := got[tc.wantMetric]
			if !ok {
				t.Fatalf("metric %q not emitted, got metrics: %v", tc.wantMetric, got)
			}

			if value != tc.wantValue {
				t.Errorf("metric %q == %v, want %v", tc.wantMetric, value, tc.wantValue)
			}
		})
	}
}
