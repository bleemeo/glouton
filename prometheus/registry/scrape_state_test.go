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
	"errors"
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

// TestLastScrapeStateStuck checks a run that never returns is reported as stuck, rather than
// with the last run that did complete, once it lasted scrapeStuckIntervals intervals.
func TestLastScrapeStateStuck(t *testing.T) {
	t0 := time.Now()
	reg := &registration{
		interval:    10 * time.Second,
		lastScrapes: []scrapeRun{{ScrapeAt: t0}},
	}

	runID := reg.startRun(t0.Add(10 * time.Second))

	if got := reg.lastScrapeState(t0.Add(30 * time.Second)); got.Error != "" {
		t.Errorf("a run started two intervals ago is reported as %+v, want the last run", got)
	}

	got := reg.lastScrapeState(t0.Add(time.Minute))
	if got != (types.ScrapeState{Status: types.ScrapeRan, Error: errScrapeStuck.Error()}) {
		t.Errorf("a run started five intervals ago is reported as %+v, want stuck", got)
	}

	reg.endRun(runID)

	if got := reg.lastScrapeState(t0.Add(time.Minute)); got.Error != "" {
		t.Errorf("once the run returned, reported as %+v, want the last run", got)
	}
}

// TestLastScrapeStateStuckAcrossRestart checks a hung run stays reported as stuck when its loop
// is restarted and the new loop starts a run of its own: the old run is still not returned.
func TestLastScrapeStateStuckAcrossRestart(t *testing.T) {
	t0 := time.Now()
	reg := &registration{interval: 10 * time.Second}

	reg.startRun(t0)
	// The loop is restarted at t0+60s, and its first run starts.
	reg.startRun(t0.Add(60 * time.Second))

	if got := reg.lastScrapeState(t0.Add(65 * time.Second)); got.Error != errScrapeStuck.Error() {
		t.Errorf("a run hung since six intervals is reported as %+v after a restart, want stuck", got)
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
