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
	"slices"
	"time"

	"github.com/bleemeo/bleemeo-go"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/bleemeoapi"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/inputs/vsphere"
	"github.com/bleemeo/glouton/logger"

	"github.com/google/uuid"
)

const (
	tooManyConsecutiveError       = 3
	vSphereAgentsPurgeMinInterval = 2 * time.Minute
	vSphereCachePrefix            = "bleemeo:vsphere:"
	vSphereAgentFields            = "id,display_name,account,agent_type,abstracted,fqdn,initial_password,created_at,next_config_at,current_config,tags,initial_server_group_name"
)

func (s *Synchronizer) syncVSphere(ctx context.Context, syncType types.SyncType, execution types.SynchronizationExecution) (updateThresholds bool, err error) {
	_ = syncType

	cfg, ok := s.option.Cache.CurrentAccountConfig()
	if !ok || !cfg.VSphereIntegration {
		return false, nil
	}

	if execution.IsOnlyEssential() {
		// no essential vSphere, skip registering.
		return false, nil
	}

	return false, s.VSphereRegisterAndUpdate(ctx, execution.BleemeoAPIClient(), s.option.VSphereDevices(ctx, time.Hour))
}

// When modifying this type, ensure to keep the compatibility with the code of
// vSphere.statusesWhenNoDevices(), in inputs/vsphere/vsphere.go. // TODO: is comment still relevant ?
type vSphereAssociation struct {
	MOID   string
	Source string
	ID     string
	// Meant for local usage
	key string
}

func deviceAssoKey(device bleemeoTypes.VSphereDevice) string {
	return device.MOID() + "__" + device.Source()
}

