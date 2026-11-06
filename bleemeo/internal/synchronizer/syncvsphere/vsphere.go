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

// Package syncvsphere registers the vSphere devices on Bleemeo, as agents, and deletes the
// agents of the devices gone.
package syncvsphere

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/bleemeo/bleemeo-go"
	"github.com/bleemeo/glouton/bleemeo/internal/cache"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/bleemeoapi"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/inputs/vsphere"
	"github.com/bleemeo/glouton/logger"

	"github.com/google/uuid"
)

const (
	tooManyConsecutiveError = 3
	agentsPurgeMinInterval  = 2 * time.Minute
	cachePrefix             = "bleemeo:vsphere:"
)

// deviceAssociation is the agent of a device, saved in the State under cachePrefix and
// deviceAssoKey.
type deviceAssociation struct {
	MOID   string
	Source string
	ID     string
	// key is the State key of the association. It isn't saved.
	key string
}

func deviceAssoKey(device bleemeoTypes.VSphereDevice) string {
	return device.MOID() + "__" + device.Source()
}

// SyncVSphere registers an agent for each vSphere device, and deletes the agents of the
// devices gone.
type SyncVSphere struct {
	state   bleemeoTypes.State
	devices func(ctx context.Context, maxAge time.Duration) []bleemeoTypes.VSphereDevice
	now     func() time.Time
	// lastChange is the last change of the devices on the last successful synchronization.
	lastChange      time.Time
	lastAgentsPurge time.Time
}

// New returns the entity. state keeps the agent of each device, devices lists the devices.
// now is the synchronizer's clock.
func New(state bleemeoTypes.State, devices func(ctx context.Context, maxAge time.Duration) []bleemeoTypes.VSphereDevice, now func() time.Time) *SyncVSphere {
	return &SyncVSphere{state: state, devices: devices, now: now}
}

func (s *SyncVSphere) Name() types.EntityName {
	return types.EntityVSphere
}

func (s *SyncVSphere) EnabledInMaintenance() bool {
	return false
}

func (s *SyncVSphere) EnabledInSuspendedMode() bool {
	return false
}

func (s *SyncVSphere) PrepareExecution(_ context.Context, execution types.SynchronizationExecution) (types.EntitySynchronizerExecution, error) {
	return &syncVSphereExecution{
		parent:    s,
		execution: execution,
	}, nil
}

type syncVSphereExecution struct {
	parent    *SyncVSphere
	execution types.SynchronizationExecution
}

// NeedSynchronization synchronizes the vSphere agents when the devices changed. SyncRemoteAndLocal
// only updates lastChange once it succeeds, so a failed synchronization is retried on the next
// execution.
func (e *syncVSphereExecution) NeedSynchronization(ctx context.Context) (bool, error) {
	return e.execution.Option().LastVSphereChange(ctx).After(e.parent.lastChange), nil
}

// RefreshCache does nothing: the vSphere agents are in the agent list of the cache, which
// EntityAgent refreshes, before this entity, on full synchronizations. A failed full
// synchronization is retried on the next execution, so this entity always runs on an agent
// list from a successful refresh. When the refresh fails, EntityAgent fails this entity
// rather than letting it register duplicates of the agents missing from the list.
func (e *syncVSphereExecution) RefreshCache(_ context.Context, _ types.SyncType) error {
	return nil
}

// SyncRemoteAndLocal registers and purges the vSphere agents, and updates lastChange when it
// succeeds.
func (e *syncVSphereExecution) SyncRemoteAndLocal(ctx context.Context, _ types.SyncType) error {
	// Read before listing the devices: a change meanwhile is synchronized on the next execution.
	lastChange := e.execution.Option().LastVSphereChange(ctx)

	if err := e.syncRemoteAndLocal(ctx); err != nil {
		return err
	}

	e.parent.lastChange = lastChange

	return nil
}

func (e *syncVSphereExecution) syncRemoteAndLocal(ctx context.Context) error {
	option := e.execution.Option()

	cfg, ok := option.Cache.CurrentAccountConfig()
	if !ok || !cfg.VSphereIntegration {
		return nil
	}

	if e.execution.IsOnlyEssential() {
		// no essential vSphere, skip registering.
		return nil
	}

	serverGroup := option.Config.Bleemeo.InitialServerGroupNameForVSphere
	if serverGroup == "" {
		serverGroup = option.Config.Bleemeo.InitialServerGroupName
	}

	return e.parent.registerAndUpdate(
		ctx,
		e.execution.BleemeoAPIClient(),
		option.Cache,
		e.parent.devices(ctx, time.Hour),
		option.VSphereEndpointsInError(),
		serverGroup,
	)
}

func (e *syncVSphereExecution) FinishExecution(_ context.Context) {
}

