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
	"net"
	"strconv"

	"github.com/bleemeo/glouton/inputs/activemq"
	"github.com/bleemeo/glouton/inputs/apache"
	"github.com/bleemeo/glouton/inputs/bind"
	"github.com/bleemeo/glouton/inputs/chrony"
	"github.com/bleemeo/glouton/inputs/clickhouse"
	"github.com/bleemeo/glouton/inputs/consul"
	"github.com/bleemeo/glouton/inputs/dovecot"
	"github.com/bleemeo/glouton/inputs/elasticsearch"
	"github.com/bleemeo/glouton/inputs/fail2ban"
	"github.com/bleemeo/glouton/inputs/haproxy"
	"github.com/bleemeo/glouton/inputs/influxdb"
	"github.com/bleemeo/glouton/inputs/jenkins"
	"github.com/bleemeo/glouton/inputs/memcached"
	"github.com/bleemeo/glouton/inputs/mongodb"
	"github.com/bleemeo/glouton/inputs/nats"
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
	"github.com/bleemeo/glouton/inputs/tomcat"
	"github.com/bleemeo/glouton/inputs/upsd"
	"github.com/bleemeo/glouton/inputs/uwsgi"
	"github.com/bleemeo/glouton/inputs/varnish"
	"github.com/bleemeo/glouton/inputs/vault"
	"github.com/bleemeo/glouton/inputs/zookeeper"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/prometheus/registry"

	"github.com/influxdata/telegraf"
)

var (
	// errMissingInfo is returned by a builder when the service lacks what its input needs,
	// such as credentials or an address.
	errMissingInfo = errors.New("missing credentials or address")
	// errMissingCommand is returned by a builder whose input needs a command the service's
	// container does not have.
	errMissingCommand = errors.New("required command not found")
	// errNoInputBuilt is reported for a builder that returned neither an input nor a reason.
	errNoInputBuilt = errors.New("the input builder returned no input")
)

// inputBuilder builds the metric input of a service, or says why it cannot with
// errMissingInfo or errMissingCommand. opts starts with the compatibility naming (name + item
// only): an input that needs more labels changes it, or replaces it with the options it
// returns.
type inputBuilder func(d *Discovery, service Service, opts *registry.RegistrationOption) (telegraf.Input, error)

// inputBuilders is the builder of each service type Glouton has a metric input for. JMX and the
// agent's own gatherers collect a few other types, see Discovery.inputSupported.
var inputBuilders = map[ServiceName]inputBuilder{ //nolint:gochecknoglobals
	ActiveMQService:      activeMQInput,
	ApacheService:        apacheInput,
	BindService:          bindInput,
	ChronyService:        chronyInput,
	ClickHouseService:    clickHouseInput,
	ConsulService:        consulInput,
	DovecotService:       dovecotInput,
	ElasticSearchService: elasticSearchInput,
	Fail2banService:      fail2banInput,
	HAProxyService:       haproxyInput,
	InfluxDBService:      influxDBInput,
	JenkinsService:       jenkinsInput,
	MariaDBService:       mariaDBInput,
	MemcachedService:     memcachedInput,
	MongoDBService:       mongoDBInput,
	MySQLService:         mySQLInput,
	NSQService:           nsqInput,
	NatsService:          natsInput,
	NfsService:           nfsInput,
	NginxService:         nginxInput,
	NTPService:           ntpInput,
	OpenBaoService:       openBaoInput,
	OpenLDAPService:      openLDAPInput,
	PHPFPMService:        phpFPMInput,
	PgBouncerService:     pgBouncerInput,
	PostgreSQLService:    postgreSQLInput,
	RabbitMQService:      rabbitMQInput,
	RedisService:         redisInput,
	TomcatService:        tomcatInput,
	UPSDService:          upsdInput,
	UWSGIService:         uwsgiInput,
	ValkeyService:        redisInput,
	VarnishService:       varnishInput,
	VaultService:         vaultInput,
	ZookeeperService:     zookeeperInput,
}

