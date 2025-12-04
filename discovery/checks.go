// Copyright 2015-2025 Bleemeo
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
	"net"
	"net/url"
	"time"

	"github.com/bleemeo/glouton/check"
	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/prometheus/registry"
	"github.com/bleemeo/glouton/types"
	"github.com/bleemeo/glouton/utils/gloutonexec"

	"github.com/prometheus/prometheus/model/labels"
)

const (
	customCheckTCP     = "tcp"
	customCheckHTTP    = "http"
	customCheckNagios  = "nagios"
	customCheckProcess = "process"
)

// CheckDetails is used to save a check and his id.
type CheckDetails struct {
	id    int
	check *check.Gatherer
}

// collectorDetails contains information about a collector.
// It could be a Telegraf input of a Prometheus collector.
type collectorDetails struct {
	gathererID int
}

// checker is an interface which specifies a check.
type checker interface {
	Check(ctx context.Context, scheduleUpdate func(runAt time.Time)) types.MetricPoint
	DiagnosticArchive(ctx context.Context, archive types.ArchiveWriter) error
	Close()
}

func (d *Discovery) configureChecks(oldServices, services map[NameInstance]Service) {
	for key := range oldServices {
		if _, ok := services[key]; !ok {
			d.removeCheck(key)
		}
	}

	for key, service := range services {
		oldService, ok := oldServices[key]
		oldServiceState := facts.ContainerUnknown
		serviceState := facts.ContainerUnknown

		if oldService.container != nil && service.container != nil {
			oldServiceState = oldService.container.State()
			serviceState = service.container.State()
		}

		if !ok || serviceNeedUpdate(oldService, service, oldServiceState, serviceState) {
			d.removeCheck(key)
			d.createCheck(service)
		}
	}
}

