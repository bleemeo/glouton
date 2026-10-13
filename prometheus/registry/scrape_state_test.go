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

package registry

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bleemeo/glouton/types"
)

var (
	errScrapeFailed = errors.New("mod_status is not enabled")
	errOld          = errors.New("an error from long ago")
)

// TestLastScrapeStateNeverScraped checks a registration that never ran reports ScrapeNotRun, so
// that discovery can report it as pending rather than as working with no metric.
func TestLastScrapeStateNeverScraped(t *testing.T) {
	reg := &registration{}

	got := reg.LastScrapeState()

	if got != (types.ScrapeState{}) {
		t.Errorf("LastScrapeState() = %+v, want the zero value", got)
	}
}

// TestLastScrapeStateUsesRecentRuns checks the last scrapeStateRuns runs are reported, with
// their most recent error, while older runs are not.
func TestLastScrapeStateUsesRecentRuns(t *testing.T) {
	reg := &registration{
		lastScrapes: []scrapeRun{
			{ScrapeAt: time.Now(), Error: errOld},
			{ScrapeAt: time.Now(), ScrapedPointsCount: 12},
			{ScrapeAt: time.Now(), Error: errScrapeFailed},
			{ScrapeAt: time.Now(), ScrapedPointsCount: 3},
		},
	}

	got := reg.LastScrapeState()

	want := types.ScrapeState{Status: types.ScrapeRan, Error: errScrapeFailed.Error()}

	if got != want {
		t.Errorf("LastScrapeState() = %+v, want %+v", got, want)
	}
}

// TestLastScrapeStateIntermittentError checks an input failing one scrape in two reports
// the same state whichever scrape came last.
func TestLastScrapeStateIntermittentError(t *testing.T) {
	ok := scrapeRun{ScrapeAt: time.Now(), ScrapedPointsCount: 5}
	failed := scrapeRun{ScrapeAt: time.Now(), Error: errScrapeFailed}

	afterFailure := (&registration{lastScrapes: []scrapeRun{ok, failed, ok, failed}}).LastScrapeState()
	afterSuccess := (&registration{lastScrapes: []scrapeRun{failed, ok, failed, ok}}).LastScrapeState()

	if afterFailure != afterSuccess {
		t.Errorf("the state alternates: %+v then %+v", afterFailure, afterSuccess)
	}
}

// TestLastScrapeStateSuccess checks runs without error report no error, once an older error
// is out of the recent runs.
func TestLastScrapeStateSuccess(t *testing.T) {
	reg := &registration{
		lastScrapes: []scrapeRun{
			{ScrapeAt: time.Now(), ScrapedPointsCount: 0, Error: errScrapeFailed},
			{ScrapeAt: time.Now(), ScrapedPointsCount: 5},
			{ScrapeAt: time.Now(), ScrapedPointsCount: 5},
			{ScrapeAt: time.Now(), ScrapedPointsCount: 5},
		},
	}

	got := reg.LastScrapeState()

	want := types.ScrapeState{Status: types.ScrapeRan}

	if got != want {
		t.Errorf("LastScrapeState() = %+v, want %+v", got, want)
	}
}

// newStuckTestRegistration registers a push-points callback running gather, with a short
// interval so that a run counts as stuck quickly. The registry is not started: the test runs
// the scrapes itself.
func newStuckTestRegistration(t *testing.T, interval time.Duration, gather pushGatherFunction) *registration {
	t.Helper()

	r, err := New(Option{})
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	reg, err := r.registerPushPointsCallback(RegistrationOption{HonorTimestamp: true}, gather)
	if err != nil {
		t.Fatalf("registerPushPointsCallback() failed: %v", err)
	}

	reg.l.Lock()
	reg.interval = interval
	reg.l.Unlock()

	return reg
}

