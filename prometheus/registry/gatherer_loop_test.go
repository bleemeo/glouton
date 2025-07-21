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

package registry

import (
	"context"
	"sync"
	"testing"
	"time"
	_ "unsafe"

	_ "github.com/prometheus/prometheus/scrape"
)

func Test_startScrapeLoop(t *testing.T) {
	t.Parallel()

	// Same constant has the Prometheus one.
	const scrapeTimestampTolerance = 2 * time.Millisecond

	const testDuration = 5 * time.Second

	tests := []struct {
		interval time.Duration
	}{
		{
			// 201 is the minimal interval for Prometheus to align timestamp.
			// Value below this limit aren't aligned.
			interval: 201 * time.Millisecond,
		},
		{interval: 500 * time.Millisecond},
		{interval: 1 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.interval.String(), func(t *testing.T) {
			t.Parallel()

			var (
				result      []time.Time
				notAfter    time.Time
				lengthAtEnd int
				l           sync.Mutex
			)

			deadlineCtx, cancel := context.WithTimeout(t.Context(), testDuration)
			defer cancel()

			callback := func(_ context.Context, _ context.Context, t0 time.Time) {
				l.Lock()
				result = append(result, t0) //nolint: wsl_v5
				l.Unlock()                  //nolint: wsl_v5
			}

			loop := startScrapeLoop(tt.interval, tt.interval, 0, callback, "", false)

			<-deadlineCtx.Done()

			loop.stop()

			notAfter = time.Now()

			l.Lock()
			lengthAtEnd = len(result) //nolint: wsl_v5
			l.Unlock()                //nolint: wsl_v5

			// More time to ensure loop is shutdown.
			time.Sleep(300 * time.Millisecond)

			l.Lock()
			defer l.Unlock()

			if len(result) != lengthAtEnd {
				t.Errorf("result(length) = %d, want %d. It changed after loop must be stopped", len(result), lengthAtEnd)
			}

			if len(result) < 2 {
				t.Errorf("len(result) = %d, want at least 2", len(result))
			}

			for i, v := range result {
				if i == 0 {
					continue
				}

				if !v.After(result[i-1]) {
					t.Errorf("result[%d] = %v, want more than %v", i, v, result[i-1])
				}

				delta := v.Sub(result[i-1])
				if delta != tt.interval && delta-tt.interval > scrapeTimestampTolerance {
					t.Errorf("result[%d]-result[%d-1] = %v, want %v", i, i, delta, tt.interval)
				}

				if !v.Before(notAfter) {
					t.Errorf("result[%d] = %v, want less than %v", i, v, notAfter)
				}
			}
		})
	}
}

