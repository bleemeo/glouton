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
	"slices"
	"testing"
	"time"

	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/inputs/vsphere"
	"github.com/bleemeo/glouton/prometheus/exporter/snmp"
	gloutonTypes "github.com/bleemeo/glouton/types"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/prometheus/prometheus/model/labels"
	"golang.org/x/oauth2"
)

// reloadStateMock keeps the full synchronization schedule across initSynchronizer, as the
// Bleemeo connector reload state does across Glouton reloads.
type reloadStateMock struct {
	nextFullSync  time.Time
	fullSyncCount int
}

func (rs *reloadStateMock) MQTTReloadState() bleemeoTypes.MQTTReloadState   { return nil }
func (rs *reloadStateMock) SetMQTTReloadState(bleemeoTypes.MQTTReloadState) {}
func (rs *reloadStateMock) NextFullSync() time.Time                         { return rs.nextFullSync }
func (rs *reloadStateMock) SetNextFullSync(t time.Time)                     { rs.nextFullSync = t }
func (rs *reloadStateMock) FullSyncCount() int                              { return rs.fullSyncCount }

func (rs *reloadStateMock) SetFullSyncCount(count int) { rs.fullSyncCount = count }
func (rs *reloadStateMock) Token() *oauth2.Token       { return nil }
func (rs *reloadStateMock) SetToken(*oauth2.Token)     {}
func (rs *reloadStateMock) Close()                     {}

// vSphereVMMock is a powered on vSphere VM.
type vSphereVMMock struct{}

func (vSphereVMMock) Kind() string             { return vsphere.KindVM }
func (vSphereVMMock) FQDN() string             { return "vm.example.com" }
func (vSphereVMMock) Source() string           { return "vcenter.example.com" }
func (vSphereVMMock) MOID() string             { return "vm-1" }
func (vSphereVMMock) Name() string             { return "vm" }
func (vSphereVMMock) Facts() map[string]string { return map[string]string{"fqdn": "vm.example.com"} }
func (vSphereVMMock) IsPoweredOn() bool        { return true }
func (vSphereVMMock) LatestError() error       { return nil }

