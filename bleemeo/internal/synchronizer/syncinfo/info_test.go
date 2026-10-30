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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bleemeo/bleemeo-go"
	"github.com/bleemeo/glouton/bleemeo/internal/cache"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/executiontest"
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

// newTestClock returns a clock at the time the tests start.
func newTestClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)}
}

// timeDriftMark returns the update marking agent_status for the time drift.
func timeDriftMark() []agentStatusUpdatePayload {
	return []agentStatusUpdatePayload{{
		CurrentStatus:     bleemeo.Status_Critical,
		StatusDescription: []string{"Agent local time too different from actual time"},
	}}
}

var (
	errConnectionRefused = errors.New("connection refused")
	errAPIDown           = errors.New("API down")
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

	agentID string
}

func (s fakeState) BleemeoCredentials() (string, string) { return s.agentID, "password" }

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
	executiontest.Recorder

	client        *fakeClient
	cache         *cache.Cache
	appender      *model.BufferAppender
	cfg           config.Config
	startedAt     time.Time
	maintenance   bool
	mqttConnected bool
	// agentID is the agent ID of the State, empty before the registration.
	agentID string
	// noState gives a nil State.
	noState bool

	disabled []disableCall
	// maintenanceSet is the maintenance mode set by the entity, nil when it didn't.
	maintenanceSet *bool
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
		client:   &fakeClient{globalInfo: globalInfo},
		cache:    c,
		appender: model.NewBufferAppender(),
		agentID:  testAgentID,
	}
}

func (e *fakeExecution) BleemeoAPIClient() types.Client { return e.client }
func (e *fakeExecution) StartedAt() time.Time           { return e.startedAt }
func (e *fakeExecution) GlobalState() types.SynchronizedGlobalState {
	return fakeGlobalState{maintenance: e.maintenance}
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

	return fakeState{agentID: e.agentID}
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

// runWithError runs one execution of the entity, which was requested, and returns the error of
// SyncRemoteAndLocal.
func runWithError(t *testing.T, s *SyncInfo, execution *fakeExecution) error {
	t.Helper()

	e, err := s.PrepareExecution(t.Context(), execution)
	if err != nil {
		t.Fatal(err)
	}

	defer e.FinishExecution(t.Context())

	if err := e.RefreshCache(t.Context(), types.SyncTypeNormal); err != nil {
		t.Fatal(err)
	}

	return e.SyncRemoteAndLocal(t.Context(), types.SyncTypeNormal)
}

// run runs one execution of the entity, which was requested.
func run(t *testing.T, s *SyncInfo, execution *fakeExecution) {
	t.Helper()

	if err := runWithError(t, s, execution); err != nil {
		t.Fatal(err)
	}
}

// checkMaintenanceSet checks the entity set the maintenance mode to true.
func checkMaintenanceSet(t *testing.T, name string, execution *fakeExecution) {
	t.Helper()

	if execution.maintenanceSet == nil || !*execution.maintenanceSet {
		t.Errorf("%s: maintenance set = %v, want true", name, execution.maintenanceSet)
	}
}

// TestMaintenancePolling checks info is synchronized every 15 minutes while in maintenance,
// when MQTT can't tell the maintenance ended.
func TestMaintenancePolling(t *testing.T) {
	t.Parallel()

	start := newTestClock().now
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

	clock := newTestClock()
	s := newEntity(clock)

	execution := newFakeExecution(bleemeoTypes.GlobalInfo{MaintenanceEnabled: true})
	run(t, s, execution)

	checkMaintenanceSet(t, "fetched", execution)

	if got := s.LastInfo().FetchedAt; !got.Equal(clock.now) {
		t.Errorf("LastInfo().FetchedAt = %v, want %v", got, clock.now)
	}

	if s.NeedSynchronizationWithOthers() {
		t.Error("NeedSynchronizationWithOthers() = true just after a fetch, want false")
	}

	clock.now = clock.now.Add(time.Hour)

	if !s.NeedSynchronizationWithOthers() {
		t.Error("NeedSynchronizationWithOthers() = false after an hour, want true")
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

	clock := newTestClock()
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

	if diff := cmp.Diff(map[types.EntityName]bool{types.EntityInfo: true}, execution.Requested); diff != "" {
		t.Errorf("requested synchronizations mismatch (-want +got)\n%s", diff)
	}
}

// TestTimeDrift checks a large time drift disables the connector, marks agent_status critical
// once, and pushes the time_drift metric.
func TestTimeDrift(t *testing.T) {
	t.Parallel()

	clock := newTestClock()
	s := newEntity(clock)
	drifting := bleemeoTypes.GlobalInfo{
		CurrentTime:  float64(clock.now.Add(-time.Hour).Unix()),
		MaxTimeDrift: 60,
	}

	execution := newFakeExecution(drifting)
	run(t, s, execution)

	if len(execution.disabled) != 1 || execution.disabled[0].reason != bleemeoTypes.DisableTimeDrift {
		t.Errorf("disabled = %v, want once for the time drift", execution.disabled)
	}

	if diff := cmp.Diff(timeDriftMark(), execution.client.statusUpdates); diff != "" {
		t.Errorf("agent_status updates mismatch (-want +got)\n%s", diff)
	}

	mfs, err := execution.appender.AsMF()
	if err != nil {
		t.Fatal(err)
	}

	if len(mfs) != 1 || mfs[0].GetName() != "time_drift" || mfs[0].GetMetric()[0].GetUntyped().GetValue() != time.Hour.Seconds() {
		t.Errorf("pushed metrics = %v, want time_drift of an hour", mfs)
	}

	// Still drifting: the connector is disabled again, agent_status isn't updated again.
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

	if diff := cmp.Diff(map[types.EntityName]bool{types.EntityInfo: true}, execution.Requested); diff != "" {
		t.Errorf("requested synchronizations mismatch (-want +got)\n%s", diff)
	}
}

// TestMQTTStatus checks agent_status is marked critical when MQTT gets disconnected and isn't
// reachable, and only then.
func TestMQTTStatus(t *testing.T) {
	t.Parallel()

	s := newEntity(newTestClock())

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

// expiredCertificate returns a self-signed certificate for 127.0.0.1 that expired an hour ago.
func expiredCertificate(t *testing.T) tls.Certificate {
	t.Helper()

	leaf, key := newCertificate(t, &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "api"},
		NotBefore:             time.Now().Add(-48 * time.Hour),
		NotAfter:              time.Now().Add(-time.Hour),
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1)},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}, nil, nil)

	return tls.Certificate{Certificate: [][]byte{leaf.Raw}, PrivateKey: key, Leaf: leaf}
}

