package synchronizer

import (
	"agentgo/bleemeo/types"
	"agentgo/logger"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func (s *Synchronizer) syncFacts(fullSync bool) error {

	localFacts, err := s.option.Facts.Facts(s.ctx, 24*time.Hour)
	if err != nil {
		return err
	}

	// s.factUpdateList() is already done by checkDuplicated
	// s.serviceDeleteFromRemote() is uneeded, API don't delete facts

	if err := s.factRegister(localFacts); err != nil {
		return err
	}
	if err := s.factDeleteFromLocal(localFacts); err != nil {
		return err
	}
	s.lastFactUpdatedAt = localFacts["fact_updated_at"]
	return nil
}

func (s *Synchronizer) factsUpdateList() error {

	params := map[string]string{
		"agent": s.option.State.AgentID(),
	}
	result, err := s.client.Iter("agentfact", params)
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

	currentConfig := s.option.Cache.AccountConfig()

	registeredFacts := s.option.Cache.FactsByKey()
	facts := make([]types.AgentFact, 0, len(registeredFacts))
	for _, v := range registeredFacts {
		facts = append(facts, v)
	}
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
			"agent": s.option.State.AgentID(),
			"key":   key,
			"value": value,
		}
		var response types.AgentFact
		_, err := s.client.Do("POST", "v1/agentfact/", nil, payload, &response)
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
	registeredFacts := s.option.Cache.FactsByUUID()
	for k, v := range registeredFacts {
		if _, ok := localFacts[v.Key]; ok {
			continue
		}
		_, err := s.client.Do("DELETE", fmt.Sprintf("v1/agentfact/%s/", v.ID), nil, nil, nil)
		if err != nil {
			return err
		}
		delete(registeredFacts, k)
	}

	facts := make([]types.AgentFact, 0, len(registeredFacts))
	for _, v := range registeredFacts {
		facts = append(facts, v)
	}
	s.option.Cache.SetFacts(facts)
	return nil
}