func (s *Synchronizer) FindVSphereAgent(ctx context.Context, device bleemeoTypes.VSphereDevice, agentTypeID string, agentsByID map[string]bleemeoTypes.Agent) (bleemeoTypes.Agent, error) {
	var association vSphereAssociation

	err := s.option.State.Get(vSphereCachePrefix+deviceAssoKey(device), &association)
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
	// Otherwise, if no agent is found, errNotExist is returned.

	devices := s.option.VSphereDevices(ctx, time.Hour)
	associatedID := make(map[string]bool, len(devices))

	for _, dev := range devices {
		err := s.option.State.Get(vSphereCachePrefix+deviceAssoKey(dev), &association)
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

	return bleemeoTypes.Agent{}, errNotExist
}

func (s *Synchronizer) VSphereRegisterAndUpdate(ctx context.Context, apiClient types.VSphereClient, localDevices []bleemeoTypes.VSphereDevice) error {
	vSphereAgentTypes, found := s.GetVSphereAgentTypes()
	if !found {
		return errRetryLater
	}

	remoteAgentList := s.option.Cache.AgentsByUUID()

	seenDeviceAgents := make(map[string]string, len(localDevices))

	var ( //nolint: prealloc
		newAgents []bleemeoTypes.Agent
		errs      []error
	)

	for _, device := range localDevices {
		agentTypeID, ok := vSphereAgentTypes[device.Kind()]
		if !ok {
			logger.V(1).Printf("Unknown vSphere kind %q for device %s", device.Kind(), device.FQDN())

			continue
		}

		if a, err := s.FindVSphereAgent(ctx, device, agentTypeID, remoteAgentList); err != nil && !errors.Is(err, errNotExist) {
			logger.V(2).Printf("Skip registration of vSphere agent: %v", err)

			continue
		} else if err == nil {
			seenDeviceAgents[a.ID] = agentTypeID

			continue
		}

		serverGroup := s.option.Config.Bleemeo.InitialServerGroupNameForVSphere
		if serverGroup == "" {
			serverGroup = s.option.Config.Bleemeo.InitialServerGroupName
		}

		payload := bleemeoapi.AgentPayload{
			Agent: bleemeoTypes.Agent{
				FQDN:        device.FQDN(),
				DisplayName: device.Name(),
				AgentType:   agentTypeID,
				Tags:        []bleemeoTypes.Tag{},
			},
			Abstracted:         true,
			InitialPassword:    uuid.New().String(),
			InitialServerGroup: serverGroup,
		}

		registeredAgent, err := s.remoteRegisterVSphereDevice(ctx, apiClient, payload)
		if err != nil {
			errs = append(errs, err)

			if len(errs) > tooManyConsecutiveError {
				break
			}

			continue
		}

		newAgents = append(newAgents, registeredAgent)
		seenDeviceAgents[registeredAgent.ID] = agentTypeID

		err = s.option.State.Set(vSphereCachePrefix+deviceAssoKey(device), vSphereAssociation{
			MOID:   device.MOID(),
			Source: device.Source(),
			ID:     registeredAgent.ID,
		})
		if err != nil {
			logger.V(2).Printf("Failed to update state: %v", err)
		}
	}

	if len(newAgents) > 0 {
		agents := s.option.Cache.Agents()
		agents = append(agents, newAgents...)
		s.option.Cache.SetAgentList(agents)
	}

	if s.now().After(s.lastVSphereAgentsPurge.Add(vSphereAgentsPurgeMinInterval)) && len(remoteAgentList) > 0 {
		agentTypeIDsToPurge := map[string]bool{
			vSphereAgentTypes[vsphere.KindCluster]: true,
			vSphereAgentTypes[vsphere.KindHost]:    true,
			vSphereAgentTypes[vsphere.KindVM]:      true,
		}

		err := s.purgeVSphereAgents(ctx, apiClient, remoteAgentList, seenDeviceAgents, agentTypeIDsToPurge)
		if err != nil {
			logger.V(1).Printf("Failed to purge vSphere agents: %v", err)
		}

		s.lastVSphereAgentsPurge = s.now()
	}

	return errors.Join(errs...)
}

func (s *Synchronizer) remoteRegisterVSphereDevice(ctx context.Context, apiClient types.VSphereClient, payload bleemeoapi.AgentPayload) (bleemeoTypes.Agent, error) {
	result, err := apiClient.RegisterVSphereAgent(ctx, payload)
	if err != nil {
		return result, err
	}

	logger.V(2).Printf("vSphere agent %v registered with UUID %s", payload.DisplayName, result.ID)

	return result, nil
}

// GetVSphereAgentTypes returns a map[vSphereKind]=>AgentTypeID of all vSphere resource kinds.
func (s *Synchronizer) GetVSphereAgentTypes() (map[vsphere.ResourceKind]string, bool) {
	const vSphereAgentTypesCount = 3

	agentTypes := s.option.Cache.AgentTypes()
	vSphereAgentTypes := make(map[vsphere.ResourceKind]string, vSphereAgentTypesCount)

	for i := 0; i < len(agentTypes) && len(vSphereAgentTypes) < vSphereAgentTypesCount; i++ {
		switch a := agentTypes[i]; a.Name { //nolint: exhaustive
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

func (s *Synchronizer) GetVSphereAgentType(kind vsphere.ResourceKind) (agentTypeID string, found bool) {
	vSphereAgentTypes, found := s.GetVSphereAgentTypes()
	if !found {
		return "", false
	}

	agentTypeID, found = vSphereAgentTypes[kind]

	return agentTypeID, found
}

func (s *Synchronizer) purgeVSphereAgents(ctx context.Context, apiClient types.VSphereClient, remoteAgents map[string]bleemeoTypes.Agent, seenDeviceAgents map[string]string, vSphereAgentTypes map[string]bool) error {
	associations, err := s.option.State.GetByPrefix(vSphereCachePrefix, vSphereAssociation{})
	if err != nil {
		return err
	}

	assosByID := make(map[string]vSphereAssociation, len(associations))

	for key, value := range associations {
		asso, ok := value.(vSphereAssociation)
		if !ok {
			continue // false-positive from GetByPrefix()
		}

		asso.key = key

		assosByID[asso.ID] = asso
	}

	failingEndpoints := s.option.VSphereEndpointsInError()
	agentsToRemoveFromCache := make(map[string]bool)

	defer s.removeAgentsFromCache(agentsToRemoveFromCache)

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

			err = s.option.State.Delete(asso.key)
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
		if err != nil && !IsNotFound(err) {
			return err
		}
	}

	return nil
}

func (s *Synchronizer) removeAgentsFromCache(agentsToRemove map[string]bool) {
	agents := s.option.Cache.Agents()
	finalAgents := make([]bleemeoTypes.Agent, 0, len(agents)-len(agentsToRemove))

	for _, agent := range agents {
		if agentsToRemove[agent.ID] {
			continue // agent is not added to the final list
		}

		finalAgents = append(finalAgents, agent)
	}

	s.option.Cache.SetAgentList(slices.Clip(finalAgents))
}
