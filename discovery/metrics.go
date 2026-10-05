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
	"errors"
	"fmt"
	"net"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/facts/container-runtime/veth"
	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/inputs/cpu"
	"github.com/bleemeo/glouton/inputs/disk"
	"github.com/bleemeo/glouton/inputs/diskio"
	"github.com/bleemeo/glouton/inputs/mem"
	"github.com/bleemeo/glouton/inputs/modify"
	"github.com/bleemeo/glouton/inputs/mysql"
	netInput "github.com/bleemeo/glouton/inputs/net"
	"github.com/bleemeo/glouton/inputs/swap"
	"github.com/bleemeo/glouton/inputs/system"
	"github.com/bleemeo/glouton/inputs/winperfcounters"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/prometheus/exporter/zfs"
	"github.com/bleemeo/glouton/prometheus/registry"
	"github.com/bleemeo/glouton/types"
	"github.com/bleemeo/glouton/utils/gloutonexec"
	"github.com/bleemeo/glouton/version"

	"github.com/influxdata/telegraf"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	// bindDefaultStatsPort is the default port of BIND's statistics-channel, which is
	// disabled by default and unrelated to the DNS port used for discovery.
	bindDefaultStatsPort = 8053
	// dovecotDefaultStatsPort is the default port of Dovecot's old_stats plugin listener.
	dovecotDefaultStatsPort = 24242
	// varnishProbeTimeout bounds the "varnishstat -V" that checks a container carries one.
	// It is short because nothing is waiting on it: finding no varnishstat only means no
	// input for this Varnish.
	varnishProbeTimeout = 10 * time.Second
	// varnishStatBinary is where telegraf's varnish plugin runs varnishstat from, and so
	// the only place a container can carry one this input is able to use.
	varnishStatBinary = "/usr/bin/varnishstat"
	// chronyDefaultCmdPort is the UDP port of chronyd's command protocol, which is how its metrics are read.
	chronyDefaultCmdPort = 323
)

// AddDefaultInputs adds system inputs to a collector.
func AddDefaultInputs(commandRunner *gloutonexec.Runner, metricRegistry GathererRegistry, inputsConfig inputs.CollectorConfig, vethProvider *veth.Provider, k8sResolver disk.KubernetesPodResolver) error {
	input, err := system.New()
	if err != nil {
		return err
	}

	if err = addEssentialInputToRegistry(metricRegistry, input, "system"); err != nil {
		return err
	}

	input, err = cpu.New()
	if err != nil {
		return err
	}

	if err = addEssentialInputToRegistry(metricRegistry, input, "cpu"); err != nil {
		return err
	}

	input, err = netInput.New(inputsConfig.NetIfMatcher, vethProvider)
	if err != nil {
		return err
	}

	if err = addEssentialInputToRegistry(metricRegistry, input, "net"); err != nil {
		return err
	}

	if inputsConfig.DFRootPath != "" {
		input, err = disk.New(inputsConfig.DFRootPath, inputsConfig.DFPathMatcher, inputsConfig.DFIgnoreFSTypes, k8sResolver)
		if err != nil {
			return err
		}

		if err = addEssentialInputToRegistry(metricRegistry, input, "disk"); err != nil {
			return err
		}
	}

	source, err := zfs.New(commandRunner, time.Minute)

	switch {
	case errors.Is(err, zfs.ErrZFSNotAvailable):
		logger.V(2).Printf("zfs isn't available: %v", err)
	case err != nil:
		logger.V(2).Printf("failed to create ZFS source: %v", err)
	default:
		_, err = metricRegistry.RegisterGatherer(
			registry.RegistrationOption{
				Description: "ZFS metrics",
				JitterSeed:  0,
			},
			source,
		)
		if err != nil {
			return fmt.Errorf("unable to add ZFS input: %w", err)
		}
	}

	input, err = diskio.New(inputsConfig.IODiskMatcher, k8sResolver)
	if err != nil {
		return err
	}

	if err = addEssentialInputToRegistry(metricRegistry, input, "diskio"); err != nil {
		return err
	}

	return addDefaultFromOS(inputsConfig, metricRegistry)
}

