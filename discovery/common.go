package discovery

import (
	"context"
	"net"
	"time"
)

const tcpPortocol = "tcp"

// Discoverer allow to discover services. See DynamicDiscovery and Discovery
type Discoverer interface {
	Discovery(ctx context.Context, maxAge time.Duration) (services []Service, err error)
	LastUpdate() time.Time
}

// PersistentDiscoverer also allow to remove a non-running service
type PersistentDiscoverer interface {
	Discoverer
	RemoveIfNonRunning(ctx context.Context, services []Service)
}

type nameContainer struct {
	name        ServiceName
	containerID string
}

// ServiceName is the name of a supported service
type ServiceName string

// List of known service names
const (
	ApacheService        ServiceName = "apache"
	AsteriskService      ServiceName = "asterisk"
	BindService          ServiceName = "bind"
	BitBucketService     ServiceName = "bitbucket"
	CassandraService     ServiceName = "cassandra"
	ConfluenceService    ServiceName = "confluence"
	DovecoteService      ServiceName = "dovecot"
	EjabberService       ServiceName = "ejabberd"
	ElasticSearchService ServiceName = "elasticsearch"
	EximService          ServiceName = "exim"
	FreeradiusService    ServiceName = "freeradius"
	HAProxyService       ServiceName = "haproxy"
	InfluxDBService      ServiceName = "influxdb"
	JIRAService          ServiceName = "jira"
	LibvirtService       ServiceName = "libvirt"
	MemcachedService     ServiceName = "memcached"
	MongoDBService       ServiceName = "mongodb"
	MosquittoService     ServiceName = "mosquitto" //nolint:misspell
	MySQLService         ServiceName = "mysql"
	NginxService         ServiceName = "nginx"
	NTPService           ServiceName = "ntp"
	OpenLDAPService      ServiceName = "openldap"
	OpenVPNService       ServiceName = "openvpn"
	PHPFPMService        ServiceName = "phpfpm"
	PostfixService       ServiceName = "postfix"
	PostgreSQLService    ServiceName = "postgresql"
	RabbitMQService      ServiceName = "rabbitmq"
	RedisService         ServiceName = "redis"
	SaltMasterService    ServiceName = "salt-master"
	SquidService         ServiceName = "squid"
	UWSGIService         ServiceName = "uwsgi"
	VarnishService       ServiceName = "varnish"
	ZookeeperService     ServiceName = "zookeeper"
)

// Service is the information found about a given service
type Service struct {
	Name            ServiceName
	ContainerID     string
	ContainerName   string
	IPAddress       string // IPAddress is the IPv4 address to reach service for metrics gathering. If empty, it means IP was not found
	ListenAddresses []net.Addr
	ExePath         string
	// ExtraAttributes contains additional service-dependant attribute. It may be password for MySQL, URL for HAProxy, ...
	// Both configuration and dynamic discovery may set value here.
	ExtraAttributes map[string]string
	Active          bool

	CheckIgnored bool // TODO: fill this value

	hasNetstatInfo bool
	container      container
}

// nolint:gochecknoglobals
var (
	servicesDiscoveryInfo = map[ServiceName]discoveryInfo{
		ApacheService: {
			ServicePort:     80,
			ServiceProtocol: "tcp",
		},
		BitBucketService: {
			ServicePort:     7990,
			ServiceProtocol: "tcp",
			IgnoreHighPort:  true,
		},
		BindService: {
			ServicePort:     53,
			ServiceProtocol: "tcp",
		},
		CassandraService: {
			ServicePort:     9042,
			ServiceProtocol: "tcp",
			IgnoreHighPort:  true,
		},
		ConfluenceService: {
			ServicePort:     8090,
			ServiceProtocol: "tcp",
			IgnoreHighPort:  true,
		},
		DovecoteService: {
			ServicePort:     143,
			ServiceProtocol: "tcp",
		},
		ElasticSearchService: {
			ServicePort:     9200,
			ServiceProtocol: "tcp",
		},
		EjabberService: {
			ServicePort:     5222,
			ServiceProtocol: "tcp",
			IgnoreHighPort:  true,
		},
		EximService: {
			ServicePort:     25,
			ServiceProtocol: "tcp",
		},
		HAProxyService: {
			IgnoreHighPort: true, // HAProxy use a random high-port when Syslog over-UDP is enabled.
		},
		InfluxDBService: {
			ServicePort:     8086,
			ServiceProtocol: "tcp",
		},
		JIRAService: {
			ServicePort:     8080,
			ServiceProtocol: "tcp",
			IgnoreHighPort:  true,
		},
		MemcachedService: {
			ServicePort:     11211,
			ServiceProtocol: "tcp",
		},
		MongoDBService: {
			ServicePort:     27017,
			ServiceProtocol: "tcp",
		},
		MosquittoService: {
			ServicePort:     1883,
			ServiceProtocol: "tcp",
		},
		MySQLService: {
			ServicePort:     3306,
			ServiceProtocol: "tcp",
		},
		NginxService: {
			ServicePort:     80,
			ServiceProtocol: "tcp",
		},
		NTPService: {
			ServicePort:     123,
			ServiceProtocol: "udp",
		},
		OpenLDAPService: {
			ServicePort:     389,
			ServiceProtocol: "tcp",
		},
		OpenVPNService: {
			DisablePersistentConnection: true,
		},
		PostfixService: {
			ServicePort:     25,
			ServiceProtocol: "tcp",
		},
		PostgreSQLService: {
			ServicePort:     5432,
			ServiceProtocol: "tcp",
		},
		RabbitMQService: {
			ServicePort:     5672,
			ServiceProtocol: "tcp",
			IgnoreHighPort:  true,
		},
		RedisService: {
			ServicePort:     6379,
			ServiceProtocol: "tcp",
		},
		SaltMasterService: {
			ServicePort:     4505,
			ServiceProtocol: "tcp",
		},
		SquidService: {
			ServicePort:     3128,
			ServiceProtocol: "tcp",
		},
		VarnishService: {
			ServicePort:     6082,
			ServiceProtocol: "tcp",
		},
		ZookeeperService: {
			ServicePort:     2181,
			ServiceProtocol: "tcp",
			IgnoreHighPort:  true,
		},
	}
)

type discoveryInfo struct {
	ServicePort                 int
	ServiceProtocol             string // "tcp", "udp" or "unix"
	IgnoreHighPort              bool
	DisablePersistentConnection bool
}
