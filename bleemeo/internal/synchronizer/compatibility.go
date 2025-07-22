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
	"sync"
	"time"

	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	"github.com/bleemeo/glouton/facts"
)

// Those state should be moved to each entity-synchronizer as needed.
type synchronizerState struct {
	l sync.Mutex

	currentConfigNotified string
	lastFactUpdatedAt     string
	lastSNMPcount         int
	lastVSphereUpdate     time.Time
	metricRetryAt         time.Time
	lastMetricCount       int
	lastMetricActivation  time.Time

	onDemandDiagnostic synchronizerOnDemandDiagnostic

	// An edge case occurs when an agent is spawned while the maintenance mode is enabled on the backend:
	// the agent cannot register agent_status, thus the MQTT connector cannot start, and we cannot receive
	// notifications to tell us the backend is out of maintenance. So we resort to HTTP polling every 15
	// minutes to check whether we are still in maintenance of not.
	lastMaintenanceSync time.Time

	// configSyncDone is true when the config items were successfully synced.
	configSyncDone bool
}

type CompatibilityWrapper struct {
	name                   types.EntityName
	enabledInMaintenance   bool
	enabledInSuspendedMode bool
	skipOnlyEssential      bool
	method                 func(context.Context, types.SyncType, types.SynchronizationExecution) (updateThresholds bool, err error)
	state                  *synchronizerState
}

type CompatibilityWrapperExecution struct {
	*CompatibilityWrapper

	execution types.SynchronizationExecution
}

func (cw *CompatibilityWrapper) Name() types.EntityName {
	return cw.name
}

func (cw *CompatibilityWrapper) EnabledInMaintenance() bool {
	return cw.enabledInMaintenance
}

func (cw *CompatibilityWrapper) EnabledInSuspendedMode() bool {
	return cw.enabledInSuspendedMode
}

func (cw *CompatibilityWrapper) PrepareExecution(_ context.Context, execution types.SynchronizationExecution) (types.EntitySynchronizerExecution, error) {
	return CompatibilityWrapperExecution{execution: execution, CompatibilityWrapper: cw}, nil
}

func (cw CompatibilityWrapperExecution) FinishExecution(_ context.Context) {
}

// RefreshCache refresh the cache with information from Bleemeo API. This could no nothing
// if the cache is still valid.
// On this wrapper, this does nothing.
func (cw CompatibilityWrapperExecution) RefreshCache(ctx context.Context, syncType types.SyncType) error {
	_ = ctx
	_ = syncType

	return nil
}

// SyncRemoteAndLocal does all actions (RefreshCache and SyncRemoteAndLocal).
func (cw CompatibilityWrapperExecution) SyncRemoteAndLocal(ctx context.Context, syncType types.SyncType) error {
	updateThresholds, err := cw.method(ctx, syncType, cw.execution)

	if updateThresholds {
		cw.execution.RequestUpdateThresholds()
	}

	return err
}

// SyncLocal does nothing on CompatibilityWrapper.
func (cw CompatibilityWrapperExecution) SyncLocal(ctx context.Context) error {
	_ = ctx

	return nil
}

func (cw CompatibilityWrapperExecution) NeedSynchronization(ctx context.Context) (bool, error) {
	_ = ctx

	return false, nil
}

func compatibilitySyncToPerform(ctx context.Context, execution types.SynchronizationExecution, state *synchronizerState) {
	option := execution.Option()

	// Take values that will be used later before taking the lock. This reduce dead-lock risk
	localFacts, _ := option.Facts.Facts(ctx, 24*time.Hour)
	currentSNMPCount := option.SNMPOnlineTarget()
	lastVSphereChange := option.LastVSphereChange(ctx)
	_, lastDiscovery := option.Discovery.GetLatestDiscovery()
	currentMetricCount := option.Store.MetricsCount()
	mqttIsConnected := option.IsMqttConnected()
	lastAnnotationChange := option.LastMetricAnnotationChange()

	state.l.Lock()
	defer state.l.Unlock()

	agent := option.Cache.Agent()

	nextConfigAt := agent.NextConfigAt
	if !nextConfigAt.IsZero() && nextConfigAt.Before(execution.StartedAt()) {
		execution.RequestSynchronizationForAll(true)
	}

	if state.currentConfigNotified != agent.CurrentAccountConfigID {
		execution.RequestSynchronization(types.EntityAccountConfig, true)
	}

	if state.lastFactUpdatedAt != localFacts[facts.FactUpdatedAt] {
		execution.RequestSynchronization(types.EntityFact, false)
	}

	if state.lastSNMPcount != currentSNMPCount {
		execution.RequestSynchronization(types.EntityFact, false)
		execution.RequestSynchronization(types.EntitySNMP, false)
		// TODO: this isn't idea. If the synchronization fail, it won't be retried.
		// I think the ideal fix would be to always retry all syncMethods that was to synchronize but failed.
		state.lastSNMPcount = currentSNMPCount
	}

	if lastVSphereChange.After(state.lastVSphereUpdate) {
		execution.RequestSynchronization(types.EntityVSphere, false)

		state.lastVSphereUpdate = lastVSphereChange
	}

	// After a reload, the config has been changed, so we want to do a fullsync
	// without waiting the nextFullSync that is kept between reload.
	if !state.configSyncDone {
		execution.RequestSynchronization(types.EntityConfig, true)
	}

	_, minDelayed := execution.GlobalState().DelayedContainers()

	if execution.LastSync().Before(lastDiscovery) || (!minDelayed.IsZero() && execution.StartedAt().After(minDelayed)) {
		execution.RequestSynchronization(types.EntityContainer, false)
	}

	if execution.IsSynchronizationRequested(types.EntityContainer) {
		// Metrics registration may need containers to be synced, trigger metrics synchronization
		execution.RequestSynchronization(types.EntityMetric, false)
	}

	if execution.IsSynchronizationRequested(types.EntityMonitor) {
		// Metrics registration may need monitors to be synced, trigger metrics synchronization
		execution.RequestSynchronization(types.EntityMetric, false)
	}

	if execution.StartedAt().After(state.metricRetryAt) || execution.LastSync().Before(lastDiscovery) || execution.LastSync().Before(lastAnnotationChange) || state.lastMetricCount != currentMetricCount {
		execution.RequestSynchronization(types.EntityMetric, false)
	}

	// when the mqtt connector is not connected, we cannot receive notifications to get out of maintenance
	// mode, so we poll more often.
	if execution.GlobalState().IsMaintenance() && !mqttIsConnected && execution.StartedAt().After(state.lastMaintenanceSync.Add(15*time.Minute)) {
		execution.RequestSynchronization(types.EntityInfo, false)

		state.lastMaintenanceSync = execution.StartedAt()
	}
}
