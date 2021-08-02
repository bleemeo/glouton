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
	"bytes"
	"encoding/json"
	"fmt"
	"glouton/bleemeo/types"
	"glouton/facts"
	"glouton/logger"
	"strings"
	"time"
)

const apiContainerNameLength = 100

// containerUpdateDelay is minimal the delay between container update for change
// other that status (likely healthcheck log).
const containerUpdateDelay = 30 * time.Minute

type nullTime time.Time

// MarshalJSON marshall the time.Time as usual BUT zero time is sent as "null".
func (t nullTime) MarshalJSON() ([]byte, error) {
	if time.Time(t).IsZero() {
		return []byte("null"), nil
	}

	return json.Marshal(time.Time(t))
}

// UnmarshalJSON the time.Time as usual BUT zero time is read as "null".
func (t *nullTime) UnmarshalJSON(b []byte) error {
	if bytes.Equal(b, []byte("null")) {
		*t = nullTime{}

		return nil
	}

	return json.Unmarshal(b, (*time.Time)(t))
}

type containerPayload struct {
	types.Container
	Host             string   `json:"host"`
	Command          string   `json:"command"`
	StartedAt        nullTime `json:"container_started_at"`
	FinishedAt       nullTime `json:"container_finished_at"`
	ImageID          string   `json:"container_image_id"`
	ImageName        string   `json:"container_image_name"`
	DockerAPIVersion string   `json:"docker_api_version"`

	// TODO: Older fields name, to remove when API is updated
	DockerID         string   `json:"docker_id,omitempty"`
	DockerInspect    string   `json:"docker_inspect,omitempty"`
	DockerStatus     string   `json:"docker_status,omitempty"`
	DockerCreatedAt  nullTime `json:"docker_created_at,omitempty"`
	DockerStartedAt  nullTime `json:"docker_started_at,omitempty"`
	DockerFinishedAt nullTime `json:"docker_finished_at,omitempty"`
	DockerImageID    string   `json:"docker_image_id,omitempty"`
	DockerImageName  string   `json:"docker_image_name,omitempty"`
}

// compatibilityContainer return a types.Container... applying compatibility with older
// API. Once updated, this could just be "return c.Container".
func (c containerPayload) compatibilityContainer() types.Container {
	if c.ContainerID == "" {
		c.ContainerID = c.DockerID
	}

	if c.ContainerInspect == "" {
		c.ContainerInspect = c.DockerInspect
	}

	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Time(c.DockerCreatedAt)
	}

	if c.Status == "" {
		c.Status = c.DockerStatus
	}

	return c.Container
}