// TestAPICallsBaseline runs a scripted life of an agent and checks the API calls made, method
// by method, and the entities synchronized at each step.
// Any change of the calls made fails it.
// The steps stay less than 15 minutes apart, except the full synchronization, so the periodic
// duplicated agent check, jittered between 15 and 25 minutes, always runs at the same steps.
func TestAPICallsBaseline(t *testing.T) { //nolint:maintidx // The script is a table of steps.
	helper := newHelper(t)
	defer helper.Close()

	helper.ReloadState = &reloadStateMock{}
	helper.ConfigItems = []config.Item{
		{Key: "agent.metrics_format", Value: "Bleemeo", Type: config.TypeString, Source: config.SourceDefault},
		{Key: "bleemeo.enable", Value: true, Type: config.TypeBool, Source: config.SourceFile, Path: "/etc/glouton/glouton.conf", Priority: 1},
	}
	helper.wrapperClientMock.resources.monitors.add(newMonitor)
	helper.SetAPIConfigs(append(slices.Clone(defaultAccountConfigs), bleemeoTypes.Config{
		ID: "cfg-vsphere-int", Type: bleemeoTypes.ConfigTypeVSphereIntegration, Account: accountID, Value: true,
	}))

	// MQTT is unreachable: no connection can be made to port 0.
	helper.cfg.Bleemeo.MQTT.Host = "127.0.0.1"
	helper.cfg.Bleemeo.MQTT.Port = 0

	// The SNMP target is offline until its step.
	snmpOnline := 0
	helper.SNMP = []*snmp.Target{
		snmp.NewMock(config.SNMPTarget{InitialName: "printer", Target: snmpAddress}, map[string]string{"fqdn": "printer.example.com"}),
	}
	helper.SNMPOnlineTarget = func() int { return snmpOnline }

	helper.initSynchronizer(t)

	helper.pushPoints(t, []labels.Labels{
		labels.New(labels.Label{Name: gloutonTypes.LabelName, Value: metricCPUUsed}),
	})

	// fullSync is what a full synchronization synchronizes: all the entities.
	fullSync := map[types.EntityName]types.SyncType{
		types.EntityInfo:               types.SyncTypeForceCacheRefresh,
		types.EntityAgent:              types.SyncTypeForceCacheRefresh,
		types.EntityConfig:             types.SyncTypeForceCacheRefresh,
		types.EntityFact:               types.SyncTypeForceCacheRefresh,
		types.EntityContainer:          types.SyncTypeForceCacheRefresh,
		types.EntitySNMP:               types.SyncTypeForceCacheRefresh,
		types.EntityVSphere:            types.SyncTypeForceCacheRefresh,
		types.EntityApplication:        types.SyncTypeForceCacheRefresh,
		types.EntityService:            types.SyncTypeForceCacheRefresh,
		types.EntityMonitor:            types.SyncTypeForceCacheRefresh,
		types.EntityMetric:             types.SyncTypeForceCacheRefresh,
		types.EntityRegistrationStatus: types.SyncTypeForceCacheRefresh,
		types.EntityGloutonConfig:      types.SyncTypeForceCacheRefresh,
		types.EntityDiagnostics:        types.SyncTypeForceCacheRefresh,
	}

	steps := []struct {
		name string
		// prepare runs before the synchronization of the step.
		prepare   func()
		wantCalls map[string]int
		wantSync  map[types.EntityName]types.SyncType
		// wantErr is whether the synchronization fails, because the connector got disabled.
		wantErr bool
	}{
		{
			// Includes the execution limited to config, agent and metric that follows the registration.
			name: "registration",
			wantCalls: map[string]int{
				"GetGlobalInfo":              1,
				"ListActiveMetrics":          2,
				"ListAgentTypes":             2,
				"ListAgents":                 2,
				"ListApplications":           1,
				"ListConfigs":                4,
				"ListContainers":             1,
				"ListDiagnostics":            1,
				"ListFacts":                  1,
				"ListGloutonConfigItems":     1,
				"ListMetricsBy":              1,
				"ListMonitors":               1,
				"ListServices":               1,
				"RegisterFact":               1,
				"RegisterGloutonConfigItems": 1,
				"RegisterMetric":             2,
				"RegisterSNMPAgent":          1,
				"UpdateAgent":                2,
			},
			wantSync: fullSync,
		},
		{
			name: "nothing changed",
		},
		{
			name: "on-demand diagnostic",
			prepare: func() {
				helper.s.ScheduleDiagnosticUpload("diagnostic.zip", "request-token", []byte("the archive"))
			},
			wantCalls: map[string]int{"ListDiagnostics": 1, "UploadDiagnostic": 1},
			wantSync:  map[types.EntityName]types.SyncType{types.EntityDiagnostics: types.SyncTypeNormal},
		},
		{
			name: "monitor change notification",
			prepare: func() {
				helper.s.UpdateMonitor("change", newMonitor.ID)
			},
			wantCalls: map[string]int{"GetMonitorByID": 1, "ListAgents": 1, "UpdateAgent": 1},
			wantSync: map[types.EntityName]types.SyncType{
				types.EntityAgent:   types.SyncTypeForceCacheRefresh,
				types.EntityMonitor: types.SyncTypeNormal,
				types.EntityMetric:  types.SyncTypeNormal,
			},
		},
		{
			name: "kubernetes agent list change",
			prepare: func() {
				helper.s.UpdateK8SAgentList()
			},
			wantCalls: map[string]int{"ListAgents": 1, "UpdateAgent": 1},
			wantSync:  map[types.EntityName]types.SyncType{types.EntityAgent: types.SyncTypeForceCacheRefresh},
		},
		{
			name: "SNMP target online",
			prepare: func() {
				snmpOnline = 1
			},
			// The SNMP agent was registered by the first full synchronization, which synchronized
			// the facts before it: its facts are registered now.
			wantCalls: map[string]int{"ListFacts": 1, "RegisterFact": 1},
			wantSync: map[types.EntityName]types.SyncType{
				types.EntityFact: types.SyncTypeNormal,
				types.EntitySNMP: types.SyncTypeNormal,
			},
		},
		{
			name: "vSphere device discovered",
			prepare: func() {
				helper.devices = []bleemeoTypes.VSphereDevice{vSphereVMMock{}}
				helper.LastVSphereChange = helper.Now()
			},
			wantCalls: map[string]int{"RegisterVSphereAgent": 1},
			wantSync:  map[types.EntityName]types.SyncType{types.EntityVSphere: types.SyncTypeNormal},
		},
		{
			name: "vSphere device removed",
			prepare: func() {
				// The vSphere agents are purged at most every 2 minutes.
				helper.AddTime(2 * time.Minute)

				helper.devices = nil
				helper.LastVSphereChange = helper.Now()
			},
			wantCalls: map[string]int{"DeleteAgent": 1},
			wantSync:  map[types.EntityName]types.SyncType{types.EntityVSphere: types.SyncTypeNormal},
		},
		{
			name: "local facts changed",
			prepare: func() {
				helper.facts.SetFact("kernel", "6.1.0")
				helper.facts.SetFact(facts.FactUpdatedAt, helper.Now().Format(time.RFC3339))
			},
			// Updating the facts forces the duplicated agent check, it makes the ListFacts.
			wantCalls: map[string]int{"ListFacts": 1, "RegisterFact": 2},
			wantSync:  map[types.EntityName]types.SyncType{types.EntityFact: types.SyncTypeNormal},
		},
		{
			name: "facts updated again",
			prepare: func() {
				helper.facts.SetFact(facts.FactUpdatedAt, helper.Now().Format(time.RFC3339))
			},
			// The API can't update a fact: the new value is registered, then the old one deleted.
			wantCalls: map[string]int{"ListFacts": 1, "RegisterFact": 1, "DeleteFact": 1},
			wantSync:  map[types.EntityName]types.SyncType{types.EntityFact: types.SyncTypeNormal},
		},
		{
			name: "facts unchanged",
			prepare: func() {
				// Only a new fact_updated_at triggers the facts synchronization.
				helper.facts.SetFact("kernel", "6.2.0")
			},
		},
		{
			name: "full synchronization",
			prepare: func() {
				helper.SetTimeToNextFullSync()

				// The next full synchronization is jittered by hours: without a recent point, the
				// metric would sometimes be deactivated.
				helper.pushPoints(t, []labels.Labels{
					labels.New(labels.Label{Name: gloutonTypes.LabelName, Value: metricCPUUsed}),
				})
			},
			// The duplicated agent check is due again, it makes the ListFacts. The kernel changed
			// without a new fact_updated_at: only a full synchronization sends it.
			wantCalls: map[string]int{
				"DeleteFact":             1,
				"GetGlobalInfo":          1,
				"ListActiveMetrics":      1,
				"ListAgentTypes":         1,
				"ListAgents":             1,
				"ListApplications":       1,
				"ListConfigs":            2,
				"ListContainers":         1,
				"ListDiagnostics":        1,
				"ListFacts":              1,
				"ListGloutonConfigItems": 1,
				"ListMonitors":           1,
				"ListServices":           1,
				"RegisterFact":           1,
				"UpdateAgent":            1,
			},
			wantSync: fullSync,
		},
		{
			name: "maintenance enabled",
			prepare: func() {
				helper.wrapperClientMock.globalInfo.MaintenanceEnabled = true

				helper.s.UpdateMaintenance(0)
			},
			wantCalls: map[string]int{"GetGlobalInfo": 1},
			wantSync:  map[types.EntityName]types.SyncType{types.EntityInfo: types.SyncTypeNormal},
		},
		{
			// MQTT is disconnected: the maintenance is polled, at most every 15 minutes.
			// The agent is skipped, it is requested again on each execution.
			name: "maintenance polled",
			prepare: func() {
				helper.s.UpdateK8SAgentList()
			},
			wantCalls: map[string]int{"GetGlobalInfo": 1},
			wantSync: map[types.EntityName]types.SyncType{
				types.EntityInfo:  types.SyncTypeNormal,
				types.EntityAgent: types.SyncTypeForceCacheRefresh,
			},
		},
		{
			name:     "maintenance not polled again",
			wantSync: map[types.EntityName]types.SyncType{types.EntityAgent: types.SyncTypeForceCacheRefresh},
		},
		{
			// Leaving the maintenance checks for a duplicated agent, it makes the ListFacts.
			// The agent was skipped before info left the maintenance: it runs on the next execution.
			name: "maintenance disabled",
			prepare: func() {
				helper.wrapperClientMock.globalInfo.MaintenanceEnabled = false

				helper.s.UpdateMaintenance(0)
			},
			wantCalls: map[string]int{"GetGlobalInfo": 1, "ListFacts": 1},
			wantSync: map[types.EntityName]types.SyncType{
				types.EntityInfo:  types.SyncTypeNormal,
				types.EntityAgent: types.SyncTypeForceCacheRefresh,
			},
		},
		{
			name:      "after maintenance",
			wantCalls: map[string]int{"ListAgents": 1, "UpdateAgent": 1},
			wantSync:  map[types.EntityName]types.SyncType{types.EntityAgent: types.SyncTypeForceCacheRefresh},
		},
		{
			// MQTT isn't reachable: agent_status is marked critical.
			name: "MQTT disconnected",
			prepare: func() {
				helper.s.SetMQTTConnected(false)
			},
			wantCalls: map[string]int{"GetGlobalInfo": 1, "UpdateMetric": 1},
			wantSync:  map[types.EntityName]types.SyncType{types.EntityInfo: types.SyncTypeNormal},
		},
		{
			name: "MQTT still disconnected",
			prepare: func() {
				helper.s.SetMQTTConnected(false)
			},
		},
		{
			// The connector is disabled, and agent_status is marked critical.
			name: "time drift",
			prepare: func() {
				helper.wrapperClientMock.globalInfo.CurrentTime = float64(helper.Now().Add(-time.Hour).Unix())
				helper.wrapperClientMock.globalInfo.MaxTimeDrift = 60

				helper.s.UpdateInfo()
			},
			wantCalls: map[string]int{"GetGlobalInfo": 1, "UpdateMetric": 1},
			wantSync:  map[types.EntityName]types.SyncType{types.EntityInfo: types.SyncTypeNormal},
			wantErr:   true,
		},
		{
			// The connector is enabled again, as Connector.UpdateInfo does. Info runs because it
			// requested itself when it disabled the connector.
			name: "time drift fixed",
			prepare: func() {
				helper.wrapperClientMock.globalInfo.CurrentTime = 0

				helper.s.ClearDisable(bleemeoTypes.DisableTimeDrift, 0)
			},
			wantCalls: map[string]int{"GetGlobalInfo": 1},
			wantSync:  map[types.EntityName]types.SyncType{types.EntityInfo: types.SyncTypeForceCacheRefresh},
		},
		{
			// An invalid minimum version is newer than any version.
			name: "agent too old",
			prepare: func() {
				helper.wrapperClientMock.globalInfo.Agents.MinVersions.Glouton = "invalid version"

				helper.s.UpdateInfo()
			},
			wantCalls: map[string]int{"GetGlobalInfo": 1},
			wantSync:  map[types.EntityName]types.SyncType{types.EntityInfo: types.SyncTypeNormal},
		},
		{
			name: "agent upgraded",
			prepare: func() {
				helper.wrapperClientMock.globalInfo.Agents.MinVersions.Glouton = ""

				helper.s.ClearDisable(bleemeoTypes.DisableAgentTooOld, 0)
			},
			wantCalls: map[string]int{"GetGlobalInfo": 1},
			wantSync:  map[types.EntityName]types.SyncType{types.EntityInfo: types.SyncTypeForceCacheRefresh},
		},
		{
			name: "reload",
			prepare: func() {
				helper.initSynchronizer(t)
			},
			// The config items are synchronized again after a reload, without waiting for the next
			// full synchronization. The new Synchronizer also checks for a duplicated agent, and
			// sees the SNMP and vSphere changes again.
			wantCalls: map[string]int{"GetGlobalInfo": 1, "ListFacts": 1, "ListGloutonConfigItems": 1},
			wantSync: map[types.EntityName]types.SyncType{
				types.EntityInfo:          types.SyncTypeNormal,
				types.EntityFact:          types.SyncTypeNormal,
				types.EntityContainer:     types.SyncTypeNormal,
				types.EntitySNMP:          types.SyncTypeNormal,
				types.EntityVSphere:       types.SyncTypeNormal,
				types.EntityApplication:   types.SyncTypeNormal,
				types.EntityService:       types.SyncTypeNormal,
				types.EntityMetric:        types.SyncTypeNormal,
				types.EntityGloutonConfig: types.SyncTypeForceCacheRefresh,
			},
		},
		{
			name: "nothing changed after reload",
		},
	}

	for i, step := range steps {
		if i > 0 {
			helper.AddTime(time.Minute)
		}

		if step.prepare != nil {
			step.prepare()
		}

		result := helper.runOnceWithResult(t)
		if (result.err != nil) != step.wantErr {
			t.Fatalf("step %q: runOnce error = %v, want error %v", step.name, result.err, step.wantErr)
		}

		if diff := cmp.Diff(step.wantCalls, helper.wrapperClientMock.methodCounts, cmpopts.EquateEmpty()); diff != "" {
			t.Errorf("step %q: API calls mismatch (-want +got)\n%s", step.name, diff)
		}

		if diff := cmp.Diff(step.wantSync, result.syncPerEntity, cmpopts.EquateEmpty()); diff != "" {
			t.Errorf("step %q: synchronized entities mismatch (-want +got)\n%s", step.name, diff)
		}
	}
}

