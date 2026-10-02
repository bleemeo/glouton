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

package synchronizer

import (
	"strconv"
	"testing"
	"time"

	"github.com/bleemeo/glouton/bleemeo/internal/cache"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	gloutonTypes "github.com/bleemeo/glouton/types"
	"github.com/google/go-cmp/cmp"
)

// labelsMetric is a metric with arbitrary labels, for tests that need more than a name.
type labelsMetric struct {
	lbls map[string]string
}

func (m labelsMetric) Points(time.Time, time.Time) ([]gloutonTypes.Point, error) {
	return nil, errNotImplemented
}

func (m labelsMetric) LastPointReceivedAt() time.Time { return time.Now() }

func (m labelsMetric) Annotations() gloutonTypes.MetricAnnotations {
	return gloutonTypes.MetricAnnotations{}
}

func (m labelsMetric) Labels() map[string]string { return m.lbls }

func metricWithItem(name, item string) labelsMetric {
	return labelsMetric{lbls: map[string]string{gloutonTypes.LabelName: name, gloutonTypes.LabelItem: item}}
}

func newMetricsStateSync(notRegistered map[metricStateKey]int) *Synchronizer {
	return &Synchronizer{
		option:               types.Option{Cache: &cache.Cache{}},
		notRegisteredMetrics: notRegistered,
	}
}

// failure is a registration failure to record in the cache, as registerMetrics would.
type failure struct {
	metric gloutonTypes.Metric
	kind   bleemeoTypes.FailureKind
}

// publishUnregistered runs publishMetricsState on metrics, as the end of a synchronization
// would: notRegistered is what filterMetrics staged, failures what registerMetrics left in
// the cache.
func publishUnregistered(
	s *Synchronizer,
	notRegistered map[metricStateKey]int,
	failures []failure,
	metrics ...gloutonTypes.Metric,
) {
	registrations := make([]bleemeoTypes.MetricRegistration, 0, len(failures))

	for _, f := range failures {
		registrations = append(registrations, bleemeoTypes.MetricRegistration{
			LabelsText:   s.metricKey(f.metric.Labels(), f.metric.Annotations()),
			LastFailKind: f.kind,
		})
	}

	s.option.Cache.SetMetricRegistrationsFail(registrations)

	keys := make([]string, 0, len(metrics))

	for _, metric := range metrics {
		keys = append(keys, s.metricKey(metric.Labels(), metric.Annotations()))
	}

	s.stagedNotRegistered = notRegistered
	s.publishMetricsState(metrics, keys)
}

// TestMetricsStateMergesEveryStage checks the three stages a metric can be lost at all
// reach the report. Before this, only the registration failures were recorded anywhere,
// so a metric dropped by the filter was invisible.
func TestMetricsStateMergesEveryStage(t *testing.T) {
	s := newMetricsStateSync(nil)
	cpu := fakeMetric{label: "cpu_used"}

	publishUnregistered(
		s,
		map[metricStateKey]int{
			{Name: "disk_used_perc", Reason: bleemeoTypes.ReasonItemTooLong}:    3,
			{Name: "redis_commands", Reason: bleemeoTypes.ReasonWaitingService}: 1,
		},
		[]failure{{metric: cpu, kind: bleemeoTypes.FailureTooManyCustomMetrics}},
		cpu, metricWithItem("mem_used", "a"), metricWithItem("mem_used", "b"),
	)

	want := []bleemeoTypes.MetricState{
		{Name: "cpu_used", Reason: bleemeoTypes.ReasonTooManyCustomMetrics, Count: 1},
		{Name: "disk_used_perc", Reason: bleemeoTypes.ReasonItemTooLong, Count: 3},
		{Name: "mem_used", Reason: bleemeoTypes.ReasonPending, Count: 2},
		{Name: "redis_commands", Reason: bleemeoTypes.ReasonWaitingService, Count: 1},
	}

	if diff := cmp.Diff(want, s.MetricsState()); diff != "" {
		t.Errorf("MetricsState() mismatch (-want +got):\n%s", diff)
	}
}

