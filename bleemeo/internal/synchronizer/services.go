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
	"glouton/discovery"
	"glouton/facts"
	"glouton/logger"
	"sort"
	"strings"
	"time"
)

const apiServiceInstanceLength = 50

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

func (sni *serviceNameInstance) truncateInstance() {
	if len(sni.instance) > apiServiceInstanceLength {
		sni.instance = sni.instance[:apiServiceInstanceLength]
	}
}

type servicePayload struct {
	types.Service
	Account string `json:"account"`
	Agent   string `json:"agent"`
}

// Bleemeo API only support limited service instance name. Truncate internal container name to match limitation of the API
func longToShortKey(services []discovery.Service) map[serviceNameInstance]serviceNameInstance {
	revertLookup := make(map[serviceNameInstance]discovery.Service)

	for _, srv := range services {
		shortKey := serviceNameInstance{
			name:     srv.Name,
			instance: srv.ContainerName,
		}

		shortKey.truncateInstance()

		if otherSrv, ok := revertLookup[shortKey]; !ok {
			revertLookup[shortKey] = srv
		} else {
			if srv.Active && !otherSrv.Active {
				revertLookup[shortKey] = srv
			} else if strings.Compare(srv.ContainerID, otherSrv.ContainerID) > 0 {
				// Completly arbitrary condition that will hopefully keep a consistent result whatever the services order is.
				revertLookup[shortKey] = srv
			}
		}
	}

	result := make(map[serviceNameInstance]serviceNameInstance, len(revertLookup))

	for shortKey, srv := range revertLookup {
		key := serviceNameInstance{
			name:     srv.Name,
			instance: srv.ContainerName,
		}
		result[key] = shortKey
	}

	return result
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

func (s *Synchronizer) syncServices(fullSync bool) error {
	localServices, err := s.option.Discovery.Discovery(s.ctx, 24*time.Hour)
	if err != nil {
		return err
	}

	if s.successiveErrors == 3 {
		// After 3 error, try to force a full synchronization to see if it solve the issue.
		fullSync = true
	}

	previousServices := s.option.Cache.ServicesByUUID()

	if fullSync {
		err := s.serviceUpdateList()
		if err != nil {
			return err
		}
	}

	if err := s.serviceDeleteFromRemote(localServices, previousServices); err != nil {
		return err
	}

	localServices, err = s.option.Discovery.Discovery(s.ctx, 24*time.Hour)
	if err != nil {
		return err
	}

	if err := s.serviceRegisterAndUpdate(localServices); err != nil {
		return err
	}

	if err := s.serviceDeleteFromLocal(localServices); err != nil {
		return err
	}

	return nil
}

func (s *Synchronizer) serviceUpdateList() error {
	params := map[string]string{
		"agent":  s.agentID,
		"fields": "id,label,instance,listen_addresses,exe_path,stack,active",
	}

	result, err := s.client.Iter("service", params)
	if err != nil {
		return err
	}

	services := make([]types.Service, len(result))

	for i, jsonMessage := range result {
		var service types.Service

		if err := json.Unmarshal(jsonMessage, &service); err != nil {
			continue
		}

		services[i] = service
	}

	s.option.Cache.SetServices(services)

	return nil
}

func (s *Synchronizer) serviceDeleteFromRemote(localServices []discovery.Service, previousServices map[string]types.Service) error {
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
			instance: srv.ContainerName,
		}
		if _, ok := deletedServiceNameInstance[key]; ok {
			localServiceToDelete = append(localServiceToDelete, srv)
		}
	}

	s.option.Discovery.RemoveIfNonRunning(s.ctx, localServiceToDelete)

	return nil
}

func (s *Synchronizer) serviceRegisterAndUpdate(localServices []discovery.Service) error {
	remoteServices := s.option.Cache.Services()
	remoteIndexByKey := serviceIndexByKey(remoteServices)
	longToShortLookup := longToShortKey(localServices)
	params := map[string]string{
		"fields": "id,label,instance,listen_addresses,exe_path,stack,active,account,agent",
	}

	for _, srv := range localServices {
		key := serviceNameInstance{
			name:     srv.Name,
			instance: srv.ContainerName,
		}

		var (
			shortKey serviceNameInstance
			ok       bool
		)

		if shortKey, ok = longToShortLookup[key]; !ok {
			continue
		}

		remoteIndex, remoteFound := remoteIndexByKey[shortKey]

		var remoteSrv types.Service

		if remoteFound {
			remoteSrv = remoteServices[remoteIndex]
		}

		listenAddresses := getListenAddress(srv.ListenAddresses)
		if remoteFound && remoteSrv.Label == srv.Name && remoteSrv.ListenAddresses == listenAddresses && remoteSrv.ExePath == srv.ExePath && remoteSrv.Active == srv.Active && remoteSrv.Stack == srv.Stack {
			continue
		}

		payload := servicePayload{
			Service: types.Service{
				Label:           srv.Name,
				Instance:        shortKey.instance,
				ListenAddresses: listenAddresses,
				ExePath:         srv.ExePath,
				Stack:           srv.Stack,
				Active:          srv.Active,
			},
			Account: s.option.Cache.AccountID(),
			Agent:   s.agentID,
		}

		var result types.Service

		if remoteFound {
			_, err := s.client.Do("PUT", fmt.Sprintf("v1/service/%s/", remoteSrv.ID), params, payload, &result)
			if err != nil {
				return err
			}

			remoteServices[remoteIndex] = result
			logger.V(2).Printf("Service %v updated with UUID %s", key, result.ID)
		} else {
			_, err := s.client.Do("POST", "v1/service/", params, payload, &result)
			if err != nil {
				return err
			}

			remoteServices = append(remoteServices, result)
			logger.V(2).Printf("Service %v registrered with UUID %s", key, result.ID)
		}

		if remoteFound && remoteSrv.Active != result.Active {
			// API will update all associated metrics and update their active status. Apply the same rule on local cache
			var newDeactivatedAt time.Time

			if !result.Active {
				newDeactivatedAt = time.Now()
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

func (s *Synchronizer) serviceDeleteFromLocal(localServices []discovery.Service) error {
	duplicatedKey := make(map[serviceNameInstance]bool)
	longToShortLookup := longToShortKey(localServices)
	shortToLongLookup := make(map[serviceNameInstance]serviceNameInstance, len(longToShortLookup))

	for k, v := range longToShortLookup {
		shortToLongLookup[v] = k
	}

	registeredServices := s.option.Cache.ServicesByUUID()
	for k, v := range registeredServices {
		shortKey := serviceNameInstance{name: v.Label, instance: v.Instance}
		if _, ok := shortToLongLookup[shortKey]; ok && !duplicatedKey[shortKey] {
			duplicatedKey[shortKey] = true
			continue
		}

		key := serviceNameInstance{name: v.Label, instance: v.Instance}

		_, err := s.client.Do("DELETE", fmt.Sprintf("v1/service/%s/", v.ID), nil, nil, nil)
		if err != nil {
			logger.V(1).Printf("Failed to delete service %v on Bleemeo API: %v", key, err)
			continue
		}

		logger.V(2).Printf("Service %v deleted (UUID %s)", key, v.ID)
		delete(registeredServices, k)
	}

	services := make([]types.Service, 0, len(registeredServices))
	for _, v := range registeredServices {
		services = append(services, v)
	}

	s.option.Cache.SetServices(services)

	return nil
}
