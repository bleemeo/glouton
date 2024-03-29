// Copyright 2015-2023 Bleemeo
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
	"glouton/bleemeo/types"
	"glouton/logger"
	"glouton/prometheus/exporter/snmp"
	"time"

	"github.com/google/uuid"
)

const snmpCachePrefix = "bleemeo:snmp:"

type payloadAgent struct {
	types.Agent
	Abstracted         bool   `json:"abstracted"`
	InitialPassword    string `json:"initial_password"`
	InitialServerGroup string `json:"initial_server_group_name,omitempty"`
}

// TODO the deletion need to be done

func (s *Synchronizer) syncSNMP(_ context.Context, fullSync bool, onlyEssential bool) (updateThresholds bool, err error) {
	_ = fullSync

	cfg, ok := s.option.Cache.CurrentAccountConfig()
	if !ok || !cfg.SNMPIntegration {
		return false, nil
	}

	if onlyEssential {
		// no essential snmp, skip registering.
		return false, nil
	}

	return false, s.snmpRegisterAndUpdate(s.option.SNMP)
}

type snmpAssociation struct {
	Address string
	ID      string
}

func (s *Synchronizer) FindSNMPAgent(ctx context.Context, target *snmp.Target, snmpType string, agentsByID map[string]types.Agent) (types.Agent, error) {
	var association snmpAssociation

	err := s.option.State.Get(snmpCachePrefix+target.Address(), &association)
	if err != nil {
		return types.Agent{}, err
	}

	if agent, ok := agentsByID[association.ID]; ok && association.ID != "" {
		return agent, nil
	}

	// Match any SNMP agent that: has the correct FQDN & don't have current association

	facts, err := target.Facts(ctx, 24*time.Hour)
	if err != nil {
		return types.Agent{}, err
	}

	associatedID := make(map[string]bool, len(s.option.SNMP))

	for _, v := range s.option.SNMP {
		err := s.option.State.Get(snmpCachePrefix+v.Address(), &association)
		if err != nil {
			return types.Agent{}, err
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

	return types.Agent{}, errNotExist
}

func (s *Synchronizer) snmpRegisterAndUpdate(localTargets []*snmp.Target) error {
	var newAgent []types.Agent //nolint: prealloc

	remoteAgentList := s.option.Cache.AgentsByUUID()

	agentTypeID, found := s.getAgentType(types.AgentTypeSNMP)
	if !found {
		return errRetryLater
	}

	params := map[string]string{
		"fields": "id,display_name,account,agent_type,abstracted,fqdn,initial_password,created_at,next_config_at,current_config,tags,initial_server_group_name",
	}

	for _, snmp := range localTargets {
		if _, err := s.FindSNMPAgent(s.ctx, snmp, agentTypeID, remoteAgentList); err != nil && !errors.Is(err, errNotExist) {
			logger.V(2).Printf("skip registration of SNMP agent: %v", err)

			continue
		} else if err == nil {
			continue
		}

		facts, err := snmp.Facts(s.ctx, 24*time.Hour)
		if err != nil {
			logger.V(2).Printf("skip registration of SNMP agent: %v", err)

			continue
		}

		name, err := snmp.Name(s.ctx)
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

		payload := payloadAgent{
			Agent: types.Agent{
				FQDN:        fqdn,
				DisplayName: name,
				AgentType:   agentTypeID,
				Tags:        []types.Tag{},
			},
			Abstracted:         true,
			InitialPassword:    uuid.New().String(),
			InitialServerGroup: serverGroup,
		}

		tmp, err := s.remoteRegisterSNMP(params, payload)
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

func (s *Synchronizer) remoteRegisterSNMP(params map[string]string, payload payloadAgent) (types.Agent, error) {
	var result types.Agent

	_, err := s.client.Do(s.ctx, "POST", "v1/agent/", params, payload, &result)
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
