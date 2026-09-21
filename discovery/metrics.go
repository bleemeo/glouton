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
	"github.com/bleemeo/glouton/inputs/activemq"
	"github.com/bleemeo/glouton/inputs/apache"
	"github.com/bleemeo/glouton/inputs/bind"
	"github.com/bleemeo/glouton/inputs/chrony"
	"github.com/bleemeo/glouton/inputs/clickhouse"
	"github.com/bleemeo/glouton/inputs/consul"
	"github.com/bleemeo/glouton/inputs/cpu"
	"github.com/bleemeo/glouton/inputs/disk"
	"github.com/bleemeo/glouton/inputs/diskio"
	"github.com/bleemeo/glouton/inputs/dovecot"
	"github.com/bleemeo/glouton/inputs/elasticsearch"
	"github.com/bleemeo/glouton/inputs/fail2ban"
	"github.com/bleemeo/glouton/inputs/haproxy"
	"github.com/bleemeo/glouton/inputs/influxdb"
	"github.com/bleemeo/glouton/inputs/jenkins"
	"github.com/bleemeo/glouton/inputs/mem"
	"github.com/bleemeo/glouton/inputs/memcached"
	"github.com/bleemeo/glouton/inputs/modify"
	"github.com/bleemeo/glouton/inputs/mongodb"
	"github.com/bleemeo/glouton/inputs/mysql"
	"github.com/bleemeo/glouton/inputs/nats"
	netInput "github.com/bleemeo/glouton/inputs/net"
	"github.com/bleemeo/glouton/inputs/nfs"
	"github.com/bleemeo/glouton/inputs/nginx"
	"github.com/bleemeo/glouton/inputs/nsq"
	"github.com/bleemeo/glouton/inputs/ntp"
	"github.com/bleemeo/glouton/inputs/openbao"
	"github.com/bleemeo/glouton/inputs/openldap"
	"github.com/bleemeo/glouton/inputs/pgbouncer"
	"github.com/bleemeo/glouton/inputs/phpfpm"
	"github.com/bleemeo/glouton/inputs/postgresql"
	"github.com/bleemeo/glouton/inputs/rabbitmq"
	"github.com/bleemeo/glouton/inputs/redis"
	"github.com/bleemeo/glouton/inputs/swap"
	"github.com/bleemeo/glouton/inputs/system"
	"github.com/bleemeo/glouton/inputs/tomcat"
	"github.com/bleemeo/glouton/inputs/upsd"
	"github.com/bleemeo/glouton/inputs/uwsgi"
	"github.com/bleemeo/glouton/inputs/varnish"
	"github.com/bleemeo/glouton/inputs/vault"
	"github.com/bleemeo/glouton/inputs/winperfcounters"
	"github.com/bleemeo/glouton/inputs/zookeeper"
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

			if serviceState != facts.ContainerStopped {
				err.Append(d.createInput(service))
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
	if collector, ok := d.activeCollector[key]; ok {
		logger.V(2).Printf("Remove input for service %v on instance %s", key.Name, key.Instance)
		delete(d.activeCollector, key)

		collector.gathererRegistration.Unregister()
	}
}

