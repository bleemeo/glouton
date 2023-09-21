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
	"encoding/json"
	"fmt"
	"glouton/bleemeo/client"
	"glouton/bleemeo/types"
	"glouton/facts"
	"glouton/logger"
	"time"
)

func getEssentialFacts() map[string]bool {
	return map[string]bool{
		"agent_version":       true,
		"architecture":        true,
		"fqdn":                true,
		"glouton_version":     true,
		"hostname":            true,
		"installation_format": true,
		"kernel":              true,
		"os_name":             true,
		"os_pretty_name":      true,
		"public_ip":           true,
		"virtual":             true,
		"metrics_format":      true,
	}
}

func (s *Synchronizer) syncFacts(ctx context.Context, fullSync bool, onlyEssential bool) (updateThresholds bool, err error) {
	_ = fullSync

	localFacts, err := s.option.Facts.Facts(ctx, 24*time.Hour)
	if err != nil {
		return false, err
	}

	if onlyEssential {
		essentialFacts := getEssentialFacts()
		copyFacts := make(map[string]string)

		for k, v := range localFacts {
			if essentialFacts[k] {
				copyFacts[k] = v
			}
		}

		localFacts = copyFacts
	}

	previousFacts := s.option.Cache.FactsByKey()

	allAgentFacts := make(map[string]map[string]string, 1+len(s.option.SNMP))
	allAgentFacts[s.agentID] = localFacts

	if !onlyEssential {
		agentTypeID, found := s.getAgentType(types.AgentTypeSNMP)
		if !found {
			return false, errRetryLater
		}

		remoteAgentList := s.option.Cache.AgentsByUUID()

		for _, t := range s.option.SNMP {
			if agent, err := s.FindSNMPAgent(ctx, t, agentTypeID, remoteAgentList); err == nil {
				facts, err := t.Facts(ctx, 24*time.Hour)
				if err != nil {
					logger.V(2).Printf("unable to get SNMP facts: %v", err)

					// Reuse previous facts
					tmp := previousFacts[agent.ID]
					facts = make(map[string]string, len(tmp))

					for _, v := range tmp {
						facts[v.Key] = v.Value
					}
				}

				allAgentFacts[agent.ID] = facts
			}
		}

		for _, dev := range s.option.VSphereDevices(ctx, time.Hour) {
			agentTypeID, found := s.getVSphereAgentType(dev.Kind())
			if !found {
				continue
			}

			if agent, err := s.FindVSphereAgent(ctx, dev, agentTypeID, remoteAgentList); err == nil {
				// As vSphere facts are only gathered during the device discovery,
				// there is no need to specify a max age below.
				facts, err := dev.Facts(ctx, 0)
				if err != nil {
					logger.V(2).Printf("Unable to get vSphere facts: %v", err)

					// Reuse previous facts; it avoids removing then adding them back.
					tmp := previousFacts[agent.ID]
					facts = make(map[string]string, len(tmp))

					for _, v := range tmp {
						facts[v.Key] = v.Value
					}
				}

				allAgentFacts[agent.ID] = facts
			}
		}
	}

	// s.factUpdateList() is already done by checkDuplicated
	// s.serviceDeleteFromRemote() is uneeded, API don't delete facts

	if err := s.factRegister(allAgentFacts); err != nil {
		return false, err
	}

	if onlyEssential {
		// localFacts was filtered, can't delete
		return false, nil
	}

	if err := s.factDeleteFromLocal(allAgentFacts); err != nil {
		return false, err
	}

	s.lastFactUpdatedAt = localFacts[facts.FactUpdatedAt]

	return false, nil
}

func (s *Synchronizer) factsUpdateList(ctx context.Context) error {
	params := map[string]string{}

	result, err := s.client.Iter(ctx, "agentfact", params)
	if err != nil {
		return err
	}

	facts := make([]types.AgentFact, 0, len(result))

	for _, jsonMessage := range result {
		var fact types.AgentFact

		if err := json.Unmarshal(jsonMessage, &fact); err != nil {
			continue
		}

		facts = append(facts, fact)
	}

	s.option.Cache.SetFacts(facts)

	return nil
}

func (s *Synchronizer) factRegister(allAgentFacts map[string]map[string]string) error {
	registeredFacts := s.option.Cache.FactsByKey()
	facts := s.option.Cache.Facts()

	for agentID, localFacts := range allAgentFacts {
		for key, value := range localFacts {
			remoteValue := registeredFacts[agentID][key]
			if value == remoteValue.Value {
				continue
			}

			logger.V(3).Printf("fact %s:%#v changed from %#v to %#v", agentID, key, remoteValue.Value, value)

			// Agent can't update fact. We delete and re-create the facts
			payload := map[string]string{
				"agent": agentID,
				"key":   key,
				"value": value,
			}

			var response types.AgentFact

			_, err := s.client.Do(s.ctx, "POST", "v1/agentfact/", nil, payload, &response)
			if err != nil {
				return err
			}

			facts = append(facts, response)
			logger.V(2).Printf("Send fact %s:%s, stored with uuid %s", agentID, key, response.ID)
		}
	}

	s.option.Cache.SetFacts(facts)

	return nil
}

func (s *Synchronizer) factDeleteFromLocal(allAgentFacts map[string]map[string]string) error {
	duplicatedKey := make(map[string]bool)
	registeredFacts := s.option.Cache.FactsByUUID()

	for k, v := range registeredFacts {
		localFacts := allAgentFacts[v.AgentID]
		localValue, ok := localFacts[v.Key]

		if ok && localValue == v.Value && !duplicatedKey[v.AgentID+"\x00"+v.Key] {
			duplicatedKey[v.AgentID+"\x00"+v.Key] = true

			continue
		}

		_, err := s.client.Do(s.ctx, "DELETE", fmt.Sprintf("v1/agentfact/%s/", v.ID), nil, nil, nil)
		// If the fact was not found it has already been deleted.
		if err != nil && !client.IsNotFound(err) {
			return err
		}

		logger.V(2).Printf("Fact %s:%v (uuid=%v) deleted", v.AgentID, v.Key, v.ID)
		delete(registeredFacts, k)
	}

	facts := make([]types.AgentFact, 0, len(registeredFacts))

	for _, v := range registeredFacts {
		facts = append(facts, v)
	}

	s.option.Cache.SetFacts(facts)

	return nil
}
