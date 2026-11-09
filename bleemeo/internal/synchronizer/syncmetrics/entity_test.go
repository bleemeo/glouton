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

package syncmetrics

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/bleemeo/glouton/bleemeo/internal/cache"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/bleemeoapi"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/executiontest"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/discovery"
	gloutonTypes "github.com/bleemeo/glouton/types"

	"github.com/google/go-cmp/cmp"
)

const testAgentID = "agent-id"

var errAPIUnavailable = errors.New("API unavailable")

func testNow() time.Time { return time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC) }

// fakeStore gives the number of metrics. Any other call panics.
type fakeStore struct {
	bleemeoTypes.Store

	count int
}

func (s fakeStore) MetricsCount() int { return s.count }

// fakeClient records the metrics deactivated, and fails the metrics list with err when set. Any
// other call panics.
type fakeClient struct {
	types.Client

	err         error
	deactivated []string
}

func (c *fakeClient) ListActiveMetrics(context.Context) ([]bleemeoapi.MetricPayload, error) {
	return nil, c.err
}

func (c *fakeClient) SetMetricActive(_ context.Context, id string, active bool) error {
	if !active {
		c.deactivated = append(c.deactivated, id)
	}

	return nil
}

// fakeExecution gives the times and the client to the entity, and records the synchronizations
// it requests. Any other call panics.
type fakeExecution struct {
	executiontest.Recorder

	lastSuccessfulSync time.Time
}

func (e *fakeExecution) StartedAt() time.Time                          { return testNow() }
func (e *fakeExecution) LastSuccessfulSync(types.EntityName) time.Time { return e.lastSuccessfulSync }

// newExecution returns an execution of a new entity, which last synchronized at lastSync.
func newExecution(option types.Option, lastSync time.Time) (*syncMetricsExecution, *fakeExecution) {
	if option.Cache == nil {
		option.Cache = &cache.Cache{}
	}

	s := New(option, testNow, func() time.Time { return testNow().Add(-time.Hour) })
	execution := &fakeExecution{lastSuccessfulSync: lastSync}

	return &syncMetricsExecution{SyncMetrics: s, execution: execution, agentID: testAgentID}, execution
}

// TestNeedSynchronization checks the metrics are synchronized when a registration retry is due,
// after a discovery or an annotation change, and when the number of metrics changes, also while
// they are skipped in maintenance or suspended mode, and with the containers and the monitors.
func TestNeedSynchronization(t *testing.T) {
	t.Parallel()

	lastSync := testNow().Add(-time.Minute)
	before := lastSync.Add(-time.Minute)
	after := lastSync.Add(time.Second)

	tests := []struct {
		name       string
		retryAt    time.Time
		discovery  time.Time
		annotation time.Time
		lastCount  int
		count      int
		want       bool
	}{
		{name: "nothing new", retryAt: testNow().Add(time.Minute), discovery: before, annotation: before, want: false},
		{name: "retry due", retryAt: testNow().Add(-time.Second), discovery: before, annotation: before, want: true},
		{name: "discovery", retryAt: testNow().Add(time.Minute), discovery: after, annotation: before, want: true},
		{name: "annotation change", retryAt: testNow().Add(time.Minute), discovery: before, annotation: after, want: true},
		{name: "metric count change", retryAt: testNow().Add(time.Minute), discovery: before, annotation: before, lastCount: 3, count: 4, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			option := types.Option{
				Discovery:                  &discovery.MockDiscoverer{UpdatedAt: tt.discovery},
				Store:                      fakeStore{count: tt.count},
				LastMetricAnnotationChange: func() time.Time { return tt.annotation },
			}

			s, execution := newExecution(option, lastSync)
			s.metricRetryAt = tt.retryAt
			s.lastMetricCount = tt.lastCount

			need, err := s.NeedSynchronization(t.Context())
			if err != nil || need != tt.want {
				t.Errorf("NeedSynchronization() = %v, %v, want %v", need, err, tt.want)
			}

			if need := s.NeedSynchronizationWhileSkipped(t.Context(), execution); need != tt.want {
				t.Errorf("NeedSynchronizationWhileSkipped() = %v, want %v", need, tt.want)
			}

			want := [][2]types.EntityName{
				{types.EntityMetric, types.EntityContainer},
				{types.EntityMetric, types.EntityMonitor},
			}
			if diff := cmp.Diff(want, execution.Linked); diff != "" {
				t.Errorf("linked synchronizations mismatch (-want +got)\n%s", diff)
			}
		})
	}
}

