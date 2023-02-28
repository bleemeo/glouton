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
	"errors"
	"fmt"
	"glouton/bleemeo/client"
	bleemeoTypes "glouton/bleemeo/types"
	"glouton/logger"
	"glouton/prometheus/exporter/blackbox"
	"glouton/types"
	"time"

	"github.com/cespare/xxhash"
)

var (
	errMissingAccountConf = errors.New("missing account configuration")
	errCannotParse        = errors.New("couldn't parse monitor")
)

type monitorOperation int

const (
	// Change allows to add or update a monitor.
	Change monitorOperation = iota
	// Delete specifies the monitor should be deleted.
	Delete
)

const fieldList string = "id,account_config,agent,created_at,monitor_url,monitor_expected_content," +
	"monitor_expected_response_code,monitor_unexpected_content,monitor_ca_file,monitor_headers"

// MonitorUpdate represents an operation to execute on a monitor.
type MonitorUpdate struct {
	op   monitorOperation
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
	s.forceSync[syncMethodMonitor] = false
}

// syncMonitors updates the list of monitors accessible to the agent.
func (s *Synchronizer) syncMonitors(ctx context.Context, fullSync bool, onlyEssential bool) (updateThresholds bool, err error) {
	if !s.option.Config.Blackbox.Enable {
		// prevent a tiny memory leak
		s.pendingMonitorsUpdate = nil

		return false, nil
	}

	s.l.Lock()

	pendingMonitorsUpdate := s.pendingMonitorsUpdate
	s.pendingMonitorsUpdate = nil
	// 5 is definitely a random heuristic, but we consider more than five simultaneous updates as more
	// costly that a single full sync, due to the cost of updateMonitorManager()
	if len(pendingMonitorsUpdate) > 5 {
		fullSync = true
		// force metric synchronization
		if _, forceSync := s.forceSync[syncMethodMetric]; !forceSync {
			s.forceSync[syncMethodMetric] = false
		}
	}

	s.l.Unlock()

	if !fullSync && len(pendingMonitorsUpdate) == 0 {
		return false, nil
	}

	var monitors []bleemeoTypes.Monitor

	if fullSync {
		monitors, err = s.getMonitorsFromAPI()
		if err != nil {
			return false, err
		}
	} else {
		monitors, err = s.getListOfMonitorsFromAPI(pendingMonitorsUpdate)
		if err != nil {
			return false, err
		}
	}

	s.option.Cache.SetMonitors(monitors)

	needConfigUpdate := false
	configs := s.option.Cache.AccountConfigsByUUID()

	for _, m := range monitors {
		if cfg, ok := configs[m.AccountConfig]; !ok {
			needConfigUpdate = true

			break
		} else if _, ok := cfg.AgentConfigByName[bleemeoTypes.AgentTypeMonitor]; !ok {
			needConfigUpdate = true

			break
		}
	}

	if needConfigUpdate {
		s.l.Lock()

		s.forceSync[syncMethodAccountConfig] = true

		s.l.Unlock()
	}

	return false, s.ApplyMonitorUpdate()
}

