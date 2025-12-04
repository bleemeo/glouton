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
	"time"

	"github.com/bleemeo/glouton/crashreport"
)

// scrapeLoop allow to run metric scraping at regular interval.
// The scraping itself is done by a callback.
// The metric points timestamp will be rounded such as the delta between two
// points (try) to be exactly the interval.
type scrapeLoop struct {
	cancel   context.CancelFunc
	stopped  chan struct{}
	trigger  chan any
	callback func(ctx context.Context, loopCtx context.Context, t0 time.Time)
	interval time.Duration
	// description is useful for debugging.
	description string
}

func startScrapeLoop(
	interval, timeout time.Duration,
	jitterSeed uint64,
	callback func(ctx context.Context, loopCtx context.Context, t0 time.Time),
	description string,
	triggerImmediate bool,
) *scrapeLoop {
	ctx, cancel := context.WithCancel(context.Background())

	sl := &scrapeLoop{
		cancel:      cancel,
		callback:    callback,
		interval:    interval,
		stopped:     make(chan struct{}),
		trigger:     make(chan any, 1),
		description: description,
	}

	go func() {
		defer crashreport.ProcessPanic()

		sl.run(ctx, interval, timeout, jitterSeed, triggerImmediate)
	}()

	return sl
}

func (sl *scrapeLoop) runOnce(ctx context.Context, interval time.Duration, timeout time.Duration, alignedScrapeTime time.Time) time.Time {
	select {
	case <-sl.trigger:
	default:
	}

	scrapeTime := time.Now().Round(0)

	// For some reason, a tick might have been skipped, in which case we
	// would call alignedScrapeTime.Add(interval) multiple times.
	for scrapeTime.Sub(alignedScrapeTime) >= interval {
		alignedScrapeTime = alignedScrapeTime.Add(interval)
	}

	// Align the scrape time if we are in the tolerance boundaries.
	// The tolerance is 25% of the interval
	if scrapeTime.Sub(alignedScrapeTime) <= interval/4 {
		scrapeTime = alignedScrapeTime
	}

	subCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	sl.callback(subCtx, ctx, scrapeTime)

	return alignedScrapeTime
}

func (sl *scrapeLoop) run(ctx context.Context, interval, timeout time.Duration, jitterSeed uint64, triggerImmediate bool) {
	defer close(sl.stopped)

	alignedScrapeTime := scrapeLoopOffset(time.Now(), interval, jitterSeed).Round(0)

	if triggerImmediate {
		alignedScrapeTime = sl.runOnce(ctx, interval, timeout, alignedScrapeTime)
	}

	select {
	case <-time.After(time.Until(alignedScrapeTime)):
		// Continue after a scraping offset.
	case <-ctx.Done():
		return
	}

	// Calling Round ensures the time used is the wall clock, as otherwise .Sub
	// and .Add on time.Time behave differently (see time package docs).
	ticker := time.NewTicker(interval)

	defer ticker.Stop()

	for ctx.Err() == nil {
		alignedScrapeTime = sl.runOnce(ctx, interval, timeout, alignedScrapeTime)

		select {
		case <-ctx.Done():
			return
		case <-sl.trigger:
		case <-ticker.C:
		}
	}
}

func (sl *scrapeLoop) Trigger() {
	select {
	case sl.trigger <- nil:
	default:
	}
}

func (sl *scrapeLoop) stopNoWait() {
	sl.cancel()
}

func (sl *scrapeLoop) stop() {
	sl.cancel()
	<-sl.stopped
}

func scrapeLoopOffset(now time.Time, interval time.Duration, jitterSeed uint64) time.Time {
	nowNano := now.UnixNano()

	var (
		base   = int64(interval) - nowNano%int64(interval)
		offset = jitterSeed % uint64(interval)
		next   = base + int64(offset)
	)

	if next > int64(interval) {
		next -= int64(interval)
	}

	return time.Unix(0, nowNano+next)
}

// JitterForTime return a Jitter such as scrape run at align + N * interval.
func JitterForTime(align time.Time, interval time.Duration) uint64 {
	return uint64(align.UnixNano()) % uint64(interval)
}
