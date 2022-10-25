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
	"context"
	"fmt"
	"glouton/config"
	"glouton/facts"
	"glouton/logger"
	"glouton/types"
	"net"
	"strconv"
	"time"

	"github.com/imdario/mergo"
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

// NameInstance contains the service and instance names.
//
// The instance could be either a container name OR simply a arbitrary value.
type NameInstance struct {
	Name     string
	Instance string
}

type ServiceOverride struct {
	IgnoredPorts   []int
	Interval       time.Duration
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
	DovecotService       ServiceName = "dovecot"
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
	Config          config.Service
	Name            string
	Instance        string
	ServiceType     ServiceName
	ContainerID     string
	ContainerName   string // If ContainerName is set, Instance must be the same value.
	IPAddress       string // IPAddress is the IPv4 address to reach service for metrics gathering. If empty, it means IP was not found
	ListenAddresses []facts.ListenAddress
	ExePath         string
	Stack           string
	IgnoredPorts    map[int]bool
	Active          bool
	CheckIgnored    bool
	MetricsIgnored  bool
	// The interval of the check, used only for custom checks.
	Interval time.Duration

	HasNetstatInfo  bool
	LastNetstatInfo time.Time
	container       facts.Container
}

func (s Service) String() string {
	if s.ContainerName != "" {
		return fmt.Sprintf("%s (on container %s)", s.Name, s.Instance)
	}

	if s.Instance != "" {
		return fmt.Sprintf("%s (instance %s)", s.Name, s.Instance)
	}

	return s.Name
}

