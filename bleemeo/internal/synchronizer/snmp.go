// Copyright 2015-2021 Bleemeo
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
	"glouton/bleemeo/types"
	"glouton/logger"
	"glouton/prometheus/exporter/snmp"

	"github.com/google/uuid"
)

type payloadAgent struct {
	types.Agent
	Abstracted      bool   `json:"abstracted"`
	InitialPassword string `json:"initial_password"`
}

// TODO the deletion need to be done

func (s *Synchronizer) syncSNMP(fullSync bool, onlyEssential bool) error {
	cfg, ok := s.option.Cache.CurrentAccountConfig()
	if !ok || !cfg.SNMPIntergration {
		return nil
	}

	if len(s.option.SNMP) > 0 {
		logger.V(1).Println("SNMP integration is currently in beta.")

		return nil
	}

	if onlyEssential {
		// no essential snmp, skip registering.
		return nil
	}

	return s.snmpRegisterAndUpdate(s.option.SNMP)
}

func (s *Synchronizer) snmpRegisterAndUpdate(localTargets []snmp.Target) error {
	remoteAgentList := s.option.Cache.Agents()
	remoteIndexByFqdn := make(map[string]int, len(remoteAgentList))

	agentTypeID, found := s.getAgentType(types.AgentTypeSNMP)
	if !found {
		return errRetryLater
	}

	params := map[string]string{
		"fields": "id,display_name,account,agent_type,abstracted,fqdn,initial_password,created_at,next_config_at,current_config,tags",
	}

	for i, agent := range remoteAgentList {
		if agent.AgentType == agentTypeID {
			remoteIndexByFqdn[agent.FQDN] = i
		}
	}

	for _, snmp := range localTargets {
		payload := payloadAgent{
			Agent: types.Agent{
				FQDN:        snmp.Address,
				DisplayName: snmp.InitialName,
				AgentType:   agentTypeID,
				Tags:        []types.Tag{},
			},
			Abstracted:      true,
			InitialPassword: uuid.New().String(),
		}

		address := snmp.Address
		_, remoteFound := remoteIndexByFqdn[address]

		if remoteFound {
			continue
		}

		err := s.remoteRegisterSNMP(&remoteAgentList, params, payload)
		if err != nil {
			return err
		}
	}

	s.option.Cache.SetAgentList(remoteAgentList)

	return nil
}

func (s *Synchronizer) remoteRegisterSNMP(remoteSNMPs *[]types.Agent, params map[string]string, payload payloadAgent) error {
	var result types.Agent

	_, err := s.client.Do(s.ctx, "POST", "v1/agent/", params, payload, &result)
	if err != nil {
		return err
	}

	logger.V(2).Printf("SNMP agent %v registered with UUID %s", payload.DisplayName, result.ID)
	*remoteSNMPs = append(*remoteSNMPs, result)

	return nil
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
