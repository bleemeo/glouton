// Copyright 2015-2024 Bleemeo
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
	"errors"
	"fmt"
	"net"
	"os"
	"reflect"
	"strconv"
	"time"

	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/facts/container-runtime/veth"
	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/inputs/apache"
	"github.com/bleemeo/glouton/inputs/cpu"
	"github.com/bleemeo/glouton/inputs/disk"
	"github.com/bleemeo/glouton/inputs/diskio"
	"github.com/bleemeo/glouton/inputs/elasticsearch"
	"github.com/bleemeo/glouton/inputs/fail2ban"
	"github.com/bleemeo/glouton/inputs/haproxy"
	"github.com/bleemeo/glouton/inputs/jenkins"
	"github.com/bleemeo/glouton/inputs/mem"
	"github.com/bleemeo/glouton/inputs/memcached"
	"github.com/bleemeo/glouton/inputs/mongodb"
	"github.com/bleemeo/glouton/inputs/mysql"
	"github.com/bleemeo/glouton/inputs/nats"
	netInput "github.com/bleemeo/glouton/inputs/net"
	"github.com/bleemeo/glouton/inputs/nfs"
	"github.com/bleemeo/glouton/inputs/nginx"
	"github.com/bleemeo/glouton/inputs/openldap"
	"github.com/bleemeo/glouton/inputs/phpfpm"
	"github.com/bleemeo/glouton/inputs/postgresql"
	"github.com/bleemeo/glouton/inputs/rabbitmq"
	"github.com/bleemeo/glouton/inputs/redis"
	"github.com/bleemeo/glouton/inputs/swap"
	"github.com/bleemeo/glouton/inputs/system"
	"github.com/bleemeo/glouton/inputs/upsd"
	"github.com/bleemeo/glouton/inputs/uwsgi"
	"github.com/bleemeo/glouton/inputs/winperfcounters"
	"github.com/bleemeo/glouton/inputs/zookeeper"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/prometheus/exporter/zfs"
	"github.com/bleemeo/glouton/prometheus/registry"
	"github.com/bleemeo/glouton/types"
	"github.com/bleemeo/glouton/version"

	"github.com/influxdata/telegraf"
	"github.com/prometheus/client_golang/prometheus"
)

var errNotSupported = errors.New("service not supported by Prometheus collector")

// AddDefaultInputs adds system inputs to a collector.
func AddDefaultInputs(metricRegistry GathererRegistry, inputsConfig inputs.CollectorConfig, vethProvider *veth.Provider) error {
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
		input, err = disk.New(inputsConfig.DFRootPath, inputsConfig.DFPathMatcher, inputsConfig.DFIgnoreFSTypes)
		if err != nil {
			return err
		}

		if err = addEssentialInputToRegistry(metricRegistry, input, "disk"); err != nil {
			return err
		}
	}

	source, err := zfs.New(time.Minute)

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

	input, err = diskio.New(inputsConfig.IODiskMatcher)
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

			if serviceState != facts.ContainerStopped {
				err.Append(d.createInput(service))
			}
		}
	}

	return err.MaybeUnwrap()
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
		oldServiceState != serviceState:
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
	if collector, ok := d.activeCollector[key]; ok {
		logger.V(2).Printf("Remove input for service %v on instance %s", key.Name, key.Instance)
		delete(d.activeCollector, key)

		if !d.metricRegistry.Unregister(collector.gathererID) {
			logger.V(2).Printf("The gatherer wasn't present")
		}
	}
}

// createPrometheusCollector create a Prometheus collector for given service
// Return errNotSupported if no Prometheus collector exists for this service.
func (d *Discovery) createPrometheusCollector(service Service) error {
	if service.ServiceType == MemcachedService {
		return d.createPrometheusMemcached(service)
	}

	return errNotSupported
}

