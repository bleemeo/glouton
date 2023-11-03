// Copyright 2015-2023 Bleemeo
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
	"glouton/bleemeo/client"
	"glouton/bleemeo/types"
	"glouton/inputs/vsphere"
	"glouton/logger"
	"slices"
	"time"

	"github.com/google/uuid"
)

const (
	tooManyConsecutiveError       = 3
	vSphereAgentsPurgeMinInterval = 2 * time.Minute
)

func (s *Synchronizer) syncVSphere(ctx context.Context, fullSync bool, onlyEssential bool) (updateThresholds bool, err error) {
	_ = fullSync

	cfg, ok := s.option.Cache.CurrentAccountConfig()
	if !ok || !cfg.VSphereIntegration {
		return false, nil
	}

	if onlyEssential {
		// no essential vSphere, skip registering.
		return false, nil
	}

	logger.Printf("Synchronizing vSphere") // TODO: remove

	return false, s.VSphereRegisterAndUpdate(s.option.VSphereDevices(ctx, time.Hour))
}

// When modifying this type, ensure to keep the compatibility with the code of
// vSphere.statusesWhenNoDevices(), in inputs/vsphere/vsphere.go.
type vSphereAssociation struct {
	MOID   string
	Source string
	ID     string
	// Meant for local usage
	key string
}

func deviceAssoKey(device types.VSphereDevice) string {
	return device.MOID() + "__" + device.Source()
}

