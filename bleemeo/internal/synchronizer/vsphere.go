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
	"glouton/bleemeo/types"
	"glouton/inputs/vsphere"
	"glouton/logger"
	"time"

	"github.com/google/uuid"
)

func (s *Synchronizer) syncVSphere(ctx context.Context, fullSync bool, onlyEssential bool) (updateThresholds bool, err error) {
	_ = fullSync

	if onlyEssential {
		// no essential vSphere, skip registering.
		return false, nil
	}

	logger.Printf("Synchronizing vSphere") // TODO: remove

	return false, s.vSphereRegisterAndUpdate(s.option.VSphereDevices(ctx, time.Hour))
}

type vSphereAssociation struct {
	FQDN string
	ID   string
}

func (s *Synchronizer) FindVSphereAgent(ctx context.Context, device vsphere.Device, agentTypeID string, agentsByID map[string]types.Agent) (types.Agent, error) {
	var association vSphereAssociation

	err := s.option.State.Get("bleemeo:vsphere:"+device.FQDN(), &association)
	if err != nil {
		return types.Agent{}, err
	}

	if agent, ok := agentsByID[association.ID]; ok && association.ID != "" {
		return agent, nil
	}

	// For each vSphere device, try to match any vSphere agent that:
	// has the correct FQDN & don't have current association.
	// If no agent is found, one will be registered by s.vSphereRegisterAndUpdate().

	devices := s.option.VSphereDevices(ctx, time.Hour)
	associatedID := make(map[string]bool, len(devices))

	for _, dev := range devices {
		err := s.option.State.Get("bleemeo:vsphere:"+dev.FQDN(), &association)
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

func (s *Synchronizer) vSphereRegisterAndUpdate(localTargets []vsphere.Device) error {
	var newAgents []types.Agent //nolint: prealloc

	remoteAgentList := s.option.Cache.AgentsByUUID()

	hostAgentTypeID, vmAgentTypeID, found := s.getVSphereAgentTypes()
	if !found {
		return errRetryLater
	}

	params := map[string]string{
		"fields": "id,display_name,account,agent_type,abstracted,fqdn,initial_password,created_at,next_config_at,current_config,tags,initial_server_group_name",
	}

	for _, device := range localTargets {
		var agentTypeID string

		switch kind := device.Kind(); kind {
		case vsphere.KindHost:
			agentTypeID = hostAgentTypeID
		case vsphere.KindVM:
			agentTypeID = vmAgentTypeID
		default:
			logger.V(1).Printf("Unknown vSphere kind %q for device %s", kind, device.FQDN())

			continue
		}

		if _, err := s.FindVSphereAgent(s.ctx, device, agentTypeID, remoteAgentList); err != nil && !errors.Is(err, errNotExist) {
			logger.V(0).Printf("Skip registration of vSphere agent: %v", err) // TODO: V(2)

			continue
		} else if err == nil {
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
			return err
		}

		newAgents = append(newAgents, registeredAgent)

		err = s.option.State.Set("bleemeo:vsphere:"+device.FQDN(), vSphereAssociation{
			FQDN: device.FQDN(),
			ID:   registeredAgent.ID,
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

	return nil
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

func (s *Synchronizer) getVSphereAgentTypes() (hostAgentTypeID, vmAgentTypeID string, foundBoth bool) {
	agentTypes := s.option.Cache.AgentTypes()

	for i := 0; i < len(agentTypes) && !foundBoth; i++ {
		switch a := agentTypes[i]; {
		case a.Name == types.AgentTypeVSphereHost:
			hostAgentTypeID = a.ID
		case a.Name == types.AgentTypeVSphereVM:
			vmAgentTypeID = a.ID
		default:
			continue
		}

		foundBoth = hostAgentTypeID != "" && vmAgentTypeID != ""
	}

	return hostAgentTypeID, vmAgentTypeID, foundBoth
}

func (s *Synchronizer) getVSphereAgentType(kind string) (agentTypeID string, found bool) {
	hostAgentTypeID, vmAgentTypeID, found := s.getVSphereAgentTypes()
	if !found {
		return "", false
	}

	switch kind {
	case vsphere.KindHost:
		return hostAgentTypeID, true
	case vsphere.KindVM:
		return vmAgentTypeID, true
	default:
		return "", false
	}
}
