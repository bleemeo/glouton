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

// Package syncmonitors reads the monitors from Bleemeo, and gives them to the blackbox prober.
package syncmonitors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/bleemeo/glouton/bleemeo/internal/cache"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/prometheus/exporter/blackbox"
	gloutonTypes "github.com/bleemeo/glouton/types"

	"github.com/cespare/xxhash/v2"
)

var errMissingAccountConf = errors.New("missing account configuration")

const monitorsSyncRetryDelay = time.Minute

type monitorOperation int

const (
	// change allows to add or update a monitor.
	change monitorOperation = iota
	// remove specifies the monitor should be deleted.
	remove
)

type monitorUpdate struct {
	op   monitorOperation
	uuid string
}

// SyncMonitors reads the monitors from the API, all of them on a cache refresh, or else the ones
// notified as changed, and gives them to the blackbox prober.
type SyncMonitors struct {
	cache          *cache.Cache
	state          bleemeoTypes.State
	monitorManager bleemeoTypes.MonitorManager

	// l protects pending: UpdateMonitor is called from the MQTT goroutine.
	l       sync.Mutex
	pending []monitorUpdate
}

// New returns the entity. monitorManager gets the monitors to probe.
func New(c *cache.Cache, state bleemeoTypes.State, monitorManager bleemeoTypes.MonitorManager) *SyncMonitors {
	return &SyncMonitors{
		cache:          c,
		state:          state,
		monitorManager: monitorManager,
	}
}

func (s *SyncMonitors) Name() types.EntityName {
	return types.EntityMonitor
}

func (s *SyncMonitors) EnabledInMaintenance() bool {
	return false
}

func (s *SyncMonitors) EnabledInSuspendedMode() bool {
	return false
}

func (s *SyncMonitors) PrepareExecution(_ context.Context, execution types.SynchronizationExecution) (types.EntitySynchronizerExecution, error) {
	return &syncMonitorsExecution{
		parent:    s,
		execution: execution,
	}, nil
}

// UpdateMonitor records an update of the monitor uuid, to read on the next synchronization.
// op is "change", for an addition or a change, or "delete".
func (s *SyncMonitors) UpdateMonitor(op string, uuid string) {
	s.l.Lock()
	defer s.l.Unlock()

	mu := monitorUpdate{uuid: uuid}

	switch op {
	case "change":
		mu.op = change
	case "delete":
		mu.op = remove
	}

	s.pending = append(s.pending, mu)
}

func (s *SyncMonitors) DiagnosticArchive(_ context.Context, archive gloutonTypes.ArchiveWriter) error {
	file, err := archive.Create("bleemeo-sync-monitors.json")
	if err != nil {
		return err
	}

	s.l.Lock()

	obj := struct {
		PendingMonitorsUpdateCount int
	}{
		PendingMonitorsUpdateCount: len(s.pending),
	}

	s.l.Unlock()

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")

	return enc.Encode(obj)
}

func (s *SyncMonitors) popPending() []monitorUpdate {
	s.l.Lock()
	defer s.l.Unlock()

	pending := s.pending
	s.pending = nil

	return pending
}

// requeue puts back updates which failed, before the ones received since.
func (s *SyncMonitors) requeue(updates []monitorUpdate) {
	s.l.Lock()
	defer s.l.Unlock()

	s.pending = append(slices.Clone(updates), s.pending...)
}

type syncMonitorsExecution struct {
	parent    *SyncMonitors
	execution types.SynchronizationExecution
}

func (e *syncMonitorsExecution) NeedSynchronization(_ context.Context) (bool, error) {
	// The monitor updates request their synchronization when they're received.
	return false, nil
}

func (e *syncMonitorsExecution) RefreshCache(_ context.Context, _ types.SyncType) error {
	// The monitors are read by SyncRemoteAndLocal, with the updates they apply: the updates are
	// only taken when the monitors are synchronized.
	return nil
}

