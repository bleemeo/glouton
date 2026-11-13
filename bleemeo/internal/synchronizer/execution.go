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
	"maps"
	"strings"
	"time"

	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/delay"
	"github.com/bleemeo/glouton/logger"
)

type Execution struct {
	client              types.Client
	initialRequestCount uint32
	// initialAuthenticatedSuccessCount is Synchronizer.authenticatedSuccessCounter when the
	// execution started.
	initialAuthenticatedSuccessCount uint32
	startedAt                        time.Time
	onlyEssential                    bool
	isNewAgent                       bool
	isLimitedExecution               bool

	updateThresholds bool
	callUpdateLabels bool
	synchronizer     *Synchronizer
	syncListStarted  bool
	entities         []EntityExecution
	// Requests whose deadline wasn't reached when this execution started, as they were then.
	// dropFulfilledLaterRequests uses it to tell them apart from a request made since by
	// another goroutine, which this execution isn't going to honor.
	deferredSync map[types.EntityName]types.SyncRequest
}

type EntityExecution struct {
	entity            types.EntitySynchronizer
	synchronizer      types.EntitySynchronizerExecution
	syncType          types.SyncType
	err               error
	linkedSyncTargets []types.EntityName
	// synced is whether SyncRemoteAndLocal succeeded.
	synced bool
}

var errSkippedExecution = errors.New("execution skipped to due maintenance mode or similar reason")

const (
	// entityMaxRetryDelay is the longest a failed entity waits before its next attempt.
	entityMaxRetryDelay = 15 * time.Minute
	// longStandingFailure is how long an entity fails before it waits up to
	// longStandingMaxRetryDelay between attempts.
	longStandingFailure       = 36 * time.Hour
	longStandingMaxRetryDelay = time.Hour
)

func (s *Synchronizer) getEntityExecution(entitySync map[types.EntityName]types.SyncType, limitedExec bool) []EntityExecution {
	entities := make([]EntityExecution, 0, len(s.synchronizers))

	for _, entity := range s.synchronizers {
		row := EntityExecution{
			entity:   entity,
			syncType: entitySync[entity.Name()],
		}

		if !limitedExec && s.maintenanceMode && !entity.EnabledInMaintenance() {
			row.err = errSkippedExecution
		}

		if !limitedExec && s.suspendedMode && !entity.EnabledInSuspendedMode() {
			row.err = errSkippedExecution
		}

		entities = append(entities, row)
	}

	return entities
}

func (s *Synchronizer) newExecution(onlyEssential bool, isNewAgent bool) *Execution {
	s.l.Lock()
	defer s.l.Unlock()

	startedAt := s.now()

	// A request whose deadline isn't reached yet is kept for a subsequent execution.
	// dropFulfilledLaterRequests drops it if this execution synchronizes its entity anyway.
	dueSync := make(map[types.EntityName]types.SyncType, len(s.forceSync))
	pendingSync := make(map[types.EntityName]types.SyncRequest, len(s.forceSync))

	for entityName, request := range s.forceSync {
		if request.Deadline.After(startedAt) {
			pendingSync[entityName] = request
		} else {
			dueSync[entityName] = request.Type
		}
	}

	execution := &Execution{
		synchronizer:        s,
		client:              s.getClient(),
		initialRequestCount: s.requestCounter.Load(),

		initialAuthenticatedSuccessCount: s.authenticatedSuccessCounter.Load(),
		startedAt:                        startedAt,
		onlyEssential:                    onlyEssential,
		isNewAgent:                       isNewAgent,
		entities:                         s.getEntityExecution(dueSync, false),
		// A copy: s.forceSync keeps being updated by the other goroutines.
		deferredSync: maps.Clone(pendingSync),
	}

	s.forceSync = pendingSync

	return execution
}

// newLimitedExecution returns a synchronization execution for calling
// only some entity synchronizer outside the synchronization loop.
// It won't call NeedSynchronization. It will also ignore maintenance & suspended mode.
func (s *Synchronizer) newLimitedExecution(onlyEssential bool, entities map[types.EntityName]types.SyncType) *Execution {
	execution := &Execution{
		synchronizer:        s,
		client:              s.getClient(),
		initialRequestCount: s.requestCounter.Load(),

		initialAuthenticatedSuccessCount: s.authenticatedSuccessCounter.Load(),
		startedAt:                        s.now(),
		onlyEssential:                    onlyEssential,
		entities:                         s.getEntityExecution(entities, true),
		isLimitedExecution:               true,
	}

	return execution
}

