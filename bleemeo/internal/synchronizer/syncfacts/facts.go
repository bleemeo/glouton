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

// Package syncfacts registers the facts of the agent, and of its SNMP and vSphere agents, on
// Bleemeo.
package syncfacts

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/bleemeo/bleemeo-go"
	"github.com/bleemeo/glouton/bleemeo/internal/cache"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/syncsnmp"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/syncvsphere"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/logger"
	gloutonTypes "github.com/bleemeo/glouton/types"
)

const (
	factAgentVersion    = "agent_version"
	factArchitecture    = "architecture"
	FactFQDN            = "fqdn"
	FactGloutonVersion  = "glouton_version"
	factHostname        = "hostname"
	factInstallationFmt = "installation_format"
	factKernel          = "kernel"
	factOSName          = "os_name"
	factOSPrettyName    = "os_pretty_name"
	FactGloutonPID      = "glouton_pid"
	factPublicIP        = "public_ip"
	factVirtual         = "virtual"
)

func getEssentialFacts() map[string]bool {
	return map[string]bool{
		factAgentVersion:    true,
		factArchitecture:    true,
		FactFQDN:            true,
		FactGloutonVersion:  true,
		factHostname:        true,
		factInstallationFmt: true,
		factKernel:          true,
		factOSName:          true,
		factOSPrettyName:    true,
		factPublicIP:        true,
		factVirtual:         true,
	}
}

// CheckDuplicated checks that no other Glouton uses the same agent ID. It updates the facts
// list from the API, and must run before the facts are updated on the API. It does nothing if
// it already ran during the execution started at executionStartedAt.
type CheckDuplicated func(ctx context.Context, client types.Client, executionStartedAt time.Time) error

// SyncFacts registers the facts of the agent, and of its SNMP and vSphere agents.
type SyncFacts struct {
	snmp            *syncsnmp.SyncSNMP
	vsphere         *syncvsphere.SyncVSphere
	checkDuplicated CheckDuplicated

	// l protects lastFactUpdatedAt: DiagnosticArchive reads it from another goroutine.
	l sync.Mutex
	// lastFactUpdatedAt is the fact_updated_at of the last facts fully synchronized.
	lastFactUpdatedAt string
}

// New returns the entity. snmp and vsphere find the SNMP and vSphere agents, whose facts are
// registered too.
func New(snmp *syncsnmp.SyncSNMP, vsphere *syncvsphere.SyncVSphere, checkDuplicated CheckDuplicated) *SyncFacts {
	return &SyncFacts{
		snmp:            snmp,
		vsphere:         vsphere,
		checkDuplicated: checkDuplicated,
	}
}

func (s *SyncFacts) Name() types.EntityName {
	return types.EntityFact
}

func (s *SyncFacts) EnabledInMaintenance() bool {
	return false
}

func (s *SyncFacts) EnabledInSuspendedMode() bool {
	return false
}

// needSynchronization returns whether the facts were updated since the last facts fully
// synchronized.
func (s *SyncFacts) needSynchronization(ctx context.Context, execution types.SynchronizationExecution) bool {
	localFacts, _ := execution.Option().Facts.Facts(ctx, 24*time.Hour)

	s.l.Lock()
	defer s.l.Unlock()

	return s.lastFactUpdatedAt != localFacts[facts.FactUpdatedAt]
}

func (s *SyncFacts) PrepareExecution(_ context.Context, execution types.SynchronizationExecution) (types.EntitySynchronizerExecution, error) {
	return &syncFactsExecution{
		parent:    s,
		execution: execution,
	}, nil
}

func (s *SyncFacts) DiagnosticArchive(_ context.Context, archive gloutonTypes.ArchiveWriter) error {
	file, err := archive.Create("bleemeo-sync-facts.json")
	if err != nil {
		return err
	}

	s.l.Lock()

	obj := struct {
		LastFactUpdatedAt string
	}{
		LastFactUpdatedAt: s.lastFactUpdatedAt,
	}

	s.l.Unlock()

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")

	return enc.Encode(obj)
}

type syncFactsExecution struct {
	parent    *SyncFacts
	execution types.SynchronizationExecution
}

