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

			deadlineCtx, cancel := context.WithTimeout(context.Background(), testDuration)
			defer cancel()

			callback := func(_ context.Context, t0 time.Time) {
				l.Lock()
				result = append(result, t0)
				l.Unlock()
			}

			loop := startScrapeLoop(tt.interval, tt.interval, 0, callback, "")

			<-deadlineCtx.Done()

			loop.stop()

			notAfter = time.Now()

			l.Lock()
			lengthAtEnd = len(result)
			l.Unlock()

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
