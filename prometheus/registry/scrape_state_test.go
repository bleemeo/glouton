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
	"fmt"
	"net"
	"net/url"
	"syscall"
	"testing"
	"time"

	"github.com/bleemeo/glouton/types"
)

var (
	errScrapeFailed = errors.New("mod_status is not enabled")
	errOld          = errors.New("an error from long ago")
	errAnother      = errors.New("another error")
	errTLSAlert     = errors.New("tls: bad certificate")
	errNoAuth       = errors.New("NOAUTH authentication required")
	errTextOnly     = errors.New("dial tcp 127.0.0.1:9200: connect: connection refused")
	// As Elasticsearch's input leaves them, the type lost; capitalized as Go's HTTP client words them.
	errDNSText        = errors.New(`Get "http://es:9200/": dial tcp: lookup es on 127.0.0.53:53: no such host`) //nolint:staticcheck
	errTimeoutText    = errors.New(`Get "http://127.0.0.1:9200/": context deadline exceeded`)                   //nolint:staticcheck
	errTLSAlertText   = errors.New(`Get "https://127.0.0.1:9200/": remote error: tls: bad certificate`)         //nolint:staticcheck
	errHTTPStatusText = errors.New("elasticsearch: API responded with status-code 401, expected 200")
	errTLSTimeoutText = errors.New(`Get "https://127.0.0.1:9200/": net/http: TLS handshake timeout`) //nolint:staticcheck
	errJoinedText     = errors.New("status 401\ndial tcp 127.0.0.1:9200: connect: connection refused")
	// A service's own message about a backend of its own, quoting Go's wording.
	errServiceQuotesDial    = errors.New("ERR backend: dial tcp 10.0.0.5:5432: connect: connection refused")
	errServiceQuotesTimeout = errors.New("query failed: context deadline exceeded")
)

// otherTimeoutError is a timeout of no other kind, as net/http's TLS handshake timeout, worded so
// that only its type tells.
type otherTimeoutError struct{}

func (otherTimeoutError) Error() string { return "the handshake timed out" }
func (otherTimeoutError) Timeout() bool { return true }

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

	want := types.ScrapeState{Status: types.ScrapeRan, Error: errScrapeFailed.Error(), ErrorKind: types.ScrapeErrorGatherer}

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

// stuckTestRegistration is a registration whose loop runs every minute, with a run in progress
// since runningSince (none when zero) and these runs recorded.
func stuckTestRegistration(runningSince time.Time, runs ...scrapeRun) *registration {
	return &registration{
		loop:         &scrapeLoop{interval: time.Minute},
		runningSince: runningSince,
		lastScrapes:  runs,
	}
}