// FindAgent returns the agent of the device, among agentsByID. agentTypeID is the ID of the
// agent type of the device. It returns types.ErrNotExist when the device has no agent.
func (s *SyncVSphere) FindAgent(ctx context.Context, device bleemeoTypes.VSphereDevice, agentTypeID string, agentsByID map[string]bleemeoTypes.Agent) (bleemeoTypes.Agent, error) {
	var association deviceAssociation

	err := s.state.Get(cachePrefix+deviceAssoKey(device), &association)
	if err != nil {
		return bleemeoTypes.Agent{}, err
	}

	if agent, ok := agentsByID[association.ID]; ok && association.ID != "" {
		return agent, nil
	}

	// If no agent has been found using the sole association key,
	// for each vSphere device we will try to match any vSphere agent that:
	// has the same agent type, don't have current association and has the same FQDN.
	// If the cache has been lost, the agent can be retrieved this way.
	// Otherwise, if no agent is found, types.ErrNotExist is returned.

	devices := s.devices(ctx, time.Hour)
	associatedID := make(map[string]bool, len(devices))

	for _, dev := range devices {
		err := s.state.Get(cachePrefix+deviceAssoKey(dev), &association)
		if err != nil {
			return bleemeoTypes.Agent{}, err
		}

		if association.ID != "" {
			associatedID[association.ID] = true
		}
	}

	for _, agent := range agentsByID {
		if agent.AgentType != agentTypeID {
			continue
		}

		if _, ok := associatedID[agent.ID]; ok {
			continue
		}

		if agent.FQDN == device.FQDN() && device.FQDN() != "" {
			return agent, nil
		}
	}

	return bleemeoTypes.Agent{}, types.ErrNotExist
}

// registerAndUpdate registers an agent for the devices which have none, in serverGroup, and
// purges the agents of the devices gone. failingEndpoints are the vCenter endpoints which
// failed to list their devices.
func (s *SyncVSphere) registerAndUpdate(
	ctx context.Context,
	apiClient types.VSphereClient,
	c *cache.Cache,
	localDevices []bleemeoTypes.VSphereDevice,
	failingEndpoints map[string]bool,
	serverGroup string,
) error {
	vSphereAgentTypes, found := AgentTypes(c)
	if !found {
		return types.ErrRetryLater
	}

	remoteAgentList := c.AgentsByUUID()

	seenDeviceAgents := make(map[string]string, len(localDevices))

	var (
		newAgents []bleemeoTypes.Agent
		errs      []error
	)

	for _, device := range localDevices {
		agentTypeID, ok := vSphereAgentTypes[device.Kind()]
		if !ok {
			logger.V(1).Printf("Unknown vSphere kind %q for device %s", device.Kind(), device.FQDN())

			continue
		}

		if a, err := s.FindAgent(ctx, device, agentTypeID, remoteAgentList); err != nil && !errors.Is(err, types.ErrNotExist) {
			logger.V(2).Printf("Skip registration of vSphere agent: %v", err)

			continue
		} else if err == nil {
			seenDeviceAgents[a.ID] = agentTypeID

			continue
		}

		payload := bleemeoapi.AgentPayload{
			FQDN:               device.FQDN(),
			DisplayName:        device.Name(),
			AgentType:          agentTypeID,
			Tags:               []bleemeoTypes.Tag{},
			Abstracted:         true,
			InitialPassword:    uuid.New().String(),
			InitialServerGroup: serverGroup,
		}

		registeredAgent, err := remoteRegister(ctx, apiClient, payload)
		if err != nil {
			errs = append(errs, err)

			if len(errs) > tooManyConsecutiveError {
				break
			}

			continue
		}

		newAgents = append(newAgents, registeredAgent)
		seenDeviceAgents[registeredAgent.ID] = agentTypeID

		err = s.state.Set(cachePrefix+deviceAssoKey(device), deviceAssociation{
			MOID:   device.MOID(),
			Source: device.Source(),
			ID:     registeredAgent.ID,
		})
		if err != nil {
			logger.V(2).Printf("Failed to update state: %v", err)
		}
	}

	if len(newAgents) > 0 {
		agents := c.Agents()
		agents = append(agents, newAgents...)
		c.SetAgentList(agents)
	}

	if s.now().After(s.lastAgentsPurge.Add(agentsPurgeMinInterval)) && len(remoteAgentList) > 0 {
		agentTypeIDsToPurge := map[string]bool{
			vSphereAgentTypes[vsphere.KindCluster]: true,
			vSphereAgentTypes[vsphere.KindHost]:    true,
			vSphereAgentTypes[vsphere.KindVM]:      true,
		}

		err := s.purgeAgents(ctx, apiClient, c, remoteAgentList, seenDeviceAgents, agentTypeIDsToPurge, failingEndpoints)
		if err != nil {
			logger.V(1).Printf("Failed to purge vSphere agents: %v", err)
		}

		s.lastAgentsPurge = s.now()
	}

	return errors.Join(errs...)
}