func addEssentialInputToRegistry(reg GathererRegistry, input telegraf.Input, name string) error {
	opt := registry.RegistrationOption{
		Description: name + " input",
		IsEssential: true,
	}
	_, err := reg.RegisterInput(opt, input)

	return err
}

func addDefaultFromOS(inputsConfig inputs.CollectorConfig, metricRegistry GathererRegistry) error {
	var input telegraf.Input

	var err error

	switch {
	case version.IsWindows():
		input, err = winperfcounters.New(inputsConfig)
		if err != nil {
			return err
		}

		err = addEssentialInputToRegistry(metricRegistry, input, "win_perf_counters")
		if err != nil {
			return err
		}
	default:
		// on windows, win_perf_counters provides the metrics for the memory
		input, err = mem.New()
		if err != nil {
			return err
		}

		if err = addEssentialInputToRegistry(metricRegistry, input, "mem"); err != nil {
			return err
		}

		input, err = swap.New()
		if err != nil {
			return err
		}

		if err = addEssentialInputToRegistry(metricRegistry, input, "swap"); err != nil {
			return err
		}
	}

	return nil
}

func (d *Discovery) configureMetricInputs(oldServices, services map[NameInstance]Service) error {
	for key := range oldServices {
		if _, ok := services[key]; !ok {
			d.removeInput(key)
		}
	}

	var err prometheus.MultiError

	for key, service := range services {
		oldService, ok := oldServices[key]
		serviceState := facts.ContainerRunning
		oldServiceState := facts.ContainerRunning

		if service.container != nil {
			serviceState = service.container.State()
		}

		if oldService.container != nil {
			oldServiceState = oldService.container.State()
		}

		if !ok || serviceNeedUpdate(oldService, service, oldServiceState, serviceState) {
			d.removeInput(key)

			if serviceState == facts.ContainerStopped {
				d.inputResult[key] = newInputResult(inputContainerStopped, nil)
			} else {
				outcome, createErr := d.createInput(service)

				d.inputResult[key] = newInputResult(outcome, createErr)

				err.Append(createErr)
			}
		}
	}

	return err.MaybeUnwrap()
}

// containerPID is the PID of the service's container, or 0 when it has none. Nil-safe so
// that it can be compared for any service.
func containerPID(service Service) int {
	if service.container == nil {
		return 0
	}

	return service.container.PID()
}

func serviceNeedUpdate(oldService, service Service, oldServiceState facts.ContainerState, serviceState facts.ContainerState) bool {
	switch {
	case oldService.Name != service.Name,
		oldService.Instance != service.Instance,
		oldService.ServiceType != service.ServiceType,
		oldService.ContainerID != service.ContainerID,
		oldService.ContainerName != service.ContainerName,
		oldService.IPAddress != service.IPAddress,
		oldService.ExePath != service.ExePath,
		oldService.Active != service.Active,
		oldService.CheckIgnored != service.CheckIgnored,
		oldService.MetricsIgnored != service.MetricsIgnored,
		oldServiceState != serviceState,
		containerPID(oldService) != containerPID(service):
		return true
	case !reflect.DeepEqual(oldService.Config, service.Config):
		return true
	case len(oldService.ListenAddresses) != len(service.ListenAddresses):
		return true
	}

	// We assume order of ListenAddresses is mostly stable. serviceEqual may return
	// some false positive.
	for i, old := range oldService.ListenAddresses {
		newListenAddress := service.ListenAddresses[i]
		if old.Network() != newListenAddress.Network() || old.String() != newListenAddress.String() {
			return true
		}
	}

	return false
}

