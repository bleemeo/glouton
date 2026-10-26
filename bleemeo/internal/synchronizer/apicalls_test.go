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
	"testing"
	"time"

	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/config"
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

// TestAPICallsBaseline runs a scripted life of an agent and checks the API calls made, method
// by method, and the entities synchronized at each step.
// It guards the migration of the entities to their sub-packages: the calls must not change.
// The steps stay less than 15 minutes apart, except the full synchronization, so the periodic
// duplicated agent check, jittered between 15 and 25 minutes, always runs at the same steps.
func TestAPICallsBaseline(t *testing.T) {
	helper := newHelper(t)
	defer helper.Close()

	helper.ReloadState = &reloadStateMock{}
	helper.ConfigItems = []config.Item{
		{Key: "agent.metrics_format", Value: "Bleemeo", Type: config.TypeString, Source: config.SourceDefault},
		{Key: "bleemeo.enable", Value: true, Type: config.TypeBool, Source: config.SourceFile, Path: "/etc/glouton/glouton.conf", Priority: 1},
	}
	helper.wrapperClientMock.resources.monitors.add(newMonitor)

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
	}{
		{
			// Includes the execution limited to config, agent and metric that follows the registration.
			name: "registration",
			wantCalls: map[string]int{
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
			name: "full synchronization",
			prepare: func() {
				helper.SetTimeToNextFullSync()

				// The next full synchronization is jittered by hours: without a recent point, the
				// metric would sometimes be deactivated.
				helper.pushPoints(t, []labels.Labels{
					labels.New(labels.Label{Name: gloutonTypes.LabelName, Value: metricCPUUsed}),
				})
			},
			// The duplicated agent check is due again, it makes the ListFacts.
			wantCalls: map[string]int{
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
				"UpdateAgent":            1,
			},
			wantSync: fullSync,
		},
		{
			name: "reload",
			prepare: func() {
				helper.initSynchronizer(t)
			},
			// The config items are synchronized again after a reload, without waiting for the next
			// full synchronization. The new Synchronizer also checks for a duplicated agent.
			wantCalls: map[string]int{"ListFacts": 1, "ListGloutonConfigItems": 1},
			wantSync: map[types.EntityName]types.SyncType{
				types.EntityInfo:          types.SyncTypeNormal,
				types.EntityContainer:     types.SyncTypeNormal,
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
		if result.err != nil {
			t.Fatalf("step %q: runOnce failed: %v", step.name, result.err)
		}

		if diff := cmp.Diff(step.wantCalls, helper.wrapperClientMock.methodCounts, cmpopts.EquateEmpty()); diff != "" {
			t.Errorf("step %q: API calls mismatch (-want +got)\n%s", step.name, diff)
		}

		if diff := cmp.Diff(step.wantSync, result.syncPerEntity, cmpopts.EquateEmpty()); diff != "" {
			t.Errorf("step %q: synchronized entities mismatch (-want +got)\n%s", step.name, diff)
		}
	}
}
