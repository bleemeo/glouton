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

package discovery

import (
	"errors"
	"fmt"
	"glouton/collector"
	"glouton/facts"
	"glouton/facts/container-runtime/veth"
	"glouton/inputs"
	"glouton/inputs/apache"
	"glouton/inputs/cpu"
	"glouton/inputs/disk"
	"glouton/inputs/diskio"
	"glouton/inputs/elasticsearch"
	"glouton/inputs/fail2ban"
	"glouton/inputs/haproxy"
	"glouton/inputs/mem"
	"glouton/inputs/memcached"
	"glouton/inputs/modify"
	"glouton/inputs/mongodb"
	"glouton/inputs/mysql"
	"glouton/inputs/nats"
	netInput "glouton/inputs/net"
	"glouton/inputs/nginx"
	"glouton/inputs/openldap"
	"glouton/inputs/phpfpm"
	"glouton/inputs/postgresql"
	"glouton/inputs/rabbitmq"
	"glouton/inputs/redis"
	"glouton/inputs/swap"
	"glouton/inputs/system"
	"glouton/inputs/uwsgi"
	"glouton/inputs/winperfcounters"
	"glouton/inputs/zookeeper"
	"glouton/logger"
	"glouton/prometheus/registry"
	"glouton/types"
	"net"
	"os"
	"reflect"
	"runtime"
	"strconv"

	"github.com/influxdata/telegraf"
)

var errNotSupported = errors.New("service not supported by Prometheus collector")

// AddDefaultInputs adds system inputs to a collector.
func AddDefaultInputs(
	coll *collector.Collector,
	inputsConfig inputs.CollectorConfig,
	vethProvider *veth.Provider,
) error {
	input, err := system.New()
	if err != nil {
		return err
	}

	if _, err = coll.AddInput(input, "system"); err != nil {
		return err
	}

	input, err = cpu.New()
	if err != nil {
		return err
	}

	if _, err = coll.AddInput(input, "cpu"); err != nil {
		return err
	}

	input, err = netInput.New(inputsConfig.NetIfBlacklist, vethProvider)
	if err != nil {
		return err
	}

	if _, err = coll.AddInput(input, "net"); err != nil {
		return err
	}

	if inputsConfig.DFRootPath != "" {
		input, err = disk.New(inputsConfig.DFRootPath, inputsConfig.DFPathBlacklist)
		if err != nil {
			return err
		}

		if _, err = coll.AddInput(input, "disk"); err != nil {
			return err
		}
	}

	input, err = diskio.New(inputsConfig.IODiskWhitelist, inputsConfig.IODiskBlacklist)
	if err != nil {
		return err
	}

	if _, err = coll.AddInput(input, "diskio"); err != nil {
		return err
	}

	return addDefaultFromOS(inputsConfig, coll)
}

func addDefaultFromOS(inputsConfig inputs.CollectorConfig, coll *collector.Collector) error {
	var input telegraf.Input

	var err error

	switch runtime.GOOS {
	case "windows":
		input, err = winperfcounters.New(inputsConfig)
		if err != nil {
			return err
		}

		_, err = coll.AddInput(input, "win_perf_counters")
		if err != nil {
			return err
		}
	default:
		// on windows, win_perf_counters provides the metrics for the memory
		input, err = mem.New()
		if err != nil {
			return err
		}

		if _, err = coll.AddInput(input, "mem"); err != nil {
			return err
		}

		input, err = swap.New()
		if err != nil {
			return err
		}

		if _, err = coll.AddInput(input, "swap"); err != nil {
			return err
		}
	}

	return nil
}

func (d *Discovery) configureMetricInputs(oldServices, services map[NameInstance]Service) (err error) {
	for key := range oldServices {
		if _, ok := services[key]; !ok {
			d.removeInput(key)
		}
	}

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
				err = d.createInput(service)
				if err != nil {
					return
				}
			}
		}
	}

	return nil
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
		oldService.Stack != service.Stack,
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
		new := service.ListenAddresses[i] //nolint:predeclared
		if old.Network() != new.Network() || old.String() != new.String() {
			return true
		}
	}

	return false
}