func (d *Discovery) removeInput(key NameInstance) {
	delete(d.inputResult, key)

	if collector, ok := d.activeCollector[key]; ok {
		logger.V(2).Printf("Remove input for service %v on instance %s", key.Name, key.Instance)
		delete(d.activeCollector, key)

		collector.gathererRegistration.Unregister()
	}
}

// createInput builds and registers the metric input of a service, and returns what it could
// do: the ways of ending with no input mean different things to a user.
func (d *Discovery) createInput(service Service) (inputOutcome, error) {
	if !service.Active {
		return inputInactive, nil
	}

	if service.MetricsIgnored {
		logger.V(2).Printf("The input associated to the service '%s' on container '%s' is ignored by the configuration", service.Name, service.ContainerID)

		return inputIgnored, nil
	}

	// A custom check never has a metric input, which is not an unsupported type.
	if service.ServiceType == CustomService {
		return inputCheckOnly, nil
	}

	build, ok := inputBuilders[service.ServiceType]
	if !ok {
		logger.V(1).Printf("service type %s don't support metrics", service.ServiceType)

		return inputUnsupportedType, nil
	}

	// Most input use the compatibility naming with only name + item.
	// Inputs that what more flexibility could return their own gathererOptions.
	// Note that some fields have default anyway see code of registerInput.
	gathererOptions := registry.RegistrationOption{
		CompatibilityNameItem: true,
	}

	input, err := build(d, service, &gathererOptions)

	switch {
	case errors.Is(err, errMissingInfo):
		return inputMissingInfo, nil
	case errors.Is(err, errMissingCommand):
		return inputMissingCommand, nil
	}

	// An input that says it is disabled isn't a failure to report on every discovery run:
	// it is a plugin that doesn't exist on this platform (the Windows stubs of the inputs
	// reading a local daemon, like inputs/varnish) or one Telegraf wasn't built with. There
	// is simply no input for that service here.
	if errors.Is(err, inputs.ErrDisabledInput) {
		logger.V(1).Printf("No input for service %s on this platform: %v", service.Name, err)

		return inputUnsupportedPlatform, nil
	}

	if err != nil {
		return inputCreateFailed, err
	}

	// A builder says why it has no input: one that does not is a bug, not missing credentials.
	if input == nil {
		return inputCreateFailed, fmt.Errorf("%w: no input built for %s", errNoInputBuilt, service.ServiceType)
	}

	logger.V(2).Printf("Add input for service %v instance %s", service.Name, service.Instance)

	if err := d.registerInput(input, gathererOptions, service); err != nil {
		return inputCreateFailed, err
	}

	return inputRegistered, nil
}

func createMySQLInput(service Service) (telegraf.Input, error) {
	if unixSocket := getMetricsSocket(service); unixSocket != "" && service.Config.Password != "" {
		username := service.Config.Username
		if username == "" {
			username = mysqlDefaultUser
		}

		return mysql.New(fmt.Sprintf("%s:%s@unix(%s)/", username, service.Config.Password, unixSocket))
	}

	if ip, port := service.AddressPort(); ip != "" && service.Config.Password != "" {
		username := service.Config.Username
		if username == "" {
			username = mysqlDefaultUser
		}

		return mysql.New(fmt.Sprintf("%s:%s@tcp(%s)/", username, service.Config.Password, net.JoinHostPort(ip, strconv.Itoa(port))))
	}

	return nil, errMissingInfo
}

func createMariaDBInput(service Service) (telegraf.Input, error) {
	if unixSocket := getMetricsSocket(service); unixSocket != "" && service.Config.Password != "" {
		username := service.Config.Username
		if username == "" {
			username = mariadbDefaultUser
		}

		return mysql.NewMariaDB(fmt.Sprintf("%s:%s@unix(%s)/", username, service.Config.Password, unixSocket))
	}

	if ip, port := service.AddressPort(); ip != "" && service.Config.Password != "" {
		username := service.Config.Username
		if username == "" {
			username = mariadbDefaultUser
		}

		return mysql.NewMariaDB(fmt.Sprintf("%s:%s@tcp(%s)/", username, service.Config.Password, net.JoinHostPort(ip, strconv.Itoa(port))))
	}

	return nil, errMissingInfo
}