func (e *Execution) RequestUpdateThresholds() {
	e.updateThresholds = true
}

func (e *Execution) RequestNotifyLabelsUpdate() {
	e.callUpdateLabels = true
}

func (e *Execution) forceCacheRefreshForAll() bool {
	e.synchronizer.l.Lock()
	defer e.synchronizer.l.Unlock()

	if e.synchronizer.nextFullSync.Before(e.synchronizer.now()) {
		return true
	}

	agent := e.synchronizer.option.Cache.Agent()

	nextConfigAt := agent.NextConfigAt
	if !nextConfigAt.IsZero() && nextConfigAt.Before(e.synchronizer.now()) {
		return true
	}

	return e.allForcedCacheRefresh()
}

func (e *Execution) IsOnlyEssential() bool {
	return e.onlyEssential
}

// RequestSynchronization ask for a execution of synchronization of specified entity.
// If this is called during calls to NeedSynchronization, it's tried to be run during
// current execution of synchronization (no guarantee, e.g. on error).
// If called later, once SyncRemote start being called, it will be run during *next* execution.
func (e *Execution) RequestSynchronization(entityName types.EntityName, forceCacheRefresh bool) {
	if e.syncListStarted {
		e.synchronizer.l.Lock()
		e.synchronizer.requestSynchronizationLocked(entityName, forceCacheRefresh)

		// linked synchronization are only checked in the e.syncListStarted branch.
		// e.checkLinkedSynchronization() take care of everything before e.syncListStarted.
		// At this point e.expandLinkedTarget() is called, we don't need to do any recursion for
		// indirectly linked entity.
		for _, row := range e.entities {
			if row.entity.Name() == entityName {
				for _, target := range row.linkedSyncTargets {
					e.synchronizer.requestSynchronizationLocked(target, forceCacheRefresh)
				}

				break
			}
		}

		e.synchronizer.l.Unlock()
	} else {
		for idx, row := range e.entities {
			if row.entity.Name() == entityName {
				if forceCacheRefresh {
					e.entities[idx].syncType = types.SyncTypeForceCacheRefresh
				} else if row.syncType == types.SyncTypeNone {
					e.entities[idx].syncType = types.SyncTypeNormal
				}

				break
			}
		}
	}
}

func (e *Execution) RequestLinkedSynchronization(targetEntityName types.EntityName, triggerEntityName types.EntityName) error {
	if e.syncListStarted {
		return fmt.Errorf("%w: RequestLinkedSynchronization must be called during NeedSynchronization()", types.ErrUnexpectedWorkflow)
	}

	for idx, ee := range e.entities {
		if ee.entity.Name() != triggerEntityName {
			continue
		}

		e.entities[idx].linkedSyncTargets = append(e.entities[idx].linkedSyncTargets, targetEntityName)
	}

	return nil
}

// FailOtherEntity fails another entity of this execution, when it is scheduled: an entity that
// isn't scheduled has nothing to fail. It must be called during RefreshCache or SyncRemoteAndLocal,
// when the sync types of the execution no longer change.
func (e *Execution) FailOtherEntity(entityName types.EntityName, reason error) error {
	if !e.syncListStarted {
		return fmt.Errorf("%w: FailOtherEntity must be called during RefreshCache() or SyncRemoteAndLocal()", types.ErrUnexpectedWorkflow)
	}

	for idx, row := range e.entities {
		if row.entity.Name() == entityName {
			if row.err == nil && row.syncType != types.SyncTypeNone {
				e.entities[idx].err = reason
			}

			break
		}
	}

	return nil
}

func (e *Execution) RequestSynchronizationForAll(forceCacheRefresh bool) {
	for _, row := range e.entities {
		e.RequestSynchronization(row.entity.Name(), forceCacheRefresh)
	}
}

// RequestLaterSynchronization asks for a synchronization of the specified entity to occur within
// delay, during a later execution. It might happen earlier.
func (e *Execution) RequestLaterSynchronization(entityName types.EntityName, forceCacheRefresh bool, delay time.Duration) {
	e.synchronizer.l.Lock()
	defer e.synchronizer.l.Unlock()

	e.synchronizer.requestLaterSynchronizationLocked(entityName, forceCacheRefresh, delay)
}

