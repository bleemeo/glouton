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

package syncmonitors

import (
	"context"
	"errors"
	"math/rand"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/bleemeo/bleemeo-go"
	"github.com/bleemeo/glouton/bleemeo/internal/cache"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/config"
	gloutonTypes "github.com/bleemeo/glouton/types"

	"github.com/google/go-cmp/cmp"
)

func Test_applyJitterToMonitorCreationDate_fixedProbe(t *testing.T) {
	// This test ensure that some probes set yield enough randomization.
	// Those ID should be ID of real probes.
	tests := []struct {
		name     string
		probeIDs []string
		// remember than wantMinDelta is the smaller delta between two probes.
		// So the more their is probeIDs, the smaller this delta will be.
		// Theoretically, the best value should be spreadRange / (len(probeIDs)-1)
		wantMinDelta time.Duration
	}{
		// Those are an examples with random uuid (and low wantDelta).
		// Real case should use true agent ID and high wantDelta.
		{
			name: "default-europe",
			probeIDs: []string{
				"7c6ab4b8-fc54-4615-8e9e-d83750666b90",
				"cf173500-530b-473b-a45f-39d85ef148f9",
				"4cf13deb-94d5-40a8-a279-df0e7bfd4146",
			},
			wantMinDelta: 0,
		},
		{
			name: "old-default",
			probeIDs: []string{
				"7c6ab4b8-fc54-4615-8e9e-d83750666b90",
				"61680075-9af4-4318-824d-c5ae87ace0d5",
				"68ac67fe-ad18-40ee-88fc-5debc553562d",
			},
			wantMinDelta: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for range 10 {
				good := true
				probesRuns := make([]time.Time, 0, len(tt.probeIDs))

				createDate := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(rand.Int63n(int64(365 * 24 * time.Hour)))) //nolint:gosec
				monitor := bleemeoTypes.Monitor{
					CreationDate: createDate.Format(time.RFC3339),
				}

				for _, id := range tt.probeIDs {
					agentHash := hashAgentID(id)

					got, err := applyJitterToMonitorCreationDate(monitor, agentHash)
					if err != nil {
						t.Fatal(err)
					}

					probesRuns = append(probesRuns, got)
				}

				for i, firstTime := range probesRuns {
					for j, secondTime := range probesRuns[i+1:] {
						delta := secondTime.Sub(firstTime)
						if delta < 0 {
							delta = -delta
						}

						if delta < tt.wantMinDelta {
							t.Errorf("applyJitterToMonitorCreationDate() delta = %s, want >= %s (i=%d j=%d)", delta, tt.wantMinDelta, i, j+i+1)

							good = false
						}
					}
				}

				if !good {
					break
				}
			}
		})
	}
}

func Test_applyJitterToMonitorCreationDate_different(t *testing.T) {
	// For one probe, not all monitor run at the same time
	monitor1 := bleemeoTypes.Monitor{
		CreationDate: time.Date(2020, 1, 1, 12, 54, 7, 4, time.UTC).Format(time.RFC3339),
	}
	monitor2 := bleemeoTypes.Monitor{
		CreationDate: time.Date(2020, 1, 1, 12, 54, 30, 0, time.UTC).Format(time.RFC3339),
	}
	agentID := "0943c46b-c4e3-4234-b99d-a6548296056e"

	got1, err := applyJitterToMonitorCreationDate(monitor1, hashAgentID(agentID))
	if err != nil {
		t.Fatal(err)
	}

	got2, err := applyJitterToMonitorCreationDate(monitor2, hashAgentID(agentID))
	if err != nil {
		t.Fatal(err)
	}

	delta := got1.Sub(got2)
	if delta < 0 {
		delta = -delta
	}

	// 20s should be reached, the two monitor create date are 23s apart.
	if delta < 20*time.Second {
		t.Fatalf("applyJitterToMonitorCreationDate() delta = %s, want >= 20s", delta)
	}
}