// TestMetricsStateGroupsByNameOnly checks the item and other labels are dropped, so that
// one metric with many items is one row and not thousands.
func TestMetricsStateGroupsByNameOnly(t *testing.T) {
	failures := make([]failure, 0, 40)
	metrics := make([]gloutonTypes.Metric, 0, 40)

	for i := range 40 {
		metric := metricWithItem("disk_used_perc", "/mnt/"+strconv.Itoa(i))
		failures = append(failures, failure{metric: metric, kind: bleemeoTypes.FailureAllowList})
		metrics = append(metrics, metric)
	}

	s := newMetricsStateSync(nil)
	publishUnregistered(s, nil, failures, metrics...)

	got := s.MetricsState()

	if len(got) != 1 {
		t.Fatalf("got %d rows, want 1: %+v", len(got), got)
	}

	if got[0].Name != "disk_used_perc" || got[0].Reason != bleemeoTypes.ReasonNotAllowed {
		t.Errorf("got %+v, want disk_used_perc / not-allowed", got[0])
	}

	if got[0].Count != 40 {
		t.Errorf("got count %d, want 40", got[0].Count)
	}
}

// TestMetricsStateCountsOnlyCurrentMetrics is a regression test: failures used to be
// counted from the whole failure cache. That cache keeps an entry for up to
// failedRegistrationExpiration after its metric is gone, and also holds metrics the filter
// now drops, which were then reported under two reasons.
func TestMetricsStateCountsOnlyCurrentMetrics(t *testing.T) {
	present := fakeMetric{label: "cpu_used"}
	gone := fakeMetric{label: "container_cpu_used"}
	denied := fakeMetric{label: "mem_used"}

	s := newMetricsStateSync(nil)

	publishUnregistered(
		s,
		map[metricStateKey]int{{Name: "mem_used", Reason: bleemeoTypes.ReasonNotAvailableInPlan}: 1},
		[]failure{
			{metric: present, kind: bleemeoTypes.FailureTooManyCustomMetrics},
			{metric: gone, kind: bleemeoTypes.FailureTooManyCustomMetrics},
			{metric: denied, kind: bleemeoTypes.FailureAllowList},
		},
		// Only present passed the filter: gone no longer exists, denied was dropped.
		present,
	)

	want := []bleemeoTypes.MetricState{
		{Name: "cpu_used", Reason: bleemeoTypes.ReasonTooManyCustomMetrics, Count: 1},
		{Name: "mem_used", Reason: bleemeoTypes.ReasonNotAvailableInPlan, Count: 1},
	}

	if diff := cmp.Diff(want, s.MetricsState()); diff != "" {
		t.Errorf("MetricsState() mismatch (-want +got):\n%s", diff)
	}
}

// TestMetricsStateIsSorted checks the result is deterministic: it is compared against what
// the API already has, so an unstable order would re-send it on every collection.
func TestMetricsStateIsSorted(t *testing.T) {
	notRegistered := make(map[metricStateKey]int)

	for _, name := range []string{"zzz", "aaa", "mmm"} {
		notRegistered[metricStateKey{Name: name, Reason: bleemeoTypes.ReasonItemTooLong}] = 1
		notRegistered[metricStateKey{Name: name, Reason: bleemeoTypes.ReasonNotAllowed}] = 1
	}

	s := newMetricsStateSync(notRegistered)

	first := s.MetricsState()

	for range 20 {
		if diff := cmp.Diff(first, s.MetricsState()); diff != "" {
			t.Fatalf("MetricsState() is not deterministic:\n%s", diff)
		}
	}

	want := []bleemeoTypes.MetricState{
		{Name: "aaa", Reason: bleemeoTypes.ReasonItemTooLong, Count: 1},
		{Name: "aaa", Reason: bleemeoTypes.ReasonNotAllowed, Count: 1},
		{Name: "mmm", Reason: bleemeoTypes.ReasonItemTooLong, Count: 1},
		{Name: "mmm", Reason: bleemeoTypes.ReasonNotAllowed, Count: 1},
		{Name: "zzz", Reason: bleemeoTypes.ReasonItemTooLong, Count: 1},
		{Name: "zzz", Reason: bleemeoTypes.ReasonNotAllowed, Count: 1},
	}

	if diff := cmp.Diff(want, first); diff != "" {
		t.Errorf("not sorted by name then reason (-want +got):\n%s", diff)
	}
}

