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

package syncregistrationstatus

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/bleemeo/glouton/bleemeo/internal/filter"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/discovery"
	"github.com/bleemeo/glouton/logger"
	gloutonTypes "github.com/bleemeo/glouton/types"
)

// metricsStateWait is how long after discovery first ran the services are held back waiting
// for the metrics state. Past it, they are sent with the metrics unknown, rather than not at
// all when the metrics synchronization keeps failing.
const metricsStateWait = 10 * time.Minute

// collectionWait is how long after discovery first ran the state is held back while a service's
// collection has not run yet: its metrics would read as none, as after a restart. It covers
// jmxtrans' window; past it, a collection that never runs does not block the others.
const collectionWait = 5 * time.Minute

// SyncServicesState sends the services state to the Agent's registration_status field.
type SyncServicesState struct {
	metricsState func() ([]bleemeoTypes.MetricState, bool)
	// now is the clock a send is recorded with.
	now func() time.Time

	// lastBuildAt is when NeedSynchronization last built a payload.
	lastBuildAt time.Time
	// discoveryReadyAt is when NeedSynchronization first saw a completed discovery.
	discoveryReadyAt time.Time

	currentExecution types.SynchronizationExecution
	// payload is what NeedSynchronization decided to send, nil when nothing is to be sent.
	payload *Payload
}

// New returns the entity, which reads the not-registered metrics from metricsState, along
// with whether a synchronization published them yet. now is the synchronizer's clock.
func New(metricsState func() ([]bleemeoTypes.MetricState, bool), now func() time.Time) *SyncServicesState {
	return &SyncServicesState{metricsState: metricsState, now: now}
}

func (s *SyncServicesState) Name() types.EntityName {
	return types.EntityServicesState
}

func (s *SyncServicesState) EnabledInMaintenance() bool {
	return false
}

func (s *SyncServicesState) EnabledInSuspendedMode() bool {
	return false
}

func (s *SyncServicesState) PrepareExecution(_ context.Context, execution types.SynchronizationExecution) (types.EntitySynchronizerExecution, error) {
	s.currentExecution = execution

	return s, nil
}

// NeedSynchronization builds the payload and tells whether to send it. It is built here, from
// the state the previous execution left, because a true answer alone makes the execution call
// the API (see Execution.hadWork).
func (s *SyncServicesState) NeedSynchronization(_ context.Context) (bool, error) {
	if s.currentExecution == nil {
		return false, fmt.Errorf("%w: currentExecution is nil", types.ErrUnexpectedWorkflow)
	}

	// An essential-only execution does not synchronize metrics, so their state is not current.
	if s.currentExecution.IsOnlyEssential() {
		return false, nil
	}

	option := s.currentExecution.Option()

	agentID, _ := option.State.BleemeoCredentials()
	if agentID == "" {
		return false, nil
	}

	last := option.Cache.ServicesStateSent()
	now := s.currentExecution.StartedAt()

	if !MaySend(last, agentID, now) {
		return false, nil
	}

	// Until discovery ran and a synchronization published the metrics state, both lists are
	// empty for lack of a run: sent, they would replace a value that still holds after a
	// restart, and the next change may not be sent before minSendInterval.
	if _, lastDiscovery := option.Discovery.GetLatestDiscovery(); lastDiscovery.IsZero() {
		return false, nil
	}

	if s.discoveryReadyAt.IsZero() {
		s.discoveryReadyAt = now
	}

	metrics, published := s.metricsState()
	if !published && now.Sub(s.discoveryReadyAt) < metricsStateWait {
		return false, nil
	}

	// Built at most once per minNewServiceInterval, the shortest delay before a send, rather
	// than on each execution: building encodes and hashes both lists. A clock that went back
	// builds at once.
	if !s.lastBuildAt.IsZero() && !now.Before(s.lastBuildAt) && now.Sub(s.lastBuildAt) < minNewServiceInterval {
		return false, nil
	}

	s.lastBuildAt = now

	storeMetrics, err := option.Store.Metrics(nil)
	if err != nil {
		logger.V(1).Printf("Unable to read the metrics for the services state: %v", err)

		return false, nil
	}

	bleemeoFilter := filter.NewFilter(option.Cache)
	isAllowed := func(metric gloutonTypes.Metric) bool {
		allowed, _, err := bleemeoFilter.IsAllowed(metric.Labels(), metric.Annotations())

		return err == nil && allowed
	}

	metricNames := ServiceMetricNames(storeMetrics, agentID, isAllowed, time.Now())
	states := option.Discovery.ServicesState()

	if now.Sub(s.discoveryReadyAt) < collectionWait && slices.ContainsFunc(states, isCollectionPending) {
		return false, nil
	}

	services := FromDiscovery(states, option.Cache.ServiceLookupFromList(), metricNames)
	payload := NewPayload(services, metrics, published)

	if !NeedSend(last, payload, agentID, now) {
		return false, nil
	}

	s.payload = &payload

	return true, nil
}

func (s *SyncServicesState) RefreshCache(_ context.Context, _ types.SyncType) error {
	return nil
}

// SyncRemoteAndLocal sends the payload NeedSynchronization chose. An execution may call it
// without, when every entity is requested: then there is nothing to send.
func (s *SyncServicesState) SyncRemoteAndLocal(ctx context.Context, _ types.SyncType) error {
	if s.currentExecution == nil {
		return fmt.Errorf("%w: currentExecution is nil", types.ErrUnexpectedWorkflow)
	}

	if s.payload == nil {
		return nil
	}

	option := s.currentExecution.Option()
	agentID, _ := option.State.BleemeoCredentials()
	// When the send happens, not when the execution started: the delays before the next send
	// run from it, and an execution can last long.
	now := s.now()

	stored, err := s.currentExecution.BleemeoAPIClient().UpdateAgentRegistrationStatus(ctx, agentID, s.payload)
	if err != nil {
		option.Cache.SetServicesStateSent(SendFailed(option.Cache.ServicesStateSent(), agentID, now))

		// A refused value is retried after minSendInterval. Failing the execution would also
		// back off the whole synchronization and log at the default level each time, for a
		// value that is only diagnostic.
		if types.IsBadRequest(err) {
			logger.V(1).Printf("Bleemeo refused the services state: %v", err)

			return nil
		}

		return err
	}

	// Recorded as sent, the value would not be sent again once the API has the field, for as
	// long as it does not change.
	if !stored {
		option.Cache.SetServicesStateSent(SendFailed(option.Cache.ServicesStateSent(), agentID, now))
		logger.V(1).Printf("Bleemeo did not store the services state: the API does not support it yet")

		return nil
	}

	option.Cache.SetServicesStateSent(Sent(*s.payload, agentID, now))

	return nil
}

func isCollectionPending(state discovery.ServiceState) bool {
	return state.CollectionPending
}

func (s *SyncServicesState) FinishExecution(_ context.Context) {
	s.currentExecution = nil
	s.payload = nil
}
