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

// TODO the deletion need to be done

func (s *Synchronizer) syncSNMP(fullSync bool, onlyEssential bool) error {
	var snmpTargets []*snmp.SNMPTarget

	if s.option.Cache.CurrentAccountConfig().SNMPIntergration {
		snmpTargets = s.option.SNMP
	}

	if s.successiveErrors == 3 {
		// After 3 error, try to force a full synchronization to see if it solve the issue.
		fullSync = true
	}

	if fullSync {
		err := s.snmpUpdateList()
		if err != nil {
			return err
		}
	}

	if onlyEssential {
		// no essential snmp, skip registering.
		return nil
	}

	if err := s.snmpRegisterAndUpdate(snmpTargets); err != nil {
		return err
	}

	return nil
}

func (s *Synchronizer) snmpRegisterAndUpdate(localTargets []*snmp.SNMPTarget) error {
	remoteAgentList := s.option.Cache.AgentList()
	remoteIndexByFqdn := make(map[string]int, len(remoteAgentList))

	agentTypeID, err := s.getAgentType("snmp")

	if err != nil {
		return err
	}

	params := map[string]string{
		"fields": "id,display_name,account,agent_type,abstracted,fqdn,initial_password,created_at,next_config_at,current_config,tags",
	}

	var agent types.PayloadSNMPAgent

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
		payload := types.PayloadSNMPAgent{
			DisplayName:     snmp.Name,
			Fqdn:            snmp.Address,
			AgentTypeID:     agentTypeID,
			Abstracted:      true,
			InitialPassword: uuid.New().String(),
		}

		address := snmp.Address
		remoteIndex, remoteFound := remoteIndexByFqdn[address]

		var remoteSNMP types.PayloadSNMPAgent

		if remoteFound {
			remoteSNMP.Agent = remoteAgentList[remoteIndex]
		}

		err := s.remoteRegisterSNMP(remoteFound, &remoteSNMP, &remoteAgentList, params, payload, remoteIndex)

		if err != nil {
			return err
		}
	}

	s.option.Cache.SetAgentList(remoteAgentList)

	return nil
}

func (s *Synchronizer) remoteRegisterSNMP(remoteFound bool, remoteSNMP *types.PayloadSNMPAgent,
	remoteSNMPs *[]types.Agent, params map[string]string, payload types.PayloadSNMPAgent, remoteIndex int) error {
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

func (s *Synchronizer) snmpUpdateList() error {
	params := map[string]string{
		"fields": "id,created_at,account,next_config_at,current_config,tags",
	}

	result, err := s.client.Iter(s.ctx, "agent", params)
	if err != nil {
		return err
	}

	snmps := make([]types.Agent, len(result))

	for i, jsonMessage := range result {
		var snmp types.Agent

		if err := json.Unmarshal(jsonMessage, &snmp); err != nil {
			continue
		}

		snmps[i] = snmp
	}

	s.option.Cache.SetAgentList(snmps)

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