func (d *Discovery) createInput(service Service) error { //nolint:maintidx
	if !service.Active {
		return nil
	}

	if service.MetricsIgnored {
		logger.V(2).Printf("The input associated to the service '%s' on container '%s' is ignored by the configuration", service.Name, service.ContainerID)

		return nil
	}

	var (
		input telegraf.Input
		err   error
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
	case ActiveMQService:
		if url, username, password := activeMQURL(service); url != "" {
			// One point per queue, topic and subscriber, all sharing their metric name, so
			// what identifies a destination has to be a label of its own -- the
			// compatibility naming keeps only the item and would collapse them onto one
			// series. The item stays the service instance instead of being glued to a
			// destination name; see the renameGlobal of inputs/activemq.
			gathererOptions.CompatibilityNameItem = false

			input, err = activemq.New(url, username, password)
		}
	case ApacheService:
		if ip, port := service.AddressPort(); ip != "" {
			input, err = apache.New(apacheStatusURL(ip, port))
		}
	case BindService:
		if url := bindStatsURL(service); url != "" {
			input, err = bind.New(url)
		}
	case ClickHouseService:
		if service.Config.StatsURL != "" {
			if service.Config.Username == "" {
				service.Config.Username = "default"
			}

			input, err = clickhouse.New(service.Config.StatsURL, service.Config.Username, service.Config.Password)
		} else if ip, port := clickHouseAddress(service); ip != "" {
			if service.Config.Username == "" {
				service.Config.Username = "default"
			}

			url := "http://" + net.JoinHostPort(ip, strconv.Itoa(port))
			input, err = clickhouse.New(url, service.Config.Username, service.Config.Password)
		}
	case ConsulService:
		// Some Consul metrics come as one series per label set, so those labels have to be
		// kept rather than reduced to the item -- see the renameGlobal of inputs/consul for
		// which ones and why they are safe to keep.
		gathererOptions.CompatibilityNameItem = false

		if service.Config.StatsURL != "" {
			input, err = consul.New(service.Config.StatsURL, service.Config.Password)
		} else if ip, port := service.AddressPort(); ip != "" {
			url := "http://" + net.JoinHostPort(ip, strconv.Itoa(port))
			input, err = consul.New(url, service.Config.Password)
		}
	case DovecotService:
		if server := dovecotStatsServer(service); server != "" {
			input, err = dovecot.New(server)
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
	case InfluxDBService:
		// The server's root rather than an endpoint: the three lines publish their metrics
		// in different places -- 1.x as JSON on "/debug/vars", 2.x and 3.x as Prometheus
		// text on "/metrics" -- and which one to read is decided from the version the
		// server reports. See inputs/influxdb.
		//
		// The token is used by 3.x, which answers 401 everywhere without one; the user and
		// password by 1.x.
		if service.Config.StatsURL != "" {
			input, gathererOptions, err = influxdb.New(
				service.Config.StatsURL, service.Config.Username, service.Config.Password, service.Config.Password,
			)
		} else if ip, port := service.AddressPort(); ip != "" {
			url := "http://" + net.JoinHostPort(ip, strconv.Itoa(port))
			// The password is offered as both: 1.x authenticates with a user and a
			// password, 3.x with a bearer token, and there is one field for either. A
			// server only ever reads the one its line uses.
			input, gathererOptions, err = influxdb.New(
				url, service.Config.Username, service.Config.Password, service.Config.Password,
			)
		}
	case JenkinsService:
		if service.Config.StatsURL != "" && service.Config.Password != "" {
			input, gathererOptions, err = jenkins.New(service.Config)
		}
	case MariaDBService:
		input, err = createMariaDBInput(service)
	case MemcachedService:
		if ip, port := service.AddressPort(); ip != "" {
			input, err = memcached.New(net.JoinHostPort(ip, strconv.Itoa(port)))
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

		if ip := service.AddressForPort(port, tcpProtocol, true); ip != "" {
			url := "http://" + net.JoinHostPort(service.IPAddress, strconv.Itoa(port))
			input, gathererOptions, err = nats.New(url)
		}
	case NfsService:
		input, gathererOptions, err = nfs.New()
	case NginxService:
		if ip, port := service.AddressPort(); ip != "" {
			input, err = nginx.New(fmt.Sprintf("http://%s/nginx_status", net.JoinHostPort(ip, strconv.Itoa(port))))
		}
	case NSQService:
		if service.Config.StatsURL != "" {
			input, err = nsq.New(service.Config.StatsURL)
		} else if ip, port := service.AddressPort(); ip != "" {
			url := "http://" + net.JoinHostPort(ip, strconv.Itoa(port))
			input, err = nsq.New(url)
		}
	case ChronyService:
		if address, ok := chronyCmdAddress(service); ok {
			input, gathererOptions, err = chrony.New(address)
		} else {
			logger.V(1).Printf(
				"No address to read the chrony daemon of service '%s' on container '%s', not gathering its metrics",
				service.Name, service.ContainerName,
			)
		}
	case NTPService:
		if address, ok := ntpdAddress(service); ok {
			input, gathererOptions, err = ntp.New(address)
		} else {
			logger.V(1).Printf(
				"No address to read the NTP daemon of service '%s' on container '%s', not gathering its metrics",
				service.Name, service.ContainerName,
			)
		}
	case OpenBaoService:
		if service.Config.StatsURL != "" {
			input, err = openbao.New(service.Config.StatsURL, service.Config.Password)
		} else if ip, port := service.AddressPort(); ip != "" {
			url := "http://" + net.JoinHostPort(ip, strconv.Itoa(port))
			input, err = openbao.New(url, service.Config.Password)
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
	case PgBouncerService:
		if ip, port := service.AddressPort(); ip != "" {
			username := service.Config.Username
			if username == "" {
				username = "pgbouncer"
			}

			address := fmt.Sprintf(
				"host=%s port=%d user=%s password=%s dbname=pgbouncer sslmode=disable",
				ip, port, username, service.Config.Password,
			)
			gathererOptions.CompatibilityNameItem = false

			input, err = pgbouncer.New(address)
		}
	case RabbitMQService:
		mgmtPort := 15672
		force := false

		if service.Config.StatsPort != 0 {
			mgmtPort = service.Config.StatsPort
			force = true
		}

		if ip := service.AddressForPort(mgmtPort, tcpProtocol, force); ip != "" {
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
	case RedisService, ValkeyService:
		if ip, port := service.AddressPort(); ip != "" {
			input, err = redis.New("tcp://"+net.JoinHostPort(ip, strconv.Itoa(port)), service.Config.Password)
		}
	case TomcatService:
		// The manager webapp the metrics are read from requires a user with the
		// "manager-status" role, so without credentials every gather would only get a
		// 401 (or a 404 when the webapp isn't even deployed).
		//
		// Both credentials are required, not just the password: the plugin always sends
		// basic auth, and Tomcat ships an empty tomcat-users.xml, so unlike ActiveMQ there
		// is no factory account to fall back on -- a password alone could only ever 401.
		hasCredentials := service.Config.Username != "" && service.Config.Password != ""

		// One point per connector and per memory pool, all sharing their metric name, so
		// the "name" telling them apart has to be a label of its own rather than the item,
		// which is the service instance; see the renameGlobal of inputs/tomcat.
		gathererOptions.CompatibilityNameItem = false

		if service.Config.StatsURL != "" && hasCredentials {
			input, err = tomcat.New(service.Config.StatsURL, service.Config.Username, service.Config.Password)
		} else if ip, port := service.AddressPort(); ip != "" && hasCredentials {
			url := fmt.Sprintf("http://%s/manager/status/all?XML=true", net.JoinHostPort(ip, strconv.Itoa(port)))
			input, err = tomcat.New(url, service.Config.Username, service.Config.Password)
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
		protocol := tcpProtocol
		if service.Config.StatsProtocol != "" {
			protocol = service.Config.StatsProtocol
		}

		if ip := service.AddressForPort(port, tcpProtocol, true); ip != "" {
			url := fmt.Sprintf("%s://%s", protocol, net.JoinHostPort(ip, strconv.Itoa(port)))
			input, gathererOptions, err = uwsgi.New(url)
		}
	case VarnishService:
		// The input runs "varnishstat" rather than reading a port, since that is the only
		// way Varnish publishes its counters: they live in a shared memory segment the
		// daemon maps, with no socket in front of them. The agent image carries no
		// varnishstat, so it comes from wherever the daemon is -- the machine for a
		// Varnish installed on it, the container's own image for a containerised one.
		//
		// Not being able to read it means no input, rather than an input failing on every
		// gather under that container's name.
		if d.canReadVarnish(service) {
			input, gathererOptions, err = varnish.New(d.commandRunner, d.containerInfo, service.ContainerID)
		}
	case VaultService:
		if service.Config.StatsURL != "" {
			input, err = vault.New(service.Config.StatsURL, service.Config.Password)
		} else if ip, port := service.AddressPort(); ip != "" {
			url := "http://" + net.JoinHostPort(ip, strconv.Itoa(port))
			input, err = vault.New(url, service.Config.Password)
		}
	case ZookeeperService:
		if ip, port := service.AddressPort(); ip != "" {
			input, err = zookeeper.New(net.JoinHostPort(ip, strconv.Itoa(port)))
		}
	case CustomService:
		return nil
	default:
		logger.V(1).Printf("service type %s don't support metrics", service.ServiceType)
	}

	// An input that says it is disabled isn't a failure to report on every discovery run:
	// it is a plugin that doesn't exist on this platform (the Windows stubs of the inputs
	// reading a local daemon, like inputs/varnish) or one Telegraf wasn't built with. There
	// is simply no input for that service here.
	if errors.Is(err, inputs.ErrDisabledInput) {
		logger.V(1).Printf("No input for service %s on this platform: %v", service.Name, err)

		return nil
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

	return nil, nil //nolint: nilnil
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
// credentials to read it with, or an empty URL when no input should be created.
//
// The console always requires authentication (admin/admin on a default install), so without
// credentials every gather would only get a 401.
func activeMQURL(service Service) (url string, username string, password string) {
	if service.Config.Password == "" {
		return "", "", ""
	}

	username = service.Config.Username
	if username == "" {
		username = activeMQDefaultUser
	}

	if service.Config.StatsURL != "" {
		return service.Config.StatsURL, username, service.Config.Password
	}

	if ip, port := service.AddressPort(); ip != "" {
		return "http://" + net.JoinHostPort(ip, strconv.Itoa(port)), username, service.Config.Password
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
