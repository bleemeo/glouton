// Copyright 2015-2024 Bleemeo
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
	"context"
	"errors"
	"time"

	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/bleemeoapi"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/prometheus/exporter/snmp"

	"github.com/google/uuid"
)

const (
	snmpCachePrefix = "bleemeo:snmp:"
	snmpAgentFields = "id,display_name,account,agent_type,abstracted,fqdn,initial_password,created_at,next_config_at,current_config,tags,initial_server_group_name"
)

// TODO the deletion need to be done

func (s *Synchronizer) syncSNMP(ctx context.Context, syncType types.SyncType, execution types.SynchronizationExecution) (updateThresholds bool, err error) {
	_ = syncType

	cfg, ok := s.option.Cache.CurrentAccountConfig()
	if !ok || !cfg.SNMPIntegration {
		return false, nil
	}

	if execution.IsOnlyEssential() {
		// no essential snmp, skip registering.
		return false, nil
	}

	return false, s.snmpRegisterAndUpdate(ctx, execution, s.option.SNMP)
}

type snmpAssociation struct {
	Address string
	ID      string
}

func (s *Synchronizer) FindSNMPAgent(ctx context.Context, target *snmp.Target, snmpType string, agentsByID map[string]bleemeoTypes.Agent) (bleemeoTypes.Agent, error) {
	var association snmpAssociation

	err := s.option.State.Get(snmpCachePrefix+target.Address(), &association)
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

	associatedID := make(map[string]bool, len(s.option.SNMP))

	for _, v := range s.option.SNMP {
		err := s.option.State.Get(snmpCachePrefix+v.Address(), &association)
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

	return bleemeoTypes.Agent{}, errNotExist
}

func (s *Synchronizer) snmpRegisterAndUpdate(ctx context.Context, execution types.SynchronizationExecution, localTargets []*snmp.Target) error {
	var newAgent []bleemeoTypes.Agent //nolint: prealloc

	remoteAgentList := s.option.Cache.AgentsByUUID()

	agentTypeID, found := s.getAgentType(bleemeoTypes.AgentTypeSNMP)
	if !found {
		return errRetryLater
	}

	for _, snmp := range localTargets {
		if _, err := s.FindSNMPAgent(ctx, snmp, agentTypeID, remoteAgentList); err != nil && !errors.Is(err, errNotExist) {
			logger.V(2).Printf("skip registration of SNMP agent: %v", err)

			continue
		} else if err == nil {
			continue
		}

		facts, err := snmp.Facts(ctx, 24*time.Hour)
		if err != nil {
			logger.V(2).Printf("skip registration of SNMP agent: %v", err)

			continue
		}

		name, err := snmp.Name(ctx)
		if err != nil {
			return err
		}

		fqdn := facts["fqdn"]
		if fqdn == "" {
			fqdn = snmp.Address()
		}

		serverGroup := s.option.Config.Bleemeo.InitialServerGroupNameForSNMP
		if serverGroup == "" {
			serverGroup = s.option.Config.Bleemeo.InitialServerGroupName
		}

		payload := bleemeoapi.AgentPayload{
			Agent: bleemeoTypes.Agent{
				FQDN:        fqdn,
				DisplayName: name,
				AgentType:   agentTypeID,
				Tags:        []bleemeoTypes.Tag{},
			},
			Abstracted:         true,
			InitialPassword:    uuid.New().String(),
			InitialServerGroup: serverGroup,
		}

		tmp, err := s.remoteRegisterSNMP(ctx, execution.BleemeoAPIClient(), payload)
		if err != nil {
			return err
		}

		newAgent = append(newAgent, tmp)

		err = s.option.State.Set(snmpCachePrefix+snmp.Address(), snmpAssociation{
			Address: snmp.Address(),
			ID:      tmp.ID,
		})
		if err != nil {
			logger.V(2).Printf("failed to update state: %v", err)
		}
	}

	if len(newAgent) > 0 {
		agents := s.option.Cache.Agents()
		agents = append(agents, newAgent...)
		s.option.Cache.SetAgentList(agents)
	}

	return nil
}

func (s *Synchronizer) remoteRegisterSNMP(ctx context.Context, apiClient types.SNMPClient, payload bleemeoapi.AgentPayload) (bleemeoTypes.Agent, error) {
	result, err := apiClient.RegisterSNMPAgent(ctx, payload)
	if err != nil {
		return result, err
	}

	logger.V(2).Printf("SNMP agent %v registered with UUID %s", payload.DisplayName, result.ID)

	return result, nil
}

func (s *Synchronizer) getAgentType(name string) (id string, found bool) {
	agentTypes := s.option.Cache.AgentTypes()

	for _, a := range agentTypes {
		if a.Name == name {
			return a.ID, true
		}
	}

	return "", false
}
