// Copyright 2015-2019 Bleemeo
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
	"glouton/facts"
	"glouton/logger"
	"glouton/types"
	"net"
	"strconv"
	"time"
)

const tcpPortocol = "tcp"

// Discoverer allow to discover services. See DynamicDiscovery and Discovery.
type Discoverer interface {
	Discovery(ctx context.Context, maxAge time.Duration) (services []Service, err error)
	LastUpdate() time.Time
}

// PersistentDiscoverer also allow to remove a non-running service.
type PersistentDiscoverer interface {
	Discoverer
	RemoveIfNonRunning(ctx context.Context, services []Service)
}

// NameContainer contains the service and container names.
type NameContainer struct {
	Name          string
	ContainerName string
}

type ServiceOveride struct {
	IgnoredPorts   []int
	ExtraAttribute map[string]string
}

// ServiceName is the name of a supported service.
type ServiceName string

// List of known service names.
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
	SaltMasterService    ServiceName = "salt_master"
	SquidService         ServiceName = "squid"
	UWSGIService         ServiceName = "uwsgi"
	VarnishService       ServiceName = "varnish"
	ZookeeperService     ServiceName = "zookeeper"

	CustomService ServiceName = "__custom__"
)

// Service is the information found about a given service.
type Service struct {
	Name            string
	ServiceType     ServiceName
	ContainerID     string
	ContainerName   string
	IPAddress       string // IPAddress is the IPv4 address to reach service for metrics gathering. If empty, it means IP was not found
	ListenAddresses []facts.ListenAddress
	ExePath         string
	Stack           string
	// ExtraAttributes contains additional service-dependant attribute. It may be password for MySQL, URL for HAProxy, ...
	// Both configuration and dynamic discovery may set value here.
	ExtraAttributes map[string]string
	IgnoredPorts    map[int]bool
	Active          bool
	CheckIgnored    bool
	MetricsIgnored  bool

	HasNetstatInfo bool
	container      facts.Container
}

func (s Service) String() string {
	if s.ContainerName != "" {
		return fmt.Sprintf("%s (on %s)", s.Name, s.ContainerName)
	}

	return s.Name
}

// AddressForPort return the IP address for given port & network (tcp, udp).
func (s Service) AddressForPort(port int, network string, force bool) string {
	if s.ExtraAttributes["address"] != "" {
		return s.ExtraAttributes["address"]
	}

	for _, a := range s.ListenAddresses {
		if a.Network() != network {
			continue
		}

		address, portStr, err := net.SplitHostPort(a.String())
		if err != nil {
			continue
		}

		p, err := strconv.ParseInt(portStr, 10, 0)
		if err != nil {
			continue
		}

		if address == net.IPv4zero.String() {
			address = s.IPAddress
		}

		if int(p) == port {
			return address
		}
	}

	if force {
		return s.IPAddress
	}

	return ""
}

// AddressPort return the IP address &port for the "main" service (e.g. for RabbitMQ the AMQP port, not the management port).
func (s Service) AddressPort() (string, int) {
	di := servicesDiscoveryInfo[s.ServiceType]
	port := di.ServicePort
	force := false

	if s.ExtraAttributes["port"] != "" {
		tmp, err := strconv.ParseInt(s.ExtraAttributes["port"], 10, 0)
		if err != nil {
			logger.V(1).Printf("Invalid port %#v: %v", s.ExtraAttributes["port"], err)
		} else {
			port = int(tmp)
			force = true
		}
	}

	if port == 0 {
		return "", 0
	}

	return s.AddressForPort(port, di.ServiceProtocol, force), port
}

// LabelsOfStatus returns the labels for the status metrics of this service.
func (s Service) LabelsOfStatus() map[string]string {
	labels := map[string]string{
		types.LabelName: fmt.Sprintf("%s_status", s.Name),
	}

	if s.ContainerName != "" {
		labels[types.LabelMetaContainerName] = s.ContainerName
	}

	return labels
}

// AnnotationsOfStatus returns the annotations for the status metrics of this service.
func (s Service) AnnotationsOfStatus() types.MetricAnnotations {
	annotations := types.MetricAnnotations{
		ServiceName: s.Name,
	}

	if s.ContainerName != "" {
		annotations.BleemeoItem = s.ContainerName
		annotations.ContainerID = s.ContainerID
	}

	return annotations
}

