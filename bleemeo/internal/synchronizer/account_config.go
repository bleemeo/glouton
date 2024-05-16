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
	"mime"
	"net/url"
	"reflect"
	"strings"

	"github.com/bleemeo/bleemeo-go"
	"github.com/bleemeo/glouton/bleemeo/client"
	"github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/logger"
)

func (s *Synchronizer) syncAccountConfig(ctx context.Context, fullSync bool, onlyEssential bool) (updateThresholds bool, err error) {
	_ = onlyEssential

	if fullSync {
		currentConfig, _ := s.option.Cache.CurrentAccountConfig()

		if err := s.agentTypesUpdateList(); err != nil {
			return false, err
		}

		if err := s.accountConfigUpdateList(); err != nil {
			return false, err
		}

		if err := s.agentConfigUpdateList(); err != nil {
			return false, err
		}

		newConfig, ok := s.option.Cache.CurrentAccountConfig()
		if ok && s.option.UpdateConfigCallback != nil {
			hasChanged := !reflect.DeepEqual(currentConfig, newConfig)
			nameHasChanged := currentConfig.Name != newConfig.Name

			if s.currentConfigNotified != newConfig.ID {
				hasChanged = true
				nameHasChanged = true
			}

			if hasChanged {
				s.option.UpdateConfigCallback(ctx, nameHasChanged)
			}
		}

		s.currentConfigNotified = newConfig.ID

		// Set suspended mode if it changed.
		if s.suspendedMode != newConfig.Suspended {
			s.option.SetBleemeoInSuspendedMode(newConfig.Suspended)
			s.suspendedMode = newConfig.Suspended
		}
	}

	return false, nil
}

func (s *Synchronizer) agentTypesUpdateList() error {
	params := url.Values{
		"fields": {"id,name,display_name"},
	}

	iter := s.client.Iterator(bleemeo.ResourceAgentType, params)

	count, err := iter.Count(s.ctx)
	if err != nil {
		return err
	}

	agentTypes := make([]types.AgentType, 0, count)

	for iter.Next(s.ctx) {
		var agentType types.AgentType

		if err = json.Unmarshal(iter.At(), &agentType); err != nil {
			continue
		}

		agentTypes = append(agentTypes, agentType)
	}

	if iter.Err() != nil {
		return iter.Err()
	}

	s.option.Cache.SetAgentTypes(agentTypes)

	return nil
}

func (s *Synchronizer) accountConfigUpdateList() error {
	params := url.Values{
		"fields": {"id,name,live_process_resolution,live_process,docker_integration,snmp_integration,vsphere_integration,number_of_custom_metrics,suspended"},
	}

	iter := s.client.Iterator(bleemeo.ResourceAccountConfig, params)

	count, err := iter.Count(s.ctx)
	if err != nil {
		return err
	}

	configs := make([]types.AccountConfig, 0, count)

	for iter.Next(s.ctx) {
		var config types.AccountConfig

		if err = json.Unmarshal(iter.At(), &config); err != nil {
			continue
		}

		configs = append(configs, config)
	}

	if iter.Err() != nil {
		return iter.Err()
	}

	s.option.Cache.SetAccountConfigs(configs)

	return nil
}

func (s *Synchronizer) agentConfigUpdateList() error {
	params := url.Values{
		"fields": {"id,account_config,agent_type,metrics_allowlist,metrics_resolution"},
	}

	iter := s.client.Iterator("agentconfig", params)

	count, err := iter.Count(s.ctx)
	if apiErr, ok := err.(client.APIError); ok {
		mediatype, _, err := mime.ParseMediaType(apiErr.ContentType)
		if err == nil && mediatype == "text/html" && strings.Contains(apiErr.FinalURL, "login") {
			logger.V(2).Printf("Bleemeo API don't support AgentConfig")
			s.option.Cache.SetAgentConfigs(nil)

			return nil
		}
	}

	if err != nil {
		return err
	}

	configs := make([]types.AgentConfig, 0, count)

	for iter.Next(s.ctx) {
		var config types.AgentConfig

		if err = json.Unmarshal(iter.At(), &config); err != nil {
			continue
		}

		configs = append(configs, config)
	}

	if iter.Err() != nil {
		return iter.Err()
	}

	s.option.Cache.SetAgentConfigs(configs)

	return nil
}
