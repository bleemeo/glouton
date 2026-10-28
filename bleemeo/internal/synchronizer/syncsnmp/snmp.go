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

// Package syncsnmp registers the SNMP targets on Bleemeo, as agents.
package syncsnmp

import (
	"context"
	"errors"
	"time"

	"github.com/bleemeo/bleemeo-go"
	"github.com/bleemeo/glouton/bleemeo/internal/cache"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/bleemeoapi"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/prometheus/exporter/snmp"

	"github.com/google/uuid"
)

const cachePrefix = "bleemeo:snmp:"

// TODO the deletion need to be done

type targetAssociation struct {
	Address string
	ID      string
}

// SyncSNMP registers an agent for each SNMP target.
type SyncSNMP struct {
	state   bleemeoTypes.State
	targets []*snmp.Target
	// lastCount is the number of SNMP targets online on the last execution.
	lastCount int
}

// New returns the entity. state keeps the agent of each target.
func New(state bleemeoTypes.State, targets []*snmp.Target) *SyncSNMP {
	return &SyncSNMP{state: state, targets: targets}
}

func (s *SyncSNMP) Name() types.EntityName {
	return types.EntitySNMP
}

func (s *SyncSNMP) EnabledInMaintenance() bool {
	return false
}

func (s *SyncSNMP) EnabledInSuspendedMode() bool {
	return false
}

func (s *SyncSNMP) PrepareExecution(_ context.Context, execution types.SynchronizationExecution) (types.EntitySynchronizerExecution, error) {
	return &syncSNMPExecution{
		parent:    s,
		execution: execution,
	}, nil
}

type syncSNMPExecution struct {
	parent    *SyncSNMP
	execution types.SynchronizationExecution
}

// NeedSynchronization synchronizes the SNMP agents when the number of SNMP targets online
// changes. The facts entity synchronizes their facts with them.
func (e *syncSNMPExecution) NeedSynchronization(_ context.Context) (bool, error) {
	count := e.execution.Option().SNMPOnlineTarget()
	if count == e.parent.lastCount {
		return false, nil
	}

	// TODO: this isn't ideal. If the synchronization fails, it won't be retried.
	// I think the ideal fix would be to always retry all syncMethods that was to synchronize but failed.
	e.parent.lastCount = count

	return true, nil
}

func (e *syncSNMPExecution) RefreshCache(_ context.Context, _ types.SyncType) error {
	return nil
}

func (e *syncSNMPExecution) SyncRemoteAndLocal(ctx context.Context, _ types.SyncType) error {
	option := e.execution.Option()

	cfg, ok := option.Cache.CurrentAccountConfig()
	if !ok || !cfg.SNMPIntegration {
		return nil
	}

	if e.execution.IsOnlyEssential() {
		// no essential snmp, skip registering.
		return nil
	}

	serverGroup := option.Config.Bleemeo.InitialServerGroupNameForSNMP
	if serverGroup == "" {
		serverGroup = option.Config.Bleemeo.InitialServerGroupName
	}

	return e.parent.registerAndUpdate(ctx, e.execution.BleemeoAPIClient(), option.Cache, serverGroup)
}

func (e *syncSNMPExecution) FinishExecution(_ context.Context) {
}

// FindAgent returns the agent of the target, among agentsByID. snmpType is the ID of the SNMP
// agent type. It returns types.ErrNotExist when the target has no agent.
func (s *SyncSNMP) FindAgent(ctx context.Context, target *snmp.Target, snmpType string, agentsByID map[string]bleemeoTypes.Agent) (bleemeoTypes.Agent, error) {
	var association targetAssociation

	err := s.state.Get(cachePrefix+target.Address(), &association)
	if err != nil {
		return bleemeoTypes.Agent{}, err
	}

	if agent, ok := agentsByID[association.ID]; ok && association.ID != "" {
		return agent, nil
	}

	// Match any SNMP agent that: has the correct FQDN & don't have current association

	facts, err := target.Facts(ctx, 24*time.Hour)
	if err != nil {
		return bleemeoTypes.Agent{}, err
	}

	associatedID := make(map[string]bool, len(s.targets))

	for _, v := range s.targets {
		err := s.state.Get(cachePrefix+v.Address(), &association)
		if err != nil {
			return bleemeoTypes.Agent{}, err
		}

		if association.ID != "" {
			associatedID[association.ID] = true
		}
	}

	for _, agent := range agentsByID {
		if agent.AgentType != snmpType {
			continue
		}

		if _, ok := associatedID[agent.ID]; ok {
			continue
		}

		if agent.FQDN == facts["fqdn"] && facts["fqdn"] != "" {
			return agent, nil
		}
	}

	return bleemeoTypes.Agent{}, types.ErrNotExist
}

// registerAndUpdate registers an agent for the targets which have none, in serverGroup.
func (s *SyncSNMP) registerAndUpdate(ctx context.Context, apiClient types.SNMPClient, c *cache.Cache, serverGroup string) error {
	var newAgent []bleemeoTypes.Agent

	remoteAgentList := c.AgentsByUUID()

	agentTypeID, found := c.AgentTypeID(bleemeo.AgentType_SNMP)
	if !found {
		return types.ErrRetryLater
	}

	for _, target := range s.targets {
		if _, err := s.FindAgent(ctx, target, agentTypeID, remoteAgentList); err != nil && !errors.Is(err, types.ErrNotExist) {
			logger.V(2).Printf("skip registration of SNMP agent: %v", err)

			continue
		} else if err == nil {
			continue
		}

		facts, err := target.Facts(ctx, 24*time.Hour)
		if err != nil {
			logger.V(2).Printf("skip registration of SNMP agent: %v", err)

			continue
		}

		name, err := target.Name(ctx)
		if err != nil {
			return err
		}

		fqdn := facts["fqdn"]
		if fqdn == "" {
			fqdn = target.Address()
		}

		payload := bleemeoapi.AgentPayload{
			FQDN:               fqdn,
			DisplayName:        name,
			AgentType:          agentTypeID,
			Tags:               []bleemeoTypes.Tag{},
			Abstracted:         true,
			InitialPassword:    uuid.New().String(),
			InitialServerGroup: serverGroup,
		}

		tmp, err := remoteRegister(ctx, apiClient, payload)
		if err != nil {
			return err
		}

		newAgent = append(newAgent, tmp)

		err = s.state.Set(cachePrefix+target.Address(), targetAssociation{
			Address: target.Address(),
			ID:      tmp.ID,
		})
		if err != nil {
			logger.V(2).Printf("failed to update state: %v", err)
		}
	}

	if len(newAgent) > 0 {
		agents := c.Agents()
		agents = append(agents, newAgent...)
		c.SetAgentList(agents)
	}

	return nil
}

func remoteRegister(ctx context.Context, apiClient types.SNMPClient, payload bleemeoapi.AgentPayload) (bleemeoTypes.Agent, error) {
	result, err := apiClient.RegisterSNMPAgent(ctx, payload)
	if err != nil {
		return result, err
	}

	logger.V(2).Printf("SNMP agent %v registered with UUID %s", payload.DisplayName, result.ID)

	return result, nil
}
