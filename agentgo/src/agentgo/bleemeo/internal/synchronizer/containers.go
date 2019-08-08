package synchronizer

import (
	"agentgo/bleemeo/types"
	"agentgo/facts"
	"agentgo/logger"
	"encoding/json"
	"fmt"
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

func (s *Synchronizer) syncContainers(fullSync bool) error {

	localContainers, err := s.option.Docker.Containers(s.ctx, 24*time.Second, false)
	if err != nil {
		return err
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
		"agent":  s.option.State.AgentID(),
		"fields": "id,name,docker_id,docker_inspect",
	}
	result, err := s.client.Iter("container", params)
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

	facts, err := s.option.Facts.Facts(s.ctx, 24*time.Hour)
	if err != nil {
		return nil
	}

	remoteContainers := s.option.Cache.ContainersByContainerID()
	params := map[string]string{
		"fields": "id,name,docker_id,docker_inspect,host,command,docker_status,docker_created_at,docker_started_at,docker_finished_at,docker_api_version,docker_image_id,docker_image_name",
	}
	for _, container := range localContainers {
		remoteContainer, remoteFound := remoteContainers[container.ID()]
		payloadContainer := types.Container{
			Name:          container.Name(),
			DockerID:      container.ID(),
			DockerInspect: container.InspectJSON(),
		}
		if len(payloadContainer.Name) > apiContainerNameLength {
			payloadContainer.Name = payloadContainer.Name[:apiContainerNameLength]
		}
		payloadContainer.FillInspectHash()
		if remoteFound && payloadContainer.DockerInspectHash == remoteContainer.DockerInspectHash {
			continue
		}
		payloadContainer.DockerInspectHash = "" // we don't send inspect hash to API
		payload := containerPayload{
			Container:        payloadContainer,
			Host:             s.option.State.AgentID(),
			Command:          container.Command(),
			DockerStatus:     container.State(),
			DockerCreatedAt:  container.CreatedAt(),
			DockerStartedAt:  container.StartedAt(),
			DockerFinishedAt: container.FinishedAt(),
			DockerAPIVersion: facts["docker_api_version"],
			DockerImageID:    container.Inspect().Image,
			DockerImageName:  container.Image(),
		}
		var result types.Container
		if remoteFound {
			_, err := s.client.Do("PUT", fmt.Sprintf("v1/container/%s/", remoteContainer.ID), params, payload, &result)
			if err != nil {
				return err
			}
			logger.V(2).Printf("Container %v updated with UUID %s", result.Name, result.ID)
		} else {
			_, err := s.client.Do("POST", "v1/container/", params, payload, &result)
			if err != nil {
				return err
			}
			logger.V(2).Printf("Container %v registrered with UUID %s", result.Name, result.ID)
		}
		remoteContainers[result.DockerID] = result
	}

	containers := make([]types.Container, 0, len(remoteContainers))
	for _, v := range remoteContainers {
		containers = append(containers, v)
	}
	s.option.Cache.SetContainers(containers)
	return nil
}

func (s *Synchronizer) containerDeleteFromLocal(localContainers []facts.Container) error {

	localByContainerID := make(map[string]facts.Container, len(localContainers))
	for _, v := range localContainers {
		localByContainerID[v.ID()] = v
	}

	registeredContainers := s.option.Cache.ContainersByUUID()
	for k, v := range registeredContainers {
		if _, ok := localByContainerID[v.DockerID]; ok {
			continue
		}
		_, err := s.client.Do("DELETE", fmt.Sprintf("v1/container/%s/", v.ID), nil, nil, nil)
		if err != nil {
			logger.V(1).Printf("Failed to delete container %v on Bleemeo API: %v", v.Name, err)
			continue
		}
		logger.V(2).Printf("Container %v deleted (UUID %s)", v.Name, v.ID)
		delete(registeredContainers, k)
	}
	containers := make([]types.Container, 0, len(registeredContainers))
	for _, v := range registeredContainers {
		containers = append(containers, v)
	}
	s.option.Cache.SetContainers(containers)
	return nil
}
