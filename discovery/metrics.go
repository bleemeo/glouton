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
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
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
	"github.com/bleemeo/glouton/inputs/postfix"
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
	// postfixSpoolDirectory is where Postfix keeps its queues.
	postfixSpoolDirectory = "/var/spool/postfix"
	// chronySocket is the control socket chronyd listens on, and the one telegraf's chrony
	// plugin tries first. It is used to recognize a chrony host, see isChronyDaemon.
	chronySocket = "/run/chrony/chronyd.sock"
)

// postfixQueues are the queues telegraf's postfix input reports on, all of which it
// needs to read.
//
//nolint:gochecknoglobals
var postfixQueues = []string{"active", "hold", "incoming", "maildrop", "deferred"}

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
		// "/debug/vars" only exists on InfluxDB 1.x. InfluxDB 2.x exposes its metrics in
		// the Prometheus format on "/metrics", which this input can't read.
		if service.Config.StatsURL != "" {
			input, err = influxdb.New(service.Config.StatsURL, service.Config.Username, service.Config.Password)
		} else if ip, port := service.AddressPort(); ip != "" {
			url := "http://" + net.JoinHostPort(ip, strconv.Itoa(port)) + "/debug/vars"
			input, err = influxdb.New(url, service.Config.Username, service.Config.Password)
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
	case NTPService:
		// Pick the telegraf plugin matching whichever NTP daemon was actually
		// detected: chrony has its own control-socket/UDP protocol, distinct
		// from the ntpd one queried through the ntpq CLI tool.
		//
		// Both read the daemon of the machine Glouton runs on, so an NTP service in
		// another container is reported with the metrics of the local daemon, or fails
		// to gather when there is none. Same same-host requirement as Varnish.
		if isChronyDaemon(service, chronySocket) {
			input, err = chrony.New()
		} else {
			input, err = ntp.New()
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
	case PostfixService:
		// This only adds the per-queue metrics: the total number of mails waiting
		// (postfix_queue_size) is gathered on its own from "postqueue -p", which works
		// without any extra permission (see agent.postfixQueueSize).
		//
		// The input walks the spool directory of the machine Glouton runs on, the same
		// same-host requirement as Varnish and NTP: a Postfix in another container is
		// reported with the queues of the local one. The probe is what decides whether
		// there is anything to read at all -- on a machine with no Postfix of its own,
		// there is no spool directory and no input is created.
		if postfixQueuesReadable(postfixSpoolDirectory) {
			input, err = postfix.New(postfixSpoolDirectory)
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
		// The input runs "sudo varnishstat" on the machine Glouton runs on, so it reads
		// the shared memory of the local Varnish whatever service this is: a Varnish in
		// another container is reported with the numbers of the local one, or fails to
		// gather when there is none.
		input, gathererOptions, err = varnish.New()
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

// postfixQueuesReadable tells whether every Postfix queue can be read in the given
// spool directory.
//
// The queues are only readable by the postfix user on a default install, while Glouton
// runs as its own user: read access has to be granted first (see the permissions
// section of telegraf's postfix plugin doc), otherwise every gather would only report
// errors. Like the unix socket of getMetricsSocket, this is checked once when the
// input is created, so granting the access later is only picked up when the service
// changes or when Glouton restarts.
func postfixQueuesReadable(spoolDirectory string) bool {
	for _, queue := range postfixQueues {
		f, err := os.Open(filepath.Join(spoolDirectory, queue))
		if err != nil {
			// Logged because the two reasons to end up here look the same from the outside
			// -- no Postfix on this machine, or a spool Glouton isn't allowed to read -- and
			// only one of them is worth doing something about.
			logger.V(1).Printf(
				"Not gathering the Postfix queues: %v. Read access has to be granted to the "+
					"user running Glouton, e.g. setfacl -Rm g:glouton:rX %s",
				err, spoolDirectory,
			)

			return false
		}

		// Only opening the queue matters here, so closing it can't fail in a way we care about.
		_ = f.Close()
	}

	return true
}

// isChronyDaemon tells whether the NTP service found is chronyd rather than ntpd, the
// two being queried with a different telegraf plugin.
//
// The executable path is what tells them apart, but it isn't always known: a service
// declared by the user has none, and neither has a process Glouton couldn't read the
// details of (/proc/<pid>/exe of a root-owned process isn't readable by the glouton user).
// The control socket chronyd listens on is then looked for, the same way getMetricsSocket
// looks for Dovecot's: finding it is what a chrony host looks like, and the alternative is
// running ntpq against a chronyd that doesn't speak its protocol -- and against a host that
// may not even have ntpq installed.
//
// Being denied the socket counts as finding it. Its directory is only reachable by the
// chrony user on a default install (/run/chrony is drwxr-x--- _chrony:_chrony on Debian), so
// the glouton user gets a permission error there -- while a host running no chrony has no
// such directory at all and gives a not-found one. Telegraf's plugin doesn't need to read
// the socket either: it falls back to chronyd's UDP command port on localhost, which is open
// by default.
func isChronyDaemon(service Service, socketPath string) bool {
	if exePath := service.ExePath; exePath != "" {
		return filepath.Base(exePath) == "chronyd"
	}

	_, err := os.Stat(socketPath)

	return err == nil || errors.Is(err, fs.ErrPermission)
}