// inputsWithoutNetwork lists the service types whose input runs a command or reads a file rather
// than talking to the service: their error cannot tell whether the service answered, so it is
// never reported as answered_error. Every other input reports a service that did not answer as
// unreachable, which TestUnreachableDetected proves by collecting from a closed port.
var inputsWithoutNetwork = map[ServiceName]bool{ //nolint:gochecknoglobals
	Fail2banService: true,
	NfsService:      true,
	VarnishService:  true,
}

// serviceHostPort is "ip:port" of the service's address, false when it has none.
func serviceHostPort(service Service) (string, bool) {
	ip, port := service.AddressPort()
	if ip == "" {
		return "", false
	}

	return net.JoinHostPort(ip, strconv.Itoa(port)), true
}

// statsOrServiceURL is the configured stats URL or, without one, "http://ip:port" of the
// service's address.
func statsOrServiceURL(service Service) (string, error) {
	if service.Config.StatsURL != "" {
		return service.Config.StatsURL, nil
	}

	hostPort, ok := serviceHostPort(service)
	if !ok {
		return "", errMissingInfo
	}

	return "http://" + hostPort, nil
}

// withOptions returns a builder's result for an input constructor that also returns its
// registration options, which replace opts.
func withOptions(opts *registry.RegistrationOption) func(telegraf.Input, registry.RegistrationOption, error) (telegraf.Input, error) {
	return func(input telegraf.Input, options registry.RegistrationOption, err error) (telegraf.Input, error) {
		*opts = options

		return input, err
	}
}

func activeMQInput(_ *Discovery, service Service, opts *registry.RegistrationOption) (telegraf.Input, error) {
	url, username, password := activeMQURL(service)
	if url == "" {
		return nil, errMissingInfo
	}

	// One point per queue, topic and subscriber, all sharing their metric name, so
	// what identifies a destination has to be a label of its own -- the
	// compatibility naming keeps only the item and would collapse them onto one
	// series. The item stays the service instance instead of being glued to a
	// destination name; see the renameGlobal of inputs/activemq.
	opts.CompatibilityNameItem = false

	return activemq.New(url, username, password)
}

func apacheInput(_ *Discovery, service Service, _ *registry.RegistrationOption) (telegraf.Input, error) {
	ip, port := service.AddressPort()
	if ip == "" {
		return nil, errMissingInfo
	}

	return apache.New(apacheStatusURL(ip, port))
}

func bindInput(_ *Discovery, service Service, _ *registry.RegistrationOption) (telegraf.Input, error) {
	url := bindStatsURL(service)
	if url == "" {
		return nil, errMissingInfo
	}

	return bind.New(url)
}

func clickHouseInput(_ *Discovery, service Service, _ *registry.RegistrationOption) (telegraf.Input, error) {
	username := service.Config.Username
	if username == "" {
		username = "default"
	}

	url := service.Config.StatsURL

	if url == "" {
		// stats_port or port when set, else the main address or the default monitoring port.
		ip, port := clickHouseAddress(service)
		if ip == "" {
			return nil, errMissingInfo
		}

		url = "http://" + net.JoinHostPort(ip, strconv.Itoa(port))
	}

	return clickhouse.New(url, username, service.Config.Password)
}

func consulInput(_ *Discovery, service Service, opts *registry.RegistrationOption) (telegraf.Input, error) {
	url, err := statsOrServiceURL(service)
	if err != nil {
		return nil, err
	}

	// Some Consul metrics come as one series per label set, so those labels have to be
	// kept rather than reduced to the item -- see the renameGlobal of inputs/consul for
	// which ones and why they are safe to keep.
	opts.CompatibilityNameItem = false

	return consul.New(url, service.Config.Password)
}