// TestSyncInfoBeforeFirstExecution checks what info requests before the synchronization loop,
// like a synchronization of itself after disabling the connector, is kept for the loop.
func TestSyncInfoBeforeFirstExecution(t *testing.T) {
	t.Parallel()

	helper := newHelper(t)
	defer helper.Close()

	helper.preregisterAgent(t)
	// An invalid minimum version is newer than any version.
	helper.wrapperClientMock.globalInfo.Agents.MinVersions.Glouton = "invalid version"
	helper.initSynchronizer(t)

	helper.s.syncInfoBeforeFirstExecution(t.Context())

	if _, reason := helper.s.getDisabledUntil(); reason != bleemeoTypes.DisableAgentTooOld {
		t.Errorf("disable reason = %v, want %v", reason, bleemeoTypes.DisableAgentTooOld)
	}

	want := map[types.EntityName]types.SyncType{types.EntityInfo: types.SyncTypeForceCacheRefresh}
	if diff := cmp.Diff(want, syncTypeByEntity(helper.s.forceSync)); diff != "" {
		t.Errorf("requested synchronizations mismatch (-want +got)\n%s", diff)
	}
}

// TestAPICallsBeforeFirstExecution checks the API calls of the info synchronization before the
// synchronization loop, for a registered agent restarted with its cache. It runs every entity's
// PrepareExecution and FinishExecution: only info must call the API, and the other entities
// must notify nothing.
func TestAPICallsBeforeFirstExecution(t *testing.T) {
	t.Parallel()

	for _, step := range []struct {
		name string
		// prepare sets the global information of the API.
		prepare         func(info *bleemeoTypes.GlobalInfo, now time.Time)
		wantCalls       map[string]int
		wantMaintenance bool
		wantDisable     bleemeoTypes.DisableReason
	}{
		{
			name:      "startup",
			wantCalls: map[string]int{"GetGlobalInfo": 1},
		},
		{
			name: "startup in maintenance",
			prepare: func(info *bleemeoTypes.GlobalInfo, _ time.Time) {
				info.MaintenanceEnabled = true
			},
			wantCalls:       map[string]int{"GetGlobalInfo": 1},
			wantMaintenance: true,
		},
		{
			// The connector is disabled, and agent_status is marked critical.
			name: "startup with a time drift",
			prepare: func(info *bleemeoTypes.GlobalInfo, now time.Time) {
				info.CurrentTime = float64(now.Add(-time.Hour).Unix())
				info.MaxTimeDrift = 60
			},
			wantCalls:   map[string]int{"GetGlobalInfo": 1, "UpdateMetric": 1},
			wantDisable: bleemeoTypes.DisableTimeDrift,
		},
	} {
		t.Run(step.name, func(t *testing.T) {
			t.Parallel()

			helper := newHelper(t)
			defer helper.Close()

			labelsUpdates := 0
			helper.NotifyLabelsUpdate = func() { labelsUpdates++ }

			helper.preregisterAgent(t)
			helper.initSynchronizer(t)

			// A first run fills the cache, agent_status included.
			if err := helper.runOnce(t); err != nil {
				t.Fatal(err)
			}

			// The restart, with the cache.
			helper.AddTime(time.Minute)
			helper.initSynchronizer(t)

			configUpdates := 0
			helper.s.option.UpdateConfigCallback = func() { configUpdates++ }
			labelsUpdates = 0

			if step.prepare != nil {
				step.prepare(&helper.wrapperClientMock.globalInfo, helper.Now())
			}

			helper.wrapperClientMock.resetCount()
			helper.s.syncInfoBeforeFirstExecution(t.Context())

			if diff := cmp.Diff(step.wantCalls, helper.wrapperClientMock.methodCounts, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("API calls mismatch (-want +got)\n%s", diff)
			}

			if configUpdates != 0 || labelsUpdates != 0 {
				t.Errorf("notified %d config updates and %d labels updates, want none", configUpdates, labelsUpdates)
			}

			if got := helper.s.IsMaintenance(); got != step.wantMaintenance {
				t.Errorf("maintenance = %v, want %v", got, step.wantMaintenance)
			}

			if _, reason := helper.s.getDisabledUntil(); reason != step.wantDisable {
				t.Errorf("disable reason = %v, want %v", reason, step.wantDisable)
			}
		})
	}
}