// TestLastScrapeStateStuck checks a run that does not return within scrapeStuckIntervals
// intervals is reported as stuck while it hangs, rather than with the last run that did complete, and that the state
// recovers only once the stuck run is out of the recent runs.
func TestLastScrapeStateStuck(t *testing.T) {
	const interval = 50 * time.Millisecond

	var (
		hang    atomic.Bool
		release = make(chan struct{})
	)

	// The gather ignores its context, as Telegraf inputs do.
	reg := newStuckTestRegistration(t, interval, func(context.Context, time.Time) error {
		if hang.Load() {
			<-release
		}

		return nil
	})

	reg.InternalRunScrape(t.Context(), t.Context(), time.Now())

	hang.Store(true)

	done := make(chan struct{})

	go func() {
		defer close(done)

		ctx, cancel := context.WithTimeout(t.Context(), interval*8/10)
		defer cancel()

		reg.InternalRunScrape(ctx, t.Context(), time.Now())
	}()

	stuck := types.ScrapeState{Status: types.ScrapeRan, Error: errScrapeStuck.Error()}

	for deadline := time.Now().Add(5 * time.Second); reg.LastScrapeState() != stuck; {
		if time.Now().After(deadline) {
			t.Fatalf("a hung run is reported as %+v, want stuck", reg.LastScrapeState())
		}

		time.Sleep(interval / 10)
	}

	// The run returns: it is recorded after the stuck entry, which still counts.
	hang.Store(false)
	close(release)
	<-done

	if got := reg.LastScrapeState(); got != stuck {
		t.Errorf("once the hung run returned, reported as %+v, want stuck", got)
	}

	// The next runs are quick, but a slow machine could make one last the short interval above.
	reg.l.Lock()
	reg.interval = time.Minute
	reg.l.Unlock()

	for range scrapeStateRuns - 1 {
		reg.InternalRunScrape(t.Context(), t.Context(), time.Now())
	}

	if got := reg.LastScrapeState(); got != (types.ScrapeState{Status: types.ScrapeRan}) {
		t.Errorf("after %d successful runs, reported as %+v, want no error", scrapeStateRuns, got)
	}
}

// TestLastScrapeStateSlowRunsDoNotFlap checks a run lasting more than scrapeStuckIntervals
// intervals each time, though it does return, is reported as stuck whether the last record is its stuck entry or
// its result: one would otherwise read as working most of the time and failing in between.
func TestLastScrapeStateSlowRunsDoNotFlap(t *testing.T) {
	const interval = 20 * time.Millisecond

	reg := newStuckTestRegistration(t, interval, func(context.Context, time.Time) error {
		time.Sleep(interval * scrapeStuckIntervals * 2)

		return nil
	})

	stuck := types.ScrapeState{Status: types.ScrapeRan, Error: errScrapeStuck.Error()}

	for i := range 4 {
		reg.InternalRunScrape(t.Context(), t.Context(), time.Now())

		if got := reg.LastScrapeState(); got != stuck {
			t.Errorf("after slow run %d, reported as %+v, want stuck", i+1, got)
		}
	}
}

// TestLastScrapeStateOccasionallySlowNotStuck checks a run slower than its interval, but not by
// scrapeStuckIntervals intervals, is recorded with its own result only.
func TestLastScrapeStateOccasionallySlowNotStuck(t *testing.T) {
	const interval = 50 * time.Millisecond

	reg := newStuckTestRegistration(t, interval, func(context.Context, time.Time) error {
		time.Sleep(interval * 3 / 2)

		return nil
	})

	reg.InternalRunScrape(t.Context(), t.Context(), time.Now())

	if got := reg.LastScrapeState(); got != (types.ScrapeState{Status: types.ScrapeRan}) {
		t.Errorf("a run 1.5 intervals long is reported as %+v, want no error", got)
	}
}

// TestScrapeStuckUsesLoopInterval checks the stuck threshold follows the interval of the loop
// running the scrape, not reg.interval, which changes before the loop is restarted.
func TestScrapeStuckUsesLoopInterval(t *testing.T) {
	reg := newStuckTestRegistration(t, 10*time.Millisecond, func(context.Context, time.Time) error {
		time.Sleep(100 * time.Millisecond)

		return nil
	})

	reg.registry.scrapeFromLoop(t.Context(), t.Context(), time.Now(), time.Minute, reg)

	if got := reg.LastScrapeState(); got != (types.ScrapeState{Status: types.ScrapeRan}) {
		t.Errorf("a 100 ms run of a one-minute loop is reported as %+v, want no error", got)
	}
}

// TestScrapeHonoringContextNotStuck checks a gather that returns on its context's deadline is
// recorded once, with its own error: the timeout is shorter than the interval.
func TestScrapeHonoringContextNotStuck(t *testing.T) {
	// Wide margins between the timeout and the stuck threshold, for a slow machine.
	const interval = 200 * time.Millisecond

	reg := newStuckTestRegistration(t, interval, func(ctx context.Context, _ time.Time) error {
		<-ctx.Done()

		return ctx.Err()
	})

	ctx, cancel := context.WithTimeout(t.Context(), interval*8/10)
	defer cancel()

	reg.InternalRunScrape(ctx, t.Context(), time.Now())

	reg.l.Lock()
	defer reg.l.Unlock()

	if len(reg.lastScrapes) != 1 || !errors.Is(reg.lastScrapes[0].Error, context.DeadlineExceeded) {
		t.Errorf("lastScrapes = %+v, want the gather's own error only", reg.lastScrapes)
	}
}