// NeedSynchronization synchronizes the facts when they were updated, and when the SNMP agents
// are synchronized: the facts include theirs.
func (e *syncFactsExecution) NeedSynchronization(ctx context.Context) (bool, error) {
	if err := e.execution.RequestLinkedSynchronization(types.EntityFact, types.EntitySNMP); err != nil {
		return false, err
	}

	return e.parent.needSynchronization(ctx, e.execution), nil
}

func (e *syncFactsExecution) RefreshCache(_ context.Context, _ types.SyncType) error {
	return nil
}

func (e *syncFactsExecution) SyncRemoteAndLocal(ctx context.Context, _ types.SyncType) error {
	option := e.execution.Option()

	localFacts, err := option.Facts.Facts(ctx, 24*time.Hour)
	if err != nil {
		return err
	}

	onlyEssential := e.execution.IsOnlyEssential()

	if onlyEssential {
		essentialFacts := getEssentialFacts()
		copyFacts := make(map[string]string)

		for k, v := range localFacts {
			if essentialFacts[k] {
				copyFacts[k] = v
			}
		}

		localFacts = copyFacts
	}

	agentID, _ := option.State.BleemeoCredentials()

	allAgentFacts := make(map[string]map[string]string, 1+len(option.SNMP))
	allAgentFacts[agentID] = localFacts

	if !onlyEssential {
		if err := e.addOtherAgentsFacts(ctx, allAgentFacts); err != nil {
			return err
		}
	}

	apiClient := e.execution.BleemeoAPIClient()

	// The duplicated agent check must run before we touch the facts on the API, and it refreshes
	// the facts list from the API, which the code below relies on.
	if err := e.parent.checkDuplicated(ctx, apiClient, e.execution.StartedAt()); err != nil {
		return err
	}

	if err := syncRemoteAndLocal(ctx, apiClient, option.Cache, allAgentFacts, onlyEssential); err != nil {
		return err
	}

	if onlyEssential {
		// localFacts was filtered, the facts aren't fully synchronized.
		return nil
	}

	e.parent.l.Lock()
	e.parent.lastFactUpdatedAt = localFacts[facts.FactUpdatedAt]
	e.parent.l.Unlock()

	return nil
}

func (e *syncFactsExecution) FinishExecution(_ context.Context) {
}

// addOtherAgentsFacts adds the facts of the SNMP and vSphere agents to allAgentFacts.
func (e *syncFactsExecution) addOtherAgentsFacts(ctx context.Context, allAgentFacts map[string]map[string]string) error {
	option := e.execution.Option()

	agentTypeID, found := option.Cache.AgentTypeID(bleemeo.AgentType_SNMP)
	if !found {
		return types.ErrRetryLater
	}

	previousFacts := option.Cache.FactsByKey()
	remoteAgentList := option.Cache.AgentsByUUID()

	for _, t := range option.SNMP {
		if agent, err := e.parent.snmp.FindAgent(ctx, t, agentTypeID, remoteAgentList); err == nil {
			facts, err := t.Facts(ctx, 24*time.Hour)
			if err != nil {
				logger.V(2).Printf("unable to get SNMP facts: %v", err)

				// Reuse previous facts
				tmp := previousFacts[agent.ID]
				facts = make(map[string]string, len(tmp))

				for _, v := range tmp {
					facts[v.Key] = v.Value
				}
			}

			allAgentFacts[agent.ID] = facts
		}
	}

	for _, dev := range option.VSphereDevices(ctx, time.Hour) {
		agentTypeID, found := syncvsphere.AgentType(option.Cache, dev.Kind())
		if !found {
			continue
		}

		if agent, err := e.parent.vsphere.FindAgent(ctx, dev, agentTypeID, remoteAgentList); err == nil {
			allAgentFacts[agent.ID] = dev.Facts()
		}
	}

	return nil
}

// syncRemoteAndLocal registers the facts of allAgentFacts, by agent ID, that changed, and
// deletes the others. The cache must hold the facts list from the API. Unless onlyEssential,
// in which case allAgentFacts lacks some facts, the facts which are no longer local are deleted.
func syncRemoteAndLocal(ctx context.Context, apiClient types.FactClient, c *cache.Cache, allAgentFacts map[string]map[string]string, onlyEssential bool) error {
	// s.serviceDeleteFromRemote() is unneeded, API don't delete facts

	// First delete duplicated facts but kept at least one facts for each key.
	// Normally this won't delete anything, it only does if an error occur during previous
	// execution of factRegister or the factDeleteFromLocal after factRegister.
	if err := deleteFromLocal(ctx, apiClient, c, allAgentFacts, true); err != nil {
		return err
	}

	if err := register(ctx, apiClient, c, allAgentFacts); err != nil {
		return err
	}

	if onlyEssential {
		// allAgentFacts was filtered, can't delete
		return nil
	}

	return deleteFromLocal(ctx, apiClient, c, allAgentFacts, false)
}

