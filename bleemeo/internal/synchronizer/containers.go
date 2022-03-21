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
	"context"
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

type containerPayload struct {
	types.Container
	Host             string         `json:"host"`
	Command          string         `json:"command"`
	StartedAt        types.NullTime `json:"container_started_at"`
	FinishedAt       types.NullTime `json:"container_finished_at"`
	ImageID          string         `json:"container_image_id"`
	ImageName        string         `json:"container_image_name"`
	DockerAPIVersion string         `json:"docker_api_version"`
}

func (s *Synchronizer) syncContainers(ctx context.Context, fullSync bool, onlyEssential bool) error {
	var localContainers []facts.Container

	cfg, ok := s.option.Cache.CurrentAccountConfig()

	if ok && cfg.DockerIntegration && s.option.Docker != nil {
		var err error

		// We don't need very fresh information, we sync container after discovery which will update containers anyway.
		localContainers, err = s.option.Docker.Containers(ctx, 2*time.Minute, false)
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
		"host":   s.agentID,
		"fields": "id,name,container_id,container_inspect,status,container_created_at,deleted_at",
	}

	result, err := s.client.Iter(s.ctx, "container", params)
	if err != nil {
		return err
	}

	containersByUUID := s.option.Cache.ContainersByUUID()
	containers := make([]types.Container, 0, len(result))

	for _, jsonMessage := range result {
		var container containerPayload

		if err := json.Unmarshal(jsonMessage, &container); err != nil {
			continue
		}

		// we don't need to keep the full inspect in memory
		container.FillInspectHash()
		container.ContainerInspect = ""
		container.GloutonLastUpdatedAt = containersByUUID[container.ID].GloutonLastUpdatedAt
		containers = append(containers, container.Container)
	}

	s.option.Cache.SetContainers(containers)

	return nil
}

func (s *Synchronizer) containerRegisterAndUpdate(localContainers []facts.Container) error {
	factsMap, err := s.option.Facts.Facts(s.ctx, 24*time.Hour)
	if err != nil {
		return err
	}

	remoteContainers := s.option.Cache.Containers()
	remoteIndexByName := make(map[string]int, len(remoteContainers))

	for i, v := range remoteContainers {
		remoteIndexByName[v.Name] = i
	}

	params := map[string]string{
		"fields": "id,name,host,command," +
			"container_id,container_inspect,container_status,container_created_at," +
			"container_finished_at,container_image_id,container_image_name,container_runtime,deleted_at",
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

		var remoteDelete bool

		if remoteFound {
			remoteDelete = !time.Time(remoteContainer.DeletedAt).IsZero()
		}

		if remoteFound && payloadContainer.Status == remoteContainer.Status && payloadContainer.CreatedAt.Truncate(time.Second).Equal(remoteContainer.CreatedAt.Truncate(time.Second)) && !remoteDelete {
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
			Container:  payloadContainer,
			Host:       s.agentID,
			Command:    strings.Join(container.Command(), " "),
			StartedAt:  types.NullTime(container.StartedAt()),
			FinishedAt: types.NullTime(container.FinishedAt()),
			ImageID:    container.ImageID(),
			ImageName:  container.ImageName(),
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
		enable, explicit := s.option.IsContainerEnabled(container)
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
		(*remoteContainers)[remoteIndex] = result.Container
	} else {
		_, err := s.client.Do(s.ctx, "POST", "v1/container/", params, payload, &result)
		if err != nil {
			return err
		}

		result.FillInspectHash()
		result.GloutonLastUpdatedAt = time.Now()
		logger.V(2).Printf("Container %v registered with UUID %s", result.Name, result.ID)
		*remoteContainers = append(*remoteContainers, result.Container)
	}

	return nil
}

func (s *Synchronizer) containerDeleteFromLocal(localContainers []facts.Container) error {
	var deletedIDs []string //nolint: prealloc // we don't know the size. empty is the most likely size.

	duplicatedKey := make(map[string]bool)
	localByContainerID := make(map[string]facts.Container, len(localContainers))

	for _, v := range localContainers {
		localByContainerID[v.ID()] = v
	}

	registeredContainers := s.option.Cache.ContainersByUUID()
	for _, v := range registeredContainers {
		if _, ok := localByContainerID[v.ContainerID]; ok && !duplicatedKey[v.ContainerID] {
			duplicatedKey[v.ContainerID] = true

			continue
		}

		_, err := s.client.Do(
			s.ctx,
			"PATCH",
			fmt.Sprintf("v1/container/%s/", v.ID),
			nil,
			struct {
				DeletedAt types.NullTime `json:"deleted_at"`
			}{types.NullTime(s.now())},
			nil,
		)
		if err != nil {
			logger.V(1).Printf("Failed to delete container %v on Bleemeo API: %v", v.Name, err)

			continue
		}

		logger.V(2).Printf("Container %v deleted (UUID %s)", v.Name, v.ID)
		v.DeletedAt = types.NullTime(s.now())

		registeredContainers[v.ID] = v

		deletedIDs = append(deletedIDs, v.ID)
	}

	containers := make([]types.Container, 0, len(registeredContainers))

	for _, v := range registeredContainers {
		containers = append(containers, v)
	}

	s.option.Cache.SetContainers(containers)

	if len(deletedIDs) > 0 {
		// API will update all associated metrics and update their active status. Apply the same rule on local cache
		metrics := s.option.Cache.Metrics()
		for i, m := range metrics {
			match := false

			for _, id := range deletedIDs {
				if m.ContainerID == id {
					match = true

					break
				}
			}

			if !match {
				continue
			}

			metrics[i].DeactivatedAt = time.Time(registeredContainers[m.ContainerID].DeletedAt)
		}

		s.option.Cache.SetMetrics(metrics)

		s.l.Lock()
		defer s.l.Unlock()

		s.forceSync[syncMethodService] = true
	}

	return nil
}