// RequestLaterFullSynchronization ask for a full synchronization to occur within delay. It might happen earlier.
// Prefer RequestSynchronizationForAll if you need a very soon synchronization.
func (e *Execution) RequestLaterFullSynchronization(delay time.Duration) {
	e.synchronizer.l.Lock()
	defer e.synchronizer.l.Unlock()

	targetTime := e.synchronizer.now().Add(delay)
	if e.synchronizer.nextFullSync.After(targetTime) {
		e.synchronizer.nextFullSync = targetTime
	}
}

func (e *Execution) Option() types.Option {
	return e.synchronizer.option
}

func (e *Execution) LastSuccessfulSync(entityName types.EntityName) time.Time {
	e.synchronizer.l.Lock()
	defer e.synchronizer.l.Unlock()

	return e.synchronizer.entityLastSync[entityName]
}

func (e *Execution) ConsecutiveFailures(entityName types.EntityName) int {
	e.synchronizer.l.Lock()
	defer e.synchronizer.l.Unlock()

	return e.synchronizer.entityFailures[entityName]
}

func (e *Execution) StartedAt() time.Time {
	return e.startedAt
}

func (e *Execution) GlobalState() types.SynchronizedGlobalState {
	return e.synchronizer
}

func (e *Execution) BleemeoAPIClient() types.Client {
	return e.client
}

// run execute one iteration of a synchronization with Bleemeo API.
//
// The execution might do nothing at all, this is actually what happen most of the time.
// One execution will iterate over each entity synchronizers in the order defined in Synchronizer.synchronizers.
//
// Each entity synchronizers is split in two parts (two interfaces): the EntitySynchronizer and the EntitySynchronizerExecution.
// The idea of EntitySynchronizer is to kept state between multiple execution, typically to known whether a synchronization if needed
// or not (e.g. last metrics count; if the current metrics count change a synchronization is needed).
// The EntitySynchronizerExecution kept state for one execution. Simple entity synchronizer might not need any state here.
//
// The Execution.run will iterate multiple times over each EntitySynchronizerExecution:
//   - Once to call NeedSynchronization(). At this point, each synchronizer could return true to request a synchronization
//     execution. They could also call Execution.RequestSynchronization() to trigger synchronization of another entities
//     (e.g. metrics might need a containers to be registered first).
//   - Then once more to call RefreshCache(). Entities synchronizer are expected to update the Option().Cache
//     so other synchronizer might use the updated cache if needed.
//     At this point, not update should be done. And RefreshCache() might do no API call if the cache is still valid.
//   - Finally a call to SyncRemoteAndLocal is done, why now do changes.
//
// During one execution, RequestSynchronization() could be called anytime. If called once SyncRemoteAndLocal() started,
// its effect will be for the next synchronization execution. If called during NeedSynchronization() calls, its effect
// will be during current synchronization execution.
//
// During special iteration (maintenance mode or suspsended mode), only entity synchronizer enabled in this mode
// (cf EnabledInMaintenance and EnabledInSuspendedMode) will be called.
func (e *Execution) run(ctx context.Context) error {
	if !e.isLimitedExecution {
		if e.forceCacheRefreshForAll() {
			e.RequestSynchronizationForAll(true)
		}

		e.applyNeedSynchronization(ctx)
	}

	// Before checkLinkedSynchronization, so that an entity left out doesn't make its linked
	// entities synchronize, and after, to leave out the linked entities that are in backoff.
	e.skipEntitiesInBackoff()
	e.checkLinkedSynchronization()
	e.skipEntitiesInBackoff()
	e.dropFulfilledLaterRequests()
	e.syncListStarted = true

	if !e.isLimitedExecution && e.hadWork() {
		// Piggyback the periodic duplicated agent check on an execution that is going to call
		// the API anyway, so that it never costs a request on its own.
		if err := e.synchronizer.checkDuplicatedIfNeeded(ctx, e.client, e.startedAt, false); err != nil {
			// The check either found a duplicate, and disabled the connector, or failed to reach
			// the API. Fail every requested entity, like the check did when it ran on the first
			// API call, so that their synchronization is retried later.
			for idx := range e.entities {
				if e.entities[idx].syncType != types.SyncTypeNone && e.entities[idx].err == nil {
					e.entities[idx].err = err
				}
			}
		}
	}

	e.synchronizersCall(ctx, func(ctx context.Context, ee *EntityExecution) {
		if ee.syncType == types.SyncTypeNone {
			return
		}

		ee.err = ee.synchronizer.RefreshCache(ctx, ee.syncType)
	})

	e.synchronizersCall(ctx, func(ctx context.Context, ee *EntityExecution) {
		if ee.syncType == types.SyncTypeNone {
			return
		}

		ee.err = ee.synchronizer.SyncRemoteAndLocal(ctx, ee.syncType)
		ee.synced = ee.err == nil
	})

	var errs []error

	for _, ee := range e.entities {
		if ee.synchronizer != nil {
			ee.synchronizer.FinishExecution(ctx)
		}

		if ee.err != nil && !errors.Is(ee.err, errSkippedExecution) {
			errs = append(errs, fmt.Errorf("%s: %w", ee.entity.Name(), ee.err))
		}

		if ee.syncType == types.SyncTypeNone {
			continue
		}

		if errors.Is(ee.err, errSkippedExecution) {
			// Retry syncrhonization on next execution if skipped.
			// This ensures that if the maintenance takes a long time, we will still update the
			// objects that should have been synced in that period.
			e.RequestSynchronization(ee.entity.Name(), ee.syncType == types.SyncTypeForceCacheRefresh)
		} else if ee.err != nil {
			logger.V(1).Printf("Synchronization for object %s failed: %v", ee.entity.Name(), ee.err)
		}

		if e.onlyEssential {
			// We registered only essential object. Make sure all other
			// objects are registered on the second run.
			e.RequestSynchronization(ee.entity.Name(), ee.syncType == types.SyncTypeForceCacheRefresh)
		}
	}

	if ctx.Err() != nil {
		return ctx.Err()
	}

	e.recordResults()

	syncDone := e.formatSyncDone()

	duration := e.synchronizer.now().Sub(e.startedAt)
	if e.hadWork() {
		logger.V(2).Printf(
			"Synchronization took %v for %v (and did %d requests)",
			duration,
			syncDone,
			e.synchronizer.requestCounter.Load()-e.initialRequestCount,
		)
	}

	e.executePostRunCalls()

	return errors.Join(errs...)
}

