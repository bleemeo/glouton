// Copyright 2015-2024 Bleemeo
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
	"strings"
	"time"

	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/logger"
)

type Execution struct {
	client              types.Client
	initialRequestCount uint32
	startedAt           time.Time
	onlyEssential       bool
	isNewAgent          bool
	isLimitedExecution  bool

	updateThresholds bool
	callUpdateLabels bool
	synchronizer     *Synchronizer
	syncListStarted  bool
	entities         []EntityExecution
}

type EntityExecution struct {
	entity            types.EntitySynchronizer
	synchronizer      types.EntitySynchronizerExecution
	syncType          types.SyncType
	err               error
	linkedSyncTargets []types.EntityName
}

var errSkippedExecution = errors.New("execution skipped to due maintenance mode or similar reason")

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

	execution := &Execution{
		synchronizer:        s,
		client:              s.newClient(),
		initialRequestCount: s.requestCounter.Load(),
		startedAt:           s.now(),
		onlyEssential:       onlyEssential,
		isNewAgent:          isNewAgent,
		entities:            s.getEntityExecution(s.forceSync, false),
	}

	s.forceSync = make(map[types.EntityName]types.SyncType, len(s.forceSync))

	return execution
}

// newLimitedExecution returns a synchronization execution for calling
// only some entity synchronizer outside the synchronization loop.
// It won't call NeedSynchronization. It will also ignore maintenance & suspended mode.
func (s *Synchronizer) newLimitedExecution(onlyEssential bool, entities map[types.EntityName]types.SyncType) *Execution {
	execution := &Execution{
		synchronizer:        s,
		client:              s.newClient(),
		initialRequestCount: s.requestCounter.Load(),
		startedAt:           s.now(),
		onlyEssential:       onlyEssential,
		entities:            s.getEntityExecution(entities, true),
		isLimitedExecution:  true,
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
		return fmt.Errorf("%w: RequestLinkedSynchronization must be called during RequestSynchronization()", types.ErrUnexpectedWorkflow)
	}

	for idx, ee := range e.entities {
		if ee.entity.Name() != triggerEntityName {
			continue
		}

		e.entities[idx].linkedSyncTargets = append(e.entities[idx].linkedSyncTargets, targetEntityName)
	}

	return nil
}

func (e *Execution) FailOtherEntity(entityName types.EntityName, reason error) {
	for idx, row := range e.entities {
		if row.entity.Name() == entityName {
			if row.err == nil {
				e.entities[idx].err = reason
			}

			break
		}
	}
}

func (e *Execution) RequestSynchronizationForAll(forceCacheRefresh bool) {
	for _, row := range e.entities {
		e.RequestSynchronization(row.entity.Name(), forceCacheRefresh)
	}
}

// IsSynchronizationRequested return whether a synchronization was request for the
// specific entity.
// Note: even if this method return false, a synchronization might occur if someone call RequestSynchronize later.
// Use RequestLinkedSynchronization to overcome this limitation.
func (e *Execution) IsSynchronizationRequested(entityName types.EntityName) bool {
	for _, row := range e.entities {
		if row.entity.Name() == entityName {
			return row.syncType != types.SyncTypeNone
		}
	}

	return false
}

func (e *Execution) Option() types.Option {
	return e.synchronizer.option
}

func (e *Execution) LastSync() time.Time {
	return e.synchronizer.lastSync
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

		compatibilitySyncToPerform(ctx, e, e.synchronizer.state)

		e.applyNeedSynchronization(ctx)

		if e.hadWork() && e.synchronizer.now().Sub(e.synchronizer.lastInfo.FetchedAt) > 30*time.Minute {
			// Ensure lastInfo is enough up-to-date.
			// This will help detection quickly a change on /v1/info/ and will ensure the
			// metric time_drift is updated recently to avoid unwanted deactivation.
			e.RequestSynchronization(types.EntityInfo, false)
		}
	}

	e.checkLinkedSynchronization()
	e.syncListStarted = true

	e.synchronizersCall(ctx, e.entities, func(ctx context.Context, ee EntityExecution) EntityExecution {
		if ee.syncType == types.SyncTypeNone {
			return ee
		}

		ee.err = ee.synchronizer.RefreshCache(ctx, ee.syncType)

		return ee
	})

	e.synchronizersCall(ctx, e.entities, func(ctx context.Context, ee EntityExecution) EntityExecution {
		if ee.syncType == types.SyncTypeNone {
			return ee
		}

		ee.err = ee.synchronizer.SyncRemoteAndLocal(ctx, ee.syncType)

		return ee
	})

	var errs []error

	for _, ee := range e.entities {
		if ee.synchronizer != nil {
			ee.synchronizer.FinishExecution(ctx)
		}

		if ee.err != nil && !errors.Is(ee.err, errSkippedExecution) {
			errs = append(errs, ee.err)
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

	e.executePostRunCalls(ctx)

	return errors.Join(errs...)
}

// executePostRunCalls runs any RequestXXX called on Execution (like RequestUpdateThresholds).
// RequestSynchronization aren't handled by this function, they're always either applied in
// current execution run() or directly forwarded to Synchronizer.forceSync.
func (e *Execution) executePostRunCalls(ctx context.Context) {
	if e.callUpdateLabels {
		if e.synchronizer.option.NotifyHooksUpdate != nil {
			e.synchronizer.option.NotifyHooksUpdate()
		}
	}

	if e.isNewAgent {
		e.synchronizer.UpdateUnitsAndThresholds(ctx, true)
	} else if e.updateThresholds {
		e.synchronizer.UpdateUnitsAndThresholds(ctx, false)
	}
}

func (e *Execution) applyNeedSynchronization(ctx context.Context) {
	e.synchronizersCall(ctx, e.entities, func(ctx context.Context, ee EntityExecution) EntityExecution {
		need, err := ee.synchronizer.NeedSynchronization(ctx)
		if err != nil {
			ee.err = err

			return ee
		}

		if need {
			if ee.syncType == types.SyncTypeNone {
				ee.syncType = types.SyncTypeNormal
			}
		}

		return ee
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

func (e *Execution) synchronizersCall(ctx context.Context, synchronizersExecution []EntityExecution, f func(context.Context, EntityExecution) EntityExecution) {
	for idx, ee := range synchronizersExecution {
		if ctx.Err() != nil {
			return
		}

		if ee.err != nil {
			continue
		}

		until, reason := e.synchronizer.getDisabledUntil()
		if e.synchronizer.now().Before(until) {
			// If the agent was disabled because it is too old, we do not want the synchronizer
			// to throw a DisableTooManyErrors because syncInfo() disabled the bleemeo connector.
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
			e.entities[idx] = f(ctx, ee)
		}
	}
}
