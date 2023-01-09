// Copyright 2015-2022 Bleemeo
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
	"glouton/bleemeo/types"
	"glouton/discovery"
	"glouton/facts"
	"glouton/logger"
	"sort"
	"strings"
	"time"
)

type serviceNameInstance struct {
	name     string
	instance string
}

func (sni serviceNameInstance) String() string {
	if sni.instance != "" {
		return fmt.Sprintf("%s on %s", sni.name, sni.instance)
	}

	return sni.name
}

type servicePayload struct {
	types.Service
	Account string `json:"account"`
	Agent   string `json:"agent"`
}

func servicePayloadFromDiscovery(service discovery.Service, listenAddresses string, accountID string, agentID string, serviceID string) servicePayload {
	return servicePayload{
		Service: types.Service{
			ID:              serviceID,
			Label:           service.Name,
			Instance:        service.Instance,
			ListenAddresses: listenAddresses,
			ExePath:         service.ExePath,
			Stack:           service.Stack,
			Active:          service.Active,
		},
		Account: accountID,
		Agent:   agentID,
	}
}

func serviceIndexByKey(services []types.Service) map[serviceNameInstance]int {
	result := make(map[serviceNameInstance]int, len(services))

	for i, srv := range services {
		key := serviceNameInstance{
			name:     srv.Label,
			instance: srv.Instance,
		}
		result[key] = i
	}

	return result
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

func (s *Synchronizer) syncServices(ctx context.Context, fullSync bool, onlyEssential bool) (updateThresholds bool, err error) {
	localServices, err := s.option.Discovery.Discovery(ctx, 24*time.Hour)
	if err != nil {
		return false, err
	}

	localServices = s.serviceExcludeUnregistrable(localServices)

	if s.successiveErrors == 3 {
		// After 3 error, try to force a full synchronization to see if it solve the issue.
		fullSync = true
	}

	previousServices := s.option.Cache.ServicesByUUID()

	if fullSync {
		err := s.serviceUpdateList()
		if err != nil {
			return false, err
		}
	}

	s.serviceRemoveDeletedFromRemote(localServices, previousServices)

	if onlyEssential {
		// no essential services, skip registering.
		return false, nil
	}

	localServices, err = s.option.Discovery.Discovery(ctx, 24*time.Hour)
	if err != nil {
		return false, err
	}

	localServices = s.serviceExcludeUnregistrable(localServices)

	if err := s.serviceRegisterAndUpdate(localServices); err != nil {
		return false, err
	}

	s.serviceDeactivateNonLocal(localServices)

	return false, nil
}

func (s *Synchronizer) serviceUpdateList() error {
	params := map[string]string{
		"agent":  s.agentID,
		"fields": "id,label,instance,listen_addresses,exe_path,stack,active",
	}

	result, err := s.client.Iter(s.ctx, "service", params)
	if err != nil {
		return err
	}

	services := make([]types.Service, 0, len(result))

	for _, jsonMessage := range result {
		var service types.Service

		if err := json.Unmarshal(jsonMessage, &service); err != nil {
			continue
		}

		services = append(services, service)
	}

	s.option.Cache.SetServices(services)

	return nil
}

// serviceRemoveDeletedFromRemote removes the local services that were deleted on the API.
func (s *Synchronizer) serviceRemoveDeletedFromRemote(localServices []discovery.Service, previousServices map[string]types.Service) {
	newServices := s.option.Cache.ServicesByUUID()

	deletedServiceNameInstance := make(map[serviceNameInstance]bool)

	for _, srv := range previousServices {
		if _, ok := newServices[srv.ID]; !ok {
			key := serviceNameInstance{name: srv.Label, instance: srv.Instance}
			deletedServiceNameInstance[key] = true
		}
	}

	localServiceToDelete := make([]discovery.Service, 0)

	for _, srv := range localServices {
		key := serviceNameInstance{
			name:     srv.Name,
			instance: srv.Instance,
		}
		if _, ok := deletedServiceNameInstance[key]; ok {
			localServiceToDelete = append(localServiceToDelete, srv)
		}
	}

	s.option.Discovery.RemoveIfNonRunning(s.ctx, localServiceToDelete)
}

func (s *Synchronizer) serviceRegisterAndUpdate(localServices []discovery.Service) error {
	remoteServices := s.option.Cache.Services()
	remoteIndexByKey := serviceIndexByKey(remoteServices)
	params := map[string]string{
		"fields": "id,label,instance,listen_addresses,exe_path,stack,active,account,agent",
	}

	for _, srv := range localServices {
		if _, ok := s.delayedContainer[srv.ContainerID]; ok {
			logger.V(2).Printf("Skip service %v due to delayedContainer", srv)

			continue
		}

		key := serviceNameInstance{
			name:     srv.Name,
			instance: srv.Instance,
		}

		remoteIndex, remoteFound := remoteIndexByKey[key]

		var remoteSrv types.Service

		if remoteFound {
			remoteSrv = remoteServices[remoteIndex]
		}

		// Skip updating the remote service if the service is already up to date.
		listenAddresses := getListenAddress(srv.ListenAddresses)
		if skipUpdate(remoteFound, remoteSrv, srv, listenAddresses) {
			continue
		}

		payload := servicePayloadFromDiscovery(srv, listenAddresses, s.option.Cache.AccountID(), s.agentID, "")

		var result types.Service

		if remoteFound {
			_, err := s.client.Do(s.ctx, "PUT", fmt.Sprintf("v1/service/%s/", remoteSrv.ID), params, payload, &result)
			if err != nil {
				return err
			}

			remoteServices[remoteIndex] = result
			logger.V(2).Printf("Service %v updated with UUID %s", key, result.ID)
		} else {
			_, err := s.client.Do(s.ctx, "POST", "v1/service/", params, payload, &result)
			if err != nil {
				return err
			}

			remoteServices = append(remoteServices, result)
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

	s.option.Cache.SetServices(remoteServices)

	return nil
}

// skipUpdate returns true if the service found by the discovery is up to date with the remote service on the API.
func skipUpdate(remoteFound bool, remoteSrv types.Service, srv discovery.Service, listenAddresses string) bool {
	return remoteFound &&
		remoteSrv.Label == srv.Name &&
		remoteSrv.ListenAddresses == listenAddresses &&
		remoteSrv.ExePath == srv.ExePath &&
		remoteSrv.Active == srv.Active &&
		remoteSrv.Stack == srv.Stack
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
func (s *Synchronizer) serviceDeactivateNonLocal(localServices []discovery.Service) {
	localServiceExists := make(map[serviceNameInstance]bool, len(localServices))

	for _, srv := range localServices {
		key := serviceNameInstance{
			name:     srv.Name,
			instance: srv.Instance,
		}

		localServiceExists[key] = true
	}

	registeredServices := s.option.Cache.Services()
	for _, remoteSrv := range registeredServices {
		key := serviceNameInstance{name: remoteSrv.Label, instance: remoteSrv.Instance}
		if localServiceExists[key] {
			continue
		}

		if !remoteSrv.Active {
			continue
		}

		// Deactivate the remote service that is not present locally.
		params := map[string]string{
			"fields": "id,label,instance,listen_addresses,exe_path,stack,active,account,agent",
		}

		payload := servicePayload{
			Service: types.Service{
				Active:          false,
				Label:           remoteSrv.Label,
				Instance:        remoteSrv.Instance,
				ListenAddresses: remoteSrv.ListenAddresses,
				ExePath:         remoteSrv.ExePath,
				Stack:           remoteSrv.Stack,
			},
			Account: s.option.Cache.AccountID(),
			Agent:   s.agentID,
		}

		_, err := s.client.Do(s.ctx, "PUT", fmt.Sprintf("v1/service/%s/", remoteSrv.ID), params, payload, nil)
		if err != nil {
			logger.V(1).Printf("Failed to deactivate service %v on Bleemeo API: %v", key, err)

			continue
		}
	}
}