func (d *Discovery) registerInput(input telegraf.Input, opts registry.RegistrationOption, service Service) error {
	extraLabels := map[string]string{
		types.LabelMetaServiceName:     service.Name,
		types.LabelMetaServiceInstance: service.Instance,
		types.LabelMetaContainerID:     service.ContainerID,
	}

	if !opts.CompatibilityNameItem {
		opts.InstanceUseContainerName = true
	}

	if _, port := service.AddressPort(); port != 0 {
		extraLabels[types.LabelMetaServicePort] = strconv.Itoa(port)
	}

	if service.Instance != "" {
		input = modify.AddInstance(input, service.Instance)
	}

	if opts.Description == "" {
		opts.Description = fmt.Sprintf("Service input %s %s", service.Name, service.Instance)
	}

	if opts.ExtraLabels == nil {
		opts.ExtraLabels = extraLabels
	}

	gathererID, err := d.metricRegistry.RegisterInput(
		opts,
		input,
	)
	if err != nil {
		return err
	}

	key := NameInstance{
		Name:     service.Name,
		Instance: service.Instance,
	}
	d.activeCollector[key] = collectorDetails{
		gathererRegistration: gathererID,
	}

	return nil
}

func urlForPHPFPM(service Service) string {
	url := service.Config.StatsURL
	if url != "" {
		return url
	}

	if service.Config.Port != 0 && service.IPAddress != "" {
		return fmt.Sprintf("fcgi://%s/status", net.JoinHostPort(service.IPAddress, strconv.Itoa(service.Config.Port)))
	}

	for _, v := range service.ListenAddresses {
		if v.Network() != tcpProtocol {
			continue
		}

		return fmt.Sprintf("fcgi://%s/status", v.String())
	}

	return ""
}

// activeMQURL returns the URL of the web console the ActiveMQ metrics are read from, and the
// credentials to read it with, or an empty URL when no address is known.
//
// The console always requires authentication, so a missing username or password falls back
// to the broker's factory account (admin/admin), which a default install still answers to.
func activeMQURL(service Service) (url string, username string, password string) {
	username = service.Config.Username
	password = service.Config.Password

	if username == "" || password == "" {
		logger.V(1).Printf("No metrics for %s on instance %s: a username and a password are required, trying with default credentials", service.Name, service.Instance)

		if username == "" {
			username = activeMQDefaultUser
		}

		if password == "" {
			password = activeMQDefaultPassword
		}
	}

	if service.Config.StatsURL != "" {
		return service.Config.StatsURL, username, password
	}

	if ip, port := service.AddressPort(); ip != "" {
		return "http://" + net.JoinHostPort(ip, strconv.Itoa(port)), username, password
	}

	return "", "", ""
}

// apacheStatusURL builds the server-status URL for an Apache instance, omitting the port
// from the URL when it's the HTTP default (80) -- an IPv6 address then still needs brackets
// even without a port suffix.
func apacheStatusURL(ip string, port int) string {
	if port == 80 {
		host := ip
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}

		return fmt.Sprintf("http://%s/server-status?auto", host)
	}

	return fmt.Sprintf("http://%s/server-status?auto", net.JoinHostPort(ip, strconv.Itoa(port)))
}

func clickHouseAddress(service Service) (ip string, port int) {
	if service.Config.StatsPort != 0 {
		return service.AddressForPort(service.Config.StatsPort, tcpProtocol, true), service.Config.StatsPort
	}

	if service.Config.Port != 0 {
		return service.AddressForPort(service.Config.Port, tcpProtocol, true), service.Config.Port
	}

	ip, port = service.AddressPort()

	// 8123 is the Clickhouse monitoring port by default, using another one is a special config by the user
	if ip == "" && service.Config.Port == 0 && service.IPAddress != "" {
		ip = service.IPAddress
		port = servicesDiscoveryInfo[ClickHouseService].ServicePort
	}

	return ip, port
}

