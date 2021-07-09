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

	"github.com/google/uuid"
)

func (s *Synchronizer) SnmpAgent(target types.SNMPTarget) error {
	agentTypeID, err := s.getAgentType("snmp")
	if err != nil {
		return err
	}

	agentList, err := s.getAgentList()

	if err != nil {
		return err
	}

	exist := false

	for _, a := range agentList {
		if a.Fqdn == target.Address {
			/*
				err := s.updateSnmpAgent(a.ID)
				if err != nil {
					return err
				}
			*/
			exist = true
		}
	}

	if !exist {
		return s.createSNMPAgent(agentList[0].AccountID, agentTypeID, target)
	}

	return nil
}

func (s *Synchronizer) createSNMPAgent(accountID string, agentTypeID string, target types.SNMPTarget) error {
	payload := map[string]string{
		"account":          accountID,
		"initial_password": uuid.New().String(),
		"display_name":     target.Name,
		"fqdn":             target.Address,
		"agent_type":       agentTypeID,
		"abstracted":       "True",
		//		"device_type":      target.Type,
	}

	var response types.AgentSnmp

	_, err := s.client.Do(s.ctx, "POST", "v1/agent/", nil, payload, &response)
	if err != nil {
		return err
	}

	return nil
}

func (s *Synchronizer) getAgentList() (agents []types.AgentSnmp, err error) {
	params := map[string]string{
		"fields": "id,created_at,account,fqdn,display_name,agent_type",
	}

	_, err = s.client.Do(s.ctx, "GET", "v1/agent/", params, nil, &agents)
	if err != nil {
		return nil, err
	}

	return agents, nil
}

func (s *Synchronizer) getAgentType(name string) (id string, err error) {
	var agentType []types.AgentType

	params := map[string]string{
		"fields": "id,name,display_name",
	}

	_, err = s.client.Do(s.ctx, "GET", "v1/agenttype/", params, nil, &agentType)
	if err != nil {
		return "", err
	}

	for _, a := range agentType {
		if a.Name == name {
			id = a.ID
			break
		}
	}

	return id, nil
}

/*
func (s *Synchronizer) updateSnmpAgent(ID string, target types.SNMPTarget) error {
	var agent []types.AgentSnmp

	params := map[string]string{
		"fields": "id,created_at,account,fqdn,display_name,agent_type",
	}
	// need to change the data type maybe use an update only of the display_name
	data := map[string]string{
		"display_name": target.Name,
	}

	_, err := s.client.Do(s.ctx, "PATCH", fmt.Sprintf("v1/agent/%s/", ID), params, data, &agent)
	if err != nil {
		return err
	}

	return nil
}
*/
