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
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"slices"
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
	// varnishStateDirectory is where varnishd keeps one working directory per instance,
	// the directory varnishstat's "-n" names.
	varnishStateDirectory = "/var/lib/varnish"
	// varnishPIDFile is the file varnishd writes in its working directory. Its presence is
	// what tells a live instance from a leftover directory, and from the state directory
	// holding them: the official Varnish image ships an empty directory named after the
	// host that built it, so they cannot just be taken in order.
	varnishPIDFile = "_.pid"
	// varnishInstanceDirTimeout bounds looking for that directory. It is short because
	// nothing is waiting on it: failing to find the instance only means no input for this
	// Varnish, the same as before it was looked for at all.
	varnishInstanceDirTimeout = 10 * time.Second
	// varnishStatBinary is where telegraf's varnish plugin runs varnishstat from, and so
	// the only place a container can carry one this input is able to use.
	varnishStatBinary = "/usr/bin/varnishstat"
	// chronySocket is the control socket chronyd listens on, and the one telegraf's chrony
	// plugin tries first. It is used to recognize a chrony host, see isChronyDaemon.
	chronySocket = "/run/chrony/chronyd.sock"
	// chronyDefaultCmdPort is the UDP port of chronyd's command protocol, distinct from
	// NTPService's own ServicePort (123, the NTP protocol itself, used to detect an NTP
	// service in the first place) -- see chronyCmdAddress.
	chronyDefaultCmdPort = 323
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
	case NTPService:
		// Pick the input matching whichever NTP daemon was actually detected: the two
		// answer different protocols on different ports, chrony its own command
		// protocol on 323 and ntpd the NTP control protocol (mode 6) on the NTP port.
		//
		// Both are plain UDP with no local command involved, so a daemon in another
		// container is genuinely reachable, unlike Varnish -- see chronyCmdAddress and
		// ntpdAddress for which address is used, and why it isn't always the one
		// discovery found the service at.
		// Both inputs bring their own registration options: their per-source metrics need
		// the source in a label of its own, which the default compatibility naming would
		// drop, and they read the daemon less often than the default 10 s.
		chronyDaemon := isChronyDaemon(service, chronySocket)

		address, addressKnown := ntpdAddress(service)
		if chronyDaemon {
			address, addressKnown = chronyCmdAddress(service)
		}

		switch {
		case !addressKnown:
			// No input rather than one reading the wrong daemon: given no address both
			// inputs fall back to Glouton's own loopback, which for a service that runs
			// elsewhere means publishing the numbers of whichever daemon happens to sit
			// next to Glouton under this service's name. The check makes the same call.
			logger.V(1).Printf(
				"No address to read the NTP daemon of service '%s' on container '%s', not gathering its metrics",
				service.Name, service.ContainerName,
			)
		case chronyDaemon:
			input, gathererOptions, err = chrony.New(address)
		default:
			input, gathererOptions, err = ntp.New(address)
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
		// (postfix_queue_size) is gathered on its own from "postqueue -p", which already
		// reaches a container through the runtime's exec (see agent.postfixQueueSize).
		//
		// The input walks a spool directory instead of running a command, so unlike
		// Varnish there is no namespace to enter -- see postfixSpool for how a
		// containerised Postfix is read. The probe there is what decides whether there is
		// anything to read at all: a machine with no Postfix of its own has no spool
		// directory, and no input is created.
		if spoolDirectory, ok := postfixSpool(service); ok {
			// One point per queue, all sharing their metric name, so the queue has to be a
			// label of its own: the compatibility naming keeps only the item, which would
			// collapse the five queues onto one series. The item stays the service
			// instance -- for a container its name, set by modify.AddInstance -- instead
			// of being glued to the queue.
			gathererOptions.CompatibilityNameItem = false

			input, err = postfix.New(spoolDirectory)
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
		// The input runs "varnishstat" through the command runner, since the agent image
		// carries none. varnishTarget decides whose binary that is and which instance it
		// reads -- see it for why a containerised Varnish needs both answered.
		//
		// Not being able to read it means no input, rather than an input reporting the
		// wrong Varnish: that is what a gather aimed at the machine's namespace would do
		// for a containerised service, and it would do it under the container's name.
		if containerPID, instanceDir, ok := d.varnishTarget(service); ok {
			input, gathererOptions, err = varnish.New(d.commandRunner, containerPID, instanceDir)
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

// postfixSpool returns the Postfix spool directory to walk for a service, and whether
// every queue in it can actually be read.
//
// A containerised Postfix is read through its own spool, named with /proc. The input opens
// files rather than running a command, so unlike Varnish nothing has to be executed in the
// container's namespace -- the path only has to be one Glouton can open itself, which
// /proc/<pid>/root is given the host PID namespace the agent already runs with.
//
// Anything else keeps the machine's own spool, which is all this could mean before. Note
// that for a Glouton in a container that is the agent's own filesystem, which holds no
// Postfix: reading the *host's* queues from a containerised agent would need its spool
// mounted into the agent, and is not something this can do.
func postfixSpool(service Service) (string, bool) {
	directory, ok := postfixSpoolPath(service)
	if !ok {
		// A container between states has no PID, so there is no /proc entry to walk. The
		// next discovery run sees it again.
		logger.V(1).Printf(
			"Not gathering the Postfix queues of %s: its runtime reports no PID for the container",
			service.Instance,
		)

		return "", false
	}

	if err := postfixQueuesUnreadable(directory); err != nil {
		// Logged because the reasons to end up here look the same from the outside -- no
		// Postfix there, or a spool Glouton isn't allowed to read -- and only some of them
		// are worth doing something about.
		//
		// The setfacl hint is only given for a Postfix on the machine, where the queues
		// are the ones its own documentation is about. It is not that a container needs no
		// permission: this is a plain filesystem read by whatever user Glouton runs as,
		// and reading /proc/<pid>/root of another user's process needs privilege of its
		// own -- see the note on packaged installs in postfixSpoolPath.
		if service.container == nil {
			logger.V(1).Printf(
				"Not gathering the Postfix queues: %v. Read access has to be granted to the "+
					"user running Glouton, e.g. setfacl -Rm g:glouton:rX %s",
				err, directory,
			)
		} else {
			logger.V(1).Printf("Not gathering the Postfix queues of %s: %v", service.Instance, err)
		}

		return "", false
	}

	return directory, true
}

// postfixSpoolPath names the spool directory of a service without reading anything, and
// says whether it could be named at all -- which only fails for a container the runtime
// reports no PID for.
//
// Two things the container form depends on, both deliberately left as they are because the
// input walks that directory itself with no way to route the read through anything:
//   - the host's PID namespace, since the path is not prefixed with the hostroot. Without
//     it a containerised Glouton reads its own /proc and finds no Postfix, which is what
//     it did before containers were handled at all.
//   - enough privilege to read /proc/<pid>/root of the container's process. A Glouton
//     running as root has it; a packaged one running as the glouton user does not, so
//     there the per-queue metrics stay unavailable for a containerised Postfix.
func postfixSpoolPath(service Service) (string, bool) {
	if service.container == nil {
		return postfixSpoolDirectory, true
	}

	pid := service.container.PID()
	if pid == 0 {
		return "", false
	}

	return containerRootPath(pid, postfixSpoolDirectory), true
}

// postfixQueuesUnreadable returns the error of the first queue that cannot be read in the
// given spool directory, or nil when all of them can.
//
// The queues are only readable by the postfix user on a default install, while Glouton
// runs as its own user: read access has to be granted first (see the permissions
// section of telegraf's postfix plugin doc), otherwise every gather would only report
// errors. Like the unix socket of getMetricsSocket, this is checked once when the
// input is created, so granting the access later is only picked up when the service
// changes or when Glouton restarts.
func postfixQueuesUnreadable(spoolDirectory string) error {
	for _, queue := range postfixQueues {
		f, err := os.Open(filepath.Join(spoolDirectory, queue))
		if err != nil {
			return err
		}

		// Only opening the queue matters here, so closing it can't fail in a way we care about.
		_ = f.Close()
	}

	return nil
}

// varnishTarget decides how a Varnish service is read: which container's varnishstat to
// run, which instance to ask it for, and whether it can be read at all.
//
// Preferred is the container's own varnishstat, which needs nothing installed on the
// machine and is always the version matching the daemon -- and inside that container's
// filesystem varnishd's default working directory is the right one, so no "-n" is needed.
// A container without one falls back to the machine's binary aimed at the container's
// working directory through /proc, which is all that can be done when the image carries
// only the daemon.
//
// Both zero with ok=true is a Varnish installed on the machine, read exactly as it was
// before either of these existed.
func (d *Discovery) varnishTarget(service Service) (containerPID int, instanceDir string, ok bool) {
	if service.container == nil {
		return 0, "", true
	}

	pid := service.container.PID()
	if pid == 0 {
		// A container between states has no PID, so there is nothing to look through yet.
		// The next discovery run sees it again.
		logger.V(1).Printf("Not gathering Varnish of %s: its runtime reports no PID for the container", service.Instance)

		return 0, "", false
	}

	// Located before choosing between the two, and passed on both: relying on
	// varnishstat's own default instead would depend on the image happening to leave it
	// alone. An image that starts varnishd with its own "-n" -- pointing at the state
	// directory itself, as some do -- would gather nothing, and finding that out only
	// after picking the container's binary would skip the very fallback meant to cover it.
	inContainer, found := d.varnishInstanceDir(service)
	if !found {
		return 0, "", false
	}

	if d.containerHasVarnishStat(service) {
		// varnishstat runs chrooted into the container, so the directory has to be named
		// from inside it. The /proc form used to find it does not resolve there.
		return pid, inContainer, true
	}

	// The machine's binary stays in its own namespace and reaches the container's
	// directory through /proc.
	return 0, containerRootPath(pid, inContainer), true
}

// containerHasVarnishStat tells whether a container carries the varnishstat this input
// would run. Checked rather than assumed because an image may well ship only varnishd,
// and a chroot into it would then fail every gather with a command not found.
func (d *Discovery) containerHasVarnishStat(service Service) bool {
	if d.fileReader == nil || service.container.PID() == 0 {
		return false
	}

	inContainer := containerRootPath(service.container.PID(), filepath.Dir(varnishStatBinary))

	ctx, cancel := context.WithTimeout(context.Background(), varnishInstanceDirTimeout)
	defer cancel()

	names, err := d.fileReader.ReadDir(ctx, inContainer)
	if err != nil {
		logger.V(2).Printf("Varnish of %s: can't list %s: %v", service.Instance, inContainer, err)

		return false
	}

	return slices.Contains(names, filepath.Base(varnishStatBinary))
}

// containerRootPath names a path inside a container's filesystem as seen from the machine
// the container runs on. Both the fileReader and varnishstat resolve it, each in its own
// mount namespace, so it is built the same way for both.
func containerRootPath(pid int, path string) string {
	return filepath.Join(fmt.Sprintf("/proc/%d/root", pid), path)
}

// varnishInstanceDir returns the working directory of the Varnish instance running in a
// service's container, named as the container itself sees it, and whether one was found.
//
// The caller composes the two forms that path is needed in: as-is for a varnishstat
// chrooted into the container, and through containerRootPath for one staying in the
// machine's namespace. Only the second resolves outside the container, which is also the
// form this looks through.
//
// The directory is recognised by the _.pid varnishd writes in it rather than by its name,
// because the name is not something to rely on: the current default is the fixed
// <state directory>/varnishd, older releases used the host name, and an image is free to
// start varnishd with a "-n" of its own -- including the state directory itself, which is
// why the state directory is a candidate before its subdirectories.
//
// found=false means no instance is running there and no input should be created. That is
// better than gathering the machine's Varnish under the container's name, and the very
// same numbers again for every other Varnish container.
func (d *Discovery) varnishInstanceDir(service Service) (dir string, found bool) {
	if d.fileReader == nil {
		return "", false
	}

	pid := service.container.PID()

	ctx, cancel := context.WithTimeout(context.Background(), varnishInstanceDirTimeout)
	defer cancel()

	holdsInstance := func(candidate string) bool {
		_, err := d.fileReader.ReadFile(ctx, filepath.Join(containerRootPath(pid, candidate), varnishPIDFile))

		return err == nil
	}

	if holdsInstance(varnishStateDirectory) {
		return varnishStateDirectory, true
	}

	base := containerRootPath(pid, varnishStateDirectory)

	names, err := d.fileReader.ReadDir(ctx, base)
	if err != nil {
		logger.V(1).Printf("Not gathering Varnish of %s: can't list %s: %v", service.Instance, base, err)

		return "", false
	}

	for _, name := range names {
		if candidate := filepath.Join(varnishStateDirectory, name); holdsInstance(candidate) {
			return candidate, true
		}
	}

	logger.V(1).Printf(
		"Not gathering Varnish of %s: neither %s nor any of the %d entries in it holds a %s, "+
			"so no instance is running there",
		service.Instance, varnishStateDirectory, len(names), varnishPIDFile,
	)

	return "", false
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
//
// That socket only says something about the daemon running next to Glouton, so it's only
// looked for when the service is that daemon. A service somewhere else with an unknown
// executable (a container Glouton can't read the process details of, or a user-declared
// remote address) gets the ntpd answer it got before this probe existed: a Glouton host
// that happens to run chronyd itself -- the default on RHEL and Ubuntu -- must not turn a
// declared remote ntpd into a chrony one, which would query the chrony command protocol on
// a port ntpd doesn't listen on.
func isChronyDaemon(service Service, socketPath string) bool {
	if exePath := service.ExePath; exePath != "" {
		return filepath.Base(exePath) == "chronyd"
	}

	if serviceRunsElsewhere(service) {
		return false
	}

	_, err := os.Stat(socketPath)

	return err == nil || errors.Is(err, fs.ErrPermission)
}

// serviceRunsElsewhere reports whether the service is known to run somewhere other than
// next to Glouton's own process: in a container of its own, or at an address the user
// declared explicitly and that isn't the local host.
func serviceRunsElsewhere(service Service) bool {
	if service.ContainerID != "" {
		return true
	}

	return service.Config.Address != "" && !isLoopbackAddress(service.Config.Address)
}

// isLoopbackAddress reports whether address (without a port) is a loopback one.
//
// "localhost" counts: it is how most people spell the local host in a config file, and
// taking it for a remote address sends a local daemon down the path meant for one
// somewhere else. No other name is resolved -- that would need DNS, and a name that
// resolves to a loopback address today may not tomorrow.
func isLoopbackAddress(address string) bool {
	switch address {
	case "localhost", "ip6-localhost", "localhost.localdomain":
		return true
	}

	ip := net.ParseIP(address)

	return ip != nil && ip.IsLoopback()
}

// chronyCmdAddress returns the "host:port" to reach chronyd's command protocol on, or ""
// to keep chrony.New()'s own auto-detection (its control socket, then
// udp://127.0.0.1:323). Both of those only reach a chronyd sharing Glouton's network
// namespace, but both also work out of the box, with no chrony.conf change -- unlike any
// other address, which chronyd ignores until bindcmdaddress and cmdallow are set for it
// (the command port is bound to 127.0.0.1 and ::1 only by default). So an address is only
// returned when the local auto-detection cannot be what we want:
//
//   - the daemon runs in a container of its own, so Glouton's loopback isn't the
//     container's, and auto-detection would silently report the numbers of whatever
//     chronyd runs next to Glouton under the container service's name;
//   - the user declared an address and/or a command port explicitly, which is also how a
//     chronyd reachable but not auto-detectable (bindcmdaddress on the host's LAN address,
//     a non-default cmdport) is monitored.
//
// A non-loopback service.IPAddress is deliberately NOT enough on its own: it is derived
// from the NTP port (123) bind address, which says nothing about where the command port
// is. A host chronyd serving NTP on a specific address ("bindaddress 192.168.1.5") has
// that IPAddress while its command port stays on loopback, and pointing the input there
// would break a setup the auto-detection handles.
//
// The container address comes from service.IPAddress rather than
// AddressForPort(chronyDefaultCmdPort, ...): finding a specific port in ListenAddresses
// needs a netstat scan to have actually found it there, which for a container requires
// crossing into its own network namespace -- something gopsutil's connections scan can't
// do (only the host's own namespace is visible, PID visibility from --pid host
// notwithstanding). Lacking that, discovery falls back to a synthetic ListenAddresses
// entry on NTPService's ServicePort (123) -- never chrony's command port, so searching for
// it there would never find it. service.IPAddress doesn't have this problem: it's set from
// the container's own address independently of any netstat result.
// A false ok says the opposite of an empty address: not "the local auto-detection is
// right", but "there is no telling where this daemon is". Both answers are "" today,
// which is why they have to be told apart -- see the return below for what makes them
// different.
func chronyCmdAddress(service Service) (address string, ok bool) {
	address = service.Config.Address
	port := service.Config.StatsPort

	if address == "" && serviceRunsElsewhere(service) {
		address = service.IPAddress

		if address == "" {
			// The daemon is known not to be the one on Glouton's loopback, and nothing
			// says where it is instead: a container whose address the runtime doesn't
			// report (network_mode: none or container:<other>, both of which leave
			// PrimaryAddress() empty). Neither of the two fallbacks below can be right
			// here -- the auto-detection and the loopback substitution both read
			// whatever chronyd runs next to Glouton, and would publish its numbers
			// under this service's name and instance. Saying so is the only honest
			// answer, and it is the one the check already gives.
			return "", false
		}
	}

	if address == "" && port == 0 {
		return "", true
	}

	if address == "" {
		// Only the port was overridden: chrony.New() would go back to the default 323,
		// so the loopback the auto-detection would have used is spelled out here.
		address = localhostIP
	}

	if port == 0 {
		port = chronyDefaultCmdPort
	}

	return net.JoinHostPort(address, strconv.Itoa(port)), true
}

// chronyCheckAddress returns the "host:port" the chrony status check should dial --
// unlike chronyCmdAddress, it always returns a concrete address, including for a chronyd
// left to auto-detection: a check has no local-socket fallback of its own, it just needs
// something to send a packet to, and that is the same loopback command port the input
// ends up on.
//
// It returns "" for the one case chronyCmdAddress has no address for either: the daemon
// is known not to be the one on Glouton's loopback, and nothing says where it is.
// Probing 127.0.0.1 there would report on whatever chronyd runs next to Glouton -- Ok
// while this service is down on a host that runs one, critical while it is healthy on a
// host that doesn't. The check says it couldn't run instead.
func chronyCheckAddress(service Service) string {
	address, ok := chronyCmdAddress(service)
	if !ok {
		return ""
	}

	if address != "" {
		return address
	}

	return net.JoinHostPort(localhostIP, strconv.Itoa(chronyDefaultCmdPort))
}

// ntpdAddress returns the "host:port" to read ntpd's control protocol (NTP mode 6) on,
// or "" to let the input use 127.0.0.1 and the NTP port.
//
// Mode 6 is served on the NTP port itself, so there is no separate port to configure --
// a "port" override moves both. What an address can't change is the daemon's own
// "restrict" policy, which is why one is only returned for a daemon Glouton's loopback
// cannot be, the same rule chronyCmdAddress follows: the usual default is "restrict
// default ... noquery" with only 127.0.0.1 and ::1 unrestricted, so querying a local
// ntpd anywhere but on loopback would be refused where loopback works.
//
// ok has the same meaning as chronyCmdAddress's: false is "there is no telling where this
// daemon is", which an empty address (meaning "the local default is right") cannot say.
func ntpdAddress(service Service) (address string, ok bool) {
	address = service.Config.Address
	port := service.Config.Port

	if address == "" && serviceRunsElsewhere(service) {
		address = service.IPAddress

		if address == "" {
			// Same as chronyCmdAddress: a daemon somewhere else that nothing locates.
			// ntp.New() would read the ntpd on Glouton's own loopback and publish its
			// peers under this service's name.
			return "", false
		}
	}

	if address == "" && port == 0 {
		return "", true
	}

	if address == "" {
		// Only the port was overridden: the input would go back to the default 123, so
		// the loopback it would have used is spelled out here -- the same reason
		// chronyCmdAddress does it, and the same disagreement between check and metrics
		// avoided (the check reads the port from AddressPort, which does honour it).
		address = localhostIP
	}

	if port == 0 {
		port = servicesDiscoveryInfo[NTPService].ServicePort
	}

	return net.JoinHostPort(address, strconv.Itoa(port)), true
}