func Test_applyJitterToMonitorCreationDate(t *testing.T) {
	for range 10 {
		createDate := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(rand.Int63n(int64(365 * 24 * time.Hour)))) //nolint:gosec
		monitor := bleemeoTypes.Monitor{
			CreationDate: createDate.Format(time.RFC3339),
		}

		const runCount = 5000

		maxCreateDate := createDate
		minCreateDate := createDate
		countPerTimestamp := make(map[int64]int, runCount)

		wantDelta := 16 * time.Second

		// Due to jitter being randomized, having delta being the full theoretical range would require too
		// much tries. Let's just check that delta is not way too small. This seems
		// enough to avoid flaky test.
		wantDelta -= time.Second

		for range runCount {
			got, err := applyJitterToMonitorCreationDate(monitor, uint64(rand.Int63())) //nolint:gosec
			if err != nil {
				t.Fatal(err)
			}

			// The jitterCreateDate must be within the same minute as original createDate.
			// We want to kept the minute unchanged. This is required for monitor with
			// resolution of 5 minutes because for the quorum Bleemeo assume that the monitor metrics are
			// send at the beginning of the minute after creationDate + N * 5 minutes.
			createDateMinute := createDate.Truncate(time.Minute)
			gotMinute := got.Truncate(time.Minute)

			if !createDateMinute.Equal(gotMinute) {
				t.Fatalf("applyJitterToMonitorCreationDate().Truncate(Minute) = %s, want %s", gotMinute, createDateMinute)
			}

			// The jitterCreateDate must be within the first 45 seconds of the minutes
			// The API task to compute quorum of probes starts at the beginning of every minute,
			// if we run the probe too late in the minute (e.g. 8h20m55s), the new points may
			// not be received by the API on the next quorum (e.g. 8h21m00s). This means the API
			// could use points from the last run (e.g. 8h15m55s), which are more than 5 minutes old.
			// To avoid this problem, we don't run the probes on the last 15 seconds of every minute.
			if got.Second() > 45 {
				t.Fatalf("applyJitterToMonitorCreationDate().Second() = %d, want <= 45", got.Second())
			}

			if got.After(maxCreateDate) {
				maxCreateDate = got
			}

			if got.Before(minCreateDate) {
				minCreateDate = got
			}

			countPerTimestamp[got.Unix()]++
		}

		gotDelta := maxCreateDate.Sub(minCreateDate)

		// applyJitterToMonitorCreationDate should spread creationDate enough
		if gotDelta < wantDelta {
			t.Fatalf("applyJitterToMonitorCreationDate() delta = %s, want >= %s", gotDelta, wantDelta)
		}

		var (
			maxPerBucket int
			minPerBucket int
		)

		for _, count := range countPerTimestamp {
			if maxPerBucket < count {
				maxPerBucket = count
			}

			if minPerBucket == 0 || minPerBucket > count {
				minPerBucket = count
			}
		}

		// applyJitterToMonitorCreationDate should have uniform distribution
		if maxPerBucket-minPerBucket > runCount/10 {
			t.Fatalf("maxPerBucket - minPerBucket = %d, want <= %d\ncountPerTimestamp: %v", maxPerBucket-minPerBucket, runCount/10, countPerTimestamp)
		}
	}
}

func Test_applyJitterToMonitorCreationDateFixedValue(t *testing.T) {
	createDate := time.Date(2024, 4, 30, 16, 32, 47, 123456, time.UTC)
	monitor := bleemeoTypes.Monitor{
		CreationDate: createDate.Format(time.RFC3339),
	}

	got, err := applyJitterToMonitorCreationDate(monitor, 42)
	if err != nil {
		t.Fatal(err)
	}

	// What this test check is that wanted value don't change too often, especially between
	// different run and when time.Now() changes.
	// If applyJitterToMonitorCreationDate need to be updated, the wanted value could change.
	want := time.Date(2024, 4, 30, 16, 32, 32, 42000000, time.UTC)

	if !got.Equal(want) {
		t.Errorf("applyJitterToMonitorCreationDate() = %s, want %s", got, want)
	}
}

// newTestMonitor returns an active monitor of the account.

const (
	testAccountID = "account-id"
	testMonitorID = "monitor-id"
)

var errAPIUnavailable = errors.New("API unavailable")

// fakeClient serves the monitors, and fails with err when it's set. onGet runs on each
// GetMonitorByID.
type fakeClient struct {
	types.Client

	monitors []bleemeoTypes.Monitor
	err      error
	onGet    func()

	listCount int
	getCount  int
}

func (c *fakeClient) ListMonitors(context.Context) ([]bleemeoTypes.Monitor, error) {
	c.listCount++

	return c.monitors, c.err
}

