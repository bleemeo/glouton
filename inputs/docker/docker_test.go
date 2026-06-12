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

package docker

import (
	"testing"

	"github.com/bleemeo/glouton/inputs/internal"
)

func TestTransformMetrics_CPUThrottled(t *testing.T) {
	ctx := internal.GatherContext{Measurement: measurementContainerCPU}

	tests := []struct {
		name        string
		fields      map[string]float64
		wantPresent bool
		wantValue   float64
	}{
		{
			name:        "30 of 40 periods throttled",
			fields:      map[string]float64{fieldThrottlingPeriods: 40, fieldThrottlingThrottledPeriods: 30},
			wantPresent: true,
			wantValue:   75,
		},
		{
			name:        "no period elapsed",
			fields:      map[string]float64{"throttling_periods": 0, "throttling_throttled_periods": 0},
			wantPresent: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := transformMetrics(ctx, tt.fields, nil)

			value, ok := got["throttled_perc"]
			if ok != tt.wantPresent {
				t.Fatalf("throttled_perc present = %v, want %v", ok, tt.wantPresent)
			}

			if tt.wantPresent && value != tt.wantValue {
				t.Errorf("throttled_perc = %v, want %v", value, tt.wantValue)
			}
		})
	}
}