func (s *Synchronizer) FindVSphereAgent(ctx context.Context, device types.VSphereDevice, agentTypeID string, agentsByID map[string]types.Agent) (types.Agent, error) {
	var association vSphereAssociation

	err := s.option.State.Get("bleemeo:vsphere:"+deviceAssoKey(device), &association)
	if err != nil {
		return types.Agent{}, err
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
		err := s.option.State.Get("bleemeo:vsphere:"+deviceAssoKey(dev), &association)
		if err != nil {
			return types.Agent{}, err
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

	return types.Agent{}, errNotExist
}

func (s *Synchronizer) VSphereRegisterAndUpdate(localDevices []types.VSphereDevice) error {
	vSphereAgentTypes, found := s.GetVSphereAgentTypes()
	if !found {
		return errRetryLater
	}

	remoteAgentList := s.option.Cache.AgentsByUUID()

	params := map[string]string{
		"fields": "id,display_name,account,agent_type,abstracted,fqdn,initial_password,created_at,next_config_at,current_config,tags,initial_server_group_name",
	}
	seenDeviceAgents := make(map[string]string, len(localDevices))

	var ( //nolint: prealloc
		newAgents []types.Agent
		errs      []error
	)

	for _, device := range localDevices {
		agentTypeID, ok := vSphereAgentTypes[device.Kind()]
		if !ok {
			logger.V(1).Printf("Unknown vSphere kind %q for device %s", device.Kind(), device.FQDN())

			continue
		}

		if a, err := s.FindVSphereAgent(s.ctx, device, agentTypeID, remoteAgentList); err != nil && !errors.Is(err, errNotExist) {
			logger.V(0).Printf("Skip registration of vSphere agent: %v", err) // TODO: V(2)

			continue
		} else if err == nil {
			seenDeviceAgents[a.ID] = agentTypeID

			continue
		}

		serverGroup := s.option.Config.Bleemeo.InitialServerGroupNameForVSphere
		if serverGroup == "" {
			serverGroup = s.option.Config.Bleemeo.InitialServerGroupName
		}

		payload := payloadAgent{
			Agent: types.Agent{
				FQDN:        device.FQDN(),
				DisplayName: device.Name(),
				AgentType:   agentTypeID,
				Tags:        []types.Tag{},
			},
			Abstracted:         true,
			InitialPassword:    uuid.New().String(),
			InitialServerGroup: serverGroup,
		}

		registeredAgent, err := s.remoteRegisterVSphereDevice(params, payload)
		if err != nil {
			errs = append(errs, err)

			if len(errs) > tooManyConsecutiveError {
				break
			}

			continue
		}

		newAgents = append(newAgents, registeredAgent)
		seenDeviceAgents[registeredAgent.ID] = agentTypeID

		err = s.option.State.Set("bleemeo:vsphere:"+deviceAssoKey(device), vSphereAssociation{
			MOID:   device.MOID(),
			Source: device.Source(),
			ID:     registeredAgent.ID,
		})
		if err != nil {
			logger.V(0).Printf("Failed to update state: %v", err) // TODO: V(2)
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

		err := s.purgeVSphereAgents(remoteAgentList, seenDeviceAgents, agentTypeIDsToPurge)
		if err != nil {
			logger.V(1).Printf("Failed to purge vSphere agents: %v", err)
		}

		s.lastVSphereAgentsPurge = s.now()
	}

	return errors.Join(errs...)
}

func (s *Synchronizer) remoteRegisterVSphereDevice(params map[string]string, payload payloadAgent) (types.Agent, error) {
	var result types.Agent

	_, err := s.client.Do(s.ctx, "POST", "v1/agent/", params, payload, &result)
	if err != nil {
		return result, err
	}

	logger.V(0).Printf("vSphere agent %v registered with UUID %s", payload.DisplayName, result.ID) // TODO: V(2)

	return result, nil
}

// GetVSphereAgentTypes returns a map[vSphereKind]=>AgentTypeID of all vSphere resource kinds.
func (s *Synchronizer) GetVSphereAgentTypes() (map[vsphere.ResourceKind]string, bool) {
	const vSphereAgentTypesCount = 3

	agentTypes := s.option.Cache.AgentTypes()
	vSphereAgentTypes := make(map[vsphere.ResourceKind]string, vSphereAgentTypesCount)

	for i := 0; i < len(agentTypes) && len(vSphereAgentTypes) < vSphereAgentTypesCount; i++ {
		switch a := agentTypes[i]; {
		case a.Name == types.AgentTypeVSphereCluster:
			vSphereAgentTypes[vsphere.KindCluster] = a.ID
		case a.Name == types.AgentTypeVSphereHost:
			vSphereAgentTypes[vsphere.KindHost] = a.ID
		case a.Name == types.AgentTypeVSphereVM:
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

func (s *Synchronizer) purgeVSphereAgents(remoteAgents map[string]types.Agent, seenDeviceAgents map[string]string, vSphereAgentTypes map[string]bool) error {
	associations, err := s.option.State.GetByPrefix("bleemeo:vsphere:", vSphereAssociation{})
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

	logger.Printf("Failing endpoints: %v", failingEndpoints) // TODO: remove

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

			logger.Printf("Endpoint %q is not failing; removing %q", asso.Source, asso.MOID) // TODO: remove

			err = s.option.State.Delete(asso.key)
			if err != nil {
				logger.V(1).Printf("Failed to remove vSphere association from cache: %v", err)
			}
		} else {
			logger.Printf("Device %q (%s) has no association ...", agent.DisplayName, id) // TODO: remove
		}

		agentsToRemoveFromCache[id] = true

		logger.Printf("Deleting agent %q (%s), which is not present locally", agent.DisplayName, id) // TODO: remove

		_, err := s.client.Do(s.ctx, "DELETE", fmt.Sprintf("v1/agent/%s/", id), nil, nil, nil)
		if err != nil && !client.IsNotFound(err) {
			return err
		}
	}

	return nil
}

func (s *Synchronizer) removeAgentsFromCache(agentsToRemove map[string]bool) {
	agents := s.option.Cache.Agents()
	finalAgents := make([]types.Agent, 0, len(agents)-len(agentsToRemove))

	for _, agent := range agents {
		if agentsToRemove[agent.ID] {
			continue // agent is not added to the final list
		}

		finalAgents = append(finalAgents, agent)
	}

	finalAgents = slices.Clip(finalAgents)

	s.option.Cache.SetAgentList(finalAgents)
}
