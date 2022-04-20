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
