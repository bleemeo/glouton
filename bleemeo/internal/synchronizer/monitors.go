// Copyright 2015-2026 Bleemeo
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

const (
	// monitorsRetryDelay is the delay before the first retry of a monitor synchronization which failed.
	// It doubles on each new failure, up to monitorsMaxRetryDelay.
	monitorsRetryDelay    = time.Minute
	monitorsMaxRetryDelay = 15 * time.Minute
	// monitorUpdateMaxAttempts is the number of attempts of a monitor update before giving up on it,
	// so a permanent error doesn't retry it forever. The next full synchronization applies it anyway.
	monitorUpdateMaxAttempts = 5
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
	// attempts is the number of attempts which failed.
	attempts int
}

// syncMonitors updates the list of monitors accessible to the agent.
func (s *Synchronizer) syncMonitors(ctx context.Context, syncType types.SyncType, execution types.SynchronizationExecution) (updateThresholds bool, err error) {
	if !s.option.Config.Blackbox.Enable {
		// prevent a tiny memory leak
		s.l.Lock()
		s.pendingMonitorsUpdate = nil
		s.l.Unlock()

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

	// updatesErr is returned once the monitors which could be updated are applied.
	var updatesErr error

	if syncType == types.SyncTypeForceCacheRefresh {
		monitors, err = apiClient.ListMonitors(ctx)
		if err != nil {
			// The pending updates aren't kept, the full synchronization retried covers them.
			s.retryMonitorsFullSync()

			return false, err
		}

		// The monitors are still applied, with their previous options.
		if err := applyMonitorObjectsFromAPI(ctx, apiClient, "", monitors, s.option.Cache.Monitors()); err != nil {
			s.retryMonitorsFullSync()
		} else {
			s.l.Lock()
			s.monitorsFullSyncFailures = 0
			s.l.Unlock()
		}
	} else {
		var failedUpdates []MonitorUpdate

		monitors, failedUpdates, updatesErr = s.getListOfMonitorsFromAPI(ctx, execution, pendingMonitorsUpdate)
		s.retryMonitorsUpdate(failedUpdates)
	}

	s.option.Cache.SetMonitors(monitors)

	needConfigUpdate := false
	probeConfigs := s.option.Cache.ProbeConfigByAccountID()

	for _, m := range monitors {
		if _, ok := probeConfigs[m.Account]; !ok {
			needConfigUpdate = true

			break
		}
	}

	if needConfigUpdate {
		execution.RequestSynchronization(types.EntityConfig, true)
	}

	return false, errors.Join(updatesErr, s.ApplyMonitorUpdate())
}

// monitorsRetryDelayAfter returns the delay before retrying after the given number of failed attempts.
func monitorsRetryDelayAfter(attempts int) time.Duration {
	delay := monitorsRetryDelay

	for range attempts - 1 {
		delay *= 2
		if delay >= monitorsMaxRetryDelay {
			return monitorsMaxRetryDelay
		}
	}

	return delay
}

// retryMonitorsUpdate puts back the monitor updates which couldn't be applied, before the ones
// received meanwhile, and retries them later. Without it, they would be lost until the next full
// synchronization, which can be hours later. An update is given up after monitorUpdateMaxAttempts.
func (s *Synchronizer) retryMonitorsUpdate(updates []MonitorUpdate) {
	retries := make([]MonitorUpdate, 0, len(updates))
	maxAttempts := 0

	for _, update := range updates {
		update.attempts++

		if update.attempts >= monitorUpdateMaxAttempts {
			logger.V(1).Printf(
				"Giving up the update of the monitor %s after %d attempts, the next full synchronization will apply it",
				update.uuid, update.attempts,
			)

			continue
		}

		retries = append(retries, update)
		maxAttempts = max(maxAttempts, update.attempts)
	}

	if len(retries) == 0 {
		return
	}

	s.l.Lock()
	defer s.l.Unlock()

	s.pendingMonitorsUpdate = append(retries, s.pendingMonitorsUpdate...)
	s.requestLaterSynchronizationLocked(types.EntityMonitor, false, monitorsRetryDelayAfter(maxAttempts))
}

// retryMonitorsFullSync retries a full synchronization of the monitors which failed, even when no update
// is pending, so the monitors changes which aren't notified (e.g. their settings) aren't delayed until the
// next scheduled full synchronization.
func (s *Synchronizer) retryMonitorsFullSync() {
	s.l.Lock()
	defer s.l.Unlock()

	s.monitorsFullSyncFailures++
	s.requestLaterSynchronizationLocked(types.EntityMonitor, true, monitorsRetryDelayAfter(s.monitorsFullSyncFailures))
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

	probeConfigs := s.option.Cache.ProbeConfigByAccountID()
	processedMonitors := make([]gloutonTypes.Monitor, 0, len(monitors))
	agentIDHash := hashAgentID(s.agentID)

	for _, monitor := range monitors {
		// try to retrieve the probe config associated with this monitor's account
		conf, present := probeConfigs[monitor.Account]
		if !present {
			return fmt.Errorf("%w for account '%s' of probe '%s'", errMissingAccountConf, monitor.Account, monitor.URL)
		}

		jitterCreationDate, err := applyJitterToMonitorCreationDate(monitor, agentIDHash)
		if err != nil {
			logger.V(1).Printf("Ignore monitor %s (id=%s): %s", monitor.URL, monitor.ID, err)

			continue
		}

		processedMonitors = append(processedMonitors, gloutonTypes.Monitor{
			ID:                      monitor.ID,
			MetricMonitorResolution: conf.MetricResolution,
			CreationDate:            jitterCreationDate,
			URL:                     monitor.URL,
			BleemeoAgentID:          monitor.AgentID,
			ExpectedContent:         monitor.ExpectedContent,
			ExpectedResponseCode:    monitor.ExpectedResponseCode,
			ForbiddenContent:        monitor.ForbiddenContent,
			CAFile:                  monitor.CAFile,
			Headers:                 monitor.Headers,
			UseHead:                 monitor.UseHead,
			IsPublicProbe:           monitor.Account != s.option.Cache.AccountID(),
		})
	}

	// refresh blackbox collectors to meet the new configuration
	if err := s.option.MonitorManager.UpdateDynamicTargets(processedMonitors); err != nil {
		logger.V(1).Printf("Could not update blackbox_exporter")

		return err
	}

	return nil
}

// getListOfMonitorsFromAPI applies the pending updates on the current monitors. An update which fails
// doesn't prevent applying the other ones: it's returned in failedUpdates, with its error in err.
func (s *Synchronizer) getListOfMonitorsFromAPI(
	ctx context.Context,
	execution types.SynchronizationExecution,
	pendingMonitorsUpdate []MonitorUpdate,
) (monitors []bleemeoTypes.Monitor, failedUpdates []MonitorUpdate, err error) {
	var errs []error

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

			failedUpdates = append(failedUpdates, m)
			errs = append(errs, err)

			continue
		}

		// An empty agent would list all the Monitor objects.
		if result.AgentID != "" {
			updated := []bleemeoTypes.Monitor{result}

			// The monitor is still updated, with its previous options, and the update is retried for its options.
			if err := applyMonitorObjectsFromAPI(ctx, apiClient, result.AgentID, updated, currentMonitors); err != nil {
				failedUpdates = append(failedUpdates, m)
				errs = append(errs, err)
			}

			result = updated[0]
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

	return currentMonitors, failedUpdates, errors.Join(errs...)
}

// applyMonitorObjectsFromAPI reads the Monitor objects of the given agent (all the active ones when agentID
// is empty) and applies their options on the monitors. These options aren't worth failing the monitors
// synchronization, which would delay the other changes: on error, the monitors keep their options from the
// previous monitors (with the same ID), and the error is returned so the caller retries later.
func applyMonitorObjectsFromAPI(
	ctx context.Context,
	apiClient types.MonitorClient,
	agentID string,
	monitors []bleemeoTypes.Monitor,
	previousMonitors []bleemeoTypes.Monitor,
) error {
	objects, err := apiClient.ListMonitorObjects(ctx, agentID)
	if err != nil {
		logger.V(1).Printf("Unable to read the Monitor objects, the monitors keep their previous options: %v", err)

		previousByID := make(map[string]bleemeoTypes.Monitor, len(previousMonitors))
		for _, monitor := range previousMonitors {
			previousByID[monitor.ID] = monitor
		}

		for i := range monitors {
			monitors[i].UseHead = previousByID[monitors[i].ID].UseHead
		}

		return err
	}

	applyMonitorObjects(monitors, objects)

	return nil
}

// applyMonitorObjects sets on the monitors, read from their service, the options which are only
// exposed on the Monitor objects. A monitor and its Monitor object have the same agent.
// A monitor without a Monitor object gets the default options (use_head false).
func applyMonitorObjects(monitors []bleemeoTypes.Monitor, objects []bleemeoTypes.MonitorObject) {
	objectByAgent := make(map[string]bleemeoTypes.MonitorObject, len(objects))
	for _, object := range objects {
		objectByAgent[object.AgentID] = object
	}

	for i := range monitors {
		monitors[i].UseHead = objectByAgent[monitors[i].AgentID].UseHead
	}
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
