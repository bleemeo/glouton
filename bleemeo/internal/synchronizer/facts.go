// Copyright 2015-2019 Bleemeo
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
	"strings"
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
	}
}

func (s *Synchronizer) syncFacts(fullSync bool, onlyEssential bool) error {
	localFacts, err := s.option.Facts.Facts(s.ctx, 24*time.Hour)
	if err != nil {
		return err
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

	if !s.option.Cache.CurrentAccountConfig().DockerIntegration {
		copyFacts := make(map[string]string)

		for k, v := range localFacts {
			if !strings.HasPrefix(k, "docker_") {
				copyFacts[k] = v
			}
		}

		localFacts = copyFacts
	}

	// s.factUpdateList() is already done by checkDuplicated
	// s.serviceDeleteFromRemote() is uneeded, API don't delete facts

	if err := s.factRegister(localFacts); err != nil {
		return err
	}

	if onlyEssential {
		// localFacts was filtered, can't delete
		return nil
	}

	if err := s.factDeleteFromLocal(localFacts); err != nil {
		return err
	}

	s.lastFactUpdatedAt = localFacts["fact_updated_at"]

	return nil
}

func (s *Synchronizer) factsUpdateList() error {
	params := map[string]string{
		"agent": s.agentID,
	}

	result, err := s.client.Iter(s.ctx, "agentfact", params)
	if err != nil {
		return err
	}

	facts := make([]types.AgentFact, len(result))

	for i, jsonMessage := range result {
		var fact types.AgentFact

		if err := json.Unmarshal(jsonMessage, &fact); err != nil {
			continue
		}

		facts[i] = fact
	}

	s.option.Cache.SetFacts(facts)

	return nil
}

func (s *Synchronizer) factRegister(localFacts map[string]string) error {
	currentConfig := s.option.Cache.CurrentAccountConfig()

	registeredFacts := s.option.Cache.FactsByKey()
	facts := s.option.Cache.Facts()

	for key, value := range localFacts {
		if !currentConfig.DockerIntegration && strings.HasPrefix(key, "docker_") {
			continue
		}

		remoteValue := registeredFacts[key]
		if value == remoteValue.Value {
			continue
		}

		logger.V(3).Printf("fact %#v changed from %#v to %#v", key, remoteValue.Value, value)

		// Agent can't update fact. We delete and re-create the facts
		payload := map[string]string{
			"agent": s.agentID,
			"key":   key,
			"value": value,
		}

		var response types.AgentFact

		_, err := s.client.Do(s.ctx, "POST", "v1/agentfact/", nil, payload, &response)
		if err != nil {
			return err
		}

		facts = append(facts, response)
		logger.V(2).Printf("Send fact %s, stored with uuid %s", key, response.ID)
	}

	s.option.Cache.SetFacts(facts)

	return nil
}

func (s *Synchronizer) factDeleteFromLocal(localFacts map[string]string) error {
	duplicatedKey := make(map[string]bool)
	registeredFacts := s.option.Cache.FactsByUUID()

	for k, v := range registeredFacts {
		localValue, ok := localFacts[v.Key]
		if ok && localValue == v.Value && !duplicatedKey[v.Key] {
			duplicatedKey[v.Key] = true
			continue
		}

		_, err := s.client.Do(s.ctx, "DELETE", fmt.Sprintf("v1/agentfact/%s/", v.ID), nil, nil, nil)
		if err != nil {
			return err
		}

		logger.V(2).Printf("Fact %v (uuid=%v) deleted", v.Key, v.ID)
		delete(registeredFacts, k)
	}

	facts := make([]types.AgentFact, 0, len(registeredFacts))

	for _, v := range registeredFacts {
		facts = append(facts, v)
	}

	s.option.Cache.SetFacts(facts)

	return nil
}
