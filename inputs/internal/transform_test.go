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

package internal

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

func TestAvgDuration(t *testing.T) {
	cases := []struct {
		name        string
		fields      map[string]float64
		unitDivisor float64
		want        map[string]float64
	}{
		{
			// Tomcat's numbers: 400 ms of processing and 10 requests per second is 40 ms
			// spent on each request.
			name:        "milliseconds",
			fields:      map[string]float64{"duration": 400, "count": 10},
			unitDivisor: MsPerSecond,
			want:        map[string]float64{"count": 10, "duration_seconds": 0.04},
		},
		{
			// PgBouncer's numbers.
			name:        "microseconds",
			fields:      map[string]float64{"duration": 500000, "count": 5},
			unitDivisor: UsPerSecond,
			want:        map[string]float64{"count": 5, "duration_seconds": 0.1},
		},
		{
			name:        "nanoseconds",
			fields:      map[string]float64{"duration": 5e9, "count": 2},
			unitDivisor: NsPerSecond,
			want:        map[string]float64{"count": 2, "duration_seconds": 2.5},
		},
		{
			name:        "the other fields are left alone",
			fields:      map[string]float64{"duration": 400, "count": 10, "bytes_sent": 7},
			unitDivisor: MsPerSecond,
			want:        map[string]float64{"count": 10, "bytes_sent": 7, "duration_seconds": 0.04},
		},
		{
			// Dividing by it would be a division by zero. The raw duration is dropped all
			// the same: a rate of milliseconds per second means nothing to a user.
			name:        "no operation completed -> no average, and the raw duration still goes",
			fields:      map[string]float64{"duration": 400, "count": 0},
			unitDivisor: MsPerSecond,
			want:        map[string]float64{"count": 0},
		},
		{
			// Requests were served but took no measurable time, which is a real 0 and not
			// the missing value above.
			name:        "no time spent -> an average of zero",
			fields:      map[string]float64{"duration": 0, "count": 10},
			unitDivisor: MsPerSecond,
			want:        map[string]float64{"count": 10, "duration_seconds": 0},
		},
		{
			// The service restarted: the accumulator's differentiation drops a counter
			// that went backwards instead of handing AvgDuration a negative rate, so this
			// only ever shows up as the count guard below rejecting the missing average.
			name:        "the count counter reset -> no average",
			fields:      map[string]float64{"duration": 400, "count": -10},
			unitDivisor: MsPerSecond,
			want:        map[string]float64{"count": -10},
		},
		{
			// The count isn't differentiated, or the plugin stopped reporting it.
			name:        "no count field -> no average",
			fields:      map[string]float64{"duration": 400},
			unitDivisor: MsPerSecond,
			want:        map[string]float64{},
		},
		{
			// The first gather of a differentiated field has no rate yet.
			name:        "no duration field -> nothing to do",
			fields:      map[string]float64{"count": 10},
			unitDivisor: MsPerSecond,
			want:        map[string]float64{"count": 10},
		},
		{
			name:        "neither field",
			fields:      map[string]float64{"bytes_sent": 7},
			unitDivisor: MsPerSecond,
			want:        map[string]float64{"bytes_sent": 7},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			AvgDuration(tc.fields, "duration", "count", "duration_seconds", tc.unitDivisor)

			if diff := cmp.Diff(tc.want, tc.fields, cmpopts.EquateApprox(0, 1e-9)); diff != "" {
				t.Errorf("AvgDuration() fields (-want +got):\n%s", diff)
			}
		})
	}
}
