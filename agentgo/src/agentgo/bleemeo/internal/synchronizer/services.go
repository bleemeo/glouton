package synchronizer

import (
	"agentgo/bleemeo/types"
	"agentgo/discovery"
	"agentgo/logger"
	"encoding/json"
	"fmt"
	"net"
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

type servicePayload struct {
	types.Service
	Account string `json:"account"`
	Agent   string `json:"agent"`
}

// Bleemeo API only support limited service instance name. Truncate internal container name to match limitation of the API
func longToShortKey(services []discovery.Service) map[serviceNameInstance]serviceNameInstance {
	revertLookup := make(map[serviceNameInstance]discovery.Service)
	for _, srv := range services {
		truncateIndex := apiServiceInstanceLength
		if truncateIndex > len(srv.ContainerName) {
			truncateIndex = len(srv.ContainerName)
		}
		shortKey := serviceNameInstance{
			name:     string(srv.Name),
			instance: srv.ContainerName[:truncateIndex],
		}
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
			name:     string(srv.Name),
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

func getListenAddress(addresses []net.Addr) string {
	stringList := make([]string, len(addresses))
	for i, v := range addresses {
		stringList[i] = v.String()
	}
	sort.Strings(stringList)
	return strings.Join(stringList, ",")
}

func (s *Synchronizer) syncServices(fullSync bool) error {

	if s.successiveErrors == 3 {
		// After 3 error, try to force a full synchronization to see if it solve the issue.
		fullSync = true
	}

	deletedServiceNameInstance := make(map[serviceNameInstance]bool)
	if fullSync {
		remoteDeletedServices, err := s.syncServiceUpdateList()
		if err != nil {
			return err
		}
		for _, srv := range remoteDeletedServices {
			deletedServiceNameInstance[serviceNameInstance{name: srv.Label, instance: srv.Instance}] = true
		}
	}

	if err := s.syncServiceApplyRemoteDeletion(deletedServiceNameInstance); err != nil {
		return err
	}
	localUUIDs, err := s.syncServiceRegisterAndUpdate()
	if err != nil {
		return err
	}
	if err := s.syncServiceApplyLocalDeletion(localUUIDs); err != nil {
		return err
	}
	return nil
}

func (s *Synchronizer) syncServiceUpdateList() (deletedServices []types.Service, err error) {
	result, err := s.client.Iter("service", nil)
	if err != nil {
		return nil, err
	}

	remoteUUID := make(map[string]bool)
	services := make([]types.Service, len(result))
	for i, jsonMessage := range result {
		var service types.Service
		if err := json.Unmarshal(jsonMessage, &service); err != nil {
			continue
		}
		services[i] = service
		remoteUUID[service.ID] = true
	}

	localServices := s.option.Cache.ServicesByUUID()
	deletedServices = make([]types.Service, 0)
	for _, srv := range localServices {
		if !remoteUUID[srv.ID] {
			deletedServices = append(deletedServices, srv)
		}
	}
	s.option.Cache.SetServices(services)
	return deletedServices, nil
}

func (s *Synchronizer) syncServiceApplyRemoteDeletion(deletedServiceNameInstance map[serviceNameInstance]bool) error {
	localServices, err := s.option.Discovery.Discovery(s.ctx, 24*time.Hour)
	if err != nil {
		return err
	}
	localServiceToDelete := make([]discovery.Service, 0)
	for _, srv := range localServices {
		key := serviceNameInstance{
			name:     string(srv.Name),
			instance: srv.ContainerName,
		}
		if _, ok := deletedServiceNameInstance[key]; ok {
			localServiceToDelete = append(localServiceToDelete, srv)
		}
	}
	s.option.Discovery.RemoveIfNonRunning(s.ctx, localServiceToDelete)
	return nil
}

func (s *Synchronizer) syncServiceRegisterAndUpdate() (localUUIDs map[string]bool, err error) {
	localServices, err := s.option.Discovery.Discovery(s.ctx, 24*time.Hour)
	if err != nil {
		return nil, err
	}
	localUUIDs = make(map[string]bool, len(localServices))
	remoteServices := s.option.Cache.Services()
	remoteIndexByKey := serviceIndexByKey(remoteServices)
	shortKeyLookup := longToShortKey(localServices)
	for _, srv := range localServices {
		key := serviceNameInstance{
			name:     string(srv.Name),
			instance: srv.ContainerName,
		}
		var shortKey serviceNameInstance
		var ok bool
		if shortKey, ok = shortKeyLookup[key]; !ok {
			continue
		}
		remoteIndex, remoteFound := remoteIndexByKey[shortKey]
		var remoteSrv types.Service
		if remoteFound {
			remoteSrv = remoteServices[remoteIndex]
		}
		listenAddresses := getListenAddress(srv.ListenAddresses)
		// TODO: Stack
		if remoteFound && remoteSrv.Label == string(srv.Name) && remoteSrv.ListenAddresses == listenAddresses && remoteSrv.ExePath == srv.ExePath && remoteSrv.Active == srv.Active {
			localUUIDs[remoteSrv.ID] = true
			continue
		}
		payload := servicePayload{
			Service: types.Service{
				Label:           string(srv.Name),
				Instance:        shortKey.instance,
				ListenAddresses: listenAddresses,
				ExePath:         srv.ExePath,
				Stack:           "TODO",
				Active:          srv.Active,
			},
			Account: s.option.Cache.AccountID(),
			Agent:   s.option.State.AgentID(),
		}
		var result types.Service
		if remoteFound {
			_, err := s.client.Do("PUT", fmt.Sprintf("v1/service/%s/", remoteSrv.ID), payload, &result)
			if err != nil {
				return nil, err
			}
			remoteServices[remoteIndex] = result
			logger.V(2).Printf("Service %v updated with UUID %s", key, result.ID)
		} else {
			_, err := s.client.Do("POST", "v1/service/", payload, &result)
			if err != nil {
				return nil, err
			}
			remoteServices = append(remoteServices, result)
			logger.V(2).Printf("Service %v registrered with UUID %s", key, result.ID)
		}
		localUUIDs[result.ID] = true
		if remoteFound && remoteSrv.Active != result.Active {
			// API will update all associated metrics and update their active status. Apply the same rule on local cache
			// TODO
			_ = 5
		}
	}
	s.option.Cache.SetServices(remoteServices)
	return localUUIDs, nil
}

func (s *Synchronizer) syncServiceApplyLocalDeletion(localUUIDs map[string]bool) error {
	registeredServices := s.option.Cache.ServicesByUUID()
	for k, v := range registeredServices {
		if _, ok := localUUIDs[v.ID]; ok {
			continue
		}
		_, err := s.client.Do("DELETE", fmt.Sprintf("v1/service/%s/", v.ID), nil, nil)
		key := serviceNameInstance{name: v.Label, instance: v.Instance}
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
