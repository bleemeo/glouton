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

package syncvsphere

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/bleemeo/bleemeo-go"
	"github.com/bleemeo/glouton/agent/state"
	"github.com/bleemeo/glouton/bleemeo/internal/cache"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/bleemeoapi"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/inputs/vsphere"

	"github.com/google/go-cmp/cmp"
)

const vmTypeID = "vm-type"

var t0 = time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC) //nolint:gochecknoglobals

var errRegister = errors.New("register failed")

// fakeClient registers the agents with the ID of their FQDN, or fails with err, and records the
// deleted agents. Any other call panics.
type fakeClient struct {
	types.Client

	registered []string
	deleted    []string
	err        error
}

func (c *fakeClient) RegisterVSphereAgent(_ context.Context, payload bleemeoapi.AgentPayload) (bleemeoTypes.Agent, error) {
	if c.err != nil {
		return bleemeoTypes.Agent{}, c.err
	}

	c.registered = append(c.registered, payload.FQDN)

	payload.ID = payload.FQDN

	return payload.Agent, nil
}

func (c *fakeClient) DeleteAgent(_ context.Context, id string) error {
	c.deleted = append(c.deleted, id)

	return nil
}

// fakeExecution gives the date of the last change of the devices, the client and the cache to
// the entity. Any other call panics.
type fakeExecution struct {
	types.SynchronizationExecution

	lastChange time.Time
	client     *fakeClient
	cache      *cache.Cache
}

func (e *fakeExecution) BleemeoAPIClient() types.Client { return e.client }
func (e *fakeExecution) IsOnlyEssential() bool          { return false }

func (e *fakeExecution) Option() types.Option {
	return types.Option{
		LastVSphereChange:       func(context.Context) time.Time { return e.lastChange },
		VSphereEndpointsInError: func() map[string]bool { return nil },
		Cache:                   e.cache,
	}
}

// vm is a vSphere VM of the endpoint source, whose FQDN is its MOID.
type vm struct {
	moid   string
	source string
}

func (d vm) Kind() string             { return vsphere.KindVM }
func (d vm) FQDN() string             { return d.moid }
func (d vm) Source() string           { return d.source }
func (d vm) MOID() string             { return d.moid }
func (d vm) Name() string             { return d.moid }
func (d vm) Facts() map[string]string { return nil }
func (d vm) IsPoweredOn() bool        { return true }
func (d vm) LatestError() error       { return nil }

type testEntity struct {
	*SyncVSphere

	cache  *cache.Cache
	client *fakeClient
	// localDevices and clock are what the entity's devices and now functions return.
	localDevices []bleemeoTypes.VSphereDevice
	clock        time.Time
}

// newEntity returns the entity, with the vSphere agent types in the cache, and the main agent
// and agents in the agent list.
func newEntity(t *testing.T, agents ...bleemeoTypes.Agent) *testEntity {
	t.Helper()

	// An in-memory State, which encodes the values as the State file does.
	memoryState, err := state.LoadReadOnly("", "")
	if err != nil {
		t.Fatal(err)
	}

	e := &testEntity{cache: &cache.Cache{}, client: &fakeClient{}, clock: t0}
	e.SyncVSphere = New(
		memoryState,
		func(context.Context, time.Duration) []bleemeoTypes.VSphereDevice { return e.localDevices },
		func() time.Time { return e.clock },
	)

	e.cache.SetAgentTypes([]bleemeoTypes.AgentType{
		{ID: "agent-type", Name: bleemeo.AgentType_Agent},
		{ID: "cluster-type", Name: bleemeo.AgentType_vSphereCluster},
		{ID: "host-type", Name: bleemeo.AgentType_vSphereHost},
		{ID: vmTypeID, Name: bleemeo.AgentType_vSphereVM},
	})
	e.cache.SetAgentList(append([]bleemeoTypes.Agent{{ID: "main", AgentType: "agent-type"}}, agents...))

	return e
}

// associate saves the agent of the device, as registering it does.
func (e *testEntity) associate(t *testing.T, device vm, agentID string) {
	t.Helper()

	err := e.state.Set(cachePrefix+deviceAssoKey(device), deviceAssociation{MOID: device.moid, Source: device.source, ID: agentID})
	if err != nil {
		t.Fatal(err)
	}
}

func (e *testEntity) sync(t *testing.T, failingEndpoints map[string]bool) {
	t.Helper()

	err := e.registerAndUpdate(t.Context(), e.client, e.cache, e.localDevices, failingEndpoints, "")
	if err != nil {
		t.Fatal(err)
	}
}

func (e *testEntity) agentIDs() []string {
	agents := e.cache.Agents()
	ids := make([]string, 0, len(agents))

	for _, agent := range agents {
		ids = append(ids, agent.ID)
	}

	slices.Sort(ids)

	return ids
}

