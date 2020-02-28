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
	"context"
	"fmt"
	"glouton/check"
	"glouton/logger"
	"glouton/types"
	"net"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	customCheckTCP    = "tcp"
	customCheckHTTP   = "http"
	customCheckNagios = "nagios"
)

// Check is an interface which specify a check
type Check interface {
	CheckNow(ctx context.Context) types.StatusDescription
	Run(ctx context.Context) error
}

// CheckDetails is used to save a check and his id
type CheckDetails struct {
	id    int
	check Check
}

// collectorDetails contains information about a collector.
// It could be a Telegraf input of a Prometheus collector
type collectorDetails struct {
	inputID             int
	prometheusCollector prometheus.Collector
	closeFunc           func()
}

func (d *Discovery) configureChecks(oldServices, services map[NameContainer]Service) {
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

func (d *Discovery) removeCheck(key NameContainer) {
	if d.taskRegistry == nil {
		return
	}
	if check, ok := d.activeCheck[key]; ok {
		logger.V(2).Printf("Remove check for service %v on container %s", key.Name, key.ContainerName)
		delete(d.activeCheck, key)
		d.taskRegistry.RemoveTask(check.id)
	}
}

func (d *Discovery) createCheck(service Service) {
	if !service.Active {
		return
	}

	if service.CheckIgnored {
		logger.V(2).Printf("The check associated to the service '%s' on container '%s' is ignored by the configuration", service.Name, service.ContainerID)
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

	labels := service.LabelsOfStatus()
	annotations := service.AnnotationsOfStatus()

	switch service.ServiceType {
	case DovecoteService, MemcachedService, RabbitMQService, RedisService, ZookeeperService:
		d.createTCPCheck(service, di, primaryAddress, tcpAddresses, labels, annotations)
	case ApacheService, InfluxDBService, NginxService, SquidService:
		d.createHTTPCheck(service, di, primaryAddress, tcpAddresses, labels, annotations)
	case NTPService:
		if primaryAddress != "" {
			check := check.NewNTP(
				primaryAddress,
				tcpAddresses,
				labels,
				annotations,
				d.acc,
			)
			d.addCheck(check, service)
		} else {
			d.createTCPCheck(service, di, "", tcpAddresses, labels, annotations)
		}
	case CustomService:
		switch service.ExtraAttributes["check_type"] {
		case customCheckTCP:
			d.createTCPCheck(service, di, primaryAddress, tcpAddresses, labels, annotations)
		case customCheckHTTP:
			d.createHTTPCheck(service, di, primaryAddress, tcpAddresses, labels, annotations)
		case customCheckNagios:
			d.createNagiosCheck(service, primaryAddress, labels, annotations)
		default:
			logger.V(1).Printf("Unknown check type %#v on custom service %#v", service.ExtraAttributes["check_type"], service.Name)
		}
	default:
		d.createTCPCheck(service, di, primaryAddress, tcpAddresses, labels, annotations)
	}
}

func (d *Discovery) createTCPCheck(service Service, di discoveryInfo, primaryAddress string, tcpAddresses []string, labels map[string]string, annotations types.MetricAnnotations) {

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
		labels,
		annotations,
		d.acc,
	)
	d.addCheck(tcpCheck, service)
}

func (d *Discovery) createHTTPCheck(service Service, di discoveryInfo, primaryAddress string, tcpAddresses []string, labels map[string]string, annotations types.MetricAnnotations) {
	if primaryAddress == "" {
		d.createTCPCheck(service, di, primaryAddress, tcpAddresses, labels, annotations)
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
		labels,
		annotations,
		d.acc,
	)
	d.addCheck(httpCheck, service)
}

func (d *Discovery) createNagiosCheck(service Service, primaryAddress string, labels map[string]string, annotations types.MetricAnnotations) {
	var tcpAddress []string
	if primaryAddress != "" {
		tcpAddress = []string{primaryAddress}
	}
	httpCheck := check.NewNagios(
		service.ExtraAttributes["check_command"],
		tcpAddress,
		labels,
		annotations,
		d.acc,
	)
	d.addCheck(httpCheck, service)
}

func (d *Discovery) addCheck(check Check, service Service) {
	if d.acc == nil || d.taskRegistry == nil {
		return
	}
	key := NameContainer{
		Name:          service.Name,
		ContainerName: service.ContainerName,
	}

	id, err := d.taskRegistry.AddTask(check.Run, fmt.Sprintf("check for %s", service.Name))
	if err != nil {
		logger.V(1).Printf("Unable to add check: %v", err)
	}
	savedCheck := CheckDetails{
		check: check,
		id:    id,
	}
	d.activeCheck[key] = savedCheck
}
