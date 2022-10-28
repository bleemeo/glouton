// Copyright 2015-2022 Bleemeo
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

package delay

import (
	"math"
	"testing"
	"time"
)

func TestExponential(t *testing.T) {
	type args struct {
		base        time.Duration
		powerFactor float64
		max         time.Duration
	}

	tests := []struct {
		name  string
		args  args
		wants []time.Duration
	}{
		{
			name: "60-seconds-1.55",
			args: args{
				base:        60 * time.Second,
				powerFactor: 1.55,
				max:         time.Hour,
			},
			wants: []time.Duration{
				60 * time.Second,
				93 * time.Second,
				144 * time.Second,
				3*time.Minute + 43*time.Second,
				5*time.Minute + 46*time.Second,
				8*time.Minute + 56*time.Second,
				13*time.Minute + 52*time.Second,
				21*time.Minute + 29*time.Second,
				33*time.Minute + 18*time.Second,
				51*time.Minute + 38*time.Second,
				time.Hour,
				time.Hour,
			},
		},
		{
			name: "2-seconds-1.55",
			args: args{
				base:        2 * time.Second,
				powerFactor: 1.55,
				max:         time.Hour,
			},
			wants: []time.Duration{
				2 * time.Second,
				3 * time.Second,
				4 * time.Second,
				7 * time.Second,
			},
		},
		{
			name: "1-hour-1.75",
			args: args{
				base:        time.Hour,
				powerFactor: 1.75,
				max:         12 * time.Hour,
			},
			wants: []time.Duration{
				time.Hour,
				time.Hour + 45*time.Minute,
				3*time.Hour + 3*time.Minute + 45*time.Second,
				5*time.Hour + 21*time.Minute + 33*time.Second,
				9*time.Hour + 22*time.Minute + 44*time.Second,
				12 * time.Hour,
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for n, want := range tt.wants {
				if got := Exponential(tt.args.base, tt.args.powerFactor, n+1, tt.args.max); got != want {
					t.Errorf("Exponential(%d) = %v, want %v", n, got, want)
				}
			}
		})
	}
}

func TestExponentialMax(t *testing.T) {
	type args struct {
		base        time.Duration
		powerFactor float64
		max         time.Duration
	}

	const maxIter = 100000

	tests := []struct {
		name         string
		args         args
		wantLessThan time.Duration
		wantMoreThan time.Duration
	}{
		{
			name: "60-seconds-1.55",
			args: args{
				base:        60 * time.Second,
				powerFactor: 1.55,
				max:         time.Hour,
			},
			wantLessThan: time.Hour,
			wantMoreThan: 60 * time.Second,
		},
		{
			name: "2-seconds-1.55",
			args: args{
				base:        2 * time.Second,
				powerFactor: 1.55,
				max:         time.Hour,
			},
			wantLessThan: time.Hour,
			wantMoreThan: 2 * time.Second,
		},
		{
			name: "1-hour-1.75",
			args: args{
				base:        time.Hour,
				powerFactor: 1.75,
				max:         12 * time.Hour,
			},
			wantLessThan: 12 * time.Hour,
			wantMoreThan: time.Hour,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for n := 0; n < maxIter; n++ {
				got := Exponential(tt.args.base, tt.args.powerFactor, n, tt.args.max)

				if got > tt.wantLessThan {
					t.Fatalf("Exponential(%d) = %v, want less than %v", n, got, tt.wantLessThan)
				}

				if got < tt.wantMoreThan {
					t.Fatalf("Exponential(%d) = %v, want more than %v", n, got, tt.wantMoreThan)
				}
			}

			for _, n := range []int{-1000, math.MaxInt, math.MinInt} {
				got := Exponential(tt.args.base, tt.args.powerFactor, n, tt.args.max)

				if got > tt.wantLessThan {
					t.Fatalf("Exponential(%d) = %v, want less than %v", n, got, tt.wantLessThan)
				}

				if got < tt.wantMoreThan {
					t.Fatalf("Exponential(%d) = %v, want more than %v", n, got, tt.wantMoreThan)
				}
			}
		})
	}
}
