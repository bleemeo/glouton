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

// Package syncagent updates the agent on Bleemeo, and keeps the list of the agents of the
// account.
package syncagent

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"

	"github.com/bleemeo/glouton/bleemeo/internal/cache"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/bleemeoapi"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/logger"
	gloutonTypes "github.com/bleemeo/glouton/types"
)

// ErrAgentListFirst fails the SNMP and vSphere synchronizations when the agents of the account
// couldn't be listed.
var ErrAgentListFirst = errors.New("the agents need to be listed before registering SNMP and vSphere agents")

// SyncAgent updates the tags of the agent, and lists the agents of the account on full
// synchronizations.
type SyncAgent struct {
	// warnAccountMismatchDone is set once the account ID mismatch was logged.
	// DiagnosticArchive reads it from another goroutine.
	warnAccountMismatchDone atomic.Bool
}

// New returns the entity.
func New() *SyncAgent {
	return &SyncAgent{}
}

func (s *SyncAgent) Name() types.EntityName {
	return types.EntityAgent
}

func (s *SyncAgent) EnabledInMaintenance() bool {
	return false
}

func (s *SyncAgent) EnabledInSuspendedMode() bool {
	return true
}

func (s *SyncAgent) PrepareExecution(_ context.Context, execution types.SynchronizationExecution) (types.EntitySynchronizerExecution, error) {
	return &syncAgentExecution{
		parent:    s,
		execution: execution,
	}, nil
}

func (s *SyncAgent) DiagnosticArchive(_ context.Context, archive gloutonTypes.ArchiveWriter) error {
	file, err := archive.Create("bleemeo-sync-agent.json")
	if err != nil {
		return err
	}

	obj := struct {
		WarnAccountMismatchDone bool
	}{
		WarnAccountMismatchDone: s.warnAccountMismatchDone.Load(),
	}

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")

	return enc.Encode(obj)
}

type syncAgentExecution struct {
	parent    *SyncAgent
	execution types.SynchronizationExecution
}

// NeedSynchronization returns false: the agent is synchronized on request, and on full
// synchronizations.
func (e *syncAgentExecution) NeedSynchronization(_ context.Context) (bool, error) {
	return false, nil
}

// RefreshCache lists the agents of the account, on a full synchronization.
func (e *syncAgentExecution) RefreshCache(ctx context.Context, syncType types.SyncType) error {
	if syncType != types.SyncTypeForceCacheRefresh || e.execution.IsOnlyEssential() {
		return nil
	}

	deleted, err := updateAgentList(ctx, e.execution.BleemeoAPIClient(), e.execution.Option().Cache)
	if err != nil {
		// SNMP and vSphere look for the agents of their targets in this list, and register
		// an agent for each target they don't find there. With a stale list, they would
		// register a duplicate of an agent the API already has.
		e.execution.FailOtherEntity(types.EntitySNMP, ErrAgentListFirst)
		e.execution.FailOtherEntity(types.EntityVSphere, ErrAgentListFirst)

		return err
	}

	// If an agent is deleted, ensure our Labels on metric are up-to-date.
	// If an SNMP agent is deleted, its agent UUID is no longer valid and metric
	// should no longer be labeled with it.
	if deleted {
		e.execution.RequestNotifyLabelsUpdate()
	}

	return nil
}

func (e *syncAgentExecution) SyncRemoteAndLocal(ctx context.Context, _ types.SyncType) error {
	option := e.execution.Option()
	agentID, _ := option.State.BleemeoCredentials()

	return e.parent.updateMainAgent(ctx, e.execution.BleemeoAPIClient(), option.Cache, agentID, option.Config.Tags, option.Config.Bleemeo.AccountID)
}

func (e *syncAgentExecution) FinishExecution(_ context.Context) {
}

// updateMainAgent sets the tags of the agent agentID, and updates the agent in the cache.
// configAccountID is the account ID of the configuration, only used to warn when the agent is
// in another account.
func (s *SyncAgent) updateMainAgent(ctx context.Context, apiClient types.AgentClient, c *cache.Cache, agentID string, tags []string, configAccountID string) error {
	data := map[string][]bleemeoTypes.Tag{
		"tags": make([]bleemeoTypes.Tag, 0),
	}

	for _, t := range tags {
		if len(t) <= bleemeoapi.APITagsLength && t != "" {
			data["tags"] = append(data["tags"], bleemeoTypes.Tag{Name: t})
		}
	}

	previousAgent := c.Agent()

	agent, err := apiClient.UpdateAgent(ctx, agentID, data)
	if err != nil {
		return err
	}

	c.SetAgent(agent)

	c.SetAccountID(agent.AccountID)

	if agent.AccountID != configAccountID && !s.warnAccountMismatchDone.Load() {
		s.warnAccountMismatchDone.Store(true)
		logger.Printf(
			"Account ID in configuration file (%s) mismatch the current account ID (%s). The Account ID from configuration file will be ignored.",
			configAccountID,
			agent.AccountID,
		)
	}

	if agent.IsClusterLeader && !previousAgent.IsClusterLeader {
		logger.V(1).Printf("This agent is the Kubernetes cluster leader")
	}

	if !agent.IsClusterLeader && previousAgent.IsClusterLeader {
		logger.V(1).Printf("This agent is no longer the Kubernetes cluster leader")
	}

	return nil
}

// updateAgentList replaces the agents in the cache with the agents of the account, and returns
// whether some agents were deleted.
func updateAgentList(ctx context.Context, apiClient types.AgentClient, c *cache.Cache) (deleted bool, err error) {
	oldAgents := c.AgentsByUUID()

	agents, err := apiClient.ListAgents(ctx)
	if err != nil {
		return false, err
	}

	c.SetAgentList(agents)

	newAgents := c.AgentsByUUID()
	for id := range oldAgents {
		if _, ok := newAgents[id]; !ok {
			return true, nil
		}
	}

	return false, nil
}
