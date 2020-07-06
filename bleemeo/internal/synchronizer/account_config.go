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
	"fmt"

	"glouton/bleemeo/types"
)

func (s *Synchronizer) getAccountConfig(uuid string) (config types.AccountConfig, err error) {
	params := map[string]string{
		"fields": "id,name,metrics_agent_whitelist,metrics_agent_resolution,metrics_monitor_resolution,live_process_resolution,docker_integration",
	}

	_, err = s.client.Do("GET", fmt.Sprintf("v1/accountconfig/%s/", uuid), params, nil, &config)
	if err != nil {
		return
	}

	return
}

// We assume the numbers of account configs (<10) to be low enough that it is acceptable to reload
// every single one when adding/removing a monitor.
func (s *Synchronizer) updateAccountConfigsFromList(uuids []string) error {
	configs := make(map[string]types.AccountConfig, len(uuids))

	defer func() {
		s.option.Cache.SetAccountConfigs(configs)
	}()

	for _, uuid := range uuids {
		// We already loaded this config in a previous iteration of this loop, let's not do it again
		if _, present := configs[uuid]; present {
			continue
		}

		ac, err := s.getAccountConfig(uuid)
		if err != nil {
			return err
		}

		configs[uuid] = ac
	}

	return nil
}
