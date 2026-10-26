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

package syncsnmp

import (
	"context"
	"errors"
	"testing"

	"github.com/bleemeo/bleemeo-go"
	"github.com/bleemeo/glouton/agent/state"
	"github.com/bleemeo/glouton/bleemeo/internal/cache"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/bleemeoapi"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/prometheus/exporter/snmp"

	"github.com/google/go-cmp/cmp"
)

const snmpTypeID = "snmp-type"

// fakeClient registers the SNMP agents with the ID "new-<display name>".
type fakeClient struct {
	registered []bleemeoapi.AgentPayload
}

func (c *fakeClient) RegisterSNMPAgent(_ context.Context, payload bleemeoapi.AgentPayload) (bleemeoTypes.Agent, error) {
	c.registered = append(c.registered, payload)

	payload.ID = "new-" + payload.DisplayName

	return payload.Agent, nil
}

// fakeExecution gives the number of SNMP targets online to the entity, and records the linked
// synchronizations it requests. Any other call panics.
type fakeExecution struct {
	types.SynchronizationExecution

	online int
	linked [][2]types.EntityName
}

func (e *fakeExecution) Option() types.Option {
	return types.Option{SNMPOnlineTarget: func() int { return e.online }}
}

func (e *fakeExecution) RequestLinkedSynchronization(target types.EntityName, trigger types.EntityName) error {
	e.linked = append(e.linked, [2]types.EntityName{target, trigger})

	return nil
}

func newTarget(name, address, fqdn string) *snmp.Target {
	return snmp.NewMock(config.SNMPTarget{InitialName: name, Target: address}, map[string]string{"fqdn": fqdn})
}

func newEntity(t *testing.T, targets ...*snmp.Target) *SyncSNMP {
	t.Helper()

	// An in-memory State, which encodes the values as the State file does.
	memoryState, err := state.LoadReadOnly("", "")
	if err != nil {
		t.Fatal(err)
	}

	return New(memoryState, targets)
}

func associate(t *testing.T, s *SyncSNMP, target *snmp.Target, agentID string) {
	t.Helper()

	if err := s.state.Set(cachePrefix+target.Address(), targetAssociation{Address: target.Address(), ID: agentID}); err != nil {
		t.Fatal(err)
	}
}

// TestFindAgent checks the agent of a target is the one saved for it, or else an SNMP agent
// with its FQDN which no other target has.
func TestFindAgent(t *testing.T) {
	t.Parallel()

	printer := newTarget("printer", "192.168.1.2", "printer.example.com")
	router := newTarget("router", "192.168.1.1", "router.example.com")

	agents := map[string]bleemeoTypes.Agent{
		"printer":        {ID: "printer", AgentType: snmpTypeID, FQDN: "renamed.example.com"},
		"router":         {ID: "router", AgentType: snmpTypeID, FQDN: "router.example.com"},
		"router-main":    {ID: "router-main", AgentType: "agent-type", FQDN: "router.example.com"},
		"router-printer": {ID: "router-printer", AgentType: snmpTypeID, FQDN: "printer.example.com"},
	}

	tests := []struct {
		name      string
		target    *snmp.Target
		associate map[*snmp.Target]string
		want      string
	}{
		{
			name:      "saved",
			target:    printer,
			associate: map[*snmp.Target]string{printer: "printer"},
			want:      "printer",
		},
		{
			name:   "same FQDN",
			target: router,
			want:   "router",
		},
		{
			name:      "saved agent deleted",
			target:    router,
			associate: map[*snmp.Target]string{router: "deleted"},
			want:      "router",
		},
		{
			name:      "same FQDN, of another target",
			target:    printer,
			associate: map[*snmp.Target]string{router: "router-printer"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			s := newEntity(t, printer, router)

			for target, agentID := range test.associate {
				associate(t, s, target, agentID)
			}

			agent, err := s.FindAgent(t.Context(), test.target, snmpTypeID, agents)
			if test.want == "" {
				if !errors.Is(err, types.ErrNotExist) {
					t.Errorf("FindAgent() = %v, %v, want %v", agent.ID, err, types.ErrNotExist)
				}

				return
			}

			if err != nil || agent.ID != test.want {
				t.Errorf("FindAgent() = %v, %v, want %v", agent.ID, err, test.want)
			}
		})
	}
}

// TestRegisterAndUpdate checks only the targets without an agent are registered, and that
// their new agent is saved and added to the cache.
func TestRegisterAndUpdate(t *testing.T) {
	t.Parallel()

	printer := newTarget("printer", "192.168.1.2", "printer.example.com")
	router := newTarget("router", "192.168.1.1", "")
	s := newEntity(t, printer, router)

	c := &cache.Cache{}
	c.SetAgentList([]bleemeoTypes.Agent{{ID: "printer", AgentType: snmpTypeID}})
	associate(t, s, printer, "printer")

	client := &fakeClient{}

	if err := s.registerAndUpdate(t.Context(), client, c, "group"); !errors.Is(err, types.ErrRetryLater) {
		t.Fatalf("registerAndUpdate() without the agent type = %v, want %v", err, types.ErrRetryLater)
	}

	c.SetAgentTypes([]bleemeoTypes.AgentType{{ID: snmpTypeID, Name: bleemeo.AgentType_SNMP}})

	if err := s.registerAndUpdate(t.Context(), client, c, "group"); err != nil {
		t.Fatal(err)
	}

	if len(client.registered) != 1 {
		t.Fatalf("registered %d agents, want 1", len(client.registered))
	}

	// The router has no FQDN, its address replaces it.
	got := client.registered[0]
	want := bleemeoapi.AgentPayload{
		FQDN:               "192.168.1.1",
		DisplayName:        "router",
		AgentType:          snmpTypeID,
		Tags:               []bleemeoTypes.Tag{},
		Abstracted:         true,
		InitialPassword:    got.InitialPassword,
		InitialServerGroup: "group",
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("registered agent mismatch (-want +got)\n%s", diff)
	}

	if _, ok := c.AgentsByUUID()["new-router"]; !ok {
		t.Error("the new agent isn't in the cache")
	}

	agent, err := s.FindAgent(t.Context(), router, snmpTypeID, c.AgentsByUUID())
	if err != nil || agent.ID != "new-router" {
		t.Errorf("FindAgent() = %v, %v, want new-router", agent.ID, err)
	}
}

// TestNeedSynchronization checks the SNMP agents are synchronized when the number of targets
// online changes, and the facts with them.
func TestNeedSynchronization(t *testing.T) {
	t.Parallel()

	s := newEntity(t)

	for _, step := range []struct {
		online int
		want   bool
	}{
		{online: 0, want: false},
		{online: 1, want: true},
		{online: 1, want: false},
		{online: 0, want: true},
	} {
		execution := &fakeExecution{online: step.online}

		e, err := s.PrepareExecution(t.Context(), execution)
		if err != nil {
			t.Fatal(err)
		}

		need, err := e.NeedSynchronization(t.Context())
		if err != nil || need != step.want {
			t.Errorf("%d online: NeedSynchronization() = %v, %v, want %v", step.online, need, err, step.want)
		}

		want := [][2]types.EntityName{{types.EntityFact, types.EntitySNMP}}
		if diff := cmp.Diff(want, execution.linked); diff != "" {
			t.Errorf("%d online: linked synchronizations mismatch (-want +got)\n%s", step.online, diff)
		}
	}
}
