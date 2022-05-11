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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"glouton/bleemeo/types"
	"glouton/logger"
)

var errNoConfig = errors.New("agent don't have any configuration on Bleemeo Cloud platform. Please contact support@bleemeo.com about this issue")

const apiTagsLength = 100

func (s *Synchronizer) syncAgent(ctx context.Context, fullSync bool, onlyEssential bool) (updateThresholds bool, err error) {
	if err := s.syncMainAgent(ctx); err != nil {
		return false, err
	}

	if onlyEssential {
		return false, nil
	}

	if fullSync {
		if err := s.agentsUpdateList(); err != nil {
			return false, err
		}
	}

	return false, nil
}

func (s *Synchronizer) syncMainAgent(ctx context.Context) error {
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

	previousConfig := s.option.Cache.Agent().CurrentConfigID

	_, err := s.client.Do(ctx, "PATCH", fmt.Sprintf("v1/agent/%s/", s.agentID), params, data, &agent)
	if err != nil {
		return err
	}

	s.option.Cache.SetAgent(agent)

	if agent.CurrentConfigID == "" {
		return errNoConfig
	}

	if previousConfig != agent.CurrentConfigID && s.option.UpdateConfigCallback != nil {
		s.option.UpdateConfigCallback(ctx, true)
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

	return nil
}

func (s *Synchronizer) agentsUpdateList() error {
	oldAgents := s.option.Cache.AgentsByUUID()

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

	// If an agent is deleted, ensure our Labels on metric are up-to-date.
	// If an SNMP agent is deleted, it agent UUID is no longer valid and metric
	// should no longer be labeled with it.
	newAgents := s.option.Cache.AgentsByUUID()
	for id := range oldAgents {
		if _, ok := newAgents[id]; !ok {
			s.callUpdateLabels = true
		}
	}

	return nil
}