// AddressForPort return the IP address for given port & network (tcp, udp).
func (s Service) AddressForPort(port int, network string, force bool) string {
	if s.Config.Address != "" {
		return s.Config.Address
	}

	for _, a := range s.ListenAddresses {
		if a.Network() != network {
			continue
		}

		address, portStr, err := net.SplitHostPort(a.String())
		if err != nil {
			continue
		}

		p, err := strconv.Atoi(portStr)
		if err != nil {
			continue
		}

		if address == net.IPv4zero.String() {
			address = s.IPAddress
		}

		if p == port {
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

	if s.Config.Port != 0 {
		port = s.Config.Port
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

	if s.Instance != "" {
		labels[types.LabelItem] = s.Instance
	}

	return labels
}

// AnnotationsOfStatus returns the annotations for the status metrics of this service.
func (s Service) AnnotationsOfStatus() types.MetricAnnotations {
	annotations := types.MetricAnnotations{
		ServiceName:     s.Name,
		ServiceInstance: s.Instance,
	}

	if s.Instance != "" {
		annotations.BleemeoItem = s.Instance
	}

	if s.ContainerID != "" {
		annotations.ContainerID = s.ContainerID
	}

	return annotations
}

// merge updates existing service with update.
// Value from existing Service are preferred, but "list" value are merged.
func (s Service) merge(update Service) Service {
	switch {
	case update.HasNetstatInfo && !s.HasNetstatInfo:
		s.ListenAddresses = update.ListenAddresses
		s.HasNetstatInfo = true
		s.LastNetstatInfo = update.LastNetstatInfo
	case update.HasNetstatInfo:
		for _, v := range update.ListenAddresses {
			alreadyExist := false

			for _, existing := range s.ListenAddresses {
				if v == existing {
					alreadyExist = true

					break
				}
			}

			if !alreadyExist {
				s.ListenAddresses = append(s.ListenAddresses, v)
			}
		}

		if s.LastNetstatInfo.Before(update.LastNetstatInfo) {
			s.LastNetstatInfo = update.LastNetstatInfo
		}
	}

	for k, v := range update.IgnoredPorts {
		if _, ok := s.IgnoredPorts[k]; !ok {
			s.IgnoredPorts[k] = v
		}
	}

	// Merge configs, existing values are preferred.
	if err := mergo.Merge(&s.Config, update.Config); err != nil {
		logger.V(1).Printf("Failed to merge service configs: %s", err)
	}

	return s
}

//nolint:gochecknoglobals
var (
	servicesDiscoveryInfo = map[ServiceName]discoveryInfo{
		ApacheService: {
			ServicePort:     80,
			ServiceProtocol: "tcp",
			// ExtraAttributeNames: []string{"address", "port", "http_host", "http_path", "http_status_code"},
		},
		BitBucketService: {
			ServicePort:     7990,
			ServiceProtocol: "tcp",
			IgnoreHighPort:  true,
			// ExtraAttributeNames: []string{"address", "port", "jmx_port", "jmx_username", "jmx_password", "jmx_metrics"},
			DefaultIgnoredPorts: map[int]bool{
				5701: true,
			},
		},
		BindService: {
			ServicePort:     53,
			ServiceProtocol: "tcp",
			// ExtraAttributeNames: []string{"address", "port"},
		},
		CassandraService: {
			ServicePort:     9042,
			ServiceProtocol: "tcp",
			IgnoreHighPort:  true,
			// ExtraAttributeNames: []string{"address", "port", "jmx_port", "jmx_username", "jmx_password", "jmx_metrics", "cassandra_detailed_tables"},
		},
		ConfluenceService: {
			ServicePort:     8090,
			ServiceProtocol: "tcp",
			IgnoreHighPort:  true,
			// ExtraAttributeNames: []string{"address", "port", "jmx_port", "jmx_username", "jmx_password", "jmx_metrics"},
		},
		DovecotService: {
			ServicePort:     143,
			ServiceProtocol: "tcp",
			// ExtraAttributeNames: []string{"address", "port"},
		},
		ElasticSearchService: {
			ServicePort:     9200,
			ServiceProtocol: "tcp",
			// ExtraAttributeNames: []string{"address", "port"},
		},
		EjabberService: {
			ServicePort:     5222,
			ServiceProtocol: "tcp",
			IgnoreHighPort:  true,
			// ExtraAttributeNames: []string{"address", "port"},
		},
		EximService: {
			ServicePort:     25,
			ServiceProtocol: "tcp",
			// ExtraAttributeNames: []string{"address", "port"},
		},
		HAProxyService: {
			IgnoreHighPort:  true, // HAProxy use a random high-port when Syslog over-UDP is enabled.
			ServiceProtocol: "tcp",
			// ExtraAttributeNames: []string{"address", "port", "stats_url"},
		},
		InfluxDBService: {
			ServicePort:     8086,
			ServiceProtocol: "tcp",
			// ExtraAttributeNames: []string{"address", "port"},
		},
		JIRAService: {
			ServicePort:     8080,
			ServiceProtocol: "tcp",
			IgnoreHighPort:  true,
			// ExtraAttributeNames: []string{"address", "port", "jmx_port", "jmx_username", "jmx_password", "jmx_metrics"},
		},
		MemcachedService: {
			ServicePort:     11211,
			ServiceProtocol: "tcp",
			// ExtraAttributeNames: []string{"address", "port"},
		},
		MongoDBService: {
			ServicePort:     27017,
			ServiceProtocol: "tcp",
			// ExtraAttributeNames: []string{"address", "port"},
		},
		MosquittoService: {
			ServicePort:     1883,
			ServiceProtocol: "tcp",
			// ExtraAttributeNames: []string{"address", "port"},
		},
		MySQLService: {
			ServicePort:     3306,
			ServiceProtocol: "tcp",
			// ExtraAttributeNames: []string{"address", "port", "username", "password", "metrics_unix_socket"},
		},
		NginxService: {
			ServicePort:     80,
			ServiceProtocol: "tcp",
			// ExtraAttributeNames: []string{"address", "port", "http_host", "http_path", "http_status_code"},
		},
		NTPService: {
			ServicePort:     123,
			ServiceProtocol: "udp",
			// ExtraAttributeNames: []string{"address", "port"},
		},
		OpenLDAPService: {
			ServicePort:     389,
			ServiceProtocol: "tcp",
			// ExtraAttributeNames: []string{"address", "port"},
		},
		OpenVPNService: {
			DisablePersistentConnection: true,
		},
		PHPFPMService: {
			ServiceProtocol: "tcp",
			// ExtraAttributeNames: []string{"address", "port", "stats_url"},
		},
		PostfixService: {
			ServicePort:     25,
			ServiceProtocol: "tcp",
			// ExtraAttributeNames: []string{"address", "port"},
		},
		PostgreSQLService: {
			ServicePort:     5432,
			ServiceProtocol: "tcp",
			// ExtraAttributeNames: []string{"address", "port", "username", "password"},
		},
		RabbitMQService: {
			ServicePort:     5672,
			ServiceProtocol: "tcp",
			IgnoreHighPort:  true,
			// ExtraAttributeNames: []string{"address", "port", "username", "password", "mgmt_port"},
		},
		RedisService: {
			ServicePort:     6379,
			ServiceProtocol: "tcp",
			// ExtraAttributeNames: []string{"address", "port", "password"},
		},
		SaltMasterService: {
			ServicePort:     4505,
			ServiceProtocol: "tcp",
			// ExtraAttributeNames: []string{"address", "port"},
		},
		SquidService: {
			ServicePort:     3128,
			ServiceProtocol: "tcp",
			// ExtraAttributeNames: []string{"address", "port", "http_host", "http_path", "http_status_code"},
		},
		VarnishService: {
			ServicePort:     6082,
			ServiceProtocol: "tcp",
			// ExtraAttributeNames: []string{"address", "port"},
		},
		ZookeeperService: {
			ServicePort:     2181,
			ServiceProtocol: "tcp",
			IgnoreHighPort:  true,
			// ExtraAttributeNames: []string{"address", "port", "jmx_port", "jmx_username", "jmx_password", "jmx_metrics"},
		},

		CustomService: {
			// ExtraAttributeNames: []string{"address", "port", "check_type", "check_command", "http_host", "http_path", "http_status_code", "match_process"},
		},
	}
)

type discoveryInfo struct {
	ServicePort                 int
	ServiceProtocol             string // "tcp", "udp" or "unix"
	IgnoreHighPort              bool
	DisablePersistentConnection bool
	DefaultIgnoredPorts         map[int]bool
}