func (d *Discovery) removeCheck(key NameInstance) {
	if check, ok := d.activeCheck[key]; ok {
		logger.V(2).Printf("Remove check for service %v on instance %s", key.Name, key.Instance)
		delete(d.activeCheck, key)
		d.metricRegistry.Unregister(check.id)
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

	logger.V(2).Printf("Add check for service %v instance %s", service.Name, service.Instance)

	di := servicesDiscoveryInfo[service.ServiceType]

	var primaryAddress string

	primaryIP, primaryPort := service.AddressPort()
	if primaryIP != "" {
		primaryAddress = fmt.Sprintf("%s:%d", primaryIP, primaryPort)
	}

	tcpAddresses := make([]string, 0)

	for _, a := range service.ListenAddresses {
		if a.Network() != tcpProtocol {
			continue
		}

		if a.Address == net.IPv4zero.String() {
			a.Address = service.IPAddress
		}

		tcpAddresses = append(tcpAddresses, a.String())
	}

	labels := service.LabelsOfStatus()
	annotations := service.AnnotationsOfStatus()

	if service.container != nil && service.container.State() == facts.ContainerStopped {
		d.createContainerStoppedCheck(service, primaryAddress, tcpAddresses, labels, annotations)

		return
	}

	switch service.ServiceType { //nolint:exhaustive
	case DovecotService, MemcachedService, RabbitMQService, RedisService, ValkeyService, ZookeeperService, NatsService:
		d.createTCPCheck(service, di, primaryAddress, tcpAddresses, labels, annotations)
	case ApacheService, InfluxDBService, NginxService, SquidService:
		d.createHTTPCheck(service, di, primaryAddress, tcpAddresses, labels, annotations)
	case NTPService:
		if primaryAddress != "" {
			check := check.NewNTP(
				primaryAddress,
				tcpAddresses,
				!di.DisablePersistentConnection,
				labels,
				annotations,
			)
			d.addCheck(check, service)
		} else {
			d.createTCPCheck(service, di, "", tcpAddresses, labels, annotations)
		}
	case PostfixService, EximService:
		check := check.NewSMTP(
			primaryAddress,
			tcpAddresses,
			!di.DisablePersistentConnection,
			labels,
			annotations,
		)
		d.addCheck(check, service)
	// Use a process check for services that don't expose a port.
	case Fail2banService:
		service.Config.MatchProcess = "fail2ban-server"

		d.createProcessCheck(service, labels, annotations)
	case NfsService:
		// Ignore NFS, it's hard to define a useful status for this service.
		// We can't rely on a process check since the process may be running
		// even if the NFS share failed to be mounted.
	case CustomService:
		createCheckType(d.commandRunner, service, d, di, primaryAddress, tcpAddresses, labels, annotations)
	default:
		d.createTCPCheck(service, di, primaryAddress, tcpAddresses, labels, annotations)
	}
}

func createCheckType(commandRunner *gloutonexec.Runner, service Service, d *Discovery, di discoveryInfo, primaryAddress string, tcpAddresses []string, labels map[string]string, annotations types.MetricAnnotations) {
	switch service.Config.CheckType {
	case customCheckTCP:
		d.createTCPCheck(service, di, primaryAddress, tcpAddresses, labels, annotations)
	case customCheckHTTP:
		d.createHTTPCheck(service, di, primaryAddress, tcpAddresses, labels, annotations)
	case customCheckNagios:
		d.createNagiosCheck(service, primaryAddress, labels, annotations, commandRunner)
	case customCheckProcess:
		d.createProcessCheck(service, labels, annotations)
	default:
		logger.V(1).Printf("Unknown check type %#v on custom service %#v", service.Config.CheckType, service.Name)
	}
}

func (d *Discovery) createTCPCheck(service Service, di discoveryInfo, primaryAddress string, tcpAddresses []string, labels map[string]string, annotations types.MetricAnnotations) {
	var tcpSend, tcpExpect, tcpClose []byte

	switch service.ServiceType { //nolint:exhaustive
	case DovecotService:
		tcpSend = []byte("001 NOOP\n")
		tcpExpect = []byte("001 OK")
		tcpClose = []byte("002 LOGOUT\n")
	case MemcachedService:
		tcpSend = []byte("version\r\n")
		tcpExpect = []byte("VERSION")
	case RabbitMQService:
		tcpSend = []byte("PINGAMQP")
		tcpExpect = []byte("AMQP")
	case RedisService, ValkeyService:
		tcpSend = []byte("PING\n")

		if service.Config.Password != "" {
			tcpSend = fmt.Appendf(nil, "AUTH %s\nPING\n", service.Config.Password)
		}

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
	)

	d.addCheck(tcpCheck, service)
}

func (d *Discovery) createHTTPCheck(
	service Service,
	di discoveryInfo,
	primaryAddress string,
	tcpAddresses []string,
	labels map[string]string,
	annotations types.MetricAnnotations,
) {
	if primaryAddress == "" {
		d.createTCPCheck(service, di, primaryAddress, tcpAddresses, labels, annotations)

		return
	}

	u, err := url.Parse("http://" + primaryAddress)
	if err != nil {
		logger.V(2).Printf("can't parse URL \"%s\" ? This shouldn't happen: %v", "http://"+primaryAddress, err)

		return
	}

	expectedStatusCode := 0

	if service.ServiceType == SquidService {
		// Agent does a normal HTTP request, but squid expect a proxy. It expects
		// squid to reply with a 400 - Bad request.
		expectedStatusCode = 400
	}

	if service.ServiceType == InfluxDBService {
		u.Path = "/ping"
	}

	if service.Config.HTTPPath != "" {
		u.Path = service.Config.HTTPPath
	}

	if service.Config.HTTPStatusCode != 0 {
		expectedStatusCode = service.Config.HTTPStatusCode
	}

	httpHost := u.Host
	if service.Config.HTTPHost != "" {
		httpHost = service.Config.HTTPHost
	}

	httpCheck := check.NewHTTP(
		u.String(),
		httpHost,
		tcpAddresses,
		!di.DisablePersistentConnection,
		expectedStatusCode,
		labels,
		annotations,
	)

	d.addCheck(httpCheck, service)
}

func (d *Discovery) createContainerStoppedCheck(
	service Service,
	primaryAddress string,
	tcpAddresses []string,
	labels map[string]string,
	annotations types.MetricAnnotations,
) {
	containerCheck := check.NewContainerStopped(primaryAddress, tcpAddresses, false, labels, annotations)

	d.addCheck(containerCheck, service)
}

func (d *Discovery) createNagiosCheck(
	service Service,
	primaryAddress string,
	labels map[string]string,
	annotations types.MetricAnnotations,
	runner *gloutonexec.Runner,
) {
	var tcpAddress []string

	if primaryAddress != "" {
		tcpAddress = []string{primaryAddress}
	}

	nagiosCheck := check.NewNagios(
		service.Config.CheckCommand,
		tcpAddress,
		true,
		labels,
		annotations,
		runner,
	)

	d.addCheck(nagiosCheck, service)
}

func (d *Discovery) createProcessCheck(service Service, labels map[string]string, annotations types.MetricAnnotations) {
	processCheck, err := check.NewProcess(service.Config.MatchProcess, labels, annotations, d.processFact)
	if err != nil {
		logger.V(0).Printf("Invalid custom service %s: %v", service.Name, err)
	}

	d.addCheck(processCheck, service)
}

func (d *Discovery) addCheck(serviceCheck checker, service Service) {
	checkGatherer := check.NewCheckGatherer(serviceCheck)
	lbls := service.LabelsOfStatus()

	options := registry.RegistrationOption{
		Description:              "check for " + service.Name,
		MinInterval:              max(service.Interval, time.Minute),
		InstanceUseContainerName: true,
		ExtraLabels:              lbls,
		JitterSeed:               labels.FromMap(lbls).Hash(),
		StopCallback:             checkGatherer.Close,
	}

	id, err := d.metricRegistry.RegisterGatherer(options, checkGatherer)
	if err != nil {
		logger.V(1).Printf("Unable to add check: %v", err)
	}

	key := NameInstance{
		Name:     service.Name,
		Instance: service.Instance,
	}

	savedCheck := CheckDetails{
		check: checkGatherer,
		id:    id,
	}
	d.activeCheck[key] = savedCheck
}
