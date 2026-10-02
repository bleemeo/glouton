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
	"maps"
	"testing"

	"github.com/bleemeo/glouton/bleemeo/internal/cache"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	gloutonTypes "github.com/bleemeo/glouton/types"
	"github.com/google/go-cmp/cmp"
)

func labelsTextOf(name string, extra map[string]string) string {
	lbls := map[string]string{gloutonTypes.LabelName: name}

	maps.Copy(lbls, extra)

	return gloutonTypes.LabelsToText(lbls)
}

func newMetricsStateSync(
	failures []bleemeoTypes.MetricRegistration,
	notRegistered map[metricStateKey]int,
	pending map[string]int,
) *Synchronizer {
	c := &cache.Cache{}
	c.SetMetricRegistrationsFail(failures)

	return &Synchronizer{
		option:               types.Option{Cache: c},
		notRegisteredMetrics: notRegistered,
		pendingMetrics:       pending,
	}
}

// TestMetricsStateMergesEveryStage checks the three stages a metric can be lost at all
// reach the report. Before this, only the registration failures were recorded anywhere,
// so a metric dropped by the filter was invisible.
func TestMetricsStateMergesEveryStage(t *testing.T) {
	s := newMetricsStateSync(
		[]bleemeoTypes.MetricRegistration{
			{LabelsText: labelsTextOf("cpu_used", nil), LastFailKind: bleemeoTypes.FailureTooManyCustomMetrics},
		},
		map[metricStateKey]int{
			{Name: "disk_used_perc", Reason: bleemeoTypes.ReasonItemTooLong}:    3,
			{Name: "redis_commands", Reason: bleemeoTypes.ReasonWaitingService}: 1,
		},
		map[string]int{"mem_used": 2},
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
	failures := make([]bleemeoTypes.MetricRegistration, 0, 40)

	for _, item := range []string{"/", "/home", "/var", "/tmp"} {
		for range 10 {
			failures = append(failures, bleemeoTypes.MetricRegistration{
				LabelsText:   labelsTextOf("disk_used_perc", map[string]string{gloutonTypes.LabelItem: item}),
				LastFailKind: bleemeoTypes.FailureAllowList,
			})
		}
	}

	got := newMetricsStateSync(failures, nil, nil).MetricsState()

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

// TestMetricsStateIsSorted checks the result is deterministic: it is compared against what
// the API already has, so an unstable order would re-send it on every collection.
func TestMetricsStateIsSorted(t *testing.T) {
	notRegistered := make(map[metricStateKey]int)

	for _, name := range []string{"zzz", "aaa", "mmm"} {
		notRegistered[metricStateKey{Name: name, Reason: bleemeoTypes.ReasonItemTooLong}] = 1
		notRegistered[metricStateKey{Name: name, Reason: bleemeoTypes.ReasonNotAllowed}] = 1
	}

	s := newMetricsStateSync(nil, notRegistered, nil)

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
	got := newMetricsStateSync(nil, nil, nil).MetricsState()

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

	s := newMetricsStateSync(nil, nil, nil)

	failedKey := s.metricKey(failed.Labels(), failed.Annotations())

	s.option.Cache.SetMetricRegistrationsFail([]bleemeoTypes.MetricRegistration{
		{LabelsText: failedKey, LastFailKind: bleemeoTypes.FailureAllowList},
	})

	unregistered := []gloutonTypes.Metric{failed, pending}
	keys := []string{failedKey, s.metricKey(pending.Labels(), pending.Annotations())}

	s.publishMetricsState(unregistered, keys)

	got := s.MetricsState()

	want := []bleemeoTypes.MetricState{
		{Name: "cpu_used", Reason: bleemeoTypes.ReasonNotAllowed, Count: 1},
		{Name: "mem_used", Reason: bleemeoTypes.ReasonPending, Count: 1},
	}

	if diff := cmp.Diff(want, got); diff != "" {
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
	s := newMetricsStateSync(nil, nil, nil)

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
	s := newMetricsStateSync(nil, nil, nil)

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
