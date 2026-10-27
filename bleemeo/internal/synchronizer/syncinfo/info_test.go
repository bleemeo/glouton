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

package syncinfo

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bleemeo/bleemeo-go"
	"github.com/bleemeo/glouton/bleemeo/internal/cache"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/prometheus/model"
	gloutonTypes "github.com/bleemeo/glouton/types"

	"github.com/google/go-cmp/cmp"
)

const (
	testAgentID       = "agent-id"
	testAgentStatusID = "agent-status-id"
)

var (
	errExpiredCertificate = errors.New("tls: failed to verify certificate: x509: certificate has expired or is not yet valid")
	errConnectionRefused  = errors.New("connection refused")
	errAPIDown            = errors.New("API down")
)

// fakeClient returns globalInfo, or err, and records the metric updates. Any other call
// panics.
type fakeClient struct {
	types.Client

	globalInfo bleemeoTypes.GlobalInfo
	err        error
	// updateErr is returned by UpdateMetric.
	updateErr error
	// statusUpdates are the agent_status updates.
	statusUpdates []agentStatusUpdatePayload
}

func (c *fakeClient) GetGlobalInfo(context.Context) (bleemeoTypes.GlobalInfo, error) {
	return c.globalInfo, c.err
}

func (c *fakeClient) UpdateMetric(_ context.Context, id string, payload any, _ string) error {
	if id != testAgentStatusID {
		return errors.New("unexpected metric " + id) //nolint:err113
	}

	if c.updateErr != nil {
		return c.updateErr
	}

	c.statusUpdates = append(c.statusUpdates, payload.(agentStatusUpdatePayload)) //nolint:forcetypeassert

	return nil
}

type fakeState struct {
	bleemeoTypes.State
}

func (fakeState) BleemeoCredentials() (string, string) { return testAgentID, "password" }

type fakeGlobalState struct {
	types.SynchronizedGlobalState

	maintenance bool
}

func (s fakeGlobalState) IsMaintenance() bool { return s.maintenance }

type disableCall struct {
	reason bleemeoTypes.DisableReason
	until  time.Time
}

// fakeExecution gives the client, the cache and the options to the entity, and records what
// it asks the synchronizer. Any other call panics.
type fakeExecution struct {
	types.SynchronizationExecution

	client        *fakeClient
	cache         *cache.Cache
	appender      *model.BufferAppender
	cfg           config.Config
	startedAt     time.Time
	maintenance   bool
	mqttConnected bool
	// noState gives a nil State.
	noState bool

	disabled []disableCall
	// maintenanceSet is the maintenance mode set by the entity, nil when it didn't.
	maintenanceSet *bool
	requested      map[types.EntityName]bool
}

func newFakeExecution(globalInfo bleemeoTypes.GlobalInfo) *fakeExecution {
	c := &cache.Cache{}
	c.SetMetrics([]bleemeoTypes.Metric{{
		ID: testAgentStatusID,
		LabelsText: gloutonTypes.LabelsToText(map[string]string{
			gloutonTypes.LabelName:         types.AgentStatusName,
			gloutonTypes.LabelInstanceUUID: testAgentID,
		}),
	}})

	return &fakeExecution{
		client:    &fakeClient{globalInfo: globalInfo},
		cache:     c,
		appender:  model.NewBufferAppender(),
		requested: make(map[types.EntityName]bool),
	}
}

func (e *fakeExecution) BleemeoAPIClient() types.Client { return e.client }
func (e *fakeExecution) StartedAt() time.Time           { return e.startedAt }
func (e *fakeExecution) GlobalState() types.SynchronizedGlobalState {
	return fakeGlobalState{maintenance: e.maintenance}
}

func (e *fakeExecution) RequestSynchronization(entityName types.EntityName, forceCacheRefresh bool) {
	e.requested[entityName] = forceCacheRefresh
}