func (d *Discovery) removeInput(key NameInstance) {
	if d.coll == nil {
		return
	}

	if collector, ok := d.activeCollector[key]; ok {
		logger.V(2).Printf("Remove input for service %v on instance %s", key.Name, key.Instance)
		delete(d.activeCollector, key)

		if collector.gathererID == 0 {
			d.coll.RemoveInput(collector.inputID)
		} else if !d.metricRegistry.Unregister(collector.gathererID) {
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
		gathererOptions *inputs.GathererOptions
	)

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
			input, err = elasticsearch.New(fmt.Sprintf("http://%s", net.JoinHostPort(ip, strconv.Itoa(port))))
		}
	case Fail2banService:
		input, gathererOptions, err = fail2ban.New()
	case HAProxyService:
		if service.Config.StatsURL != "" {
			input, err = haproxy.New(service.Config.StatsURL)
		}
	case MemcachedService:
		if ip, port := service.AddressPort(); ip != "" {
			input, err = memcached.New(fmt.Sprintf("%s:%d", ip, port))
		}
	case MongoDBService:
		if ip, port := service.AddressPort(); ip != "" {
			input, err = mongodb.New(fmt.Sprintf("mongodb://%s", net.JoinHostPort(ip, strconv.Itoa(port))))
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
			url := fmt.Sprintf("http://%s", net.JoinHostPort(service.IPAddress, strconv.Itoa(port)))
			input, gathererOptions, err = nats.New(url)
		}
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

			url := fmt.Sprintf("http://%s", net.JoinHostPort(ip, strconv.Itoa(mgmtPort)))
			input, err = rabbitmq.New(url, username, password)
		}
	case RedisService:
		if ip, port := service.AddressPort(); ip != "" {
			input, err = redis.New(fmt.Sprintf("tcp://%s", net.JoinHostPort(ip, strconv.Itoa(port))), service.Config.Password)
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

	// If gatherer options are used, use an input gatherer instead of the default collector.
	if gathererOptions != nil {
		return d.registerInput(input, gathererOptions, service)
	}

	return d.addToCollector(input, service)
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

	return nil, nil
}

// addToCollector is deprecated, use registerInput instead.
func (d *Discovery) addToCollector(input telegraf.Input, service Service) error {
	if d.coll == nil {
		return nil
	}

	input = modify.AddRenameCallback(input, func(labels map[string]string, annotations types.MetricAnnotations) (map[string]string, types.MetricAnnotations) {
		annotations.ServiceName = service.Name
		annotations.ServiceInstance = service.Instance
		annotations.ContainerID = service.ContainerID

		_, port := service.AddressPort()
		if port != 0 {
			labels[types.LabelMetaServicePort] = strconv.FormatInt(int64(port), 10)
		}

		if service.Instance != "" {
			if annotations.BleemeoItem != "" {
				annotations.BleemeoItem = service.Instance + "_" + annotations.BleemeoItem
			} else {
				annotations.BleemeoItem = service.Instance
			}
		}

		if annotations.BleemeoItem != "" {
			labels[types.LabelItem] = annotations.BleemeoItem
		}

		return labels, annotations
	})

	inputID, err := d.coll.AddInput(input, service.Name)
	if err != nil {
		return err
	}

	key := NameInstance{
		Name:     service.Name,
		Instance: service.Instance,
	}
	d.activeCollector[key] = collectorDetails{
		inputID: inputID,
	}

	return nil
}

func (d *Discovery) registerInput(input telegraf.Input, opts *inputs.GathererOptions, service Service) error {
	extraLabels := map[string]string{
		types.LabelMetaServiceName:     service.Name,
		types.LabelMetaServiceInstance: service.Instance,
		types.LabelMetaContainerID:     service.ContainerID,
	}

	if _, port := service.AddressPort(); port != 0 {
		extraLabels[types.LabelMetaServicePort] = strconv.Itoa(port)
	}

	if service.Instance != "" {
		extraLabels[types.LabelItem] = service.Instance
	}

	_, err := d.metricRegistry.RegisterInput(
		registry.RegistrationOption{
			Description: fmt.Sprintf("Service input %s %s", service.Name, service.Instance),
			JitterSeed:  0,
			Rules:       opts.Rules,
			MinInterval: opts.MinInterval,
			ExtraLabels: extraLabels,
		},
		input,
	)

	return err
}

func urlForPHPFPM(service Service) string {
	url := service.Config.StatsURL
	if url != "" {
		return url
	}

	if service.Config.Port != 0 && service.IPAddress != "" {
		return fmt.Sprintf("fcgi://%s/status", net.JoinHostPort(service.IPAddress, fmt.Sprint(service.Config.Port)))
	}

	for _, v := range service.ListenAddresses {
		if v.Network() != tcpPortocol {
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
