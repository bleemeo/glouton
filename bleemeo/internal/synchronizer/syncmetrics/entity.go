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

package syncmetrics

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	gloutonTypes "github.com/bleemeo/glouton/types"
)

// SyncMetrics registers the metrics of the store, deactivates the ones gone, and reports the
// ones that can't be registered.
type SyncMetrics struct {
	option types.Option
	now    func() time.Time
	// startedAt returns when Glouton started: allowed metrics aren't deactivated during the
	// first minutes.
	startedAt func() time.Time
	logs      *types.LogThrottle

	// l protects the fields below: UpdateMetrics, LastMetricActivation, MetricsState and
	// DiagnosticArchive are called from other goroutines.
	l                      sync.Mutex
	pendingMetricsUpdate   []string
	retryableMetricFailure map[bleemeoTypes.FailureKind]bool
	// metricRetryAt is when the next registration failure is due for a retry.
	metricRetryAt time.Time
	// lastMetricCount is the number of metrics in the store at the last synchronization.
	lastMetricCount int
	// lastMetricActivation is when a metric was last registered or activated.
	lastMetricActivation time.Time
	// notRegisteredMetrics counts, by name and reason, the metrics the filter dropped.
	notRegisteredMetrics map[metricStateKey]int
	// registrationMetrics counts the metrics that passed the filter but are not registered:
	// pending, or refused by the API.
	registrationMetrics map[metricStateKey]int
	// metricsStatePublished tells whether a synchronization published the metrics state yet.
	metricsStatePublished bool
	// waitingDependency tells whether the last synchronization left metrics waiting for their
	// container, service or monitor to be registered.
	waitingDependency bool
}

// New returns the entity. startedAt returns when Glouton started.
func New(option types.Option, now func() time.Time, startedAt func() time.Time) *SyncMetrics {
	return &SyncMetrics{
		option:                 option,
		now:                    now,
		startedAt:              startedAt,
		logs:                   types.NewLogThrottle(now),
		retryableMetricFailure: make(map[bleemeoTypes.FailureKind]bool),
	}
}

func (s *SyncMetrics) Name() types.EntityName {
	return types.EntityMetric
}

func (s *SyncMetrics) EnabledInMaintenance() bool {
	return false
}

func (s *SyncMetrics) EnabledInSuspendedMode() bool {
	return false
}

func (s *SyncMetrics) PrepareExecution(_ context.Context, execution types.SynchronizationExecution) (types.EntitySynchronizerExecution, error) {
	var agentID string

	if state := execution.Option().State; state != nil {
		agentID, _ = state.BleemeoCredentials()
	}

	return &syncMetricsExecution{
		SyncMetrics: s,
		execution:   execution,
		agentID:     agentID,
	}, nil
}

// UpdateMetrics records that the metrics metricUUID changed on the API, to read them on the next
// synchronization. A single empty UUID means any metric may have changed: it returns true, all
// the metrics must be read.
func (s *SyncMetrics) UpdateMetrics(metricUUID ...string) (all bool) {
	s.l.Lock()
	defer s.l.Unlock()

	if len(metricUUID) == 1 && metricUUID[0] == "" {
		s.pendingMetricsUpdate = nil

		return true
	}

	s.pendingMetricsUpdate = append(s.pendingMetricsUpdate, metricUUID...)

	return false
}

// popPendingMetricsUpdate returns the metrics to read, without duplicates, and forgets them.
func (s *SyncMetrics) popPendingMetricsUpdate() []string {
	s.l.Lock()
	defer s.l.Unlock()

	set := make(map[string]bool, len(s.pendingMetricsUpdate))

	for _, m := range s.pendingMetricsUpdate {
		set[m] = true
	}

	s.pendingMetricsUpdate = nil
	result := make([]string, 0, len(set))

	for m := range set {
		result = append(result, m)
	}

	return result
}

// LastMetricActivation returns when a metric was last registered or activated.
func (s *SyncMetrics) LastMetricActivation() time.Time {
	s.l.Lock()
	defer s.l.Unlock()

	return s.lastMetricActivation
}