// TestUpdateMetrics checks the metrics to read are deduplicated, and an empty UUID asks to read
// them all.
func TestUpdateMetrics(t *testing.T) {
	t.Parallel()

	s := New(types.Option{}, testNow, testNow)

	if all := s.UpdateMetrics("a", "b"); all {
		t.Error("UpdateMetrics(a, b) = true, want false")
	}

	if all := s.UpdateMetrics("a"); all {
		t.Error("UpdateMetrics(a) = true, want false")
	}

	got := s.popPendingMetricsUpdate()
	slices.Sort(got)

	if diff := cmp.Diff([]string{"a", "b"}, got); diff != "" {
		t.Errorf("pending updates mismatch (-want +got)\n%s", diff)
	}

	s.UpdateMetrics("c")

	if all := s.UpdateMetrics(""); !all {
		t.Error(`UpdateMetrics("") = false, want true`)
	}

	if got := s.popPendingMetricsUpdate(); len(got) != 0 {
		t.Errorf("pending updates = %v, want none: all the metrics are read", got)
	}
}

// TestPendingUpdateRequeued checks the metrics to read are kept, and their synchronization
// requested again, when reading them fails.
func TestPendingUpdateRequeued(t *testing.T) {
	t.Parallel()

	s, execution := newExecution(types.Option{}, time.Time{})
	pending := []string{"a", "b"}

	err := s.metricUpdatePendingOrSync(t.Context(), &fakeClient{err: errAPIUnavailable}, true, &pending)
	if !errors.Is(err, errAPIUnavailable) {
		t.Fatalf("metricUpdatePendingOrSync() = %v, want %v", err, errAPIUnavailable)
	}

	got := s.popPendingMetricsUpdate()
	slices.Sort(got)

	if diff := cmp.Diff(pending, got); diff != "" {
		t.Errorf("pending updates mismatch (-want +got)\n%s", diff)
	}

	if full, ok := execution.Requested[types.EntityMetric]; !ok || full {
		t.Errorf("metric synchronization requested = %v (full %v), want a normal one", ok, full)
	}
}

// TestDeactivateMakesTooManyRetryable checks deactivating a metric makes the registrations refused
// for too many metrics retryable right away: the deactivation may have made room.
func TestDeactivateMakesTooManyRetryable(t *testing.T) {
	t.Parallel()

	c := &cache.Cache{}
	c.SetMetrics([]bleemeoTypes.Metric{{
		ID:          "gone",
		AgentID:     testAgentID,
		LabelsText:  gloutonTypes.LabelsToText(map[string]string{gloutonTypes.LabelName: "mem_used"}),
		FirstSeenAt: testNow().Add(-time.Hour),
	}})
	c.SetMetricRegistrationsFail([]bleemeoTypes.MetricRegistration{{
		LabelsText:   gloutonTypes.LabelsToText(map[string]string{gloutonTypes.LabelName: "custom_metric"}),
		LastFailAt:   testNow().Add(-time.Minute),
		LastFailKind: bleemeoTypes.FailureTooManyCustomMetrics,
	}})

	option := types.Option{
		Cache:           c,
		IsMetricAllowed: func(map[string]string) bool { return true },
	}

	s, _ := newExecution(option, time.Time{})
	client := &fakeClient{}

	if err := s.metricDeactivate(t.Context(), client, nil); err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff([]string{"gone"}, client.deactivated); diff != "" {
		t.Errorf("deactivated metrics mismatch (-want +got)\n%s", diff)
	}

	if !s.retryableMetricFailure[bleemeoTypes.FailureTooManyCustomMetrics] || !s.retryableMetricFailure[bleemeoTypes.FailureTooManyStandardMetrics] {
		t.Errorf("retryable failures = %v, want the too many metrics ones", s.retryableMetricFailure)
	}

	if !s.metricRetryAt.Equal(testNow()) {
		t.Errorf("metric retry at %v, want now (%v)", s.metricRetryAt, testNow())
	}
}
