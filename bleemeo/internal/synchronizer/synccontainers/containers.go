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

// Package synccontainers registers the containers of the agent on Bleemeo, and marks deleted
// the ones which are gone.
package synccontainers

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/bleemeo/glouton/bleemeo/internal/cache"
	"github.com/bleemeo/glouton/bleemeo/internal/common"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/bleemeoapi"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/facts"
	containerTypes "github.com/bleemeo/glouton/facts/container-runtime/types"
	"github.com/bleemeo/glouton/logger"
)

const (
	// containerUpdateDelay is minimal the delay between container update for change
	// other that status (likely healthcheck log).
	containerUpdateDelay = 30 * time.Minute
)

// SyncContainers registers the containers, and marks deleted the ones which are gone.
type SyncContainers struct {
	now  func() time.Time
	logs *types.LogThrottle
}

func New(now func() time.Time) *SyncContainers {
	return &SyncContainers{
		now:  now,
		logs: types.NewLogThrottle(now),
	}
}

func (s *SyncContainers) Name() types.EntityName {
	return types.EntityContainer
}

func (s *SyncContainers) EnabledInMaintenance() bool {
	return false
}

func (s *SyncContainers) EnabledInSuspendedMode() bool {
	return false
}

func (s *SyncContainers) PrepareExecution(_ context.Context, execution types.SynchronizationExecution) (types.EntitySynchronizerExecution, error) {
	return &syncContainersExecution{
		parent:    s,
		execution: execution,
	}, nil
}

type syncContainersExecution struct {
	parent    *SyncContainers
	execution types.SynchronizationExecution
}

// NeedSynchronization synchronizes the containers after a discovery, and once a delayed
// container is due.
func (e *syncContainersExecution) NeedSynchronization(_ context.Context) (bool, error) {
	// Metrics registration may need containers to be synced.
	// TODO: move to the metric entity when it gets migrated.
	if err := e.execution.RequestLinkedSynchronization(types.EntityMetric, types.EntityContainer); err != nil {
		return false, err
	}

	_, lastDiscovery := e.execution.Option().Discovery.GetLatestDiscovery()
	_, minDelayed := e.execution.GlobalState().DelayedContainers()

	if e.execution.LastSuccessfulSync().Before(lastDiscovery) {
		return true, nil
	}

	return !minDelayed.IsZero() && e.execution.StartedAt().After(minDelayed), nil
}

// RefreshCache lists the containers from the API on a cache refresh, or after 3 errors in a row
// to see if it solves the issue.
func (e *syncContainersExecution) RefreshCache(ctx context.Context, syncType types.SyncType) error {
	if syncType != types.SyncTypeForceCacheRefresh && e.execution.GlobalState().SuccessiveErrors() != 3 {
		return nil
	}

	option := e.execution.Option()
	agentID, _ := option.State.BleemeoCredentials()

	return updateList(ctx, e.execution.BleemeoAPIClient(), option.Cache, agentID)
}

func (e *syncContainersExecution) SyncRemoteAndLocal(ctx context.Context, _ types.SyncType) error {
	var localContainers []facts.Container

	option := e.execution.Option()

	cfg, ok := option.Cache.CurrentAccountConfig()

	if ok && cfg.DockerIntegration && option.Docker != nil {
		var err error

		// We don't need very fresh information, we sync container after discovery which will update containers anyway.
		localContainers, err = option.Docker.Containers(ctx, 2*time.Minute, false)
		if err != nil {
			logger.V(1).Printf("Unable to list containers: %v", err)

			return nil
		}
	}

	if e.execution.IsOnlyEssential() {
		// no essential containers, skip registering.
		return nil
	}

	factsMap, err := option.Facts.Facts(ctx, 24*time.Hour)
	if err != nil {
		return err
	}

	agentID, _ := option.State.BleemeoCredentials()
	apiClient := e.execution.BleemeoAPIClient()

	// I'm not sure this really belong here. This update is here because it was here,
	// but this might be better being called by Synchronizer.runOnce()
	e.execution.GlobalState().UpdateDelayedContainers(localContainers)
	delayedContainer, _ := e.execution.GlobalState().DelayedContainers()

	// The API doesn't delete containers.
	err = e.parent.registerAndUpdate(ctx, apiClient, option.Cache, localContainers, delayedContainer, agentID, factsMap["docker_api_version"])
	if err != nil {
		return err
	}

	if deleted := e.parent.deleteFromLocal(ctx, apiClient, option.Cache, localContainers); deleted {
		e.execution.RequestSynchronization(types.EntityService, true)
	}

	return nil
}

func (e *syncContainersExecution) FinishExecution(_ context.Context) {
}

// updateList updates the containers of the cache from the API.
func updateList(ctx context.Context, apiClient types.ContainerClient, c *cache.Cache, agentID string) error {
	containers, err := apiClient.ListContainers(ctx, agentID)
	if err != nil {
		return err
	}

	containersByUUID := c.ContainersByUUID()

	for i, container := range containers {
		// we don't need to keep the full inspect in memory
		container.FillInspectHash()
		container.ContainerInspect = ""
		container.GloutonLastUpdatedAt = containersByUUID[container.ID].GloutonLastUpdatedAt
		containers[i] = container
	}

	c.SetContainers(containers)

	return nil
}

