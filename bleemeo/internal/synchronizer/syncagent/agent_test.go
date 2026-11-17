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

package syncagent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bleemeo/glouton/bleemeo/internal/cache"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/config"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

const (
	testAgentID   = "agent-id"
	testAccountID = "account-id"
)

var errList = errors.New("list failed")

// fakeClient holds the agents of the API. Any call other than UpdateAgent and ListAgents
// panics.
type fakeClient struct {
	types.Client

	agents     []bleemeoTypes.Agent
	updates    []any
	listCalled int
	listErr    error
}

func (c *fakeClient) UpdateAgent(_ context.Context, id string, data any) (bleemeoTypes.Agent, error) {
	c.updates = append(c.updates, data)

	return bleemeoTypes.Agent{ID: id, AccountID: testAccountID}, nil
}

func (c *fakeClient) ListAgents(context.Context) ([]bleemeoTypes.Agent, error) {
	c.listCalled++

	if c.listErr != nil {
		return nil, c.listErr
	}

	return c.agents, nil
}

type fakeState struct {
	bleemeoTypes.State
}

func (fakeState) BleemeoCredentials() (string, string) { return testAgentID, "password" }

// fakeExecution gives the client, the cache and the configuration to the entity, and records
// the labels updates it requests and the entities it fails. Any other call panics.
type fakeExecution struct {
	types.SynchronizationExecution

	client          *fakeClient
	cache           *cache.Cache
	cfg             config.Config
	onlyEssential   bool
	labelsRequested int
	failed          map[types.EntityName]error
}

func (e *fakeExecution) BleemeoAPIClient() types.Client { return e.client }
func (e *fakeExecution) IsOnlyEssential() bool          { return e.onlyEssential }
func (e *fakeExecution) RequestNotifyLabelsUpdate()     { e.labelsRequested++ }

func (e *fakeExecution) FailOtherEntity(entityName types.EntityName, reason error) {
	if e.failed == nil {
		e.failed = make(map[types.EntityName]error)
	}

	e.failed[entityName] = reason
}

func (e *fakeExecution) Option() types.Option {
	return types.Option{Cache: e.cache, State: fakeState{}, Config: e.cfg}
}

func run(t *testing.T, s *SyncAgent, execution *fakeExecution, syncType types.SyncType) {
	t.Helper()

	e, err := s.PrepareExecution(t.Context(), execution)
	if err != nil {
		t.Fatal(err)
	}

	defer e.FinishExecution(t.Context())

	if err := e.RefreshCache(t.Context(), syncType); err != nil {
		t.Fatal(err)
	}

	if err := e.SyncRemoteAndLocal(t.Context(), syncType); err != nil {
		t.Fatal(err)
	}
}

// TestUpdateMainAgent checks the tags the API accepts are sent, and the agent and its account
// are cached.
func TestUpdateMainAgent(t *testing.T) {
	t.Parallel()

	execution := &fakeExecution{
		client: &fakeClient{},
		cache:  &cache.Cache{},
		cfg:    config.Config{Tags: []string{"web", "", strings.Repeat("a", 101)}},
	}

	run(t, New(), execution, types.SyncTypeNormal)

	want := []any{map[string][]bleemeoTypes.Tag{"tags": {{Name: "web"}}}}
	if diff := cmp.Diff(want, execution.client.updates); diff != "" {
		t.Errorf("updates mismatch (-want +got)\n%s", diff)
	}

	if agent := execution.cache.Agent(); agent.ID != testAgentID {
		t.Errorf("cached agent = %q, want %q", agent.ID, testAgentID)
	}

	if accountID := execution.cache.AccountID(); accountID != testAccountID {
		t.Errorf("cached account = %q, want %q", accountID, testAccountID)
	}
}

// TestAgentList checks the agents are listed on full synchronizations only, and that the labels
// are updated once when agents were deleted.
func TestAgentList(t *testing.T) {
	t.Parallel()

	s := New()
	c := &cache.Cache{}
	client := &fakeClient{agents: []bleemeoTypes.Agent{{ID: testAgentID}, {ID: "snmp-1"}, {ID: "snmp-2"}}}

	for _, step := range []struct {
		name          string
		syncType      types.SyncType
		onlyEssential bool
		// agents, when not nil, replaces the agents of the API before the step.
		agents     []bleemeoTypes.Agent
		wantList   int
		wantLabels int
	}{
		{name: "normal", syncType: types.SyncTypeNormal},
		{name: "full, only essential", syncType: types.SyncTypeForceCacheRefresh, onlyEssential: true},
		{name: "full", syncType: types.SyncTypeForceCacheRefresh, wantList: 1},
		{name: "unchanged", syncType: types.SyncTypeForceCacheRefresh, wantList: 1},
		{
			name:       "agents deleted",
			syncType:   types.SyncTypeForceCacheRefresh,
			agents:     []bleemeoTypes.Agent{{ID: testAgentID}},
			wantList:   1,
			wantLabels: 1,
		},
	} {
		if step.agents != nil {
			client.agents = step.agents
		}

		client.listCalled = 0
		execution := &fakeExecution{client: client, cache: c, onlyEssential: step.onlyEssential}

		run(t, s, execution, step.syncType)

		if client.listCalled != step.wantList || execution.labelsRequested != step.wantLabels {
			t.Errorf(
				"%s: listed %d times, labels updates %d, want %d, %d",
				step.name, client.listCalled, execution.labelsRequested, step.wantList, step.wantLabels,
			)
		}

		if step.wantList != 0 && len(c.Agents()) != len(client.agents) {
			t.Errorf("%s: %d agents in the cache, want %d", step.name, len(c.Agents()), len(client.agents))
		}
	}
}

// TestAgentListFailure checks a failure to list the agents fails SNMP and vSphere, which would
// otherwise register duplicates of the agents missing from the cache.
func TestAgentListFailure(t *testing.T) {
	t.Parallel()

	execution := &fakeExecution{client: &fakeClient{listErr: errList}, cache: &cache.Cache{}}

	e, err := New().PrepareExecution(t.Context(), execution)
	if err != nil {
		t.Fatal(err)
	}

	if err := e.RefreshCache(t.Context(), types.SyncTypeForceCacheRefresh); !errors.Is(err, errList) {
		t.Errorf("RefreshCache() = %v, want %v", err, errList)
	}

	want := map[types.EntityName]error{
		types.EntitySNMP:    ErrAgentListFirst,
		types.EntityVSphere: ErrAgentListFirst,
	}
	if diff := cmp.Diff(want, execution.failed, cmpopts.EquateErrors()); diff != "" {
		t.Errorf("failed entities mismatch (-want +got)\n%s", diff)
	}
}

// TestAccountMismatch checks an agent in another account than the configured one is warned
// about, once.
func TestAccountMismatch(t *testing.T) {
	t.Parallel()

	s := New()

	for _, configAccountID := range []string{testAccountID, "other-account"} {
		if err := s.updateMainAgent(t.Context(), &fakeClient{}, &cache.Cache{}, testAgentID, nil, configAccountID); err != nil {
			t.Fatal(err)
		}

		if got, want := s.warnAccountMismatchDone.Load(), configAccountID != testAccountID; got != want {
			t.Errorf("configured account %q: warned = %v, want %v", configAccountID, got, want)
		}
	}
}