// newCertificate returns a certificate for template, signed by parent and parentKey, or
// self-signed when parent is nil.
func newCertificate(t *testing.T, template *x509.Certificate, parent *x509.Certificate, parentKey *ecdsa.PrivateKey) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	if parent == nil {
		parent, parentKey = template, key
	}

	der, err := x509.CreateCertificate(rand.Reader, template, parent, &key.PublicKey, parentKey)
	if err != nil {
		t.Fatal(err)
	}

	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}

	return certificate, key
}

// TestFetchExpiredCertificate checks the global information is fetched again when the
// certificate looks expired, as when the local time is far off: the certificate is then
// verified at a time when it is valid.
func TestFetchExpiredCertificate(t *testing.T) {
	t.Parallel()

	certificate := expiredCertificate(t)
	trusted := x509.NewCertPool()
	trusted.AddCert(certificate.Leaf)

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/info/" {
			http.NotFound(w, r)

			return
		}

		if r.URL.Query().Get("status") == "503" {
			w.WriteHeader(http.StatusServiceUnavailable)

			return
		}

		_, _ = w.Write([]byte(`{"maintenance": true, "current_time": 1700000000, "max_time_drift": 3600}`))
	}))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}}
	server.StartTLS()

	defer server.Close()

	// The error of the Bleemeo client for this certificate, through the transport of the
	// synchronizer.
	client, err := bleemeo.NewClient(
		bleemeo.WithCredentials("user", "password"),
		bleemeo.WithEndpoint(server.URL),
		bleemeo.WithHTTPClient(&http.Client{Transport: gloutonTypes.NewHTTPTransport(
			&tls.Config{RootCAs: trusted},
			&gloutonTypes.CustomTransportOptions{UserAgentHeader: "test", RequestCounter: &atomic.Uint32{}},
		)}),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, _, certificateErr := client.Do(t.Context(), http.MethodGet, "/v1/info/", nil, false, nil)
	if certificateErr == nil {
		t.Fatal("the Bleemeo client accepted the expired certificate")
	}

	for _, step := range []struct {
		name    string
		apiBase string
		rootCAs *x509.CertPool
		applied bool
	}{
		{name: "trusted certificate", apiBase: server.URL, rootCAs: trusted, applied: true},
		{name: "untrusted certificate", apiBase: server.URL, rootCAs: x509.NewCertPool()},
		{name: "other host", apiBase: strings.Replace(server.URL, "127.0.0.1", "localhost", 1), rootCAs: trusted},
		{name: "failed request", apiBase: server.URL + "?status=503", rootCAs: trusted},
	} {
		clock := newTestClock()
		s := newEntity(clock)
		s.rootCAs = step.rootCAs

		execution := newFakeExecution(bleemeoTypes.GlobalInfo{})
		execution.client.err = certificateErr
		execution.cfg.Bleemeo.APIBase = step.apiBase
		run(t, s, execution)

		if !step.applied {
			if execution.maintenanceSet != nil {
				t.Errorf("%s: maintenance set to %v, want nothing applied", step.name, *execution.maintenanceSet)
			}

			if !s.LastInfo().FetchedAt.IsZero() {
				t.Errorf("%s: LastInfo() = %v, want nothing applied", step.name, s.LastInfo())
			}

			continue
		}

		checkMaintenanceSet(t, step.name, execution)

		want := bleemeoTypes.GlobalInfo{MaintenanceEnabled: true, CurrentTime: 1700000000, MaxTimeDrift: 3600, FetchedAt: clock.now}
		if diff := cmp.Diff(want, s.LastInfo()); diff != "" {
			t.Errorf("%s: LastInfo() mismatch (-want +got)\n%s", step.name, diff)
		}

		if got := s.requestCounter.Load(); got != 1 {
			t.Errorf("%s: requests counted = %d, want 1", step.name, got)
		}
	}
}