func dovecotInput(_ *Discovery, service Service, _ *registry.RegistrationOption) (telegraf.Input, error) {
	server := dovecotStatsServer(service)
	if server == "" {
		return nil, errMissingInfo
	}

	return dovecot.New(server)
}

func elasticSearchInput(_ *Discovery, service Service, _ *registry.RegistrationOption) (telegraf.Input, error) {
	hostPort, ok := serviceHostPort(service)
	if !ok {
		return nil, errMissingInfo
	}

	return elasticsearch.New("http://" + hostPort)
}

func fail2banInput(_ *Discovery, _ Service, opts *registry.RegistrationOption) (telegraf.Input, error) {
	return withOptions(opts)(fail2ban.New())
}

func haproxyInput(_ *Discovery, service Service, _ *registry.RegistrationOption) (telegraf.Input, error) {
	if service.Config.StatsURL == "" {
		return nil, errMissingInfo
	}

	return haproxy.New(service.Config.StatsURL)
}

func influxDBInput(_ *Discovery, service Service, opts *registry.RegistrationOption) (telegraf.Input, error) {
	// The server's root rather than an endpoint: the three lines publish their metrics
	// in different places -- 1.x as JSON on "/debug/vars", 2.x and 3.x as Prometheus
	// text on "/metrics" -- and which one to read is decided from the version the
	// server reports. See inputs/influxdb.
	url, err := statsOrServiceURL(service)
	if err != nil {
		return nil, err
	}

	// The token is used by 3.x, which answers 401 everywhere without one; the user and
	// password by 1.x. The password is offered as both: 1.x authenticates with a user and a
	// password, 3.x with a bearer token, and there is one field for either. A server only ever
	// reads the one its line uses.
	return withOptions(opts)(influxdb.New(url, service.Config.Username, service.Config.Password, service.Config.Password))
}

func jenkinsInput(_ *Discovery, service Service, opts *registry.RegistrationOption) (telegraf.Input, error) {
	if service.Config.StatsURL == "" || service.Config.Password == "" {
		return nil, errMissingInfo
	}

	return withOptions(opts)(jenkins.New(service.Config))
}

func mariaDBInput(_ *Discovery, service Service, _ *registry.RegistrationOption) (telegraf.Input, error) {
	return createMariaDBInput(service)
}

func memcachedInput(_ *Discovery, service Service, _ *registry.RegistrationOption) (telegraf.Input, error) {
	hostPort, ok := serviceHostPort(service)
	if !ok {
		return nil, errMissingInfo
	}

	return memcached.New(hostPort)
}

func mongoDBInput(_ *Discovery, service Service, _ *registry.RegistrationOption) (telegraf.Input, error) {
	hostPort, ok := serviceHostPort(service)
	if !ok {
		return nil, errMissingInfo
	}

	return mongodb.New("mongodb://" + hostPort)
}

func mySQLInput(_ *Discovery, service Service, _ *registry.RegistrationOption) (telegraf.Input, error) {
	return createMySQLInput(service)
}

func natsInput(_ *Discovery, service Service, opts *registry.RegistrationOption) (telegraf.Input, error) {
	url, ok := natsMonitoringURL(service)
	if !ok {
		return nil, errMissingInfo
	}

	return withOptions(opts)(nats.New(url))
}

// natsMonitoringURL is the URL of the service's monitoring server: the address listening on its
// port, the configured one, or else the service's.
func natsMonitoringURL(service Service) (string, bool) {
	// The default port of the monitoring server is 8222.
	port := 8222

	if service.Config.StatsPort != 0 {
		port = service.Config.StatsPort
	}

	ip := service.AddressForPort(port, tcpProtocol, true)
	if ip == "" {
		return "", false
	}

	return "http://" + net.JoinHostPort(ip, strconv.Itoa(port)), true
}

func nfsInput(_ *Discovery, _ Service, opts *registry.RegistrationOption) (telegraf.Input, error) {
	return withOptions(opts)(nfs.New())
}

