package registry

import (
	"context"
	"fmt"
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
		interval      time.Duration
		cancelContext bool
	}{
		{
			// 201 is the minimal interval for Prometheus to align timestamp.
			// Value below this limit aren't aligned.
			interval:      201 * time.Millisecond,
			cancelContext: false,
		},
		{interval: 500 * time.Millisecond, cancelContext: true},
		{interval: 1 * time.Second, cancelContext: false},
	}
	for _, tt := range tests {
		tt := tt

		var name string

		if tt.cancelContext {
			name = fmt.Sprintf("%s-with-cancel", tt.interval.String())
		} else {
			name = fmt.Sprintf("%s-without-cancel", tt.interval.String())
		}

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var (
				result      []time.Time
				notAfter    time.Time
				lengthAtEnd int
				l           sync.Mutex
			)

			ctx := context.Background()
			deadlineCtx, cancel := context.WithTimeout(context.Background(), testDuration)
			defer cancel()

			if tt.cancelContext {
				ctx = deadlineCtx
			}

			callback := func(_ context.Context, t0 time.Time) {
				l.Lock()
				result = append(result, t0)
				l.Unlock()
			}

			loop := startScrapeLoop(ctx, tt.interval, tt.interval, 0, 0, callback)

			<-deadlineCtx.Done()

			if tt.cancelContext {
				// Give few time for scrapeLoop to shutdown
				time.Sleep(200 * time.Millisecond)
				notAfter = time.Now()
				l.Lock()
				lengthAtEnd = len(result)
				l.Unlock()
			}

			loop.stop()

			if !tt.cancelContext {
				notAfter = time.Now()
				l.Lock()
				lengthAtEnd = len(result)
				l.Unlock()
			}

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