func (e *fakeExecution) Option() types.Option {
	return types.Option{
		Cache:           e.cache,
		PushAppender:    e.appender,
		IsMqttConnected: func() bool { return e.mqttConnected },
		DisableCallback: func(reason bleemeoTypes.DisableReason, until time.Time) {
			e.disabled = append(e.disabled, disableCall{reason: reason, until: until})
		},
		SetBleemeoInMaintenanceMode: func(_ context.Context, maintenance bool) {
			e.maintenanceSet = &maintenance
		},
		Config: e.cfg,
		State:  e.state(),
	}
}

func (e *fakeExecution) state() bleemeoTypes.State {
	if e.noState {
		return nil
	}

	return fakeState{}
}

type fakeClock struct {
	now time.Time
}

func (c *fakeClock) Now() time.Time { return c.now }

// dialReachable connects to nothing: MQTT is reachable.
func dialReachable(context.Context, string, string) (net.Conn, error) {
	client, server := net.Pipe()
	_ = server.Close()

	return client, nil
}

func newEntity(clock *fakeClock) *SyncInfo {
	s := New(&atomic.Uint32{}, clock.Now)
	// MQTT is reachable, unless a test says otherwise.
	s.dial = dialReachable

	return s
}

// run runs one execution of the entity, which was requested.
func run(t *testing.T, s *SyncInfo, execution *fakeExecution) {
	t.Helper()

	e, err := s.PrepareExecution(t.Context(), execution)
	if err != nil {
		t.Fatal(err)
	}

	defer e.FinishExecution(t.Context())

	if err := e.RefreshCache(t.Context(), types.SyncTypeNormal); err != nil {
		t.Fatal(err)
	}

	if err := e.SyncRemoteAndLocal(t.Context(), types.SyncTypeNormal); err != nil {
		t.Fatal(err)
	}
}

// TestMaintenancePolling checks info is synchronized every 15 minutes while in maintenance,
// when MQTT can't tell the maintenance ended.
func TestMaintenancePolling(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)
	s := newEntity(&fakeClock{now: start})

	for _, step := range []struct {
		name          string
		startedAt     time.Time
		maintenance   bool
		mqttConnected bool
		want          bool
	}{
		{name: "not in maintenance", startedAt: start},
		{name: "MQTT connected", startedAt: start, maintenance: true, mqttConnected: true},
		{name: "first poll", startedAt: start, maintenance: true, want: true},
		{name: "polled recently", startedAt: start.Add(15 * time.Minute), maintenance: true},
		{name: "second poll", startedAt: start.Add(15*time.Minute + time.Second), maintenance: true, want: true},
	} {
		execution := newFakeExecution(bleemeoTypes.GlobalInfo{})
		execution.startedAt = step.startedAt
		execution.maintenance = step.maintenance
		execution.mqttConnected = step.mqttConnected

		e, err := s.PrepareExecution(t.Context(), execution)
		if err != nil {
			t.Fatal(err)
		}

		got, err := e.NeedSynchronization(t.Context())
		if err != nil {
			t.Fatal(err)
		}

		if got != step.want {
			t.Errorf("%s: NeedSynchronization() = %v, want %v", step.name, got, step.want)
		}
	}
}

// TestApplyMaintenance checks the maintenance mode is set from the global information, and
// that nothing is applied when it couldn't be fetched.
func TestApplyMaintenance(t *testing.T) {
	t.Parallel()

	clock := &fakeClock{now: time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)}
	s := newEntity(clock)

	execution := newFakeExecution(bleemeoTypes.GlobalInfo{MaintenanceEnabled: true})
	run(t, s, execution)

	if execution.maintenanceSet == nil || !*execution.maintenanceSet {
		t.Errorf("maintenance set = %v, want true", execution.maintenanceSet)
	}

	if got := s.LastInfo().FetchedAt; !got.Equal(clock.now) {
		t.Errorf("LastInfo().FetchedAt = %v, want %v", got, clock.now)
	}

	clock.now = clock.now.Add(time.Hour)

	if !s.IsOutdated() {
		t.Error("IsOutdated() = false after an hour, want true")
	}

	execution = newFakeExecution(bleemeoTypes.GlobalInfo{})
	execution.client.err = errAPIDown
	run(t, s, execution)

	if execution.maintenanceSet != nil {
		t.Errorf("maintenance set to %v when the fetch failed", *execution.maintenanceSet)
	}

	if !s.LastInfo().MaintenanceEnabled {
		t.Error("LastInfo() changed when the fetch failed")
	}
}

