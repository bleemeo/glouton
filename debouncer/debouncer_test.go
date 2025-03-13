// Copyright 2015-2025 Bleemeo
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

package debouncer

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestDebouncer_Trigger(t *testing.T) {
	const (
		equalityMargin = 30 * time.Millisecond
		delay          = 100 * time.Millisecond
		period         = 300 * time.Millisecond
		step           = 50 * time.Millisecond // Step should be less than delay and more than equalityMargin
	)

	tests := []struct {
		name               string
		period             time.Duration
		delay              time.Duration
		ctxDuration        time.Duration
		triggerDelayFromT0 []time.Duration
		wantedDelayFromT0  []time.Duration
	}{
		{
			name:               "once",
			triggerDelayFromT0: []time.Duration{0},
			wantedDelayFromT0:  []time.Duration{delay},
			period:             period,
			delay:              delay,
		},
		{
			name:               "no-delay",
			triggerDelayFromT0: []time.Duration{0},
			wantedDelayFromT0:  []time.Duration{0},
			period:             period,
			delay:              0,
		},
		{
			name:               "no-delay-multiple",
			triggerDelayFromT0: []time.Duration{0, period / 2},
			wantedDelayFromT0:  []time.Duration{0, period},
			period:             period,
			delay:              0,
		},
		{
			name:               "multiple-within-delay",
			triggerDelayFromT0: []time.Duration{0, delay / 4, delay / 2},
			wantedDelayFromT0:  []time.Duration{delay},
			period:             period,
			delay:              delay,
		},
		{
			name: "multiple-after-delay",
			triggerDelayFromT0: []time.Duration{
				0, delay / 2,
				delay + delay/2, period / 2, 3 * period / 4,
				3 * period / 2,
			},
			wantedDelayFromT0: []time.Duration{
				delay,
				delay + period,
				delay + 2*period,
			},
			period: period,
			delay:  delay,
		},
		{
			name: "quiet-then-burst",
			triggerDelayFromT0: []time.Duration{
				0, delay / 2,
				2*period + step, 2*period + step + delay/2, 2*period + step + 3*delay/2,
			},
			wantedDelayFromT0: []time.Duration{
				delay,
				2*period + step + delay,
				3*period + step + delay,
			},
			period: period,
			delay:  delay,
		},
		{
			name:        "context-expire",
			ctxDuration: period,
			triggerDelayFromT0: []time.Duration{
				0, 2 * delay,
			},
			wantedDelayFromT0: []time.Duration{
				delay,
			},
			period: period,
			delay:  delay,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				maxDuration time.Duration
				calledAt    []time.Time
				l           sync.Mutex
			)

			for _, d := range tt.triggerDelayFromT0 {
				if d > maxDuration {
					maxDuration = d
				}
			}

			for _, d := range tt.wantedDelayFromT0 {
				if d > maxDuration {
					maxDuration = d
				}
			}

			ctx := t.Context()
			t0 := time.Now()
			testDeadline := t0.Add(2 * tt.period).Add(maxDuration)
			target := func(_ context.Context) {
				l.Lock()
				defer l.Unlock()

				calledAt = append(calledAt, time.Now())
			}

			if tt.ctxDuration != 0 {
				var cancel context.CancelFunc

				ctx, cancel = context.WithTimeout(ctx, tt.ctxDuration)
				defer cancel()
			}

			dd := New(ctx, target, tt.delay, tt.period)

			for _, d := range tt.triggerDelayFromT0 {
				delta := time.Until(t0.Add(d))

				if delta <= 0 && d > 0 {
					t.Logf("At delay=%v, we want to sleep %v: test run too slowly", d, delta)
				}

				time.Sleep(delta)

				dd.Trigger()
			}

			delta := time.Until(testDeadline)
			if delta <= 0 {
				t.Logf("The test deadline is already expired, test run too slowly (delta=%v)", delta)
			}

			time.Sleep(delta)

			l.Lock()
			defer l.Unlock()

			for i, want := range tt.wantedDelayFromT0 {
				if len(calledAt) <= i {
					t.Errorf("Missing call after %v", want)

					continue
				}

				got := calledAt[i].Sub(t0)
				delta := want - got

				if delta < -equalityMargin || delta > equalityMargin {
					t.Errorf("callerAfter %v, want %v", got, want)
				}
			}
		})
	}
}