func (s *Synchronizer) syncContainers(fullSync bool, onlyEssential bool) error {
	var localContainers []facts.Container

	if s.option.Cache.CurrentAccountConfig().DockerIntegration {
		var err error

		// We don't need very fresh information, we sync container after discovery which will update containers anyway.
		localContainers, err = s.option.Docker.Containers(s.ctx, 2*time.Minute, false)
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

	err := s.containerDeleteFromLocal(localContainers)

	return err
}

func (s *Synchronizer) containerUpdateList() error {
	params := map[string]string{
		"agent":  s.agentID,
		"fields": "id,name,container_id,docker_id,docker_inspect,container_inspect,status,docker_status,docker_created_at,container_created_at",
	}

	result, err := s.client.Iter(s.ctx, "container", params)
	if err != nil {
		return err
	}

	containersByUUID := s.option.Cache.ContainersByUUID()
	containers := make([]types.Container, len(result))

	for i, jsonMessage := range result {
		var container containerPayload

		if err := json.Unmarshal(jsonMessage, &container); err != nil {
			continue
		}

		// we don't need to keep the full inspect in memory
		container.FillInspectHash()
		container.ContainerInspect = ""
		container.GloutonLastUpdatedAt = containersByUUID[container.ID].GloutonLastUpdatedAt
		containers[i] = container.compatibilityContainer()
	}

	s.option.Cache.SetContainers(containers)

	return nil
}

func (s *Synchronizer) containerRegisterAndUpdate(localContainers []facts.Container) error {
	factsMap, err := s.option.Facts.Facts(s.ctx, 24*time.Hour)
	if err != nil {
		return nil //nolint:nilerr
	}

	remoteContainers := s.option.Cache.Containers()
	remoteIndexByName := make(map[string]int, len(remoteContainers))

	for i, v := range remoteContainers {
		remoteIndexByName[v.Name] = i
	}

	params := map[string]string{
		"fields": "id,name,docker_id,docker_inspect,host,command,docker_status,docker_created_at,docker_started_at,docker_finished_at," +
			"docker_api_version,docker_image_id,docker_image_name,container_id,container_inspect,container_status,container_created_at," +
			"container_finished_at,container_image_id,container_image_name",
	}

	newDelayedContainer := make(map[string]time.Time, len(s.delayedContainer))

	for _, container := range localContainers {
		if s.delayedContainerCheck(newDelayedContainer, container) {
			continue
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
			Name:             name,
			ContainerID:      container.ID(),
			ContainerInspect: container.ContainerJSON(),
			Status:           container.State().String(),
			CreatedAt:        container.CreatedAt(),
			Runtime:          container.RuntimeName(),
		}

		payloadContainer.FillInspectHash()

		if remoteFound && payloadContainer.Status == remoteContainer.Status && payloadContainer.CreatedAt.Truncate(time.Second).Equal(remoteContainer.CreatedAt.Truncate(time.Second)) {
			if payloadContainer.InspectHash == remoteContainer.InspectHash {
				continue
			}

			if time.Since(remoteContainer.GloutonLastUpdatedAt) < containerUpdateDelay {
				continue
			}
		}

		payloadContainer.InspectHash = ""                   // we don't send inspect hash to API
		payloadContainer.GloutonLastUpdatedAt = time.Time{} // we don't send this time, only used internally
		payload := containerPayload{
			Container:        payloadContainer,
			Host:             s.agentID,
			Command:          strings.Join(container.Command(), " "),
			StartedAt:        nullTime(container.StartedAt()),
			FinishedAt:       nullTime(container.FinishedAt()),
			ImageID:          container.ImageID(),
			ImageName:        container.ImageName(),
			DockerID:         container.ID(),
			DockerInspect:    container.ContainerJSON(),
			DockerStatus:     container.State().String(),
			DockerCreatedAt:  nullTime(container.CreatedAt()),
			DockerStartedAt:  nullTime(container.StartedAt()),
			DockerFinishedAt: nullTime(container.FinishedAt()),
			DockerImageID:    container.ImageID(),
			DockerImageName:  container.ImageName(),
		}

		if container.RuntimeName() == "docker" {
			payload.DockerAPIVersion = factsMap["docker_api_version"]
		}

		err := s.remoteRegister(remoteFound, &remoteContainer, &remoteContainers, params, payload, remoteIndex)
		if err != nil {
			return err
		}
	}

	s.option.Cache.SetContainers(remoteContainers)
	s.delayedContainer = newDelayedContainer

	return nil
}

func (s *Synchronizer) delayedContainerCheck(newDelayedContainer map[string]time.Time, container facts.Container) bool {
	delay := time.Duration(s.option.Config.Int("bleemeo.container_registration_delay_seconds")) * time.Second

	if s.now().Sub(container.CreatedAt()) < delay {
		enable, explicit := facts.ContainerEnabled(container)
		if !enable || !explicit {
			newDelayedContainer[container.ID()] = container.CreatedAt().Add(delay)

			return true
		}
	}

	return false
}

func (s *Synchronizer) remoteRegister(remoteFound bool, remoteContainer *types.Container,
	remoteContainers *[]types.Container, params map[string]string, payload containerPayload, remoteIndex int) error {
	var result containerPayload

	if remoteFound {
		_, err := s.client.Do(s.ctx, "PUT", fmt.Sprintf("v1/container/%s/", remoteContainer.ID), params, payload, &result)
		if err != nil {
			return err
		}

		result.FillInspectHash()
		result.GloutonLastUpdatedAt = time.Now()
		logger.V(2).Printf("Container %v updated with UUID %s", result.Name, result.ID)
		(*remoteContainers)[remoteIndex] = result.compatibilityContainer()
	} else {
		_, err := s.client.Do(s.ctx, "POST", "v1/container/", params, payload, &result)
		if err != nil {
			return err
		}

		result.FillInspectHash()
		result.GloutonLastUpdatedAt = time.Now()
		logger.V(2).Printf("Container %v registered with UUID %s", result.Name, result.ID)
		*remoteContainers = append(*remoteContainers, result.compatibilityContainer())
	}

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
		if _, ok := localByContainerID[v.ContainerID]; ok && !duplicatedKey[v.ContainerID] {
			duplicatedKey[v.ContainerID] = true

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