// registerAndUpdate registers the local containers which aren't on the API, and updates the ones
// which changed. The containers of delayedContainer, by ID, are skipped. dockerAPIVersion is sent
// with the Docker containers.
func (s *SyncContainers) registerAndUpdate(
	ctx context.Context,
	apiClient types.ContainerClient,
	c *cache.Cache,
	localContainers []facts.Container,
	delayedContainer map[string]time.Time,
	agentID string,
	dockerAPIVersion string,
) error {
	remoteContainers := c.Containers()
	remoteIndexByName := make(map[string]int, len(remoteContainers))

	for i, v := range remoteContainers {
		remoteIndexByName[v.Name] = i
	}

	for _, container := range localContainers {
		if _, ok := delayedContainer[container.ID()]; ok {
			continue
		}

		name := container.ContainerName()
		if len(name) > common.APIContainerNameLength {
			msg := fmt.Sprintf(
				"Container %s will be ignored because its name is too long (> %d characters)",
				name, common.APIContainerNameLength,
			)

			s.logs.Log("container-name-too-long", 1, msg)

			continue
		}

		remoteIndex, remoteFound := remoteIndexByName[name]

		var remoteContainer bleemeoTypes.Container

		if remoteFound {
			remoteContainer = remoteContainers[remoteIndex]
		}

		payloadContainer := bleemeoTypes.Container{
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

			if s.now().Sub(remoteContainer.GloutonLastUpdatedAt) < containerUpdateDelay {
				continue
			}
		}

		payloadContainer.InspectHash = ""                   // we don't send inspect hash to API
		payloadContainer.GloutonLastUpdatedAt = time.Time{} // we don't send this time, only used internally
		payload := bleemeoapi.ContainerPayload{
			Container:  payloadContainer,
			Host:       agentID,
			Command:    strings.Join(container.Command(), " "),
			StartedAt:  bleemeoTypes.NullTime(container.StartedAt()),
			FinishedAt: bleemeoTypes.NullTime(container.FinishedAt()),
			ImageID:    container.ImageID(),
			ImageName:  container.ImageName(),
		}

		if container.RuntimeName() == containerTypes.DockerRuntime {
			payload.DockerAPIVersion = dockerAPIVersion
		}

		err := s.remoteRegister(ctx, apiClient, remoteFound, &remoteContainer, &remoteContainers, payload, remoteIndex)
		if err != nil {
			return err
		}
	}

	c.SetContainers(remoteContainers)

	return nil
}

func (s *SyncContainers) remoteRegister(
	ctx context.Context,
	apiClient types.ContainerClient,
	remoteFound bool,
	remoteContainer *bleemeoTypes.Container,
	remoteContainers *[]bleemeoTypes.Container,
	payload bleemeoapi.ContainerPayload,
	remoteIndex int,
) error {
	var (
		result bleemeoapi.ContainerPayload
		err    error
	)

	if remoteFound {
		result, err = apiClient.UpdateContainer(ctx, remoteContainer.ID, payload)
		if err != nil {
			return err
		}

		result.FillInspectHash()
		result.GloutonLastUpdatedAt = s.now()
		logger.V(2).Printf("Container %v updated with UUID %s", result.Name, result.ID)
		(*remoteContainers)[remoteIndex] = result.Container
	} else {
		result, err = apiClient.RegisterContainer(ctx, payload)
		if err != nil {
			return err
		}

		result.FillInspectHash()
		result.GloutonLastUpdatedAt = s.now()
		logger.V(2).Printf("Container %v registered with UUID %s", result.Name, result.ID)
		*remoteContainers = append(*remoteContainers, result.Container)
	}

	return nil
}

// deleteFromLocal marks deleted the containers of the cache which aren't local, and deactivates
// their metrics in the cache, as the API does. It returns whether a container was deleted.
func (s *SyncContainers) deleteFromLocal(ctx context.Context, apiClient types.ContainerClient, c *cache.Cache, localContainers []facts.Container) bool {
	var deletedIDs []string

	duplicatedKey := make(map[string]bool)
	localByContainerID := make(map[string]facts.Container, len(localContainers))

	for _, v := range localContainers {
		localByContainerID[v.ID()] = v
	}

	registeredContainers := c.ContainersByUUID()
	for _, container := range registeredContainers {
		if !time.Time(container.DeletedAt).IsZero() {
			continue
		}

		if _, ok := localByContainerID[container.ContainerID]; ok && !duplicatedKey[container.ContainerID] {
			duplicatedKey[container.ContainerID] = true

			continue
		}

		body := map[string]any{"deleted_at": bleemeoTypes.NullTime(s.now())}

		_, err := apiClient.UpdateContainer(ctx, container.ID, body)
		if err != nil {
			// If the container wasn't found, it has already been deleted.
			if types.IsNotFound(err) {
				delete(registeredContainers, container.ID)

				deletedIDs = append(deletedIDs, container.ID)
			}

			logger.V(1).Printf("Failed to delete container %q on Bleemeo API: %v", container.Name, err)

			continue
		}

		logger.V(2).Printf("Container %v deleted (UUID %s)", container.Name, container.ID)
		container.DeletedAt = bleemeoTypes.NullTime(s.now())

		registeredContainers[container.ID] = container

		deletedIDs = append(deletedIDs, container.ID)
	}

	containers := make([]bleemeoTypes.Container, 0, len(registeredContainers))

	for _, v := range registeredContainers {
		containers = append(containers, v)
	}

	c.SetContainers(containers)

	if len(deletedIDs) == 0 {
		return false
	}

	// API will update all associated metrics and update their active status. Apply the same rule on local cache
	metrics := c.Metrics()
	for i, m := range metrics {
		match := slices.Contains(deletedIDs, m.ContainerID)

		if !match {
			continue
		}

		metrics[i].DeactivatedAt = time.Time(registeredContainers[m.ContainerID].DeletedAt)
	}

	c.SetMetrics(metrics)

	return true
}