func nginxInput(_ *Discovery, service Service, _ *registry.RegistrationOption) (telegraf.Input, error) {
	hostPort, ok := serviceHostPort(service)
	if !ok {
		return nil, errMissingInfo
	}

	return nginx.New(fmt.Sprintf("http://%s/nginx_status", hostPort))
}

func nsqInput(_ *Discovery, service Service, _ *registry.RegistrationOption) (telegraf.Input, error) {
	url, err := statsOrServiceURL(service)
	if err != nil {
		return nil, err
	}

	return nsq.New(url)
}

func chronyInput(_ *Discovery, service Service, opts *registry.RegistrationOption) (telegraf.Input, error) {
	address, ok := chronyCmdAddress(service)
	if !ok {
		logger.V(1).Printf(
			"No address to read the chrony daemon of service '%s' on container '%s', not gathering its metrics",
			service.Name, service.ContainerName,
		)

		return nil, errMissingInfo
	}

	return withOptions(opts)(chrony.New(address))
}

func ntpInput(_ *Discovery, service Service, opts *registry.RegistrationOption) (telegraf.Input, error) {
	address, ok := ntpdAddress(service)
	if !ok {
		logger.V(1).Printf(
			"No address to read the NTP daemon of service '%s' on container '%s', not gathering its metrics",
			service.Name, service.ContainerName,
		)

		return nil, errMissingInfo
	}

	return withOptions(opts)(ntp.New(address))
}

func openBaoInput(_ *Discovery, service Service, _ *registry.RegistrationOption) (telegraf.Input, error) {
	url, err := statsOrServiceURL(service)
	if err != nil {
		return nil, err
	}

	return openbao.New(url, service.Config.Password)
}

func openLDAPInput(_ *Discovery, service Service, opts *registry.RegistrationOption) (telegraf.Input, error) {
	ip, port := service.AddressPort()
	if ip == "" {
		return nil, errMissingInfo
	}

	return withOptions(opts)(openldap.New(ip, port, service.Config))
}

func phpFPMInput(_ *Discovery, service Service, _ *registry.RegistrationOption) (telegraf.Input, error) {
	statsURL := urlForPHPFPM(service)
	if statsURL == "" {
		return nil, errMissingInfo
	}

	return phpfpm.New(statsURL)
}

func postgreSQLInput(_ *Discovery, service Service, _ *registry.RegistrationOption) (telegraf.Input, error) {
	ip, port := service.AddressPort()
	if ip == "" || service.Config.Password == "" {
		return nil, errMissingInfo
	}

	username := service.Config.Username
	if username == "" {
		username = "postgres"
	}

	address := fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=postgres sslmode=disable",
		ip, port, username, service.Config.Password,
	)

	return postgresql.New(address, service.Config.DetailedItems)
}

func pgBouncerInput(_ *Discovery, service Service, opts *registry.RegistrationOption) (telegraf.Input, error) {
	ip, port := service.AddressPort()
	if ip == "" {
		return nil, errMissingInfo
	}

	username := service.Config.Username
	if username == "" {
		username = "pgbouncer"
	}

	address := fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=pgbouncer sslmode=disable",
		ip, port, username, service.Config.Password,
	)
	opts.CompatibilityNameItem = false

	return pgbouncer.New(address)
}

func rabbitMQInput(_ *Discovery, service Service, _ *registry.RegistrationOption) (telegraf.Input, error) {
	mgmtPort := 15672
	force := false

	if service.Config.StatsPort != 0 {
		mgmtPort = service.Config.StatsPort
		force = true
	}

	ip := service.AddressForPort(mgmtPort, tcpProtocol, force)
	if ip == "" {
		return nil, errMissingInfo
	}

	username := service.Config.Username
	if username == "" {
		username = "guest"
	}

	password := service.Config.Password
	if password == "" {
		password = "guest"
	}

	url := "http://" + net.JoinHostPort(ip, strconv.Itoa(mgmtPort))

	return rabbitmq.New(url, username, password)
}