// TestLastScrapeStateStuck checks an input is reported stuck once its run in progress has lasted
// scrapeStuckIntervals intervals, as when a run hangs, whatever the runs before it reported.
func TestLastScrapeStateStuck(t *testing.T) {
	now := time.Now()
	stuck := types.ScrapeState{Status: types.ScrapeRan, Error: errScrapeStuck.Error(), ErrorKind: types.ScrapeErrorUnreachable}
	quick := scrapeRun{ScrapeAt: now.Add(-5 * time.Minute), ScrapeDuration: time.Second}

	cases := []struct {
		name string
		reg  *registration
		want types.ScrapeState
	}{
		{
			name: "running for 4 intervals",
			reg:  stuckTestRegistration(now.Add(-4*time.Minute), quick),
			want: stuck,
		},
		{
			name: "running for 2 intervals",
			reg:  stuckTestRegistration(now.Add(-2*time.Minute), quick),
			want: types.ScrapeState{Status: types.ScrapeRan},
		},
		{
			name: "hung after an error",
			reg:  stuckTestRegistration(now.Add(-4*time.Minute), scrapeRun{ScrapeAt: now.Add(-5 * time.Minute), Error: errScrapeFailed}),
			want: stuck,
		},
		{
			name: "hung after a relabel hook skip",
			reg:  stuckTestRegistration(now.Add(-4*time.Minute), scrapeRun{ScrapeAt: now.Add(-5 * time.Minute), Error: errSkippingScrapeDueToRelabelHook}),
			want: stuck,
		},
		{
			name: "first run hung",
			reg:  stuckTestRegistration(now.Add(-4 * time.Minute)),
			want: stuck,
		},
		{
			name: "first run in progress",
			reg:  stuckTestRegistration(now.Add(-time.Minute)),
			want: types.ScrapeState{},
		},
		{
			// Between two runs, however long ago the last one ended: nothing is running late.
			name: "no run in progress",
			reg:  stuckTestRegistration(time.Time{}, quick),
			want: types.ScrapeState{Status: types.ScrapeRan},
		},
		{
			// The registry stopped: nothing is late.
			name: "no running loop",
			reg:  &registration{runningSince: now.Add(-time.Hour), lastScrapes: []scrapeRun{quick}},
			want: types.ScrapeState{Status: types.ScrapeRan},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.reg.LastScrapeState(); got != tc.want {
				t.Errorf("LastScrapeState() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestLastScrapeStateSlowRunsDoNotFlap checks an input whose runs each last more than
// scrapeStuckIntervals intervals, though they return, is reported stuck between two runs as
// well as during one: it would otherwise read as working part of the time.
func TestLastScrapeStateSlowRunsDoNotFlap(t *testing.T) {
	now := time.Now()
	slow := func(endedAgo time.Duration) scrapeRun {
		return scrapeRun{ScrapeAt: now.Add(-endedAgo - 4*time.Minute), ScrapeDuration: 4 * time.Minute}
	}
	quick := func(endedAgo time.Duration) scrapeRun {
		return scrapeRun{ScrapeAt: now.Add(-endedAgo - time.Second), ScrapeDuration: time.Second}
	}

	for _, runs := range [][]scrapeRun{
		{slow(8 * time.Minute), slow(4 * time.Minute), slow(0)},
		{slow(time.Minute), quick(0)},
		{slow(2 * time.Minute), quick(time.Minute), quick(0)},
	} {
		reg := stuckTestRegistration(time.Time{}, runs...)

		if got := reg.LastScrapeState(); got.Error != errScrapeStuck.Error() {
			t.Errorf("with a slow run among the last %d, reported as %+v, want stuck", scrapeStateRuns, got)
		}
	}

	// Once the slow run is out of the recent runs, the input is not stuck.
	reg := stuckTestRegistration(time.Time{}, slow(3*time.Minute), quick(2*time.Minute), quick(time.Minute), quick(0))

	if got := reg.LastScrapeState(); got != (types.ScrapeState{Status: types.ScrapeRan}) {
		t.Errorf("after %d quick runs, reported as %+v, want no error", scrapeStateRuns, got)
	}
}

// TestLastScrapeStateOccasionallySlowNotStuck checks a run slower than its interval, but not by
// scrapeStuckIntervals intervals, puts the input in error neither while it runs nor after. What
// counts is how long the run lasts, not how long ago the previous one ended: here the previous
// run ended an interval before this one started, which would make it four.
func TestLastScrapeStateOccasionallySlowNotStuck(t *testing.T) {
	now := time.Now()
	quick := scrapeRun{ScrapeAt: now.Add(-3*time.Minute - 30*time.Second - time.Second), ScrapeDuration: time.Second}

	running := stuckTestRegistration(now.Add(-2*time.Minute-30*time.Second), quick)
	if got := running.LastScrapeState(); got != (types.ScrapeState{Status: types.ScrapeRan}) {
		t.Errorf("while a run lasts 2.5 intervals, reported as %+v, want no error", got)
	}

	ended := stuckTestRegistration(time.Time{}, quick, scrapeRun{ScrapeAt: now.Add(-2*time.Minute - 30*time.Second), ScrapeDuration: 2*time.Minute + 30*time.Second})
	if got := ended.LastScrapeState(); got != (types.ScrapeState{Status: types.ScrapeRan}) {
		t.Errorf("after a run 2.5 intervals long, reported as %+v, want no error", got)
	}
}

// TestLastScrapeStateUsesLoopInterval checks the limit follows the interval of the running loop,
// not reg.interval, which changes before the loop restarts with it.
func TestLastScrapeStateUsesLoopInterval(t *testing.T) {
	now := time.Now()
	reg := stuckTestRegistration(now.Add(-2*time.Minute), scrapeRun{ScrapeAt: now.Add(-5 * time.Minute), ScrapeDuration: 2 * time.Minute})
	reg.interval = 10 * time.Second

	if got := reg.LastScrapeState(); got != (types.ScrapeState{Status: types.ScrapeRan}) {
		t.Errorf("runs of 2 intervals of the running loop are reported as %+v, want no error", got)
	}
}

// TestScrapeRunningSince checks a run is followed while in progress, so that a hung one reads as
// stuck, and no longer once it returns.
func TestScrapeRunningSince(t *testing.T) {
	const interval = 10 * time.Millisecond

	release := make(chan struct{})

	// The gather ignores its context, as Telegraf inputs do.
	reg := newStuckTestRegistration(t, interval, func(context.Context, time.Time) error {
		<-release

		return nil
	})

	reg.l.Lock()
	reg.loop = &scrapeLoop{interval: interval}
	reg.l.Unlock()

	done := make(chan struct{})

	go func() {
		defer close(done)

		reg.InternalRunScrape(t.Context(), t.Context(), time.Now())
	}()

	for deadline := time.Now().Add(5 * time.Second); reg.LastScrapeState().Error != errScrapeStuck.Error(); {
		if time.Now().After(deadline) {
			t.Fatalf("a hung run is reported as %+v, want stuck", reg.LastScrapeState())
		}

		time.Sleep(interval)
	}

	close(release)
	<-done

	reg.l.Lock()
	running := reg.runningSince
	reg.l.Unlock()

	if !running.IsZero() {
		t.Errorf("runningSince = %v once the run returned, want zero", running)
	}
}

// TestLastScrapeStateRegisteredAt checks a state that ran tells when the input was registered,
// so that the metrics an earlier input of the service left in the store are not counted as its.
func TestLastScrapeStateRegisteredAt(t *testing.T) {
	before := time.Now()

	reg := newStuckTestRegistration(t, time.Minute, func(context.Context, time.Time) error {
		return nil
	})

	after := time.Now()

	if got := reg.LastScrapeState(); !got.RegisteredAt.IsZero() {
		t.Errorf("before any run, RegisteredAt = %v, want zero", got.RegisteredAt)
	}

	reg.InternalRunScrape(t.Context(), t.Context(), time.Now())

	got := reg.LastScrapeState().RegisteredAt
	if got.Before(before) || got.After(after) {
		t.Errorf("RegisteredAt = %v, want between %v and %v", got, before, after)
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

	reg.l.Lock()
	running := reg.runningSince
	reg.l.Unlock()

	if !running.IsZero() {
		t.Errorf("runningSince = %v after the run was abandoned, want zero", running)
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
			want: types.ScrapeState{Status: types.ScrapeRan, Error: errScrapeFailed.Error(), ErrorKind: types.ScrapeErrorGatherer},
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

// TestScrapeErrorKind checks where a scrape error comes from is read on its values, however
// inputs wrap or join them: a service that did not answer, one that answered (or an input that
// lost its error's type), and Glouton failing before reaching the service.
func TestScrapeErrorKind(t *testing.T) {
	refused := &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}

	cases := []struct {
		name string
		err  error
		want types.ScrapeErrorKind
	}{
		{name: "no error", err: nil, want: types.ScrapeErrorNone},
		{name: "connection refused", err: refused, want: types.ScrapeErrorUnreachable},
		{name: "wrapped", err: fmt.Errorf("error on request to %q: %w", "http://127.0.0.1/", refused), want: types.ScrapeErrorUnreachable},
		{name: "in an HTTP client error", err: &url.Error{Op: "Get", URL: "http://127.0.0.1/", Err: refused}, want: types.ScrapeErrorUnreachable},
		{name: "joined", err: errors.Join(errAnother, refused), want: types.ScrapeErrorUnreachable},
		{name: "connection reset", err: &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}, want: types.ScrapeErrorUnreachable},
		{name: "unknown host", err: &net.DNSError{Err: "no such host", Name: "db"}, want: types.ScrapeErrorUnreachable},
		{name: "context timeout", err: fmt.Errorf("query: %w", context.DeadlineExceeded), want: types.ScrapeErrorUnreachable},
		{name: "stuck run", err: errScrapeStuck, want: types.ScrapeErrorUnreachable},
		{name: "TLS alert from the service", err: &net.OpError{Op: "remote error", Net: "tcp", Err: errTLSAlert}, want: types.ScrapeErrorGatherer},
		{name: "refused by the service", err: errNoAuth, want: types.ScrapeErrorGatherer},
		{name: "type lost, Go's text", err: errTextOnly, want: types.ScrapeErrorUnreachable},
		{name: "type lost, Go's DNS text", err: errDNSText, want: types.ScrapeErrorUnreachable},
		{name: "type lost, Go's timeout text", err: errTimeoutText, want: types.ScrapeErrorUnreachable},
		{name: "TLS alert, text only", err: errTLSAlertText, want: types.ScrapeErrorGatherer},
		{name: "HTTP status, text only", err: errHTTPStatusText, want: types.ScrapeErrorGatherer},
		{name: "timeout of another kind", err: &url.Error{Op: "Get", URL: "https://127.0.0.1/", Err: otherTimeoutError{}}, want: types.ScrapeErrorUnreachable},
		{name: "TLS handshake timeout, text only", err: errTLSTimeoutText, want: types.ScrapeErrorUnreachable},
		{name: "joined, text only", err: errJoinedText, want: types.ScrapeErrorUnreachable},
		{name: "service quoting a dial error", err: errServiceQuotesDial, want: types.ScrapeErrorGatherer},
		{name: "service quoting a timeout", err: errServiceQuotesTimeout, want: types.ScrapeErrorGatherer},
		{name: "canceled", err: fmt.Errorf("query: %w", context.Canceled), want: types.ScrapeErrorInternal},
		{name: "dial canceled", err: &net.OpError{Op: "dial", Net: "tcp", Err: context.Canceled}, want: types.ScrapeErrorInternal},
		{name: "canceled, joined", err: errors.Join(context.Canceled, fmt.Errorf("query: %w", context.Canceled)), want: types.ScrapeErrorInternal},
		{name: "canceled beside an answer", err: errors.Join(errNoAuth, context.Canceled), want: types.ScrapeErrorGatherer},
		{name: "canceled beside a refused connection", err: fmt.Errorf("gather: %w", errors.Join(&net.OpError{Op: "dial", Net: "tcp", Err: context.Canceled}, refused)), want: types.ScrapeErrorUnreachable},
		{name: "no secrets gate", err: notGatheredError{ErrBadArgument}, want: types.ScrapeErrorInternal},
		{name: "secrets gate timeout", err: notGatheredError{context.DeadlineExceeded}, want: types.ScrapeErrorInternal},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := scrapeErrorKind(tc.err); got != tc.want {
				t.Errorf("scrapeErrorKind(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestLastScrapeStateErrorKind checks ErrorKind follows the error reported, the most recent of
// the last runs.
func TestLastScrapeStateErrorKind(t *testing.T) {
	refused := &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}

	reg := &registration{lastScrapes: []scrapeRun{{Error: refused}, {Error: errScrapeFailed}}}
	if got := reg.LastScrapeState(); got.ErrorKind != types.ScrapeErrorGatherer || got.Error != errScrapeFailed.Error() {
		t.Errorf("after an error from the service, got %+v, want it from the gatherer", got)
	}

	reg.lastScrapes = append(reg.lastScrapes, scrapeRun{Error: refused})
	if got := reg.LastScrapeState(); got.ErrorKind != types.ScrapeErrorUnreachable {
		t.Errorf("after a refused connection, got %+v, want unreachable", got)
	}
}

// TestLastScrapeStatePartlyUnreachable checks a run that gathered points, with a network error
// for part of it, is not reported as a service that did not answer.
func TestLastScrapeStatePartlyUnreachable(t *testing.T) {
	timeout := fmt.Errorf("GET /api/nodes: %w", context.DeadlineExceeded)

	reg := &registration{lastScrapes: []scrapeRun{{Error: timeout, ScrapedPointsCount: 12}}}
	if got := reg.LastScrapeState(); got.ErrorKind != types.ScrapeErrorGatherer {
		t.Errorf("with points gathered, got %+v, want the service to have answered", got)
	}

	reg.lastScrapes = append(reg.lastScrapes, scrapeRun{Error: timeout})
	if got := reg.LastScrapeState(); got.ErrorKind != types.ScrapeErrorUnreachable {
		t.Errorf("with nothing gathered, got %+v, want unreachable", got)
	}
}

// TestScrapeWithoutSecretsGateIsInternal checks the registry's own failure before gathering is
// marked as such, keeping its message: the service was not reached.
func TestScrapeWithoutSecretsGateIsInternal(t *testing.T) {
	err := notGatheredError{fmt.Errorf("%w: no secretInputsGate but input had SecretCount()", ErrBadArgument)}

	if !errors.Is(err, ErrBadArgument) || err.Error() != ErrBadArgument.Error()+": no secretInputsGate but input had SecretCount()" {
		t.Errorf("the wrapper changed the error: %v", err)
	}
}
