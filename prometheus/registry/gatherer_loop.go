package registry

import (
	"context"
	"time"
)

// scrapeLoop allow to run metric scraping at regular interval.
// The scraping itself is done by a callback.
// The metric points timestamp will be rounded such as the delta between two
// points (try) to be exactly the interval.
type scrapeLoop struct {
	cancel   context.CancelFunc
	stopped  chan struct{}
	callback func(context.Context, time.Time)
	interval time.Duration
}

func startScrapeLoop(
	ctx context.Context,
	interval, timeout time.Duration,
	jitterSeed uint64,
	callback func(ctx context.Context, t0 time.Time),
) *scrapeLoop {
	ctx, cancel := context.WithCancel(ctx)

	sl := &scrapeLoop{
		cancel:   cancel,
		callback: callback,
		interval: interval,
		stopped:  make(chan struct{}),
	}

	go sl.run(ctx, interval, timeout, jitterSeed)

	return sl
}

func (sl *scrapeLoop) run(ctx context.Context, interval, timeout time.Duration, jitterSeed uint64) {
	defer close(sl.stopped)

	alignedScrapeTime := sl.offset(interval, jitterSeed).Round(0)

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

		sl.callback(subCtx, scrapeTime)

		cancel()

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (sl *scrapeLoop) stop() {
	sl.cancel()
	<-sl.stopped
}

func (sl *scrapeLoop) offset(interval time.Duration, jitterSeed uint64) time.Time {
	now := time.Now().UnixNano()

	var (
		base   = int64(interval) - now%int64(interval)
		offset = jitterSeed % uint64(interval)
		next   = base + int64(offset)
	)

	if next > int64(interval) {
		next -= int64(interval)
	}

	return time.Unix(0, now+next)
}
