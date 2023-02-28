// Copyright 2015-2023 Bleemeo
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

package synchronizer

import (
	"math"
	"testing"
)

func TestQueryToThreshold(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		query         string
		wantSuccess   bool
		wantThreshold minimalThreshold
	}{
		{
			name:        "high-threshold-vector-first",
			query:       "cpu_used > 80",
			wantSuccess: true,
			wantThreshold: minimalThreshold{
				high:       80,
				low:        math.NaN(),
				metricName: "cpu_used",
			},
		},
		{
			name:        "high-threshold-number-first",
			query:       "80 < cpu_used",
			wantSuccess: true,
			wantThreshold: minimalThreshold{
				high:       80,
				low:        math.NaN(),
				metricName: "cpu_used",
			},
		},
		{
			name:        "low-threshold-vector-first",
			query:       "cpu_used < 10",
			wantSuccess: true,
			wantThreshold: minimalThreshold{
				high:       math.NaN(),
				low:        10,
				metricName: "cpu_used",
			},
		},
		{
			name:        "low-threshold-number-first",
			query:       "10 > cpu_used",
			wantSuccess: true,
			wantThreshold: minimalThreshold{
				high:       math.NaN(),
				low:        10,
				metricName: "cpu_used",
			},
		},
		{
			name:        "high-threshold-vector-first-non-strict",
			query:       "cpu_used >= 80",
			wantSuccess: true,
			wantThreshold: minimalThreshold{
				high:       79.99999999999999,
				low:        math.NaN(),
				metricName: "cpu_used",
			},
		},
		{
			name:        "high-threshold-number-first-non-strict",
			query:       "80 <= cpu_used",
			wantSuccess: true,
			wantThreshold: minimalThreshold{
				high:       79.99999999999999,
				low:        math.NaN(),
				metricName: "cpu_used",
			},
		},
		{
			name:        "low-threshold-vector-first-non-strict",
			query:       "cpu_used <= 10",
			wantSuccess: true,
			wantThreshold: minimalThreshold{
				high:       math.NaN(),
				low:        10.000000000000002,
				metricName: "cpu_used",
			},
		},
		{
			name:        "low-threshold-number-first-non-strict",
			query:       "10 >= cpu_used",
			wantSuccess: true,
			wantThreshold: minimalThreshold{
				high:       math.NaN(),
				low:        10.000000000000002,
				metricName: "cpu_used",
			},
		},
		{
			name:        "two-binary-expressions",
			query:       "10 < cpu_used < 100",
			wantSuccess: false,
		},
		{
			name:        "with-matchers",
			query:       `cpu_used{instance="myserver"} < 100`,
			wantSuccess: false,
		},
		{
			name:        "with-aggregation",
			query:       `sum(cpu_used) < 100`,
			wantSuccess: false,
		},
		{
			name:        "invalid-query",
			query:       ")cpu_used(",
			wantSuccess: false,
		},
	}

	for _, test := range tests {
		test := test

		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			threshold, success := queryToThreshold(test.query)
			if success != test.wantSuccess {
				t.Fatalf("promqlToThreshold(%s): success=%v, expected %v", test.query, success, test.wantSuccess)
			}

			if success {
				if threshold.low != test.wantThreshold.low &&
					!(math.IsNaN(threshold.low) && math.IsNaN(test.wantThreshold.low)) {
					t.Errorf("promqlToThreshold(%s): low threshold=%v, expected %v",
						test.query, threshold.low, test.wantThreshold.low)
				}

				if threshold.high != test.wantThreshold.high &&
					!(math.IsNaN(threshold.high) && math.IsNaN(test.wantThreshold.high)) {
					t.Errorf("promqlToThreshold(%s): high threshold=%v, expected %v",
						test.query, threshold.high, test.wantThreshold.high)
				}

				if threshold.metricName != test.wantThreshold.metricName {
					t.Errorf("promqlToThreshold(%s): got metric %v, expected %v",
						test.query, threshold.metricName, test.wantThreshold.metricName)
				}
			}
		})
	}
}