// dropFulfilledLaterRequests drops the pending requests whose deadline isn't reached yet, but
// whose entity this execution is going to synchronize anyway. Those requests ask for a
// synchronization *at the latest* at their deadline, so an earlier one fulfills them.
func (e *Execution) dropFulfilledLaterRequests() {
	if e.isLimitedExecution {
		// A limited execution only runs a hand-picked list of entity synchronizers,
		// possibly in maintenance or suspended mode. Don't let it fulfill anything.
		return
	}

	e.synchronizer.l.Lock()
	defer e.synchronizer.l.Unlock()

	for _, row := range e.entities {
		if row.syncType == types.SyncTypeNone {
			continue
		}

		deferred, ok := e.deferredSync[row.entity.Name()]

		// The sync types are ordered, a cache refresh doesn't get fulfilled by a normal sync.
		if !ok || row.syncType < deferred.Type {
			continue
		}

		// Only drop the request we deferred: another goroutine may have asked for more since
		// this execution started, and that asks for a synchronization we aren't doing here.
		current, ok := e.synchronizer.forceSync[row.entity.Name()]
		if ok && current.Type == deferred.Type && current.Deadline.Equal(deferred.Deadline) {
			delete(e.synchronizer.forceSync, row.entity.Name())
		}
	}
}

// executePostRunCalls runs any RequestXXX called on Execution (like RequestUpdateThresholds).
// RequestSynchronization aren't handled by this function, they're always either applied in
// current execution run() or directly forwarded to Synchronizer.forceSync.
func (e *Execution) executePostRunCalls() {
	if e.callUpdateLabels {
		if e.synchronizer.option.NotifyHooksUpdate != nil {
			e.synchronizer.option.NotifyHooksUpdate()
		}
	}

	if e.isNewAgent {
		e.synchronizer.UpdateUnitsAndThresholds(true)
	} else if e.updateThresholds {
		e.synchronizer.UpdateUnitsAndThresholds(false)
	}
}

func (e *Execution) applyNeedSynchronization(ctx context.Context) {
	e.synchronizersCall(ctx, func(ctx context.Context, ee *EntityExecution) {
		need, err := ee.synchronizer.NeedSynchronization(ctx)
		if err != nil {
			ee.err = err

			return
		}

		if need {
			if ee.syncType == types.SyncTypeNone {
				ee.syncType = types.SyncTypeNormal
			}
		}
	})
}