// TestMaintenancePolledWhileSuspended checks the maintenance is polled in suspended mode too:
// info is requested, and stays requested until it can run.
func TestMaintenancePolledWhileSuspended(t *testing.T) {
	t.Parallel()

	helper := newHelper(t)
	defer helper.Close()

	helper.preregisterAgent(t)
	helper.initSynchronizer(t)

	if err := helper.runOnce(t); err != nil {
		t.Fatal(err)
	}

	helper.s.SetMaintenance(t.Context(), true)
	helper.s.SetSuspended(true)

	for _, name := range []string{"polled", "requested again"} {
		helper.AddTime(time.Minute)

		result := helper.runOnceWithResult(t)
		if result.err != nil {
			t.Fatalf("%s: runOnce failed: %v", name, result.err)
		}

		if got := result.syncPerEntity[types.EntityInfo]; got != types.SyncTypeNormal {
			t.Errorf("%s: info sync type = %v, want %v", name, got, types.SyncTypeNormal)
		}

		if got := helper.wrapperClientMock.methodCounts["GetGlobalInfo"]; got != 0 {
			t.Errorf("%s: GetGlobalInfo called %d times, want 0 (info is skipped)", name, got)
		}
	}
}

// TestInfoSynchronizedWithOthers checks info is synchronized when it is older than 30 minutes
// and the execution synchronizes other entities, and only then.
func TestInfoSynchronizedWithOthers(t *testing.T) {
	t.Parallel()

	helper := newHelper(t)
	defer helper.Close()

	helper.preregisterAgent(t)
	helper.initSynchronizer(t)

	if err := helper.runOnce(t); err != nil {
		t.Fatal(err)
	}

	for _, step := range []struct {
		name      string
		elapsed   time.Duration
		otherWork bool
		want      types.SyncType
	}{
		{name: "outdated, no other work", elapsed: 31 * time.Minute},
		{name: "outdated, other work", otherWork: true, want: types.SyncTypeNormal},
		{name: "recent, other work", elapsed: time.Minute, otherWork: true},
	} {
		helper.AddTime(step.elapsed)

		if step.otherWork {
			helper.s.UpdateK8SAgentList()
		}

		result := helper.runOnceWithResult(t)
		if result.err != nil {
			t.Fatalf("%s: runOnce failed: %v", step.name, result.err)
		}

		if got := result.syncPerEntity[types.EntityInfo]; got != step.want {
			t.Errorf("%s: info sync type = %v, want %v", step.name, got, step.want)
		}
	}
}