// TestScrapeAbandonedOnStopNotStuck checks a run abandoned because its loop stopped, as on a
// restart, records nothing: the restarted loop's runs report the input.
func TestScrapeAbandonedOnStopNotStuck(t *testing.T) {
	release := make(chan struct{})
	defer close(release)

	reg := newStuckTestRegistration(t, time.Minute, func(context.Context, time.Time) error {
		<-release

		return nil
	})
	reg.registry.option.ShutdownDeadline = 10 * time.Millisecond

	loopCtx, cancel := context.WithCancel(t.Context())
	cancel()

	reg.InternalRunScrape(loopCtx, loopCtx, time.Now())

	if got := reg.LastScrapeState(); got != (types.ScrapeState{}) {
		t.Errorf("a run abandoned on stop is reported as %+v, want nothing recorded", got)
	}
}

// TestRegistrationImplementsScrapeStateProvider pins the optional interface discovery
// asserts on. Discovery falls back to an "unknown" status when the assertion fails, so
// breaking this would silently degrade the reported state rather than fail to build.
func TestRegistrationImplementsScrapeStateProvider(t *testing.T) {
	var provider interface {
		LastScrapeState() types.ScrapeState
	} = &registration{}

	if provider.LastScrapeState().Status != types.ScrapeNotRun {
		t.Error("a fresh registration must not report a scrape")
	}
}

// TestLastScrapeStateIgnoresRelabelHookSkip is a regression test.
//
// A scrape skipped by the relabel hook is recorded in lastScrapes with a sentinel error,
// but it is not a scrape and not a failure: it happens routinely while a service or
// container waits to be registered on Bleemeo. Reporting it as an error would tell a user
// their healthy service is broken, quoting an error about Glouton internals; it is
// reported as Skipped instead, for the caller to explain.
func TestLastScrapeStateIgnoresRelabelHookSkip(t *testing.T) {
	cases := []struct {
		name    string
		scrapes []scrapeRun
		want    types.ScrapeState
	}{
		{
			name: "only skips reads as skipped",
			scrapes: []scrapeRun{
				{Error: errSkippingScrapeDueToRelabelHook},
				{Error: errSkippingScrapeDueToRelabelHook},
			},
			want: types.ScrapeState{Status: types.ScrapeSkipped},
		},
		{
			// Only the most recent run counts. Reporting the older real scrape instead
			// would present a stale result as current and would change on its own once
			// the ring buffer dropped it.
			name: "currently skipped reads as skipped, whatever came before",
			scrapes: []scrapeRun{
				{ScrapedPointsCount: 4},
				{Error: errSkippingScrapeDueToRelabelHook},
			},
			want: types.ScrapeState{Status: types.ScrapeSkipped},
		},
		{
			name: "a real scrape after a skip is reported",
			scrapes: []scrapeRun{
				{Error: errSkippingScrapeDueToRelabelHook},
				{Error: errScrapeFailed},
			},
			want: types.ScrapeState{Status: types.ScrapeRan, Error: errScrapeFailed.Error()},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := &registration{lastScrapes: tc.scrapes}

			if got := reg.LastScrapeState(); got != tc.want {
				t.Errorf("LastScrapeState() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestLastScrapeStateNeverReportsInternalSentinel guards the property rather than the
// mechanism: whatever ends up in lastScrapes, the sentinel must not reach a user.
func TestLastScrapeStateNeverReportsInternalSentinel(t *testing.T) {
	reg := &registration{
		lastScrapes: []scrapeRun{{Error: errSkippingScrapeDueToRelabelHook}},
	}

	if got := reg.LastScrapeState(); got.Error != "" {
		t.Errorf("LastScrapeState() reported %q to the user", got.Error)
	}
}

// TestLastScrapeStateDoesNotChangeOnRingRollover pins the reason only the most recent run
// is considered: a service that is permanently relabel-skipped must report the same thing
// however many runs have accumulated, rather than flipping when the ring buffer drops the
// last real scrape.
func TestLastScrapeStateDoesNotChangeOnRingRollover(t *testing.T) {
	reg := &registration{
		lastScrapes: []scrapeRun{{Error: errScrapeFailed}},
	}

	// The real scrape is still in the ring here.
	for range maxLastScrape * 2 {
		reg.lastScrapes = append(reg.lastScrapes, scrapeRun{Error: errSkippingScrapeDueToRelabelHook})

		if len(reg.lastScrapes) > maxLastScrape {
			reg.lastScrapes = reg.lastScrapes[len(reg.lastScrapes)-maxLastScrape:]
		}

		if got := reg.LastScrapeState(); got != (types.ScrapeState{Status: types.ScrapeSkipped}) {
			t.Fatalf("after %d skips: LastScrapeState() = %+v, want skipped", len(reg.lastScrapes), got)
		}
	}
}