func remoteRegister(ctx context.Context, apiClient types.VSphereClient, payload bleemeoapi.AgentPayload) (bleemeoTypes.Agent, error) {
	result, err := apiClient.RegisterVSphereAgent(ctx, payload)
	if err != nil {
		return result, err
	}

	logger.V(2).Printf("vSphere agent %v registered with UUID %s", payload.DisplayName, result.ID)

	return result, nil
}

// AgentTypes returns a map[vSphereKind]=>AgentTypeID of all vSphere resource kinds.
func AgentTypes(c *cache.Cache) (map[vsphere.ResourceKind]string, bool) {
	const vSphereAgentTypesCount = 3

	agentTypes := c.AgentTypes()
	vSphereAgentTypes := make(map[vsphere.ResourceKind]string, vSphereAgentTypesCount)

	for i := 0; i < len(agentTypes) && len(vSphereAgentTypes) < vSphereAgentTypesCount; i++ {
		switch a := agentTypes[i]; a.Name { //nolint: exhaustive,nolintlint
		case bleemeo.AgentType_vSphereCluster:
			vSphereAgentTypes[vsphere.KindCluster] = a.ID
		case bleemeo.AgentType_vSphereHost:
			vSphereAgentTypes[vsphere.KindHost] = a.ID
		case bleemeo.AgentType_vSphereVM:
			vSphereAgentTypes[vsphere.KindVM] = a.ID
		}
	}

	return vSphereAgentTypes, len(vSphereAgentTypes) == vSphereAgentTypesCount
}

// AgentType returns the ID of the agent type of the vSphere resource kind.
func AgentType(c *cache.Cache, kind vsphere.ResourceKind) (agentTypeID string, found bool) {
	vSphereAgentTypes, found := AgentTypes(c)
	if !found {
		return "", false
	}

	agentTypeID, found = vSphereAgentTypes[kind]

	return agentTypeID, found
}

func (s *SyncVSphere) purgeAgents(
	ctx context.Context,
	apiClient types.VSphereClient,
	c *cache.Cache,
	remoteAgents map[string]bleemeoTypes.Agent,
	seenDeviceAgents map[string]string,
	vSphereAgentTypes map[string]bool,
	failingEndpoints map[string]bool,
) error {
	associations, err := s.state.GetByPrefix(cachePrefix, deviceAssociation{})
	if err != nil {
		return err
	}

	assosByID := make(map[string]deviceAssociation, len(associations))

	for key, value := range associations {
		asso, ok := value.(deviceAssociation)
		if !ok {
			continue // false-positive from GetByPrefix()
		}

		asso.key = key

		assosByID[asso.ID] = asso
	}

	agentsToRemoveFromCache := make(map[string]bool)

	defer removeAgentsFromCache(c, agentsToRemoveFromCache)

	for id, agent := range remoteAgents {
		if !vSphereAgentTypes[agent.AgentType] { // Not a vSphere device
			continue
		}

		if _, ok := seenDeviceAgents[id]; ok { // The device still exists
			continue
		}

		if asso, hasAsso := assosByID[id]; hasAsso {
			if failingEndpoints[asso.Source] {
				// Spare this device, as its vCenter endpoint is in trouble.
				continue
			}

			err = s.state.Delete(asso.key)
			if err != nil {
				logger.V(1).Printf("Failed to remove vSphere association from cache: %v", err)
			}
		} else { //nolint: gocritic
			// When endpoints are failing, we can't tell which unassociated agents belong to them.
			// So, we wait to be able to list the devices of all endpoints
			// to be sure we know which devices have been deleted and which haven't.
			if len(failingEndpoints) != 0 {
				continue
			}
		}

		logger.V(2).Printf("Deleting vSphere agent %s", id)

		agentsToRemoveFromCache[id] = true

		err = apiClient.DeleteAgent(ctx, id)
		if err != nil && !types.IsNotFound(err) {
			return err
		}
	}

	return nil
}

func removeAgentsFromCache(c *cache.Cache, agentsToRemove map[string]bool) {
	agents := c.Agents()
	finalAgents := make([]bleemeoTypes.Agent, 0, len(agents)-len(agentsToRemove))

	for _, agent := range agents {
		if agentsToRemove[agent.ID] {
			continue // agent is not added to the final list
		}

		finalAgents = append(finalAgents, agent)
	}

	c.SetAgentList(slices.Clip(finalAgents))
}