// UpdateList updates the facts list of the cache from the API.
func UpdateList(ctx context.Context, apiClient types.FactClient, c *cache.Cache) error {
	facts, err := apiClient.ListFacts(ctx)
	if err != nil {
		return err
	}

	c.SetFacts(facts)

	return nil
}

func register(ctx context.Context, apiClient types.FactClient, c *cache.Cache, allAgentFacts map[string]map[string]string) error {
	registeredFacts := c.FactsByKey()
	facts := c.Facts()

	for agentID, localFacts := range allAgentFacts {
		for key, value := range localFacts {
			remoteValue := registeredFacts[agentID][key]
			if value == remoteValue.Value {
				continue
			}

			logger.V(3).Printf("fact %s:%#v changed from %#v to %#v", agentID, key, remoteValue.Value, value)

			// Agent can't update fact. We delete and re-create the facts
			payload := bleemeoTypes.AgentFact{
				AgentID: agentID,
				Key:     key,
				Value:   value,
			}

			result, err := apiClient.RegisterFact(ctx, payload)
			if err != nil {
				// Even in case of error, stop facts that was already registered.
				// Without this, due to fact registration error, Glouton might think it has a duplicated state.json
				// * The previous run of glouton had glouton_pid=100
				// * Glouton register glouton_pid=200 facts
				// * Another facts fail. If we don't save, the following will occur
				// * During check checkDuplicated, oldFacts will contains PID=100, but newFacts,
				//   (since checkDuplicated update list from Bleemeo API) contains PID=200
				//   So checkDuplicated think Glouton is duplicated, when it's wrong.
				c.SetFacts(facts)

				return err
			}

			facts = append(facts, result)
			logger.V(2).Printf("Send fact %s:%s, stored with uuid %s", agentID, key, result.ID)
		}
	}

	c.SetFacts(facts)

	return nil
}

func deleteFromLocal(ctx context.Context, apiClient types.FactClient, c *cache.Cache, allAgentFacts map[string]map[string]string, keptAtLeastOne bool) error {
	factsToKeepGoodValue := make(map[string]string)  // map agentID+key -> factID
	factsToKeepOtherValue := make(map[string]string) // map agentID+key -> factID
	registeredFacts := c.FactsByUUID()

	for _, v := range registeredFacts {
		localFacts := allAgentFacts[v.AgentID]
		localValue, localOk := localFacts[v.Key]

		key := v.AgentID + "\x00" + v.Key

		if localOk && localValue == v.Value {
			if keptID, ok := factsToKeepGoodValue[key]; !ok || keptID > v.ID {
				factsToKeepGoodValue[key] = v.ID
			}

			continue
		}

		if keptID, ok := factsToKeepOtherValue[key]; !ok || keptID > v.ID {
			factsToKeepOtherValue[key] = v.ID
		}
	}

	dontDeleteUUID := make(map[string]bool, len(factsToKeepGoodValue))
	for _, id := range factsToKeepGoodValue {
		dontDeleteUUID[id] = true
	}

	if keptAtLeastOne {
		for k, id := range factsToKeepOtherValue {
			// Only kept from factsToKeepOtherValue if the key (agentID + fact key) isn't already present in factsToKeepGoodValue
			if _, ok := factsToKeepGoodValue[k]; !ok {
				dontDeleteUUID[id] = true
			}
		}
	}

	facts := make([]bleemeoTypes.AgentFact, 0, len(registeredFacts))

	for _, v := range registeredFacts {
		if dontDeleteUUID[v.ID] {
			facts = append(facts, v)

			continue
		}

		err := apiClient.DeleteFact(ctx, v.ID)
		// If the fact wasn't found, it has already been deleted.
		if err != nil && !types.IsNotFound(err) {
			return err
		}

		logger.V(2).Printf("Fact %s:%v (uuid=%v) deleted", v.AgentID, v.Key, v.ID)
	}

	c.SetFacts(facts)

	return nil
}
