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
	"errors"
	"fmt"
	"glouton/bleemeo/types"
	"glouton/logger"
	"reflect"
)

const apiTagsLength = 100

func (s *Synchronizer) syncAgent(fullSync bool) error {
	var agent types.Agent

	params := map[string]string{
		"fields": "tags,id,created_at,account,next_config_at,current_config,read_only",
	}
	data := map[string][]types.Tag{
		"tags": make([]types.Tag, 0),
	}

	for _, t := range s.option.Config.StringList("tags") {
		if len(t) <= apiTagsLength && t != "" {
			data["tags"] = append(data["tags"], types.Tag{Name: t})
		}
	}

	_, err := s.client.Do("PATCH", fmt.Sprintf("v1/agent/%s/", s.agentID), params, data, &agent)
	if err != nil {
		return err
	}

	s.option.Cache.SetAgent(agent)

	if agent.CurrentConfigID == "" {
		return errors.New("agent don't have any configuration on Bleemeo Cloud platform. Please contact support@bleemeo.com about this issue")
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

	if s.option.SetBleemeoInMaintenanceMode != nil {
		s.option.SetBleemeoInMaintenanceMode(agent.ReadOnly)
	}

	return nil
}

// IsMaintenance returns whether the synchronizer is currently in maintenance mode (not making any request except info/agent).
func (s *Synchronizer) IsMaintenance() bool {
	s.l.Lock()
	defer s.l.Unlock()

	return s.maintenanceMode
}

// SetMaintenance allows to trigger the maintenance mode for the synchronize.
// When running in maintenance mode, only the general infos, the agent and its configuration are synced.
func (s *Synchronizer) SetMaintenance(maintenance bool) {
	s.l.Lock()
	defer s.l.Unlock()

	s.maintenanceMode = maintenance
}

// UpdateMaintenance requests to check for the maintenance mode again.
func (s *Synchronizer) UpdateMaintenance() {
	s.l.Lock()
	defer s.l.Unlock()

	// sync the version too, in case the min version changed during the maintenance
	s.forceSync["info"] = false
	s.forceSync["agent"] = false
}
