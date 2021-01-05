// Copyright 2015-2019 Bleemeo
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
	"encoding/json"
	"fmt"
	"glouton/bleemeo/types"
	"glouton/facts"
	"glouton/logger"
	"strings"
	"time"
)

const apiContainerNameLength = 100

type containerPayload struct {
	types.Container
	Host             string    `json:"host"`
	Command          string    `json:"command"`
	DockerStatus     string    `json:"docker_status"`
	DockerCreatedAt  time.Time `json:"docker_created_at"`
	DockerStartedAt  time.Time `json:"docker_started_at"`
	DockerFinishedAt time.Time `json:"docker_finished_at"`
	DockerAPIVersion string    `json:"docker_api_version"`
	DockerImageID    string    `json:"docker_image_id"`
	DockerImageName  string    `json:"docker_image_name"`
}

func (s *Synchronizer) syncContainers(fullSync bool, onlyEssential bool) error {
	var localContainers []facts.Container

	if s.option.Cache.CurrentAccountConfig().DockerIntegration {
		var err error

		localContainers, err = s.option.Docker.Containers(s.ctx, 24*time.Second, false)
		if err != nil {
			logger.V(1).Printf("Unable to list containers: %v", err)
			return nil
		}
	}

	if s.successiveErrors == 3 {
		// After 3 error, try to force a full synchronization to see if it solve the issue.
		fullSync = true
	}

	if fullSync {
		err := s.containerUpdateList()
		if err != nil {
			return err
		}
	}

	if onlyEssential {
		// no essential containers, skip registering.
		return nil
	}

	// s.containerDeleteFromRemote(): API don't delete containers
	if err := s.containerRegisterAndUpdate(localContainers); err != nil {
		return err
	}

	if err := s.containerDeleteFromLocal(localContainers); err != nil {
		return err
	}

	return nil
}

func (s *Synchronizer) containerUpdateList() error {
	params := map[string]string{
		"agent":  s.agentID,
		"fields": "id,name,docker_id,docker_inspect",
	}

	result, err := s.client.Iter(s.ctx, "container", params)
	if err != nil {
		return err
	}

	containers := make([]types.Container, len(result))

	for i, jsonMessage := range result {
		var container types.Container

		if err := json.Unmarshal(jsonMessage, &container); err != nil {
			continue
		}
		// we don't need to keep the full inspect in memory
		container.FillInspectHash()
		container.DockerInspect = ""
		containers[i] = container
	}

	s.option.Cache.SetContainers(containers)

	return nil
}

func (s *Synchronizer) containerRegisterAndUpdate(localContainers []facts.Container) error {
	factsMap, err := s.option.Facts.Facts(s.ctx, 24*time.Hour)
	if err != nil {
		return nil
	}

	remoteContainers := s.option.Cache.Containers()
	remoteIndexByName := make(map[string]int, len(remoteContainers))

	for i, v := range remoteContainers {
		remoteIndexByName[v.Name] = i
	}

	params := map[string]string{
		"fields": "id,name,docker_id,docker_inspect,host,command,docker_status,docker_created_at,docker_started_at,docker_finished_at,docker_api_version,docker_image_id,docker_image_name",
	}

	newDelayedContainer := make(map[string]time.Time, len(s.delayedContainer))
	delay := time.Duration(s.option.Config.Int("bleemeo.container_registration_delay_seconds")) * time.Second

	for _, container := range localContainers {
		if s.now().Sub(container.CreatedAt()) < delay {
			enable, explicit := facts.ContainerEnabled(container)
			if !enable || !explicit {
				newDelayedContainer[container.ID()] = container.CreatedAt().Add(delay)
				continue
			}
		}

		name := container.ContainerName()
		if len(name) > apiContainerNameLength {
			name = name[:apiContainerNameLength]
		}

		remoteIndex, remoteFound := remoteIndexByName[name]

		var remoteContainer types.Container

		if remoteFound {
			remoteContainer = remoteContainers[remoteIndex]
		}

		payloadContainer := types.Container{
			Name:          name,
			DockerID:      container.ID(),
			DockerInspect: container.ContainerJSON(),
		}

		payloadContainer.FillInspectHash()

		if remoteFound && payloadContainer.DockerInspectHash == remoteContainer.DockerInspectHash {
			continue
		}

		payloadContainer.DockerInspectHash = "" // we don't send inspect hash to API
		payload := containerPayload{
			Container:        payloadContainer,
			Host:             s.agentID,
			Command:          strings.Join(container.Command(), " "),
			DockerStatus:     container.State().String(),
			DockerCreatedAt:  container.CreatedAt(),
			DockerStartedAt:  container.StartedAt(),
			DockerFinishedAt: container.FinishedAt(),
			DockerAPIVersion: factsMap["docker_api_version"],
			DockerImageID:    container.ImageID(),
			DockerImageName:  container.ImageName(),
		}

		var result types.Container

		if remoteFound {
			_, err := s.client.Do(s.ctx, "PUT", fmt.Sprintf("v1/container/%s/", remoteContainer.ID), params, payload, &result)
			if err != nil {
				return err
			}

			logger.V(2).Printf("Container %v updated with UUID %s", result.Name, result.ID)
			remoteContainers[remoteIndex] = result
		} else {
			_, err := s.client.Do(s.ctx, "POST", "v1/container/", params, payload, &result)
			if err != nil {
				return err
			}

			logger.V(2).Printf("Container %v registered with UUID %s", result.Name, result.ID)
			remoteContainers = append(remoteContainers, result)
		}
	}

	s.option.Cache.SetContainers(remoteContainers)
	s.delayedContainer = newDelayedContainer

	return nil
}

func (s *Synchronizer) containerDeleteFromLocal(localContainers []facts.Container) error {
	deletedPerformed := false
	duplicatedKey := make(map[string]bool)
	localByContainerID := make(map[string]facts.Container, len(localContainers))

	for _, v := range localContainers {
		localByContainerID[v.ID()] = v
	}

	registeredContainers := s.option.Cache.ContainersByUUID()
	for k, v := range registeredContainers {
		if _, ok := localByContainerID[v.DockerID]; ok && !duplicatedKey[v.DockerID] {
			duplicatedKey[v.DockerID] = true
			continue
		}

		_, err := s.client.Do(s.ctx, "DELETE", fmt.Sprintf("v1/container/%s/", v.ID), nil, nil, nil)
		if err != nil {
			logger.V(1).Printf("Failed to delete container %v on Bleemeo API: %v", v.Name, err)
			continue
		}

		logger.V(2).Printf("Container %v deleted (UUID %s)", v.Name, v.ID)
		delete(registeredContainers, k)

		deletedPerformed = true
	}

	containers := make([]types.Container, 0, len(registeredContainers))

	for _, v := range registeredContainers {
		containers = append(containers, v)
	}

	s.option.Cache.SetContainers(containers)

	if deletedPerformed {
		s.l.Lock()
		defer s.l.Unlock()

		s.forceSync["services"] = true
	}

	return nil
}