// redisInput builds the input of Redis and of Valkey, which speaks the same protocol.
func redisInput(_ *Discovery, service Service, _ *registry.RegistrationOption) (telegraf.Input, error) {
	hostPort, ok := serviceHostPort(service)
	if !ok {
		return nil, errMissingInfo
	}

	return redis.New("tcp://"+hostPort, service.Config.Password)
}

func tomcatInput(_ *Discovery, service Service, opts *registry.RegistrationOption) (telegraf.Input, error) {
	// The manager webapp the metrics are read from requires a user with the
	// "manager-status" role, so without credentials every gather would only get a
	// 401 (or a 404 when the webapp isn't even deployed).
	//
	// Both credentials are required, not just the password: the plugin always sends
	// basic auth, and Tomcat ships an empty tomcat-users.xml, so unlike ActiveMQ there
	// is no factory account to fall back on -- a password alone could only ever 401.
	if service.Config.Username == "" || service.Config.Password == "" {
		logger.V(1).Printf("No metrics for %s on instance %s: a username and a password are required", service.Name, service.Instance)

		return nil, errMissingInfo
	}

	url := service.Config.StatsURL

	if url == "" {
		hostPort, ok := serviceHostPort(service)
		if !ok {
			return nil, errMissingInfo
		}

		url = fmt.Sprintf("http://%s/manager/status/all?XML=true", hostPort)
	}

	// One point per connector and per memory pool, all sharing their metric name, so
	// the "name" telling them apart has to be a label of its own rather than the item,
	// which is the service instance; see the renameGlobal of inputs/tomcat.
	opts.CompatibilityNameItem = false

	return tomcat.New(url, service.Config.Username, service.Config.Password)
}

func upsdInput(_ *Discovery, service Service, opts *registry.RegistrationOption) (telegraf.Input, error) {
	ip, port := service.AddressPort()
	if ip == "" {
		return nil, errMissingInfo
	}

	return withOptions(opts)(upsd.New(ip, port, service.Config.Username, service.Config.Password))
}

func uwsgiInput(_ *Discovery, service Service, opts *registry.RegistrationOption) (telegraf.Input, error) {
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

	ip := service.AddressForPort(port, tcpProtocol, true)
	if ip == "" {
		return nil, errMissingInfo
	}

	url := fmt.Sprintf("%s://%s", protocol, net.JoinHostPort(ip, strconv.Itoa(port)))

	return withOptions(opts)(uwsgi.New(url))
}

func varnishInput(d *Discovery, service Service, opts *registry.RegistrationOption) (telegraf.Input, error) {
	// The input runs "varnishstat" rather than reading a port, since that is the only
	// way Varnish publishes its counters: they live in a shared memory segment the
	// daemon maps, with no socket in front of them. The agent image carries no
	// varnishstat, so it comes from wherever the daemon is -- the machine for a
	// Varnish installed on it, the container's own image for a containerised one.
	//
	// Not being able to read it means no input, rather than an input failing on every
	// gather under that container's name.
	if !d.canReadVarnish(service) {
		return nil, errMissingCommand
	}

	return withOptions(opts)(varnish.New(d.commandRunner, d.containerInfo, service.ContainerID))
}

func vaultInput(_ *Discovery, service Service, _ *registry.RegistrationOption) (telegraf.Input, error) {
	url, err := statsOrServiceURL(service)
	if err != nil {
		return nil, err
	}

	return vault.New(url, service.Config.Password)
}

func zookeeperInput(_ *Discovery, service Service, _ *registry.RegistrationOption) (telegraf.Input, error) {
	hostPort, ok := serviceHostPort(service)
	if !ok {
		return nil, errMissingInfo
	}

	return zookeeper.New(hostPort)
}