func getMetricsSocket(service Service) string {
	socket := service.Config.MetricsUnixSocket

	if socket == "" {
		return ""
	}

	if _, err := os.Stat(socket); err != nil {
		return ""
	}

	return socket
}

// statsListenerAddress resolves the address of a stats listener that runs on its own port,
// separate from the one the service was discovered on, and that is opt-in. It returns an
// empty IP when no address is known for that port.
//
// On its default port the service must be seen listening on it, since a service without
// that listener -- the default configuration -- would otherwise get an input failing on
// every single gather. That only holds when the listen addresses are the ones netstat
// reports for the process: those of a containerized service are the ports its container
// publishes (see getDiscoveryInfo), where such a listener is usually not among them, so
// there the address is forced and the input is created anyway. Setting stats_port forces
// it too, as it says the listener is there whether or not Glouton sees the port (the same
// thing RabbitMQ does for its management port).
func statsListenerAddress(service Service, defaultPort int) (ip string, port int) {
	port = defaultPort
	force := service.ContainerID != ""

	if service.Config.StatsPort != 0 {
		port = service.Config.StatsPort
		force = true
	}

	return service.AddressForPort(port, tcpProtocol, force), port
}

// bindStatsURL returns the URL of BIND's statistics-channel, or "" when no address is
// known for it. The statistics-channel is disabled by default and is unrelated to the
// DNS port used for discovery, so it's looked up on its own default port unless the
// user configured one -- see statsListenerAddress for how that port is resolved.
//
// Auto-discovery always assumes XML v3 (the only format on BIND 9.10+, and available
// on 9.9+ with --enable-newstats), since the telegraf plugin picks its parser solely
// from the URL path and can't auto-detect what the server actually speaks. Older BIND
// (9.6-9.8, or 9.9 without newstats) only has XML v2, reachable at the same port with
// no path suffix at all (9.6-9.8) or "/xml/v2" (9.9); some 9.10+ distros also expose
// JSON v1 at "/json/v1". For any of those, set the service's stats_url config
// explicitly to the right path -- see the URL table in telegraf's bind plugin doc.
// We could maybe probe the endpoint to pick the right format automatically.
func bindStatsURL(service Service) string {
	if service.Config.StatsURL != "" {
		return service.Config.StatsURL
	}

	ip, port := statsListenerAddress(service, bindDefaultStatsPort)
	if ip == "" {
		return ""
	}

	return fmt.Sprintf("http://%s/xml/v3", net.JoinHostPort(ip, strconv.Itoa(port)))
}

// dovecotStatsServer returns the address of Dovecot's old_stats plugin listener, either
// as a unix socket path or as a "host:port" TCP address, or "" when neither is known.
//
// old_stats is an opt-in plugin (and is gone from Dovecot 2.4), so like BIND's
// statistics-channel it is looked up on its own default port -- a Dovecot without the
// plugin has no listener, and an input for it would only report connection errors. See
// statsListenerAddress for how that port is resolved. Configuring a metrics unix socket
// says the listener is there too.
func dovecotStatsServer(service Service) string {
	if socket := getMetricsSocket(service); socket != "" {
		return socket
	}

	ip, port := statsListenerAddress(service, dovecotDefaultStatsPort)
	if ip == "" {
		return ""
	}

	return net.JoinHostPort(ip, strconv.Itoa(port))
}

