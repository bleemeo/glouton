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
	bleemeoTypes "glouton/bleemeo/types"
	"glouton/logger"
)

// syncMonitors updates the list of monitors accessible to the agent.
func (s *Synchronizer) syncMonitors(fullSync bool) error {
	bbEnabled := s.option.Config.Bool("blackbox.enabled")
	if !bbEnabled {
		return nil
	}

	if !fullSync {
		return nil
	}

	apiMonitors, err := s.getMonitorsFromAPI()
	if err != nil {
		return err
	}

	s.option.Cache.SetMonitors(apiMonitors)

	if s.option.MonitorManager == nil {
		logger.V(2).Println("blackbox_exporter is not configured in the synchronizer")
		return nil
	}

	// refresh blackbox collectors to meet the new configuration
	if err := s.option.MonitorManager.UpdateDynamicTargets(apiMonitors); err != nil {
		logger.V(1).Printf("Could not update blackbox_exporter")
		return err
	}

	return nil
}

func (s *Synchronizer) getMonitorsFromAPI() ([]bleemeoTypes.Monitor, error) {
	params := map[string]string{
		"monitor": "true",
		"active":  "true",
		"fields":  "id,agent,monitor_url,monitor_expected_content,monitor_expected_response_code,monitor_unexpected_content",
	}

	result, err := s.client.Iter("service", params)
	if err != nil {
		return nil, err
	}

	monitors := make([]bleemeoTypes.Monitor, 0, len(result))

	for _, jsonMessage := range result {
		var monitor bleemeoTypes.Monitor

		if err := json.Unmarshal(jsonMessage, &monitor); err != nil {
			return nil, fmt.Errorf("couldn't parse monitor %v", jsonMessage)
		}

		monitors = append(monitors, monitor)
	}

	return monitors, nil
}
