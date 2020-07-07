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
	"glouton/types"
)

type MonitorOperation int

const (
	// Change allows to add or update a monitor
	Change MonitorOperation = iota
	Delete
)

type MonitorUpdate struct {
	op   MonitorOperation
	uuid string
}

// UpdateMonitor requests to update a monitor, identified by its UUID. It allows for adding, updating and removing a monitor.
func (s *Synchronizer) UpdateMonitor(op string, uuid string) {
	s.l.Lock()
	defer s.l.Unlock()

	mu := MonitorUpdate{uuid: uuid}

	switch op {
	case "change":
		mu.op = Change
	case "delete":
		mu.op = Delete
	}

	s.pendingMonitorsUpdate = append(s.pendingMonitorsUpdate, mu)
	s.forceSync["monitors"] = true

	if mu.op == Change {
		// syncing metrics is necessary when an account in which there is a probe is downgraded to a plan with
		// a stricter metric whitelist, as we need to stop using the metrics in our cache, as they were deleted by the API.
		s.forceSync["metrics"] = true
	}
}

// syncMonitors updates the list of monitors accessible to the agent.
func (s *Synchronizer) syncMonitors(fullSync bool) error {
	bbEnabled := s.option.Config.Bool("blackbox.enabled") && s.option.Config.Bool("bleemeo.remote_probing_enabled")
	if !bbEnabled {
		// prevent a tiny memory leak
		s.pendingMonitorsUpdate = nil
		return nil
	}

	s.l.Lock()
	defer s.l.Unlock()

	// 5 is definitely a random heuristic, but we consider more than five simultaneous updates as more
	// costly that a single full sync, due to the cost of updateMonitorManager()
	if len(s.pendingMonitorsUpdate) > 5 {
		fullSync = true
	}

	// only perform partial updates if we have some and we are not going to refresh all monitors
	// immediately after
	if !fullSync && len(s.pendingMonitorsUpdate) > 0 {
		return s.syncListOfMonitors()
	}

	if !fullSync {
		return nil
	}

	apiMonitors, err := s.getMonitorsFromAPI()
	if err != nil {
		return err
	}

	// we did a full sync, that includes every pending monitor update
	s.pendingMonitorsUpdate = nil

	s.option.Cache.SetMonitors(apiMonitors)

	return s.ApplyMonitorUpdate(true)
}

// should we try to modify as much monitors as possible, and return a list of errors, instead of failing early ?
func (s *Synchronizer) syncListOfMonitors() error {
	s.l.Lock()
	defer s.l.Unlock()

	currentMonitors := s.option.Cache.Monitors()

OuterBreak:
	for _, m := range s.pendingMonitorsUpdate {
		if m.op == Delete {
			for k, v := range currentMonitors {
				if v.ID == m.uuid {
					// the order doesn't matter, so we perform a "fast" (no reallocation
					// nor moving numerous elements) deletion
					currentMonitors[k] = currentMonitors[len(currentMonitors)-1]
					currentMonitors = currentMonitors[:len(currentMonitors)-1]
					continue OuterBreak
				}
			}
			// not found, but that's not really an issue, we have the desired state: this monitor
			// is not probed
			continue
		}

		var result bleemeoTypes.Monitor
		statusCode, err := s.client.Do("GET", fmt.Sprintf("v1/service/%s/", m.uuid), nil, nil, &result)
		if err != nil {
			return err
		}

		if m.op == Change {
			// we couldn't fetch that object ? let's skip it
			if statusCode < 200 || statusCode >= 300 {
				logger.V(2).Printf("probes: couldn't update service '%s', got HTTP %d", m.uuid, statusCode)
				continue
			}

			for k, v := range currentMonitors {
				if v.ID == m.uuid {
					currentMonitors[k] = result
					continue OuterBreak
				}
			}

			// not found ? let's add it
			currentMonitors = append(currentMonitors, result)
		}
	}

	s.pendingMonitorsUpdate = nil

	s.option.Cache.SetMonitors(currentMonitors)

	return s.ApplyMonitorUpdate(true)
}

func (s *Synchronizer) ApplyMonitorUpdate(forceAccountConfigsReload bool) error {
	monitors := s.option.Cache.Monitors()

	if forceAccountConfigsReload {
		// get the list of needed account configurations
		uuids := make([]string, 0, len(monitors))

		for _, m := range monitors {
			uuids = append(uuids, m.AccountConfig)
		}

		if err := s.updateAccountConfigsFromList(uuids); err != nil {
			return err
		}
	}

	if s.option.MonitorManager == nil {
		logger.V(2).Println("blackbox_exporter is not configured in the synchronizer")
		return nil
	}

	accountConfigs := s.option.Cache.AccountConfigs()

	processedMonitors := make([]types.Monitor, 0, len(monitors))

	for _, monitor := range monitors {
		// try to retrieve the account config associated with this monitor
		conf, present := accountConfigs[monitor.AccountConfig]
		if !present {
			return fmt.Errorf("missing account configuration '%s' for probe '%s'", monitor.AccountConfig, monitor.URL)
		}

		processedMonitors = append(processedMonitors, types.Monitor{
			ID:                      monitor.ID,
			MetricMonitorResolution: conf.MetricMonitorResolution,
			CreationDate:            monitor.CreationDate,
			URL:                     monitor.URL,
			BleemeoAgentID:          monitor.AgentID,
			ExpectedContent:         monitor.ExpectedContent,
			ExpectedResponseCode:    monitor.ExpectedResponseCode,
			ForbiddenContent:        monitor.ForbiddenContent,
		})
	}

	// refresh blackbox collectors to meet the new configuration
	if err := s.option.MonitorManager.UpdateDynamicTargets(processedMonitors); err != nil {
		logger.V(1).Printf("Could not update blackbox_exporter")
		return err
	}

	return nil
}

func (s *Synchronizer) getMonitorsFromAPI() ([]bleemeoTypes.Monitor, error) {
	params := map[string]string{
		"monitor": "true",
		"active":  "true",
		"fields":  "id,account_config,agent,created_at,monitor_url,monitor_expected_content,monitor_expected_response_code,monitor_unexpected_content",
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
