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

package discovery

import (
	"encoding/json"
	"fmt"
	"glouton/facts"
	"glouton/logger"
	"strconv"
	"strings"
)

const stateKey = "DiscoveredServices"

// State allow to persite object
type State interface {
	Get(key string, result interface{}) error
	Set(key string, object interface{}) error
}

type oldServiceKeyValue []json.RawMessage

func (o oldServiceKeyValue) toService() (srv Service, err error) {
	if len(o) != 2 {
		return srv, fmt.Errorf("old service has %d part, want 2", len(o))
	}

	var (
		key    [2]string
		oldSrv oldService
	)

	if err = json.Unmarshal(o[0], &key); err != nil {
		return
	}

	if err = json.Unmarshal(o[1], &oldSrv); err != nil {
		return
	}

	srv, err = oldSrv.toService(key[1])

	return
}

type oldService struct {
	Service      string            `json:"service"`
	Address      string            `json:"address"`
	ExePath      string            `json:"exe_path"`
	Active       bool              `json:"active"`
	NetStatPorts map[string]string `json:"netstat_ports"`
	ContainerID  string            `json:"container_id"`
	Stack        string            `json:"stack"`
}

func (o oldService) toService(instance string) (srv Service, err error) {
	listenAddresses := make([]facts.ListenAddress, 0, len(o.NetStatPorts))

	for k, v := range o.NetStatPorts {
		if k == "unix" {
			listenAddresses = append(listenAddresses, facts.ListenAddress{
				NetworkFamily: "unix",
				Address:       v,
			})
		} else {
			part := strings.Split(k, "/")
			if len(part) != 2 {
				return srv, fmt.Errorf("unexpected format for old netstat key: %#v", k)
			}

			port, err := strconv.ParseInt(part[0], 10, 0)
			if err != nil {
				return srv, err
			}

			listenAddresses = append(listenAddresses, facts.ListenAddress{
				NetworkFamily: part[1],
				Address:       v,
				Port:          int(port),
			})
		}
	}

	return Service{
		ServiceType:     ServiceName(o.Service),
		Name:            o.Service,
		ContainerID:     o.ContainerID,
		ContainerName:   instance,
		IPAddress:       o.Address,
		ExePath:         o.ExePath,
		Active:          o.Active,
		Stack:           o.Stack,
		ListenAddresses: listenAddresses,
	}, nil
}

func servicesFromState(state State) []Service {
	var result []Service

	if err := state.Get(stateKey, &result); err != nil || result == nil {
		// Try to load old format
		logger.V(1).Printf("Unable to load new discovered service, try using old format: %v", err)

		var oldServices []oldServiceKeyValue

		if err := state.Get("discovered_services", &oldServices); err != nil {
			return make([]Service, 0)
		}

		result = make([]Service, len(oldServices))

		for i, o := range oldServices {
			srv, err := o.toService()
			if err != nil {
				logger.V(1).Printf("Unable to load old discovered_services: %v", err)
				return make([]Service, 0)
			}

			result[i] = srv
		}
	}

	return result
}

func saveState(state State, servicesMap map[NameContainer]Service) {
	services := make([]Service, 0, len(servicesMap))

	for _, srv := range servicesMap {
		services = append(services, srv)
	}

	err := state.Set(stateKey, services)
	if err != nil {
		logger.V(1).Printf("Unable to persist discovered services: %v", err)
	}
}