func (c *fakeClient) GetMonitorByID(_ context.Context, id string) (bleemeoTypes.Monitor, error) {
	c.getCount++

	if c.onGet != nil {
		c.onGet()
	}

	if c.err != nil {
		return bleemeoTypes.Monitor{}, c.err
	}

	for _, m := range c.monitors {
		if m.ID == id {
			return m, nil
		}
	}

	return bleemeoTypes.Monitor{}, &bleemeo.APIError{StatusCode: http.StatusNotFound, Err: bleemeo.ErrResourceNotFound}
}

type laterRequest struct {
	entity      types.EntityName
	requestFull bool
	delay       time.Duration
}

// fakeExecution gives the client and the blackbox configuration to the entity, and records the
// synchronizations it requests. Any other call panics.
type fakeExecution struct {
	types.SynchronizationExecution

	client          *fakeClient
	blackboxEnabled bool

	requested map[types.EntityName]bool
	later     []laterRequest
}

func newFakeExecution(monitors ...bleemeoTypes.Monitor) *fakeExecution {
	return &fakeExecution{
		client:          &fakeClient{monitors: monitors},
		blackboxEnabled: true,
		requested:       make(map[types.EntityName]bool),
	}
}

func (e *fakeExecution) BleemeoAPIClient() types.Client { return e.client }

func (e *fakeExecution) Option() types.Option {
	return types.Option{Config: config.Config{Blackbox: config.Blackbox{Enable: e.blackboxEnabled}}}
}

func (e *fakeExecution) RequestSynchronization(entityName types.EntityName, requestFull bool) {
	e.requested[entityName] = requestFull
}

func (e *fakeExecution) RequestLaterSynchronization(entityName types.EntityName, requestFull bool, delay time.Duration) {
	e.later = append(e.later, laterRequest{entity: entityName, requestFull: requestFull, delay: delay})
}

// fakeMonitorManager records the monitors given to the prober.
type fakeMonitorManager struct {
	monitors []gloutonTypes.Monitor
	calls    int
}

func (m *fakeMonitorManager) UpdateDynamicTargets(monitors []gloutonTypes.Monitor) error {
	m.monitors = monitors
	m.calls++

	return nil
}

func newMonitor(id string) bleemeoTypes.Monitor {
	return bleemeoTypes.Monitor{
		ID:           id,
		Account:      testAccountID,
		CreationDate: "2020-01-03T04:05:06Z",
		URL:          "https://" + id + ".example.com",
		Active:       true,
	}
}

// run runs an execution of the entity, and returns the error of its synchronization.
func run(t *testing.T, s *SyncMonitors, execution *fakeExecution, syncType types.SyncType) error {
	t.Helper()

	e, err := s.PrepareExecution(t.Context(), execution)
	if err != nil {
		t.Fatal(err)
	}

	defer e.FinishExecution(t.Context())

	if err := e.RefreshCache(t.Context(), syncType); err != nil {
		return err
	}

	return e.SyncRemoteAndLocal(t.Context(), syncType)
}

func monitorIDs(monitors []bleemeoTypes.Monitor) []string {
	ids := make([]string, 0, len(monitors))

	for _, m := range monitors {
		ids = append(ids, m.ID)
	}

	slices.Sort(ids)

	return ids
}