// TestAgentTooOld checks the connector is disabled for a day, and info requested again.
func TestAgentTooOld(t *testing.T) {
	t.Parallel()

	clock := &fakeClock{now: time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)}
	s := newEntity(clock)

	globalInfo := bleemeoTypes.GlobalInfo{}
	// An invalid minimum version is newer than any version.
	globalInfo.Agents.MinVersions.Glouton = "invalid version"

	execution := newFakeExecution(globalInfo)
	run(t, s, execution)

	if len(execution.disabled) != 1 || execution.disabled[0].reason != bleemeoTypes.DisableAgentTooOld {
		t.Fatalf("disabled = %v, want once for the agent version", execution.disabled)
	}

	if until := execution.disabled[0].until.Sub(clock.now); until < 20*time.Hour || until > 28*time.Hour {
		t.Errorf("disabled for %v, want about a day", until)
	}

	if diff := cmp.Diff(map[types.EntityName]bool{types.EntityInfo: true}, execution.requested); diff != "" {
		t.Errorf("requested synchronizations mismatch (-want +got)\n%s", diff)
	}
}

// TestTimeDrift checks a large time drift disables the connector, marks agent_status critical
// once, and pushes the time_drift metric.
func TestTimeDrift(t *testing.T) {
	t.Parallel()

	clock := &fakeClock{now: time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)}
	s := newEntity(clock)
	drifting := bleemeoTypes.GlobalInfo{
		CurrentTime:  float64(clock.now.Add(-time.Hour).Unix()),
		MaxTimeDrift: 60,
	}

	// Before the first execution, with disableOnTimeDrift false, the connector stays enabled.
	execution := newFakeExecution(drifting)
	if err := s.SyncBeforeFirstExecution(t.Context(), execution, false); err != nil {
		t.Fatal(err)
	}

	if len(execution.disabled) != 0 {
		t.Errorf("disabled = %v, want not disabled before the first execution", execution.disabled)
	}

	want := []agentStatusUpdatePayload{{
		CurrentStatus:     bleemeo.Status_Critical,
		StatusDescription: []string{"Agent local time too different from actual time"},
	}}
	if diff := cmp.Diff(want, execution.client.statusUpdates); diff != "" {
		t.Errorf("agent_status updates mismatch (-want +got)\n%s", diff)
	}

	mfs, err := execution.appender.AsMF()
	if err != nil {
		t.Fatal(err)
	}

	if len(mfs) != 1 || mfs[0].GetName() != "time_drift" || mfs[0].GetMetric()[0].GetUntyped().GetValue() != time.Hour.Seconds() {
		t.Errorf("pushed metrics = %v, want time_drift of an hour", mfs)
	}

	// Still drifting: the connector is disabled, agent_status isn't updated again.
	clock.now = clock.now.Add(time.Minute)
	drifting.CurrentTime += time.Minute.Seconds()

	execution = newFakeExecution(drifting)
	run(t, s, execution)

	if len(execution.disabled) != 1 || execution.disabled[0].reason != bleemeoTypes.DisableTimeDrift {
		t.Errorf("disabled = %v, want once for the time drift", execution.disabled)
	}

	if len(execution.client.statusUpdates) != 0 {
		t.Errorf("agent_status updated again: %v", execution.client.statusUpdates)
	}

	if diff := cmp.Diff(map[types.EntityName]bool{types.EntityInfo: true}, execution.requested); diff != "" {
		t.Errorf("requested synchronizations mismatch (-want +got)\n%s", diff)
	}
}

