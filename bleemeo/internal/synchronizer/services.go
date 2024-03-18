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
	"encoding/json"
	"fmt"
	"glouton/bleemeo/internal/common"
	"glouton/bleemeo/internal/synchronizer/types"
	bleemeoTypes "glouton/bleemeo/types"
	"glouton/discovery"
	"glouton/facts"
	"glouton/logger"
	"sort"
	"strings"
	"time"
)

// serviceFields is the fields used on the API for a service, we always use all fields to
// make sure no Metric object is returned with some empty fields which could create bugs.
// Write (POST, PUT & PATCH) use account and agent field in addition. During reading we use filter to known the account/agent.
const (
	serviceReadFields  = "id,label,instance,listen_addresses,exe_path,active,tags,created_at"
	serviceWriteFields = serviceReadFields + ",account,agent"
)

type servicePayload struct {
	bleemeoTypes.Service
	Account string `json:"account"`
	Agent   string `json:"agent"`
}

func servicePayloadFromDiscovery(service discovery.Service, listenAddresses string, accountID string, agentID string, serviceID string) servicePayload {
	tags := make([]bleemeoTypes.Tag, 0, len(service.Tags))

	for _, t := range service.Tags {
		if len(t) <= apiTagsLength && t != "" {
			tags = append(tags, bleemeoTypes.Tag{Name: t})
		}
	}

	return servicePayload{
		Service: bleemeoTypes.Service{
			ID:              serviceID,
			Label:           service.Name,
			Instance:        service.Instance,
			ListenAddresses: listenAddresses,
			ExePath:         service.ExePath,
			Active:          service.Active,
			Tags:            tags,
		},
		Account: accountID,
		Agent:   agentID,
	}
}

func getListenAddress(addresses []facts.ListenAddress) string {
	stringList := make([]string, 0, len(addresses))

	for _, v := range addresses {
		if v.Network() == "unix" {
			continue
		}

		stringList = append(stringList, v.String()+"/"+v.Network())
	}

	sort.Strings(stringList)

	return strings.Join(stringList, ",")
}

func (s *Synchronizer) syncServices(ctx context.Context, syncType types.SyncType, onlyEssential bool) (updateThresholds bool, err error) {
	localServices, err := s.option.Discovery.Discovery(ctx, 24*time.Hour)
	if err != nil {
		return false, err
	}

	localServices = s.serviceExcludeUnregistrable(localServices)

	if s.successiveErrors == 3 {
		// After 3 error, try to force a full synchronization to see if it solve the issue.
		syncType = types.SyncTypeForceCacheRefresh
	}

	previousServices := s.option.Cache.ServicesByUUID()
	apiClient := s.client

	if syncType == types.SyncTypeForceCacheRefresh {
		err := s.serviceUpdateList(ctx, apiClient)
		if err != nil {
			return false, err
		}
	}

	s.serviceRemoveDeletedFromRemote(ctx, localServices, previousServices)

	if onlyEssential {
		// no essential services, skip registering.
		return false, nil
	}

	localServices, err = s.option.Discovery.Discovery(ctx, 24*time.Hour)
	if err != nil {
		return false, err
	}

	localServices = s.serviceExcludeUnregistrable(localServices)

	if err := s.serviceRegisterAndUpdate(ctx, apiClient, localServices); err != nil {
		return false, err
	}

	s.serviceDeactivateNonLocal(ctx, apiClient, localServices)

	return false, nil
}

func (s *Synchronizer) serviceUpdateList(ctx context.Context, apiClient types.RawClient) error {
	params := map[string]string{
		"agent":  s.agentID,
		"fields": serviceReadFields,
	}

	result, err := apiClient.Iter(ctx, "service", params)
	if err != nil {
		return err
	}

	services := make([]bleemeoTypes.Service, 0, len(result))

	for _, jsonMessage := range result {
		var service bleemeoTypes.Service

		if err := json.Unmarshal(jsonMessage, &service); err != nil {
			continue
		}

		services = append(services, service)
	}

	s.option.Cache.SetServices(services)

	return nil
}

