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
	"glouton/bleemeo/types"
	"reflect"
)

func (s *Synchronizer) syncAccountConfig(fullSync bool, onlyEssential bool) error {
	if fullSync {
		currentConfig := s.option.Cache.CurrentAccountConfig()

		if err := s.accoungConfigUpdateList(); err != nil {
			return err
		}

		newConfig := s.option.Cache.CurrentAccountConfig()
		if !reflect.DeepEqual(currentConfig, newConfig) && s.option.UpdateConfigCallback != nil {
			s.option.UpdateConfigCallback()
		}
	}

	return nil
}

func (s *Synchronizer) accoungConfigUpdateList() error {
	params := map[string]string{
		"fields": "id,name,metrics_agent_whitelist,metrics_agent_resolution,metrics_monitor_resolution,live_process_resolution,live_process,docker_integration,snmp_integration",
	}

	result, err := s.client.Iter(s.ctx, "accountconfig", params)
	if err != nil {
		return err
	}

	configs := make([]types.AccountConfig, len(result))

	for i, jsonMessage := range result {
		var config types.AccountConfig

		if err := json.Unmarshal(jsonMessage, &config); err != nil {
			continue
		}

		configs[i] = config
	}

	s.option.Cache.SetAccountConfigs(configs)

	return nil
}