// TestMQTTStatus checks agent_status is marked critical when MQTT gets disconnected and isn't
// reachable, and only then.
func TestMQTTStatus(t *testing.T) {
	t.Parallel()

	s := newEntity(&fakeClock{now: time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)})

	dialed := 0
	reachable := true
	s.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		dialed++

		if !reachable {
			return nil, errConnectionRefused
		}

		return dialReachable(ctx, network, address)
	}

	for _, step := range []struct {
		name      string
		connected bool
		reachable bool
		wantSync  bool
		wantDial  bool
		wantMark  bool
	}{
		{name: "connected", connected: true},
		{name: "disconnected but reachable", reachable: true, wantSync: true, wantDial: true},
		{name: "still disconnected"},
		{name: "connected again", connected: true},
		{name: "disconnected and unreachable", wantSync: true, wantDial: true, wantMark: true},
	} {
		dialed = 0
		reachable = step.reachable

		if got := s.SetMQTTConnected(step.connected); got != step.wantSync {
			t.Errorf("%s: SetMQTTConnected() = %v, want %v", step.name, got, step.wantSync)
		}

		execution := newFakeExecution(bleemeoTypes.GlobalInfo{})
		execution.cfg.Bleemeo.MQTT.Port = 8883
		run(t, s, execution)

		if (dialed > 0) != step.wantDial {
			t.Errorf("%s: dialed %d times, want dial %v", step.name, dialed, step.wantDial)
		}

		var want []agentStatusUpdatePayload
		if step.wantMark {
			want = []agentStatusUpdatePayload{{
				CurrentStatus:     bleemeo.Status_Critical,
				StatusDescription: []string{"Agent can't access Bleemeo MQTT on port 8883, is the port blocked by a firewall?"},
			}}
		}

		if diff := cmp.Diff(want, execution.client.statusUpdates); diff != "" {
			t.Errorf("%s: agent_status updates mismatch (-want +got)\n%s", step.name, diff)
		}
	}
}