// TestMetricsStateEmpty checks a healthy agent reports an empty list rather than nil
// entries, so that "everything is fine" is a value the API can store.
func TestMetricsStateEmpty(t *testing.T) {
	got := newMetricsStateSync(nil).MetricsState()

	if len(got) != 0 {
		t.Errorf("got %+v, want no row", got)
	}
}

// TestPublishMetricsStateExcludesFailuresFromPending checks a metric that failed
// registration is not also counted as pending: it would appear twice, once with a useful
// reason and once with a useless one.
func TestPublishMetricsStateExcludesFailuresFromPending(t *testing.T) {
	failed := fakeMetric{label: "cpu_used"}
	pending := fakeMetric{label: "mem_used"}

	s := newMetricsStateSync(nil)
	publishUnregistered(s, nil, []failure{{metric: failed, kind: bleemeoTypes.FailureAllowList}}, failed, pending)

	want := []bleemeoTypes.MetricState{
		{Name: "cpu_used", Reason: bleemeoTypes.ReasonNotAllowed, Count: 1},
		{Name: "mem_used", Reason: bleemeoTypes.ReasonPending, Count: 1},
	}

	if diff := cmp.Diff(want, s.MetricsState()); diff != "" {
		t.Errorf("MetricsState() mismatch (-want +got):\n%s", diff)
	}
}

// TestFilterMetricsRecordsMissingAccountConfig is a regression test.
//
// Without an account configuration nothing can be registered. The first version of this
// test called excludeUnregistrableMetrics directly and passed, but that bypassed
// filterMetrics -- its only production caller -- which fails every metric earlier, in
// IsAllowed, because NewFilter keeps a zero account config. Through the real path the
// reason reported was the opaque "error-occurred", not "waiting-account-config". Go
// through filterMetrics so this cannot be fooled again.
func TestFilterMetricsRecordsMissingAccountConfig(t *testing.T) {
	// An empty cache has no account configuration.
	s := newMetricsStateSync(nil)

	kept := s.filterMetrics(
		[]gloutonTypes.Metric{fakeMetric{label: "cpu_used"}, fakeMetric{label: "mem_used"}},
	)

	if len(kept) != 0 {
		t.Fatalf("got %d kept metrics, want none: the account config is missing", len(kept))
	}

	// filterMetrics stages its counts; a synchronization publishes them at its end.
	s.publishMetricsState(nil, nil)

	want := []bleemeoTypes.MetricState{
		{Name: "cpu_used", Reason: bleemeoTypes.ReasonWaitingAccountConfig, Count: 1},
		{Name: "mem_used", Reason: bleemeoTypes.ReasonWaitingAccountConfig, Count: 1},
	}

	if diff := cmp.Diff(want, s.MetricsState()); diff != "" {
		t.Errorf("dropped metrics were not reported (-want +got):\n%s", diff)
	}
}

// TestPublishMetricsStateIsAtomic checks the two halves of the report always come from the
// same synchronization.
//
// filterMetrics runs early and publishMetricsState at the very end, with several error
// returns in between. When one of those is taken, the report must keep the previous, whole
// value rather than mix a fresh half with a stale one.
func TestPublishMetricsStateIsAtomic(t *testing.T) {
	s := newMetricsStateSync(nil)

	// A first synchronization completes.
	s.stagedNotRegistered = map[metricStateKey]int{
		{Name: "old_metric", Reason: bleemeoTypes.ReasonItemTooLong}: 1,
	}
	s.publishMetricsState([]gloutonTypes.Metric{fakeMetric{label: "old_pending"}}, []string{"old_pending_key"})

	before := s.MetricsState()

	// A second one stages new counts, then fails before publishing.
	s.stagedNotRegistered = map[metricStateKey]int{
		{Name: "new_metric", Reason: bleemeoTypes.ReasonNotAllowed}: 1,
	}

	if diff := cmp.Diff(before, s.MetricsState()); diff != "" {
		t.Errorf("a synchronization that did not finish changed the report (-before +after):\n%s", diff)
	}

	// Once it does publish, both halves move together.
	s.publishMetricsState(nil, nil)

	want := []bleemeoTypes.MetricState{
		{Name: "new_metric", Reason: bleemeoTypes.ReasonNotAllowed, Count: 1},
	}

	if diff := cmp.Diff(want, s.MetricsState()); diff != "" {
		t.Errorf("MetricsState() mismatch (-want +got):\n%s", diff)
	}
}
