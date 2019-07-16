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
	ElasticSearchService ServiceName = "elasticsearch"
	HAProxyService       ServiceName = "haproxy"
	InfluxDBService      ServiceName = "influxdb"
	MemcachedService     ServiceName = "memcached"
	MongoDBService       ServiceName = "mongodb"
	MySQLService         ServiceName = "mysql"
	NginxService         ServiceName = "nginx"
	PHPFPMService        ServiceName = "phpfpm"
	PostgreSQLService    ServiceName = "postgresql"
	RabbitMQService      ServiceName = "rabbitmq"
	RedisService         ServiceName = "redis"
	SquidService         ServiceName = "squid"
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
		ElasticSearchService: {
			ServicePort:     9200,
			ServiceProtocol: "tcp",
		},
		HAProxyService: {
			IgnoreHighPort: true, // HAProxy use a random high-port when Syslog over-UDP is enabled.
		},
		MemcachedService: {
			ServicePort:     11211,
			ServiceProtocol: "tcp",
		},
		MongoDBService: {
			ServicePort:     27017,
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
	}
)

type discoveryInfo struct {
	ServicePort     int
	ServiceProtocol string // "tcp", "udp" or "unix"
	IgnoreHighPort  bool
}