func (e *syncMonitorsExecution) SyncRemoteAndLocal(ctx context.Context, syncType types.SyncType) error {
	pending := e.parent.popPending()

	if !e.execution.Option().Config.Blackbox.Enable {
		// The updates are dropped: nothing probes the monitors.
		return nil
	}

	// 5 is definitely a random heuristic, but we consider more than five simultaneous updates as more
	// costly that a single full sync, due to the cost of updateMonitorManager()
	if len(pending) > 5 {
		syncType = types.SyncTypeForceCacheRefresh
		// force metric synchronization
		e.execution.RequestSynchronization(types.EntityMetric, false)
	}

	if syncType != types.SyncTypeForceCacheRefresh && len(pending) == 0 {
		return nil
	}

	var (
		monitors []bleemeoTypes.Monitor
		err      error
	)

	apiClient := e.execution.BleemeoAPIClient()

	if syncType == types.SyncTypeForceCacheRefresh {
		monitors, err = apiClient.ListMonitors(ctx)
	} else {
		monitors, err = applyUpdates(ctx, apiClient, e.parent.cache.Monitors(), pending)
	}

	if err != nil {
		e.parent.requeue(pending)
		e.execution.RequestLaterSynchronization(types.EntityMonitor, syncType == types.SyncTypeForceCacheRefresh, monitorsSyncRetryDelay)

		return err
	}

	e.parent.cache.SetMonitors(monitors)

	probeConfigs := e.parent.cache.ProbeConfigByAccountID()

	for _, m := range monitors {
		if _, ok := probeConfigs[m.Account]; !ok {
			e.execution.RequestSynchronization(types.EntityConfig, true)

			break
		}
	}

	return e.parent.ApplyMonitorUpdate()
}

func (e *syncMonitorsExecution) FinishExecution(_ context.Context) {
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
func (s *SyncMonitors) ApplyMonitorUpdate() error {
	if s.monitorManager == nil || s.monitorManager == (*blackbox.RegisterManager)(nil) {
		logger.V(2).Println("blackbox_exporter is not configured, ApplyMonitorUpdate will not update its config.")

		return nil
	}

	monitors := s.cache.Monitors()

	probeConfigs := s.cache.ProbeConfigByAccountID()
	processedMonitors := make([]gloutonTypes.Monitor, 0, len(monitors))

	var agentID string

	if s.state != nil {
		agentID, _ = s.state.BleemeoCredentials()
	}

	agentIDHash := hashAgentID(agentID)

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
			HeadRequest:             monitor.HeadRequest,
			IsPublicProbe:           monitor.Account != s.cache.AccountID(),
		})
	}

	// refresh blackbox collectors to meet the new configuration
	if err := s.monitorManager.UpdateDynamicTargets(processedMonitors); err != nil {
		logger.V(1).Printf("Could not update blackbox_exporter")

		return err
	}

	return nil
}

// applyUpdates returns currentMonitors with the updates applied: a changed monitor is read from
// the API, a deleted one removed.
// A change doesn't request the metrics synchronization: they are linked to the monitors, and run
// after them in the same execution.
func applyUpdates(ctx context.Context, apiClient types.MonitorClient, currentMonitors []bleemeoTypes.Monitor, updates []monitorUpdate) ([]bleemeoTypes.Monitor, error) {
OuterBreak:
	for _, m := range updates {
		if m.op == remove {
			currentMonitors = deleteMonitorByUUID(currentMonitors, m.uuid)

			continue
		}

		result, err := apiClient.GetMonitorByID(ctx, m.uuid)
		if err != nil {
			// Delete the monitor locally if it was not found on the API.
			if types.IsNotFound(err) {
				currentMonitors = deleteMonitorByUUID(currentMonitors, m.uuid)

				continue
			}

			return nil, err
		}

		// The API notifies a deactivation as a change, and returns inactive monitors by ID:
		// stop probing it like a full synchronization would (it only lists active monitors).
		if !result.Active {
			currentMonitors = deleteMonitorByUUID(currentMonitors, m.uuid)

			continue
		}

		if m.op == change {
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