// TestVerifyIgnoringTimeChain checks a chain is verified at a time when the server's
// certificate is valid, even when the server also sends a certificate expired by then, like a
// stale cross-signature of its root.
func TestVerifyIgnoringTimeChain(t *testing.T) {
	t.Parallel()

	now := time.Now()
	caTemplate := func(name string, notBefore, notAfter time.Time) *x509.Certificate {
		return &x509.Certificate{
			SerialNumber:          big.NewInt(1),
			Subject:               pkix.Name{CommonName: name},
			NotBefore:             notBefore,
			NotAfter:              notAfter,
			KeyUsage:              x509.KeyUsageCertSign,
			BasicConstraintsValid: true,
			IsCA:                  true,
		}
	}

	root, rootKey := newCertificate(t, caTemplate("root", now.AddDate(-5, 0, 0), now.AddDate(10, 0, 0)), nil, nil)
	oldRoot, oldRootKey := newCertificate(t, caTemplate("old root", now.AddDate(-20, 0, 0), now.AddDate(-1, 0, 0)), nil, nil)
	intermediate, intermediateKey := newCertificate(t, caTemplate("intermediate", now.AddDate(-4, 0, 0), now.AddDate(5, 0, 0)), root, rootKey)

	// The root, signed by the old root, expired a year ago. Its key isn't used.
	crossSigned, _ := newCertificate(t, caTemplate("root", now.AddDate(-6, 0, 0), now.AddDate(-1, 0, 0)), oldRoot, oldRootKey)

	// The certificate of the server looks expired at the local time.
	leaf, _ := newCertificate(t, &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "api"},
		DNSNames:     []string{"api.example.com"},
		NotBefore:    now.AddDate(0, 0, -100),
		NotAfter:     now.AddDate(0, 0, -10),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}, intermediate, intermediateKey)

	trusted := x509.NewCertPool()
	trusted.AddCert(root)

	for _, step := range []struct {
		name    string
		chain   []*x509.Certificate
		rootCAs *x509.CertPool
		wantErr bool
	}{
		{name: "chain", chain: []*x509.Certificate{leaf, intermediate}, rootCAs: trusted},
		{name: "stale cross-signature", chain: []*x509.Certificate{leaf, intermediate, crossSigned}, rootCAs: trusted},
		{name: "untrusted root", chain: []*x509.Certificate{leaf, intermediate, crossSigned}, rootCAs: x509.NewCertPool(), wantErr: true},
		{name: "missing intermediate", chain: []*x509.Certificate{leaf}, rootCAs: trusted, wantErr: true},
	} {
		err := verifyIgnoringTime(step.chain, "api.example.com", step.rootCAs)
		if (err != nil) != step.wantErr {
			t.Errorf("%s: verifyIgnoringTime() = %v, want error %v", step.name, err, step.wantErr)
		}
	}
}