func (s *SyncMetrics) DiagnosticArchive(_ context.Context, archive gloutonTypes.ArchiveWriter) error {
	file, err := archive.Create("bleemeo-sync-metrics.json")
	if err != nil {
		return err
	}

	s.l.Lock()

	obj := struct {
		LastMetricActivation      time.Time
		LastMetricCount           int
		PendingMetricsUpdateCount int
		RetryableMetricFailure    map[bleemeoTypes.FailureKind]bool
		MetricRetryAt             time.Time
	}{
		LastMetricActivation:      s.lastMetricActivation,
		LastMetricCount:           s.lastMetricCount,
		PendingMetricsUpdateCount: len(s.pendingMetricsUpdate),
		RetryableMetricFailure:    s.retryableMetricFailure,
		MetricRetryAt:             s.metricRetryAt,
	}

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")

	err = enc.Encode(obj)

	s.l.Unlock()

	return err
}

// needSynchronization returns whether a registration failure is due for a retry, a discovery or
// a change of the metric annotations happened since the metrics were last synchronized
// successfully, the number of metrics changed, or metrics wait for a dependency synchronized
// since.
func (s *SyncMetrics) needSynchronization(execution types.SynchronizationExecution) bool {
	_, lastDiscovery := s.option.Discovery.GetLatestDiscovery()
	currentMetricCount := s.option.Store.MetricsCount()
	lastAnnotationChange := s.option.LastMetricAnnotationChange()
	lastSuccessfulSync := execution.LastSuccessfulSync(types.EntityMetric)

	dependencySynced := false

	// The entities a metric may wait for to be registered.
	for _, dependency := range []types.EntityName{types.EntityContainer, types.EntityService, types.EntityMonitor} {
		if execution.LastSuccessfulSync(dependency).After(lastSuccessfulSync) {
			dependencySynced = true
		}
	}

	s.l.Lock()
	defer s.l.Unlock()

	return execution.StartedAt().After(s.metricRetryAt) ||
		lastSuccessfulSync.Before(lastDiscovery) ||
		lastSuccessfulSync.Before(lastAnnotationChange) ||
		s.lastMetricCount != currentMetricCount ||
		(s.waitingDependency && dependencySynced)
}

// logThrottle logs a message at most once per hour and per key, see types.LogThrottle.
func (s *SyncMetrics) logThrottle(key string, level int, msg string) {
	s.logs.Log(key, level, msg)
}

type syncMetricsExecution struct {
	*SyncMetrics

	execution types.SynchronizationExecution
	agentID   string
	// stagedNotRegistered is filterMetrics' count for this execution, published by
	// publishMetricsState.
	stagedNotRegistered map[metricStateKey]int
	// dependencyMissing tells whether this execution left metrics waiting for a dependency,
	// see SyncMetrics.waitingDependency.
	dependencyMissing bool
}

// NeedSynchronization synchronizes the metrics when a registration failure is due for a retry,
// after a discovery or a change of the metric annotations, when the number of metrics changes,
// and once the container, service or monitor that metrics wait for may be registered.
func (s *syncMetricsExecution) NeedSynchronization(_ context.Context) (bool, error) {
	return s.needSynchronization(s.execution), nil
}

func (s *syncMetricsExecution) RefreshCache(_ context.Context, _ types.SyncType) error {
	// The metrics read from the API depend on the containers and services registered earlier
	// in the same execution: they are read by SyncRemoteAndLocal.
	return nil
}

func (s *syncMetricsExecution) SyncRemoteAndLocal(ctx context.Context, syncType types.SyncType) error {
	updateThresholds, err := s.syncMetrics(ctx, syncType)
	if updateThresholds {
		s.execution.RequestUpdateThresholds()
	}

	return err
}

func (s *syncMetricsExecution) FinishExecution(_ context.Context) {
}

// requeuePendingMetricsUpdate records again the metrics to read after a failure, for the next
// execution.
func (s *syncMetricsExecution) requeuePendingMetricsUpdate(metricUUID []string) {
	s.l.Lock()
	s.pendingMetricsUpdate = append(s.pendingMetricsUpdate, metricUUID...)
	s.l.Unlock()

	s.execution.RequestSynchronization(types.EntityMetric, false)
}