// canReadVarnish reports whether the varnishstat this input runs can be reached for a
// service, and logs why when it cannot.
//
// A Varnish installed on the machine is read with the machine's binary.
//
// A containerised one is read with the container's own varnishstat, which an image is free
// not to ship -- plenty carry only the daemon. Running "varnishstat -V" is what answers
// that: it prints the version and exits without needing a running instance, so it says
// whether the binary is there and runnable without reading any statistics. Asking the
// binary beats listing the directory it would live in, which needed a privileged read of
// the container's filesystem through /proc to answer the same question.
//
// An image carrying no varnishstat gets no input rather than one failing on every gather.
// It is not a case Glouton can do anything about: reading it would mean a varnishstat on
// the machine, which nothing documents installing for a containerised Varnish.
func (d *Discovery) canReadVarnish(service Service) bool {
	if service.ContainerID == "" {
		return true
	}

	ctx, cancel := context.WithTimeout(context.Background(), varnishProbeTimeout)
	defer cancel()

	if _, err := d.containerInfo.Exec(ctx, service.ContainerID, []string{varnishStatBinary, "-V"}); err != nil {
		logger.V(1).Printf(
			"Not gathering Varnish of %s: its container has no usable %s (%v)",
			service.Instance, varnishStatBinary, err,
		)

		return false
	}

	return true
}

// chronyCmdAddress returns the "host:port" of chronyd's command protocol, which is UDP and
// distinct from the NTP port the service was discovered on. Both the input and the check use
// it, so they cannot disagree about which daemon they read.
//
// The address is Glouton's own loopback unless the daemon cannot be the one next to it: a
// container of its own, or an address the user declared. A non-loopback service.IPAddress is
// not enough on its own, since it comes from the NTP port's bind address: a chronyd with
// "bindaddress 192.168.1.5" still keeps its command port on loopback. For a container that
// same IPAddress IS used, netstat never reporting the command port from inside the
// container's network namespace.
//
// ok is false for a container the runtime reports no address for, so that neither the input
// nor the check runs rather than both reporting on the chronyd next to Glouton.
func chronyCmdAddress(service Service) (address string, ok bool) {
	address = service.Config.Address
	port := service.Config.StatsPort

	// A container of its own: Glouton's loopback is not the container's.
	if address == "" && service.ContainerID != "" {
		address = service.IPAddress

		if address == "" {
			// A container whose address the runtime doesn't report (network_mode: none or
			// container:<other>, both of which leave PrimaryAddress() empty).
			return "", false
		}
	}

	if address == "" {
		// The local daemon, named rather than left for the input to find, so that the
		// check reads the same one.
		address = localhostIP
	}

	if port == 0 {
		port = chronyDefaultCmdPort
	}

	return net.JoinHostPort(address, strconv.Itoa(port)), true
}

// ntpdAddress returns the "host:port" to read ntpd's control protocol (NTP mode 6) on, or ""
// to let the input use 127.0.0.1 and the NTP port.
//
// Mode 6 is served on the NTP port itself, so there is no separate port to configure -- a
// "port" override moves both. An address is only returned for a daemon Glouton's loopback
// cannot be, the same rule chronyCmdAddress follows, since ntpd's usual "restrict default
// ... noquery" only leaves 127.0.0.1 and ::1 unrestricted.
//
// ok has the same meaning as chronyCmdAddress's: false is "there is no telling where this
// daemon is", which an empty address (meaning "the local default is right") cannot say.
func ntpdAddress(service Service) (address string, ok bool) {
	address = service.Config.Address
	port := service.Config.Port

	// Same as chronyCmdAddress: reached only with no address configured, so the container is
	// all that is left to tell.
	if address == "" && service.ContainerID != "" {
		address = service.IPAddress

		if address == "" {
			return "", false
		}
	}

	if address == "" && port == 0 {
		return "", true
	}

	if address == "" {
		// Only the port was overridden: the input would go back to the default 123, so the
		// loopback it would have used is spelled out here alongside the port.
		address = localhostIP
	}

	if port == 0 {
		port = servicesDiscoveryInfo[NTPService].ServicePort
	}

	return net.JoinHostPort(address, strconv.Itoa(port)), true
}