//nolint:gochecknoglobals
var (
	servicesDiscoveryInfo = map[ServiceName]discoveryInfo{
		ApacheService: {
			ServicePort:         80,
			ServiceProtocol:     "tcp",
			ExtraAttributeNames: []string{"address", "port", "http_host", "http_path", "http_status_code"},
		},
		BitBucketService: {
			ServicePort:         7990,
			ServiceProtocol:     "tcp",
			IgnoreHighPort:      true,
			ExtraAttributeNames: []string{"address", "port", "jmx_port", "jmx_username", "jmx_password", "jmx_metrics"},
			DefaultIgnoredPorts: map[int]bool{
				5701: true,
			},
		},
		BindService: {
			ServicePort:         53,
			ServiceProtocol:     "tcp",
			ExtraAttributeNames: []string{"address", "port"},
		},
		CassandraService: {
			ServicePort:         9042,
			ServiceProtocol:     "tcp",
			IgnoreHighPort:      true,
			ExtraAttributeNames: []string{"address", "port", "jmx_port", "jmx_username", "jmx_password", "jmx_metrics", "cassandra_detailed_tables"},
		},
		ConfluenceService: {
			ServicePort:         8090,
			ServiceProtocol:     "tcp",
			IgnoreHighPort:      true,
			ExtraAttributeNames: []string{"address", "port", "jmx_port", "jmx_username", "jmx_password", "jmx_metrics"},
		},
		DovecoteService: {
			ServicePort:         143,
			ServiceProtocol:     "tcp",
			ExtraAttributeNames: []string{"address", "port"},
		},
		ElasticSearchService: {
			ServicePort:         9200,
			ServiceProtocol:     "tcp",
			ExtraAttributeNames: []string{"address", "port"},
		},
		EjabberService: {
			ServicePort:         5222,
			ServiceProtocol:     "tcp",
			IgnoreHighPort:      true,
			ExtraAttributeNames: []string{"address", "port"},
		},
		EximService: {
			ServicePort:         25,
			ServiceProtocol:     "tcp",
			ExtraAttributeNames: []string{"address", "port"},
		},
		HAProxyService: {
			IgnoreHighPort:      true, // HAProxy use a random high-port when Syslog over-UDP is enabled.
			ServiceProtocol:     "tcp",
			ExtraAttributeNames: []string{"address", "port", "stats_url"},
		},
		InfluxDBService: {
			ServicePort:         8086,
			ServiceProtocol:     "tcp",
			ExtraAttributeNames: []string{"address", "port"},
		},
		JIRAService: {
			ServicePort:         8080,
			ServiceProtocol:     "tcp",
			IgnoreHighPort:      true,
			ExtraAttributeNames: []string{"address", "port", "jmx_port", "jmx_username", "jmx_password", "jmx_metrics"},
		},
		MemcachedService: {
			ServicePort:         11211,
			ServiceProtocol:     "tcp",
			ExtraAttributeNames: []string{"address", "port"},
		},
		MongoDBService: {
			ServicePort:         27017,
			ServiceProtocol:     "tcp",
			ExtraAttributeNames: []string{"address", "port"},
		},
		MosquittoService: {
			ServicePort:         1883,
			ServiceProtocol:     "tcp",
			ExtraAttributeNames: []string{"address", "port"},
		},
		MySQLService: {
			ServicePort:         3306,
			ServiceProtocol:     "tcp",
			ExtraAttributeNames: []string{"address", "port", "username", "password"},
		},
		NginxService: {
			ServicePort:         80,
			ServiceProtocol:     "tcp",
			ExtraAttributeNames: []string{"address", "port", "http_host", "http_path", "http_status_code"},
		},
		NTPService: {
			ServicePort:         123,
			ServiceProtocol:     "udp",
			ExtraAttributeNames: []string{"address", "port"},
		},
		OpenLDAPService: {
			ServicePort:         389,
			ServiceProtocol:     "tcp",
			ExtraAttributeNames: []string{"address", "port"},
		},
		OpenVPNService: {
			DisablePersistentConnection: true,
		},
		PHPFPMService: {
			ServiceProtocol:     "tcp",
			ExtraAttributeNames: []string{"address", "port", "stats_url"},
		},
		PostfixService: {
			ServicePort:         25,
			ServiceProtocol:     "tcp",
			ExtraAttributeNames: []string{"address", "port"},
		},
		PostgreSQLService: {
			ServicePort:         5432,
			ServiceProtocol:     "tcp",
			ExtraAttributeNames: []string{"address", "port", "username", "password"},
		},
		RabbitMQService: {
			ServicePort:         5672,
			ServiceProtocol:     "tcp",
			IgnoreHighPort:      true,
			ExtraAttributeNames: []string{"address", "port", "username", "password", "mgmt_port"},
		},
		RedisService: {
			ServicePort:         6379,
			ServiceProtocol:     "tcp",
			ExtraAttributeNames: []string{"address", "port"},
		},
		SaltMasterService: {
			ServicePort:         4505,
			ServiceProtocol:     "tcp",
			ExtraAttributeNames: []string{"address", "port"},
		},
		SquidService: {
			ServicePort:         3128,
			ServiceProtocol:     "tcp",
			ExtraAttributeNames: []string{"address", "port", "http_host", "http_path", "http_status_code"},
		},
		VarnishService: {
			ServicePort:         6082,
			ServiceProtocol:     "tcp",
			ExtraAttributeNames: []string{"address", "port"},
		},
		ZookeeperService: {
			ServicePort:         2181,
			ServiceProtocol:     "tcp",
			IgnoreHighPort:      true,
			ExtraAttributeNames: []string{"address", "port", "jmx_port", "jmx_username", "jmx_password", "jmx_metrics"},
		},

		CustomService: {
			ExtraAttributeNames: []string{"address", "port", "check_type", "check_command", "http_host", "http_path", "http_status_code"},
		},
	}
)

type discoveryInfo struct {
	ServicePort                 int
	ServiceProtocol             string // "tcp", "udp" or "unix"
	IgnoreHighPort              bool
	DisablePersistentConnection bool
	ExtraAttributeNames         []string
	DefaultIgnoredPorts         map[int]bool
}