func (e *Execution) checkLinkedSynchronization() {
	nameToIdx := make(map[types.EntityName]int, len(e.entities))

	for idx, ee := range e.entities {
		nameToIdx[ee.entity.Name()] = idx
	}

	e.expandLinkedTarget(nameToIdx)

	for _, ee := range e.entities {
		if ee.syncType == types.SyncTypeNone {
			continue
		}

		for _, target := range ee.linkedSyncTargets {
			idx, ok := nameToIdx[target]
			if !ok {
				continue
			}

			if ee.syncType == types.SyncTypeForceCacheRefresh {
				e.entities[idx].syncType = types.SyncTypeForceCacheRefresh
			} else if e.entities[idx].syncType == types.SyncTypeNone {
				e.entities[idx].syncType = types.SyncTypeNormal
			}
		}
	}
}

// expandLinkedTarget expend the EntityExecution.linkedSyncTargets.
// Since entity name A could trigger a synchronization on entity B which could itself trigger on entity C, etc.
// This function expend the list of A to contains B and C.
// This is a graph and we expand to all target including indirect. Obviously we stop when a cycle is detected.
func (e *Execution) expandLinkedTarget(nameToIdx map[types.EntityName]int) {
	for idx, ee := range e.entities {
		targetsEntities := make(map[types.EntityName]struct{}, len(e.entities))

		e.recursiveExpand(nameToIdx, ee.entity.Name(), targetsEntities, ee.linkedSyncTargets)

		targetLists := make([]types.EntityName, 0, len(targetsEntities))

		for name := range targetsEntities {
			targetLists = append(targetLists, name)
		}

		e.entities[idx].linkedSyncTargets = targetLists
	}
}

func (e *Execution) recursiveExpand(nameToIdx map[types.EntityName]int, originalType types.EntityName, targetsEntities map[types.EntityName]struct{}, syncTargets []types.EntityName) {
	newTargets := make([]types.EntityName, 0)

	for _, target := range syncTargets {
		if target == originalType {
			continue
		}

		if _, ok := targetsEntities[target]; ok {
			continue
		}

		targetsEntities[target] = struct{}{}

		if idx, ok := nameToIdx[target]; ok {
			extraTargets := e.entities[idx].linkedSyncTargets
			newTargets = append(newTargets, extraTargets...)
		}
	}

	if len(newTargets) > 0 {
		e.recursiveExpand(nameToIdx, originalType, targetsEntities, newTargets)
	}
}

func (e *Execution) formatSyncDone() string {
	part := make([]string, 0, len(e.entities))

	for _, ee := range e.entities {
		if ee.syncType == types.SyncTypeNone {
			continue
		}

		if ee.syncType == types.SyncTypeForceCacheRefresh {
			part = append(part, fmt.Sprintf("%s (full)", ee.entity.Name()))
		} else {
			part = append(part, string(ee.entity.Name()))
		}
	}

	return strings.Join(part, ", ")
}

func (e *Execution) hadWork() bool {
	for _, ee := range e.entities {
		if ee.syncType != types.SyncTypeNone {
			return true
		}
	}

	return false
}

func (e *Execution) allForcedCacheRefresh() bool {
	for _, ee := range e.entities {
		if ee.syncType != types.SyncTypeForceCacheRefresh {
			return false
		}
	}

	return true
}

// recordResults records, for each entity the execution synchronized, when it last succeeded and
// how many times in a row it failed: a success resets the count. An entity skipped is left as
// it was: run requests it again.
//
// A cache refresh that failed, or didn't run because the connector is disabled, is requested
// again for the entity's next attempt: a normal synchronization succeeding meanwhile would
// otherwise lose it.
func (e *Execution) recordResults() {
	e.synchronizer.l.Lock()
	defer e.synchronizer.l.Unlock()

	firstFailuresChanged := false

	for _, ee := range e.entities {
		name := ee.entity.Name()
		_, wasFailing := e.synchronizer.entityFirstFailureAt[name]

		if ee.synced {
			e.synchronizer.entityLastSync[name] = e.startedAt
			delete(e.synchronizer.entityFailures, name)
			delete(e.synchronizer.entityRetryAt, name)
			delete(e.synchronizer.entityFirstFailureAt, name)

			firstFailuresChanged = firstFailuresChanged || wasFailing

			continue
		}

		if ee.syncType == types.SyncTypeNone || ee.err == nil || errors.Is(ee.err, errSkippedExecution) {
			continue
		}

		var retryDelay time.Duration

		if !errors.Is(ee.err, errConnectorTemporaryDisabled) {
			retryDelay = e.recordFailureLocked(name, ee.err)
			firstFailuresChanged = firstFailuresChanged || !wasFailing
		}

		if ee.syncType == types.SyncTypeForceCacheRefresh {
			e.synchronizer.requestLaterSynchronizationLocked(name, true, retryDelay)
		}
	}

	if firstFailuresChanged {
		e.synchronizer.saveEntityFirstFailuresLocked()
	}
}