// ApplyMonitorUpdate preprocesses monitors and updates blackbox target list.
// `forceAccountConfigsReload` determine whether account configurations should be updated via the API.
func (s *Synchronizer) ApplyMonitorUpdate() error {
	if s.option.MonitorManager == (*blackbox.RegisterManager)(nil) {
		logger.V(2).Println("blackbox_exporter is not configured, ApplyMonitorUpdate will not update its config.")

		return nil
	}

	monitors := s.option.Cache.Monitors()

	accountConfigs := s.option.Cache.AccountConfigsByUUID()
	processedMonitors := make([]types.Monitor, 0, len(monitors))
	agentIDHash := time.Duration(xxhash.Sum64String(s.agentID)%16000)*time.Millisecond - 8*time.Second

	for _, monitor := range monitors {
		// try to retrieve the account config associated with this monitor
		conf, present := accountConfigs[monitor.AccountConfig]
		if !present {
			return fmt.Errorf("%w '%s' for probe '%s'", errMissingAccountConf, monitor.AccountConfig, monitor.URL)
		}

		creationDate, err := time.Parse(time.RFC3339, monitor.CreationDate)
		if err != nil {
			logger.V(1).Printf("Ignore monitor %s (id=%s) due to invalid created_at: %s", monitor.URL, monitor.ID, monitor.CreationDate)

			continue
		}

		jitterCreationDate := creationDate.Add(agentIDHash)
		if creationDate.Minute() != jitterCreationDate.Minute() {
			// We want to kept the minute unchanged. This is required for monitor with
			// resolution of 5 minutes because Bleemeo assume that the monitor metrics are
			// send at the beginning of the minute after creationDate + N * 5 minutes.
			jitterCreationDate = creationDate.Add(-agentIDHash)
		}

		processedMonitors = append(processedMonitors, types.Monitor{
			ID:                      monitor.ID,
			MetricMonitorResolution: conf.AgentConfigByName[bleemeoTypes.AgentTypeMonitor].MetricResolution,
			CreationDate:            jitterCreationDate,
			URL:                     monitor.URL,
			BleemeoAgentID:          monitor.AgentID,
			ExpectedContent:         monitor.ExpectedContent,
			ExpectedResponseCode:    monitor.ExpectedResponseCode,
			ForbiddenContent:        monitor.ForbiddenContent,
			CAFile:                  monitor.CAFile,
			Headers:                 monitor.Headers,
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
		"fields":  fieldList,
	}

	result, err := s.client.Iter(s.ctx, "service", params)
	if err != nil {
		return nil, err
	}

	monitors := make([]bleemeoTypes.Monitor, 0, len(result))

	for _, jsonMessage := range result {
		var monitor bleemeoTypes.Monitor

		if err := json.Unmarshal(jsonMessage, &monitor); err != nil {
			return nil, fmt.Errorf("%w %v", errCannotParse, jsonMessage)
		}

		monitors = append(monitors, monitor)
	}

	return monitors, nil
}

// should we try to modify as much monitors as possible, and return a list of errors, instead of failing early ?
func (s *Synchronizer) getListOfMonitorsFromAPI(pendingMonitorsUpdate []MonitorUpdate) ([]bleemeoTypes.Monitor, error) {
	params := map[string]string{
		"fields": fieldList,
	}

	currentMonitors := s.option.Cache.Monitors()

	s.l.Lock()

	_, forceSync := s.forceSync[syncMethodMetric]

	s.l.Unlock()

OuterBreak:
	for _, m := range pendingMonitorsUpdate {
		if m.op == Delete {
			currentMonitors = deleteMonitorByUUID(currentMonitors, m.uuid)

			continue
		}

		var result bleemeoTypes.Monitor
		statusCode, err := s.client.Do(s.ctx, "GET", fmt.Sprintf("v1/service/%s/", m.uuid), params, nil, &result)
		if err != nil {
			// Delete the monitor locally if it was not found on the API.
			if client.IsNotFound(err) {
				currentMonitors = deleteMonitorByUUID(currentMonitors, m.uuid)

				continue
			}

			return nil, err
		}

		if m.op == Change {
			// we couldn't fetch that object ? let's skip it
			if statusCode < 200 || statusCode >= 300 {
				logger.V(2).Printf("probes: couldn't update service '%s', got HTTP %d", m.uuid, statusCode)

				continue
			}

			for k, v := range currentMonitors {
				if v.ID == m.uuid {
					// syncing metrics is necessary when an account in which there is a probe is downgraded to a plan with
					// a stricter metric allowlist, as we need to stop using the metrics in our cache, as they were deleted by the API.
					// We could trigger metrics synchronisation even less often, by checking if the linked account config really changed,
					// but that would required to compare unordered lists or to do some complex machinery, and I'm not sure it's worth
					// the added complexity.
					if !forceSync {
						s.l.Lock()
						s.forceSync[syncMethodMetric] = false
						forceSync = true
						s.l.Unlock()
					}

					currentMonitors[k] = result

					continue OuterBreak
				}
			}

			// not found ? let's add it
			currentMonitors = append(currentMonitors, result)
		}
	}

	return currentMonitors, nil
}

func deleteMonitorByUUID(monitors []bleemeoTypes.Monitor, uuid string) []bleemeoTypes.Monitor {
	for i, monitor := range monitors {
		if monitor.ID == uuid {
			// The order doesn't matter, so we perform a "fast" (no reallocation
			// nor moving numerous elements) deletion.
			monitors[i] = monitors[len(monitors)-1]
			monitors = monitors[:len(monitors)-1]

			break
		}
	}

	return monitors
}
