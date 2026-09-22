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

package discovery

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/bleemeo/glouton/check"
	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/inputs/chrony"
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
	registration types.Registration
	check        *check.Gatherer
}

// collectorDetails contains information about a collector.
// It could be a Telegraf input of a Prometheus collector.
type collectorDetails struct {
	gathererRegistration types.Registration
}

// checker is an interface which specifies a check.
type checker interface {
	Check(ctx context.Context, scheduleUpdate func(opts types.ScheduleOption)) (types.MetricPoint, error)
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
		check.registration.Unregister()
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
		primaryAddress = net.JoinHostPort(primaryIP, strconv.Itoa(primaryPort))
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
	case DovecotService, MemcachedService, RabbitMQService, RedisService,
		ValkeyService, ZookeeperService, NatsService:
		d.createTCPCheck(service, di, primaryAddress, tcpAddresses, labels, annotations)
	// Varnish is checked over HTTP rather than TCP because the three answers a cache can
	// give are worth telling apart, and only HTTP tells them apart:
	//   - 200: Varnish is up and its backend is reachable.
	//   - 503: Varnish is up and cannot reach its backend. TCP calls this healthy, since
	//     the port accepts the connection either way.
	//   - refused: Varnish itself is down.
	//
	// What it costs is that the answer comes from the backend application rather than from
	// Varnish, since Varnish relays it: the check asks for "/" and reports whatever the
	// fronted app says there. An app with nothing at its root answers 404, a warning; a VCL
	// that routes on req.http.host with no fallback answers 503 to the check's own Host
	// header, a critical. Both are configured away with http_path and http_host on the
	// service, which is what they are for. Redirects need nothing: the check does not
	// follow them, and a 3xx is below the 400 that starts a warning.
	case ApacheService, NginxService, SquidService, InfluxDBService, VarnishService:
		d.createHTTPCheck(service, di, primaryAddress, tcpAddresses, labels, annotations)
	case ChronyService, NTPService:
		d.createNTPCheck(service, di, primaryAddress, tcpAddresses, labels, annotations)
	case PostfixService, EximService:
		check := check.NewSMTP(
			primaryAddress,
			tcpAddresses,
			!di.DisablePersistentConnection,
			labels,
			annotations,
			d.containerInfo,
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

// createNTPCheck adds the check of a chrony or ntpd service: the NTP protocol itself when
// the daemon really serves it, and chrony's command protocol for a chronyd that doesn't.
//
// A chrony that only syncs the local clock -- the default install on most distributions --
// never answers an NTP query, so check.NewNTP would report a permanent "Connection timed
// out" on a healthy daemon. Its command protocol is the only thing such a daemon answers.
func (d *Discovery) createNTPCheck(service Service, di discoveryInfo, primaryAddress string, tcpAddresses []string, labels map[string]string, annotations types.MetricAnnotations) {
	if service.ServiceType == ChronyService && !servesNTPProtocol(service, di) {
		// The same address the input reads, so the check and the metrics can never disagree
		// about which daemon they are talking to.
		//
		// An address that couldn't be resolved is left empty rather than skipping the
		// check: the UDP check turns an empty address into an explicit unknown ("No UDP
		// address to check"), where no check at all would publish no status for this
		// service. What it must never fall back to is Glouton's own loopback, which reports
		// on whatever chronyd runs next to it under this service's name.
		checkAddress, _ := chronyCmdAddress(service)

		// A UDP check, the command port being UDP-only where createTCPCheck always dials
		// TCP. The payload and the reply check come from inputs/chrony, which owns the
		// protocol: see ProbePacket and ValidateReply for why neither arbitrary bytes nor
		// any reply at all would do.
		udpCheck := check.NewUDP(
			checkAddress,
			chrony.ProbePacket(),
			nil,
			chrony.ValidateReply,
			labels,
			annotations,
			d.containerInfo,
		)
		d.addCheck(udpCheck, service)

		return
	}

	if primaryAddress != "" {
		ntpCheck := check.NewNTP(
			primaryAddress,
			tcpAddresses,
			!di.DisablePersistentConnection,
			labels,
			annotations,
			d.containerInfo,
		)
		d.addCheck(ntpCheck, service)
	} else {
		d.createTCPCheck(service, di, "", tcpAddresses, labels, annotations)
	}
}

// servesNTPProtocol reports whether the daemon was really seen listening on the NTP port,
// as opposed to discovery having assumed that port from the service type: with no netstat
// information, updateListenAddresses adds a synthetic listen address on the type's default
// port, which for NTPService is the NTP port itself -- so the listen addresses alone can't
// tell a daemon serving NTP from one that was merely recognized as an NTP daemon.
func servesNTPProtocol(service Service, di discoveryInfo) bool {
	if !service.HasNetstatInfo {
		return false
	}

	port := service.defaultPort(di)
	if service.Config.Port != 0 {
		port = service.Config.Port
	}

	// A configured address or port replaces the listen addresses with a single entry that
	// applyOverrideInPlace types tcp whatever protocol the service actually speaks, so the
	// protocol can't be matched on it. That entry was built from the configured port anyway:
	// what decides is whether that port is the NTP one.
	if service.Config.Address != "" || service.Config.Port != 0 {
		return port == di.ServicePort
	}

	for _, address := range service.ListenAddresses {
		// IsProtocol rather than comparing the network name: netstat records the IP family
		// in it, so an IPv6-only daemon listens on "udp6" and would otherwise look like one
		// that doesn't serve NTP at all.
		if address.IsProtocol(di.ServiceProtocol) && address.Port == port {
			return true
		}
	}

	return false
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
		tcpSend = []byte("AMQP\x00\x00\x09\x01")
		tcpExpect = []byte{0x01, 0x00, 0x00}
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
		d.containerInfo,
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

	var okStatusCodes []int

	switch service.ServiceType { //nolint:exhaustive
	case SquidService:
		// Agent does a normal HTTP request, but squid expect a proxy. It expects
		// squid to reply with a 400 - Bad request.
		expectedStatusCode = 400
	case InfluxDBService:
		// "/ping" is the one route every line answers about itself, and answers cheaply:
		// no query is run and 1.x and 2.x leave it open even with authentication enabled.
		u.Path = "/ping"

		// InfluxDB 3 authenticates every route, so it answers 401 there when the check
		// holds no token -- which it never does. That 401 is the server saying it is up
		// and asking who is calling, so it is an Ok rather than the warning the usual
		// banding would give. It stays a narrow exception: anything else 4xx is still a
		// warning, 5xx still critical, and a server that has stopped listening still
		// fails to connect at all.
		//
		// Checking HTTP rather than TCP is what makes this worth doing: a TCP connect
		// succeeds against the proxy "docker run -p" puts in front of a container whether
		// or not the server behind it is alive, where speaking HTTP does not.
		okStatusCodes = []int{http.StatusUnauthorized}
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
		okStatusCodes,
		labels,
		annotations,
		d.containerInfo,
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
	containerCheck := check.NewContainerStopped(primaryAddress, tcpAddresses, false, labels, annotations, d.containerInfo)

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
		d.containerInfo,
	)

	d.addCheck(nagiosCheck, service)
}

func (d *Discovery) createProcessCheck(service Service, labels map[string]string, annotations types.MetricAnnotations) {
	processCheck, err := check.NewProcess(service.Config.MatchProcess, labels, annotations, d.processFact, d.containerInfo)
	if err != nil {
		logger.V(0).Printf("Invalid custom service %s: %v", service.Name, err)
	}

	d.addCheck(processCheck, service)
}

func (d *Discovery) addCheck(serviceCheck checker, service Service) {
	checkGatherer := check.NewCheckGatherer(serviceCheck)
	lbls := service.LabelsOfStatus()

	options := registry.RegistrationOption{
		Description:              fmt.Sprintf("check for %s %s", service.Name, service.Instance),
		MinInterval:              max(service.Interval, time.Minute),
		InstanceUseContainerName: true,
		ExtraLabels:              lbls,
		JitterSeed:               labels.FromMap(lbls).Hash(),
		StopCallback:             checkGatherer.Close,
	}

	id, err := d.metricRegistry.RegisterGatherer(options, checkGatherer)
	if err != nil {
		logger.V(1).Printf("Unable to add check: %v", err)

		return
	}

	checkGatherer.SetScheduleUpdate(id.ScheduleRun)

	key := NameInstance{
		Name:     service.Name,
		Instance: service.Instance,
	}

	savedCheck := CheckDetails{
		check:        checkGatherer,
		registration: id,
	}
	d.activeCheck[key] = savedCheck
}
