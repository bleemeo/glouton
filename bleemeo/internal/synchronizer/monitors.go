// Copyright 2015-2025 Bleemeo
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
	"errors"
	"fmt"
	"time"

	"github.com/bleemeo/bleemeo-go"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/prometheus/exporter/blackbox"
	gloutonTypes "github.com/bleemeo/glouton/types"

	"github.com/cespare/xxhash/v2"
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

// MonitorUpdate represents an operation to execute on a monitor.
type MonitorUpdate struct {
	op   monitorOperation
	uuid string
}

// syncMonitors updates the list of monitors accessible to the agent.
func (s *Synchronizer) syncMonitors(ctx context.Context, syncType types.SyncType, execution types.SynchronizationExecution) (updateThresholds bool, err error) {
	if !s.option.Config.Blackbox.Enable {
		// prevent a tiny memory leak
		s.pendingMonitorsUpdate = nil

		return false, nil
	}

	s.l.Lock()

	pendingMonitorsUpdate := s.pendingMonitorsUpdate
	s.pendingMonitorsUpdate = nil

	s.l.Unlock()

	// 5 is definitely a random heuristic, but we consider more than five simultaneous updates as more
	// costly that a single full sync, due to the cost of updateMonitorManager()
	if len(pendingMonitorsUpdate) > 5 {
		syncType = types.SyncTypeForceCacheRefresh
		// force metric synchronization
		execution.RequestSynchronization(types.EntityMetric, false)
	}

	if syncType != types.SyncTypeForceCacheRefresh && len(pendingMonitorsUpdate) == 0 {
		return false, nil
	}

	var monitors []bleemeoTypes.Monitor

	apiClient := execution.BleemeoAPIClient()

	if syncType == types.SyncTypeForceCacheRefresh {
		monitors, err = apiClient.ListMonitors(ctx)
		if err != nil {
			return false, err
		}
	} else {
		monitors, err = s.getListOfMonitorsFromAPI(ctx, execution, pendingMonitorsUpdate)
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
		} else if _, ok := cfg.AgentConfigByName[bleemeo.AgentType_Monitor]; !ok {
			needConfigUpdate = true

			break
		}
	}

	if needConfigUpdate {
		execution.RequestSynchronization(types.EntityAccountConfig, true)
	}

	return false, s.ApplyMonitorUpdate()
}

func hashAgentID(agentID string) uint64 {
	// The "@bleemeo.com" suffix is here to produce good jitter with existing default probe's agent ID.
	return xxhash.Sum64String(agentID + "@bleemeo.com")
}

func applyJitterToMonitorCreationDate(monitor bleemeoTypes.Monitor, agentIDHash uint64) (time.Time, error) {
	creationDate, err := time.Parse(time.RFC3339, monitor.CreationDate)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid created_at: %w", err)
	}

	creationDateBase := creationDate.Truncate(time.Minute)
	millisecondInMinute := (uint64(creationDate.UnixMilli()) + agentIDHash) % 45000
	jitterCreationDate := creationDateBase.Add(time.Duration(millisecondInMinute) * time.Millisecond)

	return jitterCreationDate, nil
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
	processedMonitors := make([]gloutonTypes.Monitor, 0, len(monitors))
	agentIDHash := hashAgentID(s.agentID)

	for _, monitor := range monitors {
		// try to retrieve the account config associated with this monitor
		conf, present := accountConfigs[monitor.AccountConfig]
		if !present {
			return fmt.Errorf("%w '%s' for probe '%s'", errMissingAccountConf, monitor.AccountConfig, monitor.URL)
		}

		jitterCreationDate, err := applyJitterToMonitorCreationDate(monitor, agentIDHash)
		if err != nil {
			logger.V(1).Printf("Ignore monitor %s (id=%s): %s", monitor.URL, monitor.ID, err)

			continue
		}

		processedMonitors = append(processedMonitors, gloutonTypes.Monitor{
			ID:                      monitor.ID,
			MetricMonitorResolution: conf.AgentConfigByName[bleemeo.AgentType_Monitor].MetricResolution,
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

// should we try to modify as much monitors as possible, and return a list of errors, instead of failing early ?
func (s *Synchronizer) getListOfMonitorsFromAPI(ctx context.Context, execution types.SynchronizationExecution, pendingMonitorsUpdate []MonitorUpdate) ([]bleemeoTypes.Monitor, error) {
	currentMonitors := s.option.Cache.Monitors()
	forceSync := execution.IsSynchronizationRequested(types.EntityMetric)
	apiClient := execution.BleemeoAPIClient()

OuterBreak:
	for _, m := range pendingMonitorsUpdate {
		if m.op == Delete {
			currentMonitors = deleteMonitorByUUID(currentMonitors, m.uuid)

			continue
		}

		result, err := apiClient.GetMonitorByID(ctx, m.uuid)
		if err != nil {
			// Delete the monitor locally if it was not found on the API.
			if IsNotFound(err) {
				currentMonitors = deleteMonitorByUUID(currentMonitors, m.uuid)

				continue
			}

			return nil, err
		}

		if m.op == Change {
			for k, v := range currentMonitors {
				if v.ID == m.uuid {
					// syncing metrics is necessary when an account in which there is a probe is downgraded to a plan with
					// a stricter metric allowlist, as we need to stop using the metrics in our cache, as they were deleted by the API.
					// We could trigger metrics synchronisation even less often, by checking if the linked account config really changed,
					// but that would required to compare unordered lists or to do some complex machinery, and I'm not sure it's worth
					// the added complexity.
					if !forceSync {
						execution.RequestSynchronization(types.EntityMetric, false)

						forceSync = true
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