// TestFetchExpiredCertificate checks the global information is fetched again without checking
// the certificate when it looks expired, as when the local time is far off.
func TestFetchExpiredCertificate(t *testing.T) {
	t.Parallel()

	status := http.StatusOK

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/info/" {
			http.NotFound(w, r)

			return
		}

		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"maintenance": true, "current_time": 1700000000, "max_time_drift": 3600}`))
	}))
	defer server.Close()

	clock := &fakeClock{now: time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)}
	requestCounter := &atomic.Uint32{}
	s := New(requestCounter, clock.Now)
	s.dial = dialReachable

	execution := newFakeExecution(bleemeoTypes.GlobalInfo{})
	execution.client.err = errExpiredCertificate
	execution.cfg.Bleemeo.APIBase = server.URL
	run(t, s, execution)

	if execution.maintenanceSet == nil || !*execution.maintenanceSet {
		t.Errorf("maintenance set = %v, want true", execution.maintenanceSet)
	}

	want := bleemeoTypes.GlobalInfo{MaintenanceEnabled: true, CurrentTime: 1700000000, MaxTimeDrift: 3600, FetchedAt: clock.now}
	if diff := cmp.Diff(want, s.LastInfo()); diff != "" {
		t.Errorf("LastInfo() mismatch (-want +got)\n%s", diff)
	}

	if requestCounter.Load() != 1 {
		t.Errorf("requests counted = %d, want 1", requestCounter.Load())
	}

	// A failed retry applies nothing.
	status = http.StatusServiceUnavailable
	clock.now = clock.now.Add(time.Hour)

	execution = newFakeExecution(bleemeoTypes.GlobalInfo{})
	execution.client.err = errExpiredCertificate
	execution.cfg.Bleemeo.APIBase = server.URL
	run(t, s, execution)

	if execution.maintenanceSet != nil {
		t.Errorf("maintenance set to %v when the retry failed", *execution.maintenanceSet)
	}

	if got := s.LastInfo().FetchedAt; !got.Equal(want.FetchedAt) {
		t.Errorf("LastInfo().FetchedAt = %v, want %v (unchanged)", got, want.FetchedAt)
	}
}

// TestTimeDriftNewAgent checks a new agent with a large time drift marks agent_status once it
// is registered: before, there is no agent_status to mark.
func TestTimeDriftNewAgent(t *testing.T) {
	t.Parallel()

	clock := &fakeClock{now: time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)}
	s := newEntity(clock)
	drifting := bleemeoTypes.GlobalInfo{
		CurrentTime:  float64(clock.now.Add(-time.Hour).Unix()),
		MaxTimeDrift: 60,
	}
	wantMark := []agentStatusUpdatePayload{{
		CurrentStatus:     bleemeo.Status_Critical,
		StatusDescription: []string{"Agent local time too different from actual time"},
	}}

	execution := newFakeExecution(drifting)
	execution.cache.SetMetrics(nil)

	if err := s.SyncBeforeFirstExecution(t.Context(), execution, false); err != nil {
		t.Fatal(err)
	}

	for _, step := range []struct {
		name       string
		timeOffset time.Duration
		want       []agentStatusUpdatePayload
	}{
		{name: "registered", timeOffset: -time.Hour, want: wantMark},
		{name: "still drifting", timeOffset: -time.Hour},
		{name: "time fixed"},
		{name: "drifting again", timeOffset: -time.Hour, want: wantMark},
	} {
		globalInfo := drifting
		globalInfo.CurrentTime = float64(clock.now.Add(step.timeOffset).Unix())

		execution := newFakeExecution(globalInfo)
		run(t, s, execution)

		if diff := cmp.Diff(step.want, execution.client.statusUpdates); diff != "" {
			t.Errorf("%s: agent_status updates mismatch (-want +got)\n%s", step.name, diff)
		}
	}
}

// TestMQTTStatusRetried checks the MQTT check is done again on the next synchronization of info
// until agent_status tells it.
func TestMQTTStatusRetried(t *testing.T) {
	t.Parallel()

	s := newEntity(&fakeClock{now: time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)})
	s.dial = func(context.Context, string, string) (net.Conn, error) {
		return nil, errConnectionRefused
	}

	if !s.SetMQTTConnected(false) {
		t.Fatal("SetMQTTConnected(false) = false, want true")
	}

	for _, step := range []struct {
		name       string
		registered bool
		failUpdate bool
		wantMarks  int
	}{
		{name: "agent_status not registered"},
		{name: "update failed", registered: true, failUpdate: true},
		{name: "marked", registered: true, wantMarks: 1},
		{name: "already marked", registered: true},
	} {
		execution := newFakeExecution(bleemeoTypes.GlobalInfo{})
		if !step.registered {
			execution.cache.SetMetrics(nil)
		}

		execution.client.updateErr = nil
		if step.failUpdate {
			execution.client.updateErr = errAPIDown
		}

		e, err := s.PrepareExecution(t.Context(), execution)
		if err != nil {
			t.Fatal(err)
		}

		if err := e.RefreshCache(t.Context(), types.SyncTypeNormal); err != nil {
			t.Fatal(err)
		}

		err = e.SyncRemoteAndLocal(t.Context(), types.SyncTypeNormal)
		if (err != nil) != step.failUpdate {
			t.Errorf("%s: SyncRemoteAndLocal() error = %v", step.name, err)
		}

		if len(execution.client.statusUpdates) != step.wantMarks {
			t.Errorf("%s: %d agent_status updates, want %d", step.name, len(execution.client.statusUpdates), step.wantMarks)
		}
	}
}

// TestApplyWithoutState checks info works with a nil State.
func TestApplyWithoutState(t *testing.T) {
	t.Parallel()

	s := newEntity(&fakeClock{now: time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)})

	execution := newFakeExecution(bleemeoTypes.GlobalInfo{MaintenanceEnabled: true})
	execution.noState = true
	run(t, s, execution)

	if execution.maintenanceSet == nil || !*execution.maintenanceSet {
		t.Errorf("maintenance set = %v, want true", execution.maintenanceSet)
	}
}