func (d *Discovery) createInput(service Service) error { //nolint:maintidx
	if !service.Active {
		return nil
	}

	if service.MetricsIgnored {
		logger.V(2).Printf("The input associated to the service '%s' on container '%s' is ignored by the configuration", service.Name, service.ContainerID)

		return nil
	}

	if d.metricFormat == types.MetricFormatPrometheus {
		err := d.createPrometheusCollector(service)
		if !errors.Is(err, errNotSupported) {
			logger.V(2).Printf("Add collector for service %v on container %s", service.Name, service.ContainerID)

			return err
		}
	}

	var (
		err   error
		input telegraf.Input
		// Inputs that return gatherer options will use an input gatherer instead of the collector,
		// this means all labels will be kept and not only the item.
	)

	// Most input use the compatibility naming with only name + item.
	// Inputs that what more flexibility could return their own gathererOptions.
	// Note that some fields have default anyway see code of registerInput.
	gathererOptions := registry.RegistrationOption{
		CompatibilityNameItem: true,
	}

	switch service.ServiceType { //nolint:exhaustive
	case ApacheService:
		if ip, port := service.AddressPort(); ip != "" {
			statusURL := fmt.Sprintf("http://%s/server-status?auto", net.JoinHostPort(ip, strconv.Itoa(port)))

			if port == 80 {
				statusURL = fmt.Sprintf("http://%s/server-status?auto", ip)
			}

			input, err = apache.New(statusURL)
		}
	case ElasticSearchService:
		if ip, port := service.AddressPort(); ip != "" {
			input, err = elasticsearch.New("http://" + net.JoinHostPort(ip, strconv.Itoa(port)))
		}
	case Fail2banService:
		input, gathererOptions, err = fail2ban.New()
	case HAProxyService:
		if service.Config.StatsURL != "" {
			input, err = haproxy.New(service.Config.StatsURL)
		}
	case JenkinsService:
		if service.Config.StatsURL != "" && service.Config.Password != "" {
			input, gathererOptions, err = jenkins.New(service.Config)
		}
	case MemcachedService:
		if ip, port := service.AddressPort(); ip != "" {
			input, err = memcached.New(fmt.Sprintf("%s:%d", ip, port))
		}
	case MongoDBService:
		if ip, port := service.AddressPort(); ip != "" {
			input, err = mongodb.New("mongodb://" + net.JoinHostPort(ip, strconv.Itoa(port)))
		}
	case MySQLService:
		input, err = createMySQLInput(service)
	case NatsService:
		// The default port of the monitoring server is 8222.
		port := 8222

		if service.Config.StatsPort != 0 {
			port = service.Config.StatsPort
		}

		if ip := service.AddressForPort(port, "tcp", true); ip != "" {
			url := "http://" + net.JoinHostPort(service.IPAddress, strconv.Itoa(port))
			input, gathererOptions, err = nats.New(url)
		}
	case NfsService:
		input, gathererOptions, err = nfs.New()
	case NginxService:
		if ip, port := service.AddressPort(); ip != "" {
			input, err = nginx.New(fmt.Sprintf("http://%s/nginx_status", net.JoinHostPort(ip, strconv.Itoa(port))))
		}
	case OpenLDAPService:
		if ip, port := service.AddressPort(); ip != "" {
			input, gathererOptions, err = openldap.New(ip, port, service.Config)
		}
	case PHPFPMService:
		statsURL := urlForPHPFPM(service)
		if statsURL != "" {
			input, err = phpfpm.New(statsURL)
		}
	case PostgreSQLService:
		if ip, port := service.AddressPort(); ip != "" && service.Config.Password != "" {
			username := service.Config.Username
			if username == "" {
				username = "postgres"
			}

			address := fmt.Sprintf(
				"host=%s port=%d user=%s password=%s dbname=postgres sslmode=disable",
				ip, port, username, service.Config.Password,
			)
			input, err = postgresql.New(address, service.Config.DetailedItems)
		}
	case RabbitMQService:
		mgmtPort := 15672
		force := false

		if service.Config.StatsPort != 0 {
			mgmtPort = service.Config.StatsPort
			force = true
		}

		if ip := service.AddressForPort(mgmtPort, "tcp", force); ip != "" {
			username := service.Config.Username
			if username == "" {
				username = "guest"
			}

			password := service.Config.Password
			if password == "" {
				password = "guest"
			}

			url := "http://" + net.JoinHostPort(ip, strconv.Itoa(mgmtPort))
			input, err = rabbitmq.New(url, username, password)
		}
	case RedisService:
		if ip, port := service.AddressPort(); ip != "" {
			input, err = redis.New("tcp://"+net.JoinHostPort(ip, strconv.Itoa(port)), service.Config.Password)
		}
	case UPSDService:
		if ip, port := service.AddressPort(); ip != "" {
			input, gathererOptions, err = upsd.New(ip, port, service.Config.Username, service.Config.Password)
		}
	case UWSGIService:
		// The port used in the stats server documentation is 1717.
		port := 1717

		if service.Config.StatsPort != 0 {
			port = service.Config.StatsPort
		}

		// The stats server can be exposed with TCP or HTTP
		// (or a socket but we don't support it).
		protocol := "tcp"
		if service.Config.StatsProtocol != "" {
			protocol = service.Config.StatsProtocol
		}

		if ip := service.AddressForPort(port, "tcp", true); ip != "" {
			url := fmt.Sprintf("%s://%s", protocol, net.JoinHostPort(ip, strconv.Itoa(port)))
			input, gathererOptions, err = uwsgi.New(url)
		}
	case ZookeeperService:
		if ip, port := service.AddressPort(); ip != "" {
			input, err = zookeeper.New(fmt.Sprintf("%s:%d", ip, port))
		}
	case CustomService:
		return nil
	default:
		logger.V(1).Printf("service type %s don't support metrics", service.ServiceType)
	}

	if err != nil {
		return err
	}

	if input == nil {
		return nil
	}

	logger.V(2).Printf("Add input for service %v instance %s", service.Name, service.Instance)

	return d.registerInput(input, gathererOptions, service)
}

func createMySQLInput(service Service) (telegraf.Input, error) {
	if unixSocket := getMySQLSocket(service); unixSocket != "" && service.Config.Password != "" {
		username := service.Config.Username
		if username == "" {
			username = "root"
		}

		return mysql.New(fmt.Sprintf("%s:%s@unix(%s)/", username, service.Config.Password, unixSocket))
	}

	if ip, port := service.AddressPort(); ip != "" && service.Config.Password != "" {
		username := service.Config.Username
		if username == "" {
			username = "root"
		}

		return mysql.New(fmt.Sprintf("%s:%s@tcp(%s:%d)/", username, service.Config.Password, ip, port))
	}

	return nil, nil //nolint: nilnil
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
		extraLabels[types.LabelItem] = service.Instance
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

	key := NameInstance{
		Name:     service.Name,
		Instance: service.Instance,
	}
	d.activeCollector[key] = collectorDetails{
		gathererID: gathererID,
	}

	return err
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

func getMySQLSocket(service Service) string {
	socket := service.Config.MetricsUnixSocket

	if socket == "" {
		return ""
	}

	if _, err := os.Stat(socket); err != nil {
		return ""
	}

	return socket
}