// serviceRemoveDeletedFromRemote removes the local services that were deleted on the API.
func (s *Synchronizer) serviceRemoveDeletedFromRemote(ctx context.Context, localServices []discovery.Service, previousServices map[string]bleemeoTypes.Service) {
	newServices := s.option.Cache.ServicesByUUID()

	deletedServiceNameInstance := make(map[common.ServiceNameInstance]bool)

	for _, srv := range previousServices {
		if _, ok := newServices[srv.ID]; !ok {
			key := common.ServiceNameInstance{Name: srv.Label, Instance: srv.Instance}
			deletedServiceNameInstance[key] = true
		}
	}

	localServiceToDelete := make([]discovery.Service, 0)

	for _, srv := range localServices {
		key := common.ServiceNameInstance{
			Name:     srv.Name,
			Instance: srv.Instance,
		}
		if _, ok := deletedServiceNameInstance[key]; ok {
			localServiceToDelete = append(localServiceToDelete, srv)
		}
	}

	s.option.Discovery.RemoveIfNonRunning(ctx, localServiceToDelete)
}

func (s *Synchronizer) serviceRegisterAndUpdate(ctx context.Context, apiClient types.RawClient, localServices []discovery.Service) error {
	remoteServices := s.option.Cache.Services()
	remoteServicesByKey := common.ServiceLookupFromList(remoteServices)
	registeredServices := s.option.Cache.ServicesByUUID()
	params := map[string]string{
		"fields": serviceWriteFields,
	}

	for _, srv := range localServices {
		if _, ok := s.state.delayedContainer[srv.ContainerID]; ok {
			logger.V(2).Printf("Skip service %v due to delayedContainer", srv)

			continue
		}

		key := common.ServiceNameInstance{
			Name:     srv.Name,
			Instance: srv.Instance,
		}

		remoteSrv, remoteFound := remoteServicesByKey[key]

		// Skip updating the remote service if the service is already up to date.
		listenAddresses := getListenAddress(srv.ListenAddresses)
		if skipUpdate(remoteFound, remoteSrv, srv, listenAddresses) {
			continue
		}

		payload := servicePayloadFromDiscovery(srv, listenAddresses, s.option.Cache.AccountID(), s.agentID, "")

		var result bleemeoTypes.Service

		if remoteFound {
			_, err := apiClient.Do(ctx, "PUT", fmt.Sprintf("v1/service/%s/", remoteSrv.ID), params, payload, &result)
			if err != nil {
				return err
			}

			registeredServices[result.ID] = result
			logger.V(2).Printf("Service %v updated with UUID %s", key, result.ID)
		} else {
			_, err := apiClient.Do(ctx, "POST", "v1/service/", params, payload, &result)
			if err != nil {
				return err
			}

			registeredServices[result.ID] = result
			logger.V(2).Printf("Service %v registered with UUID %s", key, result.ID)
		}

		if remoteFound && remoteSrv.Active != result.Active {
			// API will update all associated metrics and update their active status. Apply the same rule on local cache
			var newDeactivatedAt time.Time

			if !result.Active {
				newDeactivatedAt = s.now()
			}

			metrics := s.option.Cache.Metrics()
			for i, m := range metrics {
				if m.ServiceID == result.ID {
					metrics[i].DeactivatedAt = newDeactivatedAt
				}
			}

			s.option.Cache.SetMetrics(metrics)
		}
	}

	finalServices := make([]bleemeoTypes.Service, 0, len(registeredServices))

	for _, srv := range registeredServices {
		finalServices = append(finalServices, srv)
	}

	s.option.Cache.SetServices(finalServices)

	return nil
}

// skipUpdate returns true if the service found by the discovery is up to date with the remote service on the API.
func skipUpdate(remoteFound bool, remoteSrv bleemeoTypes.Service, srv discovery.Service, listenAddresses string) bool {
	return remoteFound &&
		remoteSrv.Label == srv.Name &&
		remoteSrv.ListenAddresses == listenAddresses &&
		remoteSrv.ExePath == srv.ExePath &&
		remoteSrv.Active == srv.Active &&
		serviceHadSameTags(remoteSrv.Tags, srv.Tags)
}