func Test_scrapeLoopOffset(t *testing.T) {
	tests := []struct {
		name       string
		now        time.Time
		interval   time.Duration
		jitterSeed uint64
		want       time.Time
	}{
		{
			// This alignment is wanted to avoid any possible breaking change with
			// old behavior.
			name:       "base-align",
			now:        time.Date(2024, 4, 30, 11, 51, 49, 0, time.UTC),
			interval:   10 * time.Second,
			jitterSeed: 0,
			want:       time.Date(2024, 4, 30, 11, 51, 50, 0, time.UTC),
		},
		{
			// This alignment is wanted to avoid any possible breaking change with
			// old behavior.
			name:       "base-align2",
			now:        time.Date(2024, 4, 30, 11, 51, 49, 123456, time.UTC),
			interval:   10 * time.Second,
			jitterSeed: 0,
			want:       time.Date(2024, 4, 30, 11, 51, 50, 0, time.UTC),
		},
		{
			// This alignment is wanted to avoid any possible breaking change with
			// old behavior.
			name:       "base-align3",
			now:        time.Date(2024, 4, 30, 11, 51, 53, 123456, time.UTC),
			interval:   10 * time.Second,
			jitterSeed: 0,
			want:       time.Date(2024, 4, 30, 11, 52, 0, 0, time.UTC),
		},
		{
			// This alignment is wanted to avoid any possible breaking change with
			// old behavior.
			name:       "base-align4",
			now:        time.Date(2024, 4, 30, 11, 51, 49, 123456, time.UTC),
			interval:   60 * time.Second,
			jitterSeed: 0,
			want:       time.Date(2024, 4, 30, 11, 52, 0, 0, time.UTC),
		},
		{
			// blackbox assume that jitterSeed could be translated nano seconds within the interval
			// e.g. that when "hash%60e9 <= 45e9" and interval = 60s then the wanted time will before XX:45
			name:       "blackbox-assumption",
			now:        time.Date(2024, 4, 30, 11, 51, 49, 123456, time.UTC),
			interval:   60 * time.Second,
			jitterSeed: 60e9*123 + 12e9,
			want:       time.Date(2024, 4, 30, 11, 52, 12, 0, time.UTC),
		},
		{
			name:       "skip-current-time",
			now:        time.Date(2024, 4, 30, 11, 51, 50, 0, time.UTC),
			interval:   10 * time.Second,
			jitterSeed: 0,
			want:       time.Date(2024, 4, 30, 11, 52, 0, 0, time.UTC),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := scrapeLoopOffset(tt.now, tt.interval, tt.jitterSeed); !got.Equal(tt.want) {
				t.Errorf("scrapeLoopOffset() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_JitterForTime(t *testing.T) {
	tests := []struct {
		name     string
		interval time.Duration
		align    time.Time
		now      time.Time
		wantNext time.Time
	}{
		{
			name:     "simple",
			interval: 10 * time.Second,
			align:    time.Date(2024, 4, 30, 11, 51, 40, 0, time.UTC),
			now:      time.Date(2024, 4, 30, 11, 51, 49, 0, time.UTC),
			wantNext: time.Date(2024, 4, 30, 11, 51, 50, 0, time.UTC),
		},
		{
			name:     "simple2",
			interval: 10 * time.Second,
			align:    time.Date(2024, 4, 30, 11, 51, 40, 0, time.UTC),
			now:      time.Date(2024, 4, 30, 11, 51, 49, 753159, time.UTC),
			wantNext: time.Date(2024, 4, 30, 11, 51, 50, 0, time.UTC),
		},
		{
			name:     "second-offset",
			interval: 10 * time.Second,
			align:    time.Date(2024, 4, 30, 11, 51, 41, 0, time.UTC),
			now:      time.Date(2024, 4, 30, 11, 51, 49, 0, time.UTC),
			wantNext: time.Date(2024, 4, 30, 11, 51, 51, 0, time.UTC),
		},
		{
			name:     "second-offset2",
			interval: 10 * time.Second,
			align:    time.Date(2024, 4, 30, 11, 51, 41, 0, time.UTC),
			now:      time.Date(2024, 4, 30, 11, 51, 49, 7523, time.UTC),
			wantNext: time.Date(2024, 4, 30, 11, 51, 51, 0, time.UTC),
		},
		{
			name:     "millisecond-offset",
			interval: 10 * time.Second,
			align:    time.Date(2024, 4, 30, 11, 51, 41, 1234, time.UTC),
			now:      time.Date(2024, 4, 30, 11, 51, 49, 0, time.UTC),
			wantNext: time.Date(2024, 4, 30, 11, 51, 51, 1234, time.UTC),
		},
		{
			name:     "millisecond-offset2",
			interval: 10 * time.Second,
			align:    time.Date(2024, 4, 30, 11, 51, 41, 1234, time.UTC),
			now:      time.Date(2024, 4, 30, 11, 51, 49, 7523, time.UTC),
			wantNext: time.Date(2024, 4, 30, 11, 51, 51, 1234, time.UTC),
		},
		{
			name:     "minute-step",
			interval: time.Minute,
			align:    time.Date(2024, 4, 30, 11, 51, 40, 0, time.UTC),
			now:      time.Date(2024, 4, 30, 11, 51, 49, 0, time.UTC),
			wantNext: time.Date(2024, 4, 30, 11, 52, 40, 0, time.UTC),
		},
		{
			name:     "minute-step2",
			interval: time.Minute,
			align:    time.Date(2024, 4, 30, 11, 51, 40, 0, time.UTC),
			now:      time.Date(2024, 4, 30, 11, 51, 49, 753, time.UTC),
			wantNext: time.Date(2024, 4, 30, 11, 52, 40, 0, time.UTC),
		},
		{
			name:     "5minute-step",
			interval: 5 * time.Minute,
			align:    time.Date(2024, 4, 30, 11, 51, 40, 0, time.UTC),
			now:      time.Date(2024, 4, 30, 11, 51, 49, 753, time.UTC),
			wantNext: time.Date(2024, 4, 30, 11, 56, 40, 0, time.UTC),
		},
		{
			name:     "past-10seconds",
			interval: 10 * time.Second,
			align:    time.Date(2023, 4, 30, 11, 51, 40, 0, time.UTC),
			now:      time.Date(2024, 4, 30, 11, 51, 49, 0, time.UTC),
			wantNext: time.Date(2024, 4, 30, 11, 51, 50, 0, time.UTC),
		},
		{
			name:     "past-1minute",
			interval: time.Minute,
			align:    time.Date(2023, 4, 30, 11, 51, 40, 0, time.UTC),
			now:      time.Date(2024, 4, 30, 11, 51, 49, 753, time.UTC),
			wantNext: time.Date(2024, 4, 30, 11, 52, 40, 0, time.UTC),
		},
		{
			name:     "past-5minutes",
			interval: 5 * time.Minute,
			align:    time.Date(2023, 4, 30, 11, 51, 40, 0, time.UTC),
			now:      time.Date(2024, 4, 30, 11, 51, 49, 753, time.UTC),
			wantNext: time.Date(2024, 4, 30, 11, 56, 40, 0, time.UTC),
		},
		{
			name:     "past-60minutes",
			interval: 60 * time.Minute,
			align:    time.Date(2023, 4, 30, 11, 51, 40, 0, time.UTC),
			now:      time.Date(2024, 4, 30, 11, 51, 49, 753, time.UTC),
			wantNext: time.Date(2024, 4, 30, 12, 51, 40, 0, time.UTC),
		},
		{
			name:     "past-7minutes",
			interval: 7 * time.Minute,
			align:    time.Date(2023, 4, 30, 11, 51, 40, 0, time.UTC),
			now:      time.Date(2024, 4, 30, 11, 51, 49, 753, time.UTC),
			// This one is a bit tricky because one day and one year are not divided by 7 minutes,
			// means align + N * interval do not result in the same "11h51" every day.
			// N = 75292 result in the wantNext date
			wantNext: time.Date(2024, 4, 30, 11, 55, 40, 0, time.UTC),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSeed := JitterForTime(tt.align, tt.interval)
			next := scrapeLoopOffset(tt.now, tt.interval, gotSeed)

			if !next.Equal(tt.wantNext) {
				t.Errorf("scrapeLoopOffset() = %v, want %v", next, tt.wantNext)
			}
		})
	}
}
