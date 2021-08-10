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
	"errors"
	"fmt"
	"glouton/bleemeo/types"
	"glouton/logger"
	"reflect"
)

var errNoConfig = errors.New("agent don't have any configuration on Bleemeo Cloud platform. Please contact support@bleemeo.com about this issue")

const apiTagsLength = 100

func (s *Synchronizer) syncAgent(fullSync bool, onlyEssential bool) error {
	if err := s.syncMainAgent(); err != nil {
		return err
	}

	if onlyEssential {
		return nil
	}

	if fullSync {
		if err := s.agentTypesUpdateList(); err != nil {
			return err
		}

		if err := s.agentsUpdateList(); err != nil {
			return err
		}
	}

	return nil
}

func (s *Synchronizer) syncMainAgent() error {
	var agent types.Agent

	params := map[string]string{
		"fields": "tags,id,created_at,account,next_config_at,current_config",
	}
	data := map[string][]types.Tag{
		"tags": make([]types.Tag, 0),
	}

	for _, t := range s.option.Config.StringList("tags") {
		if len(t) <= apiTagsLength && t != "" {
			data["tags"] = append(data["tags"], types.Tag{Name: t})
		}
	}

	_, err := s.client.Do(s.ctx, "PATCH", fmt.Sprintf("v1/agent/%s/", s.agentID), params, data, &agent)
	if err != nil {
		return err
	}

	s.option.Cache.SetAgent(agent)

	if agent.CurrentConfigID == "" {
		return errNoConfig
	}

	s.option.Cache.SetAccountID(agent.AccountID)

	if agent.AccountID != s.option.Config.String("bleemeo.account_id") && !s.warnAccountMismatchDone {
		s.warnAccountMismatchDone = true
		logger.Printf(
			"Account ID in configuration file (%s) mismatch the current account ID (%s). The Account ID from configuration file will be ignored.",
			s.option.Config.String("bleemeo.account_id"),
			agent.AccountID,
		)
	}

	config, err := s.getAccountConfig(agent.CurrentConfigID)
	if err != nil {
		return err
	}

	currentConfig := s.option.Cache.CurrentAccountConfig()
	if !reflect.DeepEqual(currentConfig, config) {
		s.option.Cache.SetCurrentAccountConfig(config)

		if s.option.UpdateConfigCallback != nil {
			s.option.UpdateConfigCallback()
		}
	}

	return nil
}

func (s *Synchronizer) agentsUpdateList() error {
	params := map[string]string{
		"fields": "id,created_at,account,next_config_at,current_config,tags,agent_type,fqdn,display_name",
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

func (s *Synchronizer) agentTypesUpdateList() error {
	params := map[string]string{
		"fields": "id,name,display_name",
	}

	result, err := s.client.Iter(s.ctx, "agenttype", params)
	if err != nil {
		return err
	}

	agentTypes := make([]types.AgentType, len(result))

	for i, jsonMessage := range result {
		var agentType types.AgentType

		if err := json.Unmarshal(jsonMessage, &agentType); err != nil {
			continue
		}

		agentTypes[i] = agentType
	}

	s.option.Cache.SetAgentTypes(agentTypes)

	return nil
}
