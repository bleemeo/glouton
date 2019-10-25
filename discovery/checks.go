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
	"fmt"
	"glouton/check"
	"glouton/logger"
	"glouton/task"
	"net"
	"strconv"
)

const (
	customCheckTCP    = "tcp"
	customCheckHTTP   = "http"
	customCheckNagios = "nagios"
)

func (d *Discovery) configureChecks(oldServices, services map[nameContainer]Service) {
	for key := range oldServices {
		if _, ok := services[key]; !ok {
			d.removeCheck(key)
		}
	}

	for key, service := range services {
		oldService, ok := oldServices[key]
		if !ok || serviceNeedUpdate(oldService, service) {
			d.removeCheck(key)
			d.createCheck(service)
		}
	}
}

func (d *Discovery) removeCheck(key nameContainer) {
	if d.taskRegistry == nil {
		return
	}
	if checkID, ok := d.activeCheck[key]; ok {
		logger.V(2).Printf("Remove check for service %v on container %s", key.name, key.containerName)
		delete(d.activeCheck, key)
		d.taskRegistry.RemoveTask(checkID)
	}
}

func (d *Discovery) createCheck(service Service) {
	if !service.Active {
		return
	}

	logger.V(2).Printf("Add check for service %v on container %s", service.Name, service.ContainerID)

	di := servicesDiscoveryInfo[service.ServiceType]
	var primaryAddress string
	primaryIP, primaryPort := service.AddressPort()
	if primaryIP != "" {
		primaryAddress = fmt.Sprintf("%s:%d", primaryIP, primaryPort)
	}
	tcpAddresses := make([]string, 0)
	for _, a := range service.ListenAddresses {
		if a.Network() != tcpPortocol {
			continue
		}
		if a.Address == net.IPv4zero.String() {
			a.Address = service.IPAddress
		}
		tcpAddresses = append(tcpAddresses, a.String())
	}

	labels := map[string]string{
		"service_name": service.Name,
	}
	if service.ContainerName != "" {
		labels["item"] = service.ContainerName
		labels["container_id"] = service.ContainerID
		labels["container_name"] = service.ContainerName
	}

	switch service.ServiceType {
	case DovecoteService, MemcachedService, RabbitMQService, RedisService, ZookeeperService:
		d.createTCPCheck(service, di, primaryAddress, tcpAddresses, labels)
	case ApacheService, InfluxDBService, NginxService, SquidService:
		d.createHTTPCheck(service, di, primaryAddress, tcpAddresses, labels)
	case NTPService:
		if primaryAddress != "" {
			check := check.NewNTP(
				primaryAddress,
				tcpAddresses,
				fmt.Sprintf("%s_status", service.Name),
				labels,
				d.acc,
			)
			d.addCheck(check.Run, service)
		} else {
			d.createTCPCheck(service, di, "", tcpAddresses, labels)
		}
	case CustomService:
		switch service.ExtraAttributes["check_type"] {
		case customCheckTCP:
			d.createTCPCheck(service, di, primaryAddress, tcpAddresses, labels)
		case customCheckHTTP:
			d.createHTTPCheck(service, di, primaryAddress, tcpAddresses, labels)
		case customCheckNagios:
			d.createNagiosCheck(service, primaryAddress, labels)
		default:
			logger.V(1).Printf("Unknown check type %#v on custom service %#v", service.ExtraAttributes["check_type"], service.Name)
		}
	default:
		d.createTCPCheck(service, di, primaryAddress, tcpAddresses, labels)
	}
}

func (d *Discovery) createTCPCheck(service Service, di discoveryInfo, primaryAddress string, tcpAddresses []string, labels map[string]string) {

	var tcpSend, tcpExpect, tcpClose []byte
	switch service.ServiceType {
	case DovecoteService:
		tcpSend = []byte("001 NOOP\n")
		tcpExpect = []byte("001 OK")
		tcpClose = []byte("002 LOGOUT\n")
	case MemcachedService:
		tcpSend = []byte("version\r\n")
		tcpExpect = []byte("VERSION")
	case RabbitMQService:
		tcpSend = []byte("PINGAMQP")
		tcpExpect = []byte("AMQP")
	case RedisService:
		tcpSend = []byte("PING\n")
		tcpExpect = []byte("+PONG")
	case ZookeeperService:
		tcpSend = []byte("ruok\n")
		tcpExpect = []byte("imok")
	}
	tcpCheck := check.NewTCP(
		primaryAddress,
		tcpAddresses,
		!di.DisablePersistentConnection,
		tcpSend,
		tcpExpect,
		tcpClose,
		fmt.Sprintf("%s_status", service.Name),
		labels,
		d.acc,
	)
	d.addCheck(tcpCheck.Run, service)
}

func (d *Discovery) createHTTPCheck(service Service, di discoveryInfo, primaryAddress string, tcpAddresses []string, labels map[string]string) {
	if primaryAddress == "" {
		d.createTCPCheck(service, di, primaryAddress, tcpAddresses, labels)
		return
	}
	url := fmt.Sprintf("http://%s", primaryAddress)
	expectedStatusCode := 0
	if service.ServiceType == SquidService {
		// Agent does a normal HTTP request, but squid expect a proxy. It expect
		// squid to reply with a 400 - Bad request.
		expectedStatusCode = 400
	}
	if service.ServiceType == InfluxDBService {
		url += "/ping"
	}
	if service.ServiceType == CustomService && service.ExtraAttributes["http_path"] != "" {
		url += service.ExtraAttributes["http_path"]
	}
	if service.ServiceType == CustomService && service.ExtraAttributes["http_status_code"] != "" {
		tmp, err := strconv.ParseInt(service.ExtraAttributes["http_status_code"], 10, 0)
		if err != nil {
			logger.V(1).Printf("Invalid http_status_code %#v on service %s. Ignoring this option", service.Name, service.ExtraAttributes["http_status_code"])
		} else {
			expectedStatusCode = int(tmp)
		}
	}
	httpCheck := check.NewHTTP(
		url,
		tcpAddresses,
		expectedStatusCode,
		fmt.Sprintf("%s_status", service.Name),
		labels,
		d.acc,
	)
	d.addCheck(httpCheck.Run, service)
}

func (d *Discovery) createNagiosCheck(service Service, primaryAddress string, labels map[string]string) {
	var tcpAddress []string
	if primaryAddress != "" {
		tcpAddress = []string{primaryAddress}
	}
	httpCheck := check.NewNagios(
		service.ExtraAttributes["check_command"],
		tcpAddress,
		fmt.Sprintf("%s_status", service.Name),
		labels,
		d.acc,
	)
	d.addCheck(httpCheck.Run, service)
}

func (d *Discovery) addCheck(task task.Runner, service Service) {
	if d.acc == nil || d.taskRegistry == nil {
		return
	}
	key := nameContainer{
		name:          service.Name,
		containerName: service.ContainerName,
	}
	id, err := d.taskRegistry.AddTask(task, fmt.Sprintf("check for %s", service.Name))
	if err != nil {
		logger.V(1).Printf("Unable to add check: %v", err)
	}
	d.activeCheck[key] = id
}