// recordFailureLocked counts a failure of the entity and returns its retry delay. The delay grows
// with the failures in a row, up to entityMaxRetryDelay, or longStandingMaxRetryDelay once the
// entity has failed for longStandingFailure. The authentication and throttle errors concern the
// whole agent and get no delay: Synchronizer.Run disables the whole connector for them.
// Caller must hold the lock e.synchronizer.l.
func (e *Execution) recordFailureLocked(name types.EntityName, err error) time.Duration {
	s := e.synchronizer

	s.entityFailures[name]++

	firstFailureAt, ok := s.entityFirstFailureAt[name]
	if !ok {
		firstFailureAt = e.startedAt
		s.entityFirstFailureAt[name] = firstFailureAt
	}

	if IsAuthenticationError(err) || IsThrottleError(err) {
		return 0
	}

	maxDelay := entityMaxRetryDelay
	if s.now().Sub(firstFailureAt) > longStandingFailure {
		maxDelay = longStandingMaxRetryDelay
	}

	retryDelay := delay.JitterDelay(delay.Exponential(15*time.Second, 1.55, s.entityFailures[name], maxDelay), 0.1)
	s.entityRetryAt[name] = s.now().Add(retryDelay)

	return retryDelay
}

// skipEntitiesInBackoff leaves out of this execution the entities that failed and whose retry
// delay isn't over (see recordResults), so that an entity failing again and again doesn't keep
// calling the API. Their synchronization is requested again for the end of the delay.
func (e *Execution) skipEntitiesInBackoff() {
	if e.isLimitedExecution {
		return
	}

	e.synchronizer.l.Lock()
	defer e.synchronizer.l.Unlock()

	for idx, row := range e.entities {
		retryAt, ok := e.synchronizer.entityRetryAt[row.entity.Name()]
		if row.syncType == types.SyncTypeNone || row.err != nil || !ok || !e.startedAt.Before(retryAt) {
			continue
		}

		e.synchronizer.requestLaterSynchronizationLocked(
			row.entity.Name(),
			row.syncType == types.SyncTypeForceCacheRefresh,
			retryAt.Sub(e.synchronizer.now()),
		)

		e.entities[idx].syncType = types.SyncTypeNone
	}
}

// calledAPISuccessfully returns whether the API answered an authenticated request without error
// during the execution.
func (e *Execution) calledAPISuccessfully() bool {
	return e.synchronizer.authenticatedSuccessCounter.Load() != e.initialAuthenticatedSuccessCount
}

// synchronizersCall calls f on each entity which can run. f updates the entity's row in
// e.entities in place: what the entity itself changes on its row during the call, like a
// RequestSynchronization of itself, is kept.
func (e *Execution) synchronizersCall(ctx context.Context, f func(context.Context, *EntityExecution)) {
	for idx := range e.entities {
		ee := &e.entities[idx]

		if ctx.Err() != nil {
			return
		}

		if ee.err != nil {
			continue
		}

		until, reason := e.synchronizer.getDisabledUntil()
		if e.synchronizer.now().Before(until) {
			// If the agent was disabled because it is too old, we do not want the synchronizer
			// to throw a DisableTooManyErrors because info disabled the bleemeo connector.
			// This could alter the synchronizer would wait to sync again, and we do not desire it.
			// This would also show errors that could confuse the user like "Synchronization with
			// Bleemeo Cloud platform still have to wait 1m27s due to too many errors".
			if reason != bleemeoTypes.DisableAgentTooOld {
				ee.err = errConnectorTemporaryDisabled
			}

			continue
		}

		if !e.isLimitedExecution && e.synchronizer.IsMaintenance() && !ee.entity.EnabledInMaintenance() {
			ee.err = errSkippedExecution

			continue
		}

		if !e.isLimitedExecution && e.synchronizer.suspendedMode && !ee.entity.EnabledInSuspendedMode() {
			ee.err = errSkippedExecution

			continue
		}

		if ee.synchronizer == nil {
			ee.synchronizer, ee.err = ee.entity.PrepareExecution(ctx, e)
		}

		if ee.err != nil {
			continue
		}

		if ee.synchronizer != nil {
			f(ctx, ee)
		}
	}
}