// TestApplyUpdates checks a change reads the monitor, and a deletion, a monitor no longer on the
// API or deactivated removes it.
func TestApplyUpdates(t *testing.T) {
	t.Parallel()

	changed := newMonitor("changed")
	changed.URL = "https://changed.example.com/new"
	inactive := newMonitor("inactive")
	inactive.Active = false

	client := &fakeClient{monitors: []bleemeoTypes.Monitor{changed, inactive, newMonitor("added")}}
	current := []bleemeoTypes.Monitor{newMonitor("changed"), newMonitor("deleted"), newMonitor("gone"), newMonitor("inactive"), newMonitor("kept")}

	got, err := applyUpdates(t.Context(), client, current, []monitorUpdate{
		{op: change, uuid: "changed"},
		{op: remove, uuid: "deleted"},
		{op: change, uuid: "gone"},
		{op: change, uuid: "inactive"},
		{op: change, uuid: "added"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff([]string{"added", "changed", "kept"}, monitorIDs(got)); diff != "" {
		t.Errorf("monitors mismatch (-want +got)\n%s", diff)
	}

	for _, m := range got {
		if m.ID == "changed" && m.URL != changed.URL {
			t.Errorf("changed monitor URL = %q, want %q", m.URL, changed.URL)
		}
	}

	if client.getCount != 4 {
		t.Errorf("GetMonitorByID called %d times, want 4 (not for the deletion)", client.getCount)
	}
}

// TestManyUpdatesListMonitors checks more than 5 updates list the monitors instead of reading
// them one by one, and request the metrics.
func TestManyUpdatesListMonitors(t *testing.T) {
	t.Parallel()

	s := New(&cache.Cache{}, nil, nil)
	execution := newFakeExecution(newMonitor(testMonitorID))

	for range 6 {
		s.UpdateMonitor("change", testMonitorID)
	}

	if err := run(t, s, execution, types.SyncTypeNormal); err != nil {
		t.Fatal(err)
	}

	if execution.client.listCount != 1 || execution.client.getCount != 0 {
		t.Errorf("ListMonitors called %d times and GetMonitorByID %d times, want 1 and 0", execution.client.listCount, execution.client.getCount)
	}

	if _, ok := execution.requested[types.EntityMetric]; !ok {
		t.Error("the metrics synchronization wasn't requested")
	}

	if diff := cmp.Diff([]string{testMonitorID}, monitorIDs(s.cache.Monitors())); diff != "" {
		t.Errorf("cached monitors mismatch (-want +got)\n%s", diff)
	}
}

// TestFailedUpdateRetried checks the updates of a failed synchronization are kept, before the
// ones received during it, and retried a minute later.
func TestFailedUpdateRetried(t *testing.T) {
	t.Parallel()

	s := New(&cache.Cache{}, nil, nil)
	execution := newFakeExecution(newMonitor("first"), newMonitor("second"))
	execution.client.err = errAPIUnavailable
	execution.client.onGet = func() { s.UpdateMonitor("delete", "second") }

	s.UpdateMonitor("change", "first")

	if err := run(t, s, execution, types.SyncTypeNormal); !errors.Is(err, errAPIUnavailable) {
		t.Fatalf("SyncRemoteAndLocal() = %v, want %v", err, errAPIUnavailable)
	}

	want := []laterRequest{{entity: types.EntityMonitor, requestFull: false, delay: time.Minute}}
	if diff := cmp.Diff(want, execution.later, cmp.AllowUnexported(laterRequest{})); diff != "" {
		t.Errorf("later synchronizations mismatch (-want +got)\n%s", diff)
	}

	if diff := cmp.Diff([]monitorUpdate{{op: change, uuid: "first"}, {op: remove, uuid: "second"}}, s.pending, cmp.AllowUnexported(monitorUpdate{})); diff != "" {
		t.Errorf("pending updates mismatch (-want +got)\n%s", diff)
	}

	execution.client.err = nil
	execution.client.onGet = nil

	if err := run(t, s, execution, types.SyncTypeNormal); err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff([]string{"first"}, monitorIDs(s.cache.Monitors())); diff != "" {
		t.Errorf("cached monitors mismatch (-want +got)\n%s", diff)
	}
}

// TestBlackboxDisabled checks the updates are dropped when the blackbox is disabled.
func TestBlackboxDisabled(t *testing.T) {
	t.Parallel()

	s := New(&cache.Cache{}, nil, nil)
	execution := newFakeExecution(newMonitor(testMonitorID))
	execution.blackboxEnabled = false

	s.UpdateMonitor("change", testMonitorID)

	if err := run(t, s, execution, types.SyncTypeForceCacheRefresh); err != nil {
		t.Fatal(err)
	}

	if execution.client.listCount != 0 || execution.client.getCount != 0 || len(s.pending) != 0 {
		t.Errorf("ListMonitors called %d times, GetMonitorByID %d times, %d updates pending, want none", execution.client.listCount, execution.client.getCount, len(s.pending))
	}
}

// TestMissingProbeConfig checks a monitor of an account without probe configuration fails the
// synchronization, and requests the configuration.
func TestMissingProbeConfig(t *testing.T) {
	t.Parallel()

	manager := &fakeMonitorManager{}
	s := New(&cache.Cache{}, nil, manager)
	execution := newFakeExecution(newMonitor(testMonitorID))

	if err := run(t, s, execution, types.SyncTypeForceCacheRefresh); !errors.Is(err, errMissingAccountConf) {
		t.Fatalf("SyncRemoteAndLocal() = %v, want %v", err, errMissingAccountConf)
	}

	if full, ok := execution.requested[types.EntityConfig]; !ok || !full {
		t.Error("the configuration synchronization, with a cache refresh, wasn't requested")
	}

	if manager.calls != 0 {
		t.Errorf("the prober got the monitors %d times, want 0", manager.calls)
	}
}
