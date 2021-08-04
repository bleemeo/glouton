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
	"encoding/json"
	"fmt"
	"glouton/bleemeo/types"
	"glouton/logger"
	"glouton/prometheus/exporter/snmp"

	"github.com/google/uuid"
)

type PayloadSNMPAgent struct {
	types.Agent
	SNMPAgent
}

type SNMPAgent struct {
	DisplayName     string `json:"display_name"`
	Fqdn            string `json:"fqdn"`
	AgentTypeID     string `json:"agent_type"`
	Abstracted      bool   `json:"abstracted"`
	InitialPassword string `json:"initial_password"`
}

// TODO the deletion need to be done

func (s *Synchronizer) syncSNMP(fullSync bool, onlyEssential bool) error {
	var snmpTargets []*snmp.Target

	if s.option.Cache.CurrentAccountConfig().SNMPIntergration {
		snmpTargets = s.option.SNMP
	}

	if s.successiveErrors == 3 {
		// After 3 error, try to force a full synchronization to see if it solve the issue.
		fullSync = true
	}

	if fullSync {
		err := s.agentsUpdateList()
		if err != nil {
			return err
		}
	}

	if onlyEssential {
		// no essential snmp, skip registering.
		return nil
	}

	return s.snmpRegisterAndUpdate(snmpTargets)
}

func (s *Synchronizer) snmpRegisterAndUpdate(localTargets []*snmp.Target) error {
	remoteAgentList := s.option.Cache.AgentList()
	remoteIndexByFqdn := make(map[string]int, len(remoteAgentList))

	agentTypeID, err := s.getAgentType("snmp")
	if err != nil {
		return err
	}

	params := map[string]string{
		"fields": "id,display_name,account,agent_type,abstracted,fqdn,initial_password,created_at,next_config_at,current_config,tags",
	}

	var agent PayloadSNMPAgent

	for i, v := range remoteAgentList {
		_, err := s.client.Do(s.ctx, "GET", fmt.Sprintf("v1/agent/%s/", v.ID), params, nil, &agent)
		if err != nil {
			return err
		}

		if agent.AgentTypeID == agentTypeID {
			remoteIndexByFqdn[agent.Fqdn] = i
		}
	}

	for _, snmp := range localTargets {
		payload := SNMPAgent{
			DisplayName:     snmp.InitialName,
			Fqdn:            snmp.Address,
			AgentTypeID:     agentTypeID,
			Abstracted:      true,
			InitialPassword: uuid.New().String(),
		}

		address := snmp.Address
		remoteIndex, remoteFound := remoteIndexByFqdn[address]

		var remoteSNMP PayloadSNMPAgent

		if remoteFound {
			remoteSNMP.Agent = remoteAgentList[remoteIndex]
			if payload.Fqdn == remoteSNMP.Fqdn {
				continue
			}
		}

		err := s.remoteRegisterSNMP(remoteFound, &remoteSNMP, &remoteAgentList, params, payload, remoteIndex)
		if err != nil {
			return err
		}
	}

	s.option.Cache.SetAgentList(remoteAgentList)

	return nil
}

func (s *Synchronizer) remoteRegisterSNMP(remoteFound bool, remoteSNMP *PayloadSNMPAgent,
	remoteSNMPs *[]types.Agent, params map[string]string, payload SNMPAgent, remoteIndex int) error {
	var result types.Agent

	if remoteFound {
		_, err := s.client.Do(s.ctx, "PUT", fmt.Sprintf("v1/agent/%s/", remoteSNMP.ID), params, payload, &result)
		if err != nil {
			return err
		}

		logger.V(2).Printf("SNMP agent %v updated with UUID %s", payload.DisplayName, result.ID)
		(*remoteSNMPs)[remoteIndex] = result
	} else {
		_, err := s.client.Do(s.ctx, "POST", "v1/agent/", params, payload, &result)
		if err != nil {
			return err
		}

		logger.V(2).Printf("SNMP agent %v registered with UUID %s", payload.DisplayName, result.ID)
		*remoteSNMPs = append(*remoteSNMPs, result)
	}

	return nil
}

func (s *Synchronizer) agentsUpdateList() error {
	params := map[string]string{
		"fields": "id,created_at,account,next_config_at,current_config,tags",
	}

	result, err := s.client.Iter(s.ctx, "agent", params)
	if err != nil {
		return err
	}

	agents := make([]types.Agent, len(result))

	for i, jsonMessage := range result {
		var agent types.Agent

		if err := json.Unmarshal(jsonMessage, &agent); err != nil {
			continue
		}

		agents[i] = agent
	}

	s.option.Cache.SetAgentList(agents)

	return nil
}

func (s *Synchronizer) getAgentType(name string) (id string, err error) {
	params := map[string]string{
		"fields": "id,name,display_name",
	}

	result, err := s.client.Iter(s.ctx, "agenttype", params)
	if err != nil {
		return "", err
	}

	agentTypes := make([]types.AgentType, len(result))

	for i, jsonMessage := range result {
		var agentType types.AgentType

		if err := json.Unmarshal(jsonMessage, &agentType); err != nil {
			continue
		}

		agentTypes[i] = agentType
	}

	for _, a := range agentTypes {
		if a.Name == name {
			id = a.ID

			break
		}
	}

	return id, nil
}