// TestTimeDriftNewAgent checks a new agent with a large time drift marks agent_status once it
// is registered: before, there is no agent_status to mark, and the connector stays enabled so
// that the agent can register it.
func TestTimeDriftNewAgent(t *testing.T) {
	t.Parallel()

	clock := newTestClock()
	s := newEntity(clock)
	drifting := bleemeoTypes.GlobalInfo{
		CurrentTime:  float64(clock.now.Add(-time.Hour).Unix()),
		MaxTimeDrift: 60,
	}

	execution := newFakeExecution(drifting)
	execution.agentID = ""
	execution.cache.SetMetrics(nil)
	run(t, s, execution)

	if len(execution.disabled) != 0 {
		t.Errorf("disabled = %v, want not disabled before the registration", execution.disabled)
	}

	for _, step := range []struct {
		name       string
		timeOffset time.Duration
		want       []agentStatusUpdatePayload
	}{
		{name: "registered", timeOffset: -time.Hour, want: timeDriftMark()},
		{name: "still drifting", timeOffset: -time.Hour},
		{name: "time fixed"},
		{name: "drifting again", timeOffset: -time.Hour, want: timeDriftMark()},
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
// until agent_status is marked, and waits for agent_status to be registered. A failed update of
// agent_status doesn't stop the rest of info.
func TestMQTTStatusRetried(t *testing.T) {
	t.Parallel()

	clock := newTestClock()
	s := newEntity(clock)
	dials := 0
	s.dial = func(context.Context, string, string) (net.Conn, error) {
		dials++

		return nil, errConnectionRefused
	}

	if !s.SetMQTTConnected(false) {
		t.Fatal("SetMQTTConnected(false) = false, want true")
	}

	for _, step := range []struct {
		name       string
		registered bool
		failUpdate bool
		wantDials  int
		wantMarks  int
	}{
		{name: "agent_status not registered"},
		{name: "update failed", registered: true, failUpdate: true, wantDials: 1},
		{name: "marked", registered: true, wantDials: 1, wantMarks: 1},
		{name: "already marked", registered: true},
	} {
		clock.now = clock.now.Add(time.Minute)
		dials = 0

		execution := newFakeExecution(bleemeoTypes.GlobalInfo{MaintenanceEnabled: true})
		if !step.registered {
			execution.cache.SetMetrics(nil)
		}

		if step.failUpdate {
			execution.client.updateErr = errAPIDown
		}

		if err := runWithError(t, s, execution); (err != nil) != step.failUpdate {
			t.Errorf("%s: SyncRemoteAndLocal() error = %v", step.name, err)
		}

		if dials != step.wantDials {
			t.Errorf("%s: %d MQTT checks, want %d", step.name, dials, step.wantDials)
		}

		if len(execution.client.statusUpdates) != step.wantMarks {
			t.Errorf("%s: %d agent_status updates, want %d", step.name, len(execution.client.statusUpdates), step.wantMarks)
		}

		checkMaintenanceSet(t, step.name, execution)

		if got := s.LastInfo().FetchedAt; !got.Equal(clock.now) {
			t.Errorf("%s: LastInfo().FetchedAt = %v, want %v", step.name, got, clock.now)
		}
	}
}

// TestMQTTDisconnectedDuringCheck checks a disconnection reported while MQTT is being checked
// gets its own check.
func TestMQTTDisconnectedDuringCheck(t *testing.T) {
	t.Parallel()

	s := newEntity(newTestClock())
	s.SetMQTTConnected(false)

	// MQTT reconnects and gets disconnected again while the first check runs, which finds it
	// reachable.
	s.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		s.SetMQTTConnected(true)
		s.SetMQTTConnected(false)

		return dialReachable(ctx, network, address)
	}

	run(t, s, newFakeExecution(bleemeoTypes.GlobalInfo{}))

	s.dial = func(context.Context, string, string) (net.Conn, error) {
		return nil, errConnectionRefused
	}

	execution := newFakeExecution(bleemeoTypes.GlobalInfo{})
	run(t, s, execution)

	if len(execution.client.statusUpdates) != 1 {
		t.Errorf("%d agent_status updates after the second disconnection, want 1", len(execution.client.statusUpdates))
	}
}

// TestApplyWithoutState checks info works with a nil State.
func TestApplyWithoutState(t *testing.T) {
	t.Parallel()

	s := newEntity(newTestClock())

	execution := newFakeExecution(bleemeoTypes.GlobalInfo{MaintenanceEnabled: true})
	execution.noState = true
	run(t, s, execution)

	checkMaintenanceSet(t, "without State", execution)
}