// serviceHadSameTags returns true if the two service had the same tags for glouton provided tags.
func serviceHadSameTags(remoteTags []bleemeoTypes.Tag, localTags []string) bool {
	localTagsTruncated := make([]string, 0, len(localTags))

	for _, tag := range localTags {
		if len(tag) <= apiTagsLength && tag != "" {
			localTagsTruncated = append(localTagsTruncated, tag)
		}
	}

	gloutonTagCount := 0

	for _, tag := range remoteTags {
		if tag.TagType == bleemeoTypes.TagTypeIsCreatedByGlouton {
			gloutonTagCount++
		}
	}

	if len(localTagsTruncated) != gloutonTagCount {
		return false
	}

	for _, wantedTag := range localTagsTruncated {
		tagPresent := false

		for _, tag := range remoteTags {
			if tag.TagType == bleemeoTypes.TagTypeIsCreatedByGlouton && tag.Name == wantedTag {
				tagPresent = true

				break
			}
		}

		if !tagPresent {
			return false
		}
	}

	return true
}

// serviceExcludeUnregistrable removes the services that cannot be registered.
func (s *Synchronizer) serviceExcludeUnregistrable(services []discovery.Service) []discovery.Service {
	i := 0

	for _, service := range services {
		// Remove services with an instance too long.
		if len(service.Instance) > common.APIServiceInstanceLength {
			msg := fmt.Sprintf(
				"Service %s will be ignored because the instance name '%s' is too long (> %d characters)",
				service.Name, service.Instance, common.APIServiceInstanceLength,
			)

			s.logThrottle(msg)

			continue
		}

		// Copy and increment index.
		services[i] = service
		i++
	}

	return services
}

// serviceDeactivateNonLocal marks inactive the registered services that were not found in the discovery.
func (s *Synchronizer) serviceDeactivateNonLocal(ctx context.Context, apiClient types.RawClient, localServices []discovery.Service) {
	localServiceExists := make(map[common.ServiceNameInstance]bool, len(localServices))

	for _, srv := range localServices {
		key := common.ServiceNameInstance{
			Name:     srv.Name,
			Instance: srv.Instance,
		}

		localServiceExists[key] = true
	}

	registeredServices := s.option.Cache.Services()
	remoteServicesByKey := common.ServiceLookupFromList(registeredServices)
	finalServices := make([]bleemeoTypes.Service, 0, len(registeredServices))

	for _, remoteSrv := range registeredServices {
		key := common.ServiceNameInstance{Name: remoteSrv.Label, Instance: remoteSrv.Instance}
		if !remoteSrv.Active || (remoteSrv.ID == remoteServicesByKey[key].ID && localServiceExists[key]) {
			finalServices = append(finalServices, remoteSrv)

			continue
		}

		logger.V(2).Printf("Mark inactive the service %v (uuid %s)", key, remoteSrv.ID)

		// Deactivate the remote service that is not present locally.
		result, err := s.serviceDeactivate(ctx, apiClient, remoteSrv)
		if err != nil {
			logger.V(1).Printf("Failed to deactivate service %v on Bleemeo API: %v", key, err)

			continue
		}

		finalServices = append(finalServices, result)
	}

	s.option.Cache.SetServices(finalServices)
}

// serviceDeactivate makes a PUT request to the Bleemeo API to mark the given service as inactive.
func (s *Synchronizer) serviceDeactivate(ctx context.Context, apiClient types.RawClient, service bleemeoTypes.Service) (bleemeoTypes.Service, error) {
	params := map[string]string{
		"fields": serviceWriteFields,
	}

	payload := servicePayload{
		Service: bleemeoTypes.Service{
			Active:          false,
			Label:           service.Label,
			Instance:        service.Instance,
			ListenAddresses: service.ListenAddresses,
			ExePath:         service.ExePath,
		},
		Account: s.option.Cache.AccountID(),
		Agent:   s.agentID,
	}

	var result bleemeoTypes.Service

	_, err := apiClient.Do(ctx, "PUT", fmt.Sprintf("v1/service/%s/", service.ID), params, payload, &result)

	return result, err
}