// TestPurge checks which agents of devices gone are deleted.
func TestPurge(t *testing.T) {
	t.Parallel()

	kept := vm{moid: "kept", source: "vcenter-1"}
	gone := vm{moid: "gone", source: "vcenter-1"}

	tests := []struct {
		name string
		// unassociated, when true, doesn't save the agent of the device gone, as when the State
		// was lost.
		unassociated     bool
		failingEndpoints map[string]bool
		wantDeleted      []string
	}{
		{
			name:        "device gone",
			wantDeleted: []string{"gone"},
		},
		{
			name:             "endpoint of the device failing",
			failingEndpoints: map[string]bool{"vcenter-1": true},
		},
		{
			name:             "another endpoint failing",
			failingEndpoints: map[string]bool{"vcenter-2": true},
			wantDeleted:      []string{"gone"},
		},
		{
			name:         "unassociated device gone",
			unassociated: true,
			wantDeleted:  []string{"gone"},
		},
		{
			// The agent may be of a device of the failing endpoint.
			name:             "unassociated, an endpoint failing",
			unassociated:     true,
			failingEndpoints: map[string]bool{"vcenter-2": true},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			e := newEntity(t, bleemeoTypes.Agent{ID: "kept", AgentType: vmTypeID}, bleemeoTypes.Agent{ID: "gone", AgentType: vmTypeID})
			e.localDevices = []bleemeoTypes.VSphereDevice{kept}
			e.associate(t, kept, "kept")

			if !test.unassociated {
				e.associate(t, gone, "gone")
			}

			e.sync(t, test.failingEndpoints)

			if len(e.client.registered) != 0 {
				t.Errorf("registered %v, want none", e.client.registered)
			}

			if diff := cmp.Diff(test.wantDeleted, e.client.deleted); diff != "" {
				t.Errorf("deleted agents mismatch (-want +got)\n%s", diff)
			}

			wantAgents := []string{"kept", "main"}
			if len(test.wantDeleted) == 0 {
				wantAgents = []string{"gone", "kept", "main"}
			}

			if diff := cmp.Diff(wantAgents, e.agentIDs()); diff != "" {
				t.Errorf("agents in the cache mismatch (-want +got)\n%s", diff)
			}

			_, err := e.FindAgent(t.Context(), gone, vmTypeID, e.cache.AgentsByUUID())
			if wantFound := len(test.wantDeleted) == 0 && !test.unassociated; (err == nil) != wantFound {
				t.Errorf("FindAgent(gone) error = %v, want found = %v", err, wantFound)
			}
		})
	}
}

// TestPurgeInterval checks the agents are purged at most every 2 minutes, while the new
// devices are registered on each synchronization.
func TestPurgeInterval(t *testing.T) {
	t.Parallel()

	first := vm{moid: "first", source: "vcenter-1"}
	second := vm{moid: "second", source: "vcenter-1"}

	e := newEntity(t)
	e.localDevices = []bleemeoTypes.VSphereDevice{first}
	e.sync(t, nil)

	// The first device goes, the second comes: it's registered, but the purge waits.
	e.clock = t0.Add(time.Minute)
	e.localDevices = []bleemeoTypes.VSphereDevice{second}
	e.sync(t, nil)

	if diff := cmp.Diff([]string{"first", "second"}, e.client.registered); diff != "" {
		t.Errorf("registered agents mismatch (-want +got)\n%s", diff)
	}

	if len(e.client.deleted) != 0 {
		t.Fatalf("deleted %v before the purge interval", e.client.deleted)
	}

	e.clock = t0.Add(2*time.Minute + time.Second)
	e.sync(t, nil)

	if diff := cmp.Diff([]string{"first"}, e.client.deleted); diff != "" {
		t.Errorf("deleted agents mismatch (-want +got)\n%s", diff)
	}
}

// TestNeedSynchronization checks the vSphere agents are synchronized when the devices change,
// and that a failed synchronization is retried.
func TestNeedSynchronization(t *testing.T) {
	t.Parallel()

	const accountID = "account-id"

	e := newEntity(t)
	e.localDevices = []bleemeoTypes.VSphereDevice{vm{moid: "web", source: "vcenter-1"}}

	// The account has the vSphere integration, which needs a configuration of a vSphere agent.
	e.cache.SetAccountID(accountID)
	e.cache.SetConfigs([]bleemeoTypes.Config{
		{Type: bleemeoTypes.ConfigTypeVSphereIntegration, Account: accountID, Value: true},
		{Type: bleemeoTypes.ConfigTypeAgentMetricsResolution, Account: accountID, AgentType: "agent-type", Value: float64(10)},
		{Type: bleemeoTypes.ConfigTypeAgentMetricsResolution, Account: accountID, AgentType: vmTypeID, Value: float64(60)},
	})

	for _, step := range []struct {
		name       string
		lastChange time.Time
		failing    bool
		want       bool
	}{
		{name: "no change", lastChange: time.Time{}, want: false},
		{name: "change, registration failing", lastChange: t0, failing: true, want: true},
		{name: "retry", lastChange: t0, want: true},
		{name: "unchanged", lastChange: t0, want: false},
		{name: "new change", lastChange: t0.Add(time.Minute), want: true},
	} {
		e.client.err = nil
		if step.failing {
			e.client.err = errRegister
		}

		execution, err := e.PrepareExecution(t.Context(), &fakeExecution{lastChange: step.lastChange, client: e.client, cache: e.cache})
		if err != nil {
			t.Fatal(err)
		}

		need, err := execution.NeedSynchronization(t.Context())
		if err != nil || need != step.want {
			t.Errorf("%s: NeedSynchronization() = %v, %v, want %v", step.name, need, err, step.want)
		}

		if !need {
			continue
		}

		if err := execution.SyncRemoteAndLocal(t.Context(), types.SyncTypeNormal); (err != nil) != step.failing {
			t.Errorf("%s: SyncRemoteAndLocal() = %v, want failure %v", step.name, err, step.failing)
		}
	}

	if diff := cmp.Diff([]string{"web"}, e.client.registered); diff != "" {
		t.Errorf("registered agents mismatch (-want +got)\n%s", diff)
	}
}
