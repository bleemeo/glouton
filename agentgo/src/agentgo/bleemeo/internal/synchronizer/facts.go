package synchronizer

import (
	"agentgo/bleemeo/types"
	"agentgo/logger"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func (s *Synchronizer) syncFactsRead() error {

	result, err := s.client.Iter("agentfact", nil)
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

func (s *Synchronizer) syncFacts(fullSync bool) error {

	// List of registered facts is updated by syncFactsRead which is always called by checkDuplicated before syncFacts

	currentConfig := s.option.Cache.AccountConfig()
	localFacts, err := s.option.Facts.Facts(s.ctx, 24*time.Hour)
	if err != nil {
		return err
	}
	localUUIDs := make(map[string]bool)
	registeredFacts := s.option.Cache.FactsByKey()

	for key, value := range localFacts {
		if !currentConfig.DockerIntegration && strings.HasPrefix(key, "docker_") {
			continue
		}
		remoteValue := registeredFacts[key]
		if value == remoteValue.Value {
			localUUIDs[remoteValue.ID] = true
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
		_, err := s.client.Do("POST", "v1/agentfact/", payload, &response)
		if err != nil {
			return err
		}
		registeredFacts[key] = response
		localUUIDs[response.ID] = true
		logger.V(2).Printf("Send fact %s, stored with uuid %s", key, response.ID)
	}

	registeredFacts = s.option.Cache.FactsByUUID()
	for k, v := range registeredFacts {
		if _, ok := localUUIDs[v.ID]; ok {
			continue
		}
		_, err = s.client.Do("DELETE", fmt.Sprintf("v1/agentfact/%s/", v.ID), nil, nil)
		if err != nil {
			return err
		}
		delete(registeredFacts, k)
	}
	s.lastFactUpdatedAt = localFacts["fact_updated_at"]
	facts := make([]types.AgentFact, 0, len(registeredFacts))
	for _, v := range registeredFacts {
		facts = append(facts, v)
	}
	s.option.Cache.SetFacts(facts)
	return nil
}
