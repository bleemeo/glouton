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

type MonitorOperation int

const (
	Change MonitorOperation = iota
	Add
	Delete
)

type MonitorUpdate struct {
	op   MonitorOperation
	uuid string
}

func (s *Synchronizer) UpdateMonitor(op string, uuid string) {
	s.l.Lock()
	defer s.l.Unlock()

	mu := MonitorUpdate{uuid: uuid}

	switch op {
	case "add":
		mu.op = Add
	case "change":
		mu.op = Change
	case "delete":
		mu.op = Delete
	}

	s.pendingMonitorsUpdate = append(s.pendingMonitorsUpdate, mu)
	s.forceSync["monitors"] = true
}

// syncMonitors updates the list of monitors accessible to the agent.
func (s *Synchronizer) syncMonitors(fullSync bool) error {
	bbEnabled := s.option.Config.Bool("blackbox.enabled") && s.option.Config.Bool("bleemeo.remote_probing_enabled")
	if !bbEnabled {
		return nil
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
	s.l.Lock()
	s.pendingMonitorsUpdate = nil
	s.l.Unlock()

	s.option.Cache.SetMonitors(apiMonitors)

	return s.updateMonitorManager(apiMonitors)
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
		} else if m.op == Add {
			// when we add a monitor, we always check if it isn't already there, in the unlikely
			// eventuality that getMonitorsFromAPI() was called after the creation of the new
			// monitor, but before this function was called.
			for _, v := range currentMonitors {
				if v.ID == m.uuid {
					continue OuterBreak
				}
			}
		}

		var result bleemeoTypes.Monitor
		statusCode, err := s.client.Do("GET", fmt.Sprintf("v1/service/%s/", m.uuid), nil, nil, &result)
		if err != nil {
			return err
		}

		if m.op == Add {
			currentMonitors = append(currentMonitors, result)
		} else if m.op == Change {
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

	return s.updateMonitorManager(currentMonitors)
}

func (s *Synchronizer) updateMonitorManager(monitors []bleemeoTypes.Monitor) error {
	if s.option.MonitorManager == nil {
		logger.V(2).Println("blackbox_exporter is not configured in the synchronizer")
		return nil
	}

	// refresh blackbox collectors to meet the new configuration
	if err := s.option.MonitorManager.UpdateDynamicTargets(monitors); err != nil {
		logger.V(1).Printf("Could not update blackbox_exporter")
		return err
	}

	return nil
}

func (s *Synchronizer) getMonitorsFromAPI() ([]bleemeoTypes.Monitor, error) {
	params := map[string]string{
		"monitor": "true",
		"active":  "true",
		"fields":  "id,agent,monitor_url,monitor_expected_content,monitor_expected_response_code,monitor_unexpected_content,monitor_metric_resolution_seconds",
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
