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
	"fmt"
	"net"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/prometheus/model"
	"github.com/bleemeo/glouton/types"
	"github.com/prometheus/prometheus/model/labels"

	"dario.cat/mergo"
)

const (
	tcpProtocol  = "tcp"
	udpProtocol  = "udp"
	unixProtocol = "unix"
)

// Discoverer allow to discover services. See DynamicDiscovery and Discovery.
type Discoverer interface {
	Discovery(ctx context.Context) ([]Service, time.Time, error)
}

// PersistentDiscoverer also allow to remove a non-running service and get latest discovery done.
type PersistentDiscoverer interface {
	GetLatestDiscovery() ([]Service, time.Time)
	RemoveIfNonRunning(services []Service)
}

// NameInstance contains the service and instance names.
//
// The instance could be either a container name OR simply a arbitrary value.
type NameInstance struct {
	Name     string
	Instance string
}

// ServiceName is the name of a supported service.
type ServiceName string

// List of known service names.
const (
	ActiveMQService      ServiceName = "activemq"
	ApacheService        ServiceName = "apache"
	AsteriskService      ServiceName = "asterisk"
	BindService          ServiceName = "bind"
	BitBucketService     ServiceName = "bitbucket"
	CassandraService     ServiceName = "cassandra"
	ClickHouseService    ServiceName = "clickhouse"
	ConfluenceService    ServiceName = "confluence"
	ConsulService        ServiceName = "consul"
	DovecotService       ServiceName = "dovecot"
	EjabberService       ServiceName = "ejabberd"
	ElasticSearchService ServiceName = "elasticsearch"
	EximService          ServiceName = "exim"
	Fail2banService      ServiceName = "fail2ban"
	FreeradiusService    ServiceName = "freeradius"
	HAProxyService       ServiceName = "haproxy"
	InfluxDBService      ServiceName = "influxdb"
	JenkinsService       ServiceName = "jenkins"
	JIRAService          ServiceName = "jira"
	KafkaService         ServiceName = "kafka"
	LibvirtService       ServiceName = "libvirt"
	MariaDBService       ServiceName = "mariadb"
	MemcachedService     ServiceName = "memcached"
	MongoDBService       ServiceName = "mongodb"
	MosquittoService     ServiceName = "mosquitto" //nolint:misspell
	MySQLService         ServiceName = "mysql"
	NatsService          ServiceName = "nats"
	NfsService           ServiceName = "nfs"
	NginxService         ServiceName = "nginx"
	NSQService           ServiceName = "nsq"
	NTPService           ServiceName = "ntp"
	OpenBaoService       ServiceName = "openbao"
	OpenLDAPService      ServiceName = "openldap"
	OpenVPNService       ServiceName = "openvpn"
	PHPFPMService        ServiceName = "phpfpm"
	PostfixService       ServiceName = "postfix"
	PostgreSQLService    ServiceName = "postgresql"
	PgBouncerService     ServiceName = "pgbouncer"
	RabbitMQService      ServiceName = "rabbitmq"
	RedisService         ServiceName = "redis"
	SaltMasterService    ServiceName = "salt_master"
	SquidService         ServiceName = "squid"
	TomcatService        ServiceName = "tomcat"
	UWSGIService         ServiceName = "uwsgi"
	ValkeyService        ServiceName = "valkey"
	VarnishService       ServiceName = "varnish"
	UPSDService          ServiceName = "upsd"
	VaultService         ServiceName = "vault"
	ZookeeperService     ServiceName = "zookeeper"

	CustomService ServiceName = "__custom__"
)

type ApplicationType int

const (
	ApplicationUnset         ApplicationType = 0
	ApplicationDockerCompose ApplicationType = 1
)

type Application struct {
	Name string
	Type ApplicationType
}

// Service is the information found about a given service.
type Service struct {
	Config          config.Service
	Name            string
	Instance        string
	Tags            []string
	Applications    []Application
	ServiceType     ServiceName
	ContainerID     string
	ContainerName   string // If ContainerName is set, Instance must be the same value.
	IPAddress       string // IPAddress is the IPv4 address to reach service for metrics gathering. If empty, it means IP was not found
	ListenAddresses []facts.ListenAddress
	ExePath         string
	IgnoredPorts    map[int]bool
	Active          bool
	CheckIgnored    bool
	MetricsIgnored  bool
	// The interval of the check, used only for custom checks.
	Interval time.Duration
	// May be nil if no log processing should be applied.
	LogProcessing []ServiceLogReceiver

	HasNetstatInfo  bool
	LastNetstatInfo time.Time
	container       facts.Container
	LastTimeSeen    time.Time
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

// defaultPort returns the port this service is expected to serve, before the listen
// addresses are consulted. It is di.ServicePort for almost everything; a service whose
// port depends on its version has it chosen from the executable instead.
func (s Service) defaultPort(di discoveryInfo) int {
	if len(di.ServicePortByExe) > 0 && s.ExePath != "" {
		if port, ok := di.ServicePortByExe[filepath.Base(s.ExePath)]; ok {
			return port
		}
	}

	return di.ServicePort
}

// AddressPort return the IP address &port for the "main" service (e.g. for RabbitMQ the AMQP port, not the management port).
func (s Service) AddressPort() (string, int) {
	di := servicesDiscoveryInfo[s.ServiceType]
	port := s.defaultPort(di)
	force := false

	if s.Config.Port != 0 {
		port = s.Config.Port
	}

	if port == 0 {
		return "", 0
	}

	if address := s.AddressForPort(port, di.ServiceProtocol, force); address != "" {
		return address, port
	}

	// Not listening on the expected port. For a service that serves different ports in
	// different versions this is the case where the executable said nothing, so the
	// version has to be read from where it is actually listening.
	if s.Config.Port == 0 {
		for _, alt := range di.AltServicePorts {
			if address := s.AddressForPort(alt, di.ServiceProtocol, false); address != "" {
				return address, alt
			}
		}
	}

	return s.AddressForPort(port, di.ServiceProtocol, force), port
}

// LabelsOfStatus returns the labels for the status metrics of this service.
func (s Service) LabelsOfStatus() map[string]string {
	lblSlice := []labels.Label{
		{Name: types.LabelName, Value: types.MetricServiceStatus},
		{Name: types.LabelService, Value: s.Name},
	}

	if s.Instance != "" {
		lblSlice = append(lblSlice, labels.Label{Name: types.LabelServiceInstance, Value: s.Instance})
	}

	lbls := model.AnnotationToMetaLabels(labels.New(lblSlice...), s.AnnotationsOfStatus())

	return lbls.Map()
}

// AnnotationsOfStatus returns the annotations for the status metrics of this service.
func (s Service) AnnotationsOfStatus() types.MetricAnnotations {
	annotations := types.MetricAnnotations{
		ServiceName:     s.Name,
		ServiceInstance: s.Instance,
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
			alreadyExist := slices.Contains(s.ListenAddresses, v)

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
		ActiveMQService: {
			ServicePort:     8161,
			ServiceProtocol: tcpProtocol,
		},
		ApacheService: {
			ServicePort:     80,
			ServiceProtocol: tcpProtocol,
		},
		BitBucketService: {
			ServicePort:     7990,
			ServiceProtocol: tcpProtocol,
			IgnoreHighPort:  true,
			DefaultIgnoredPorts: map[int]bool{
				5701: true,
			},
		},
		BindService: {
			ServicePort:     53,
			ServiceProtocol: tcpProtocol,
		},
		CassandraService: {
			ServicePort:     9042,
			ServiceProtocol: tcpProtocol,
			IgnoreHighPort:  true,
		},
		ClickHouseService: {
			ServicePort:     8123,
			ServiceProtocol: tcpProtocol,
		},
		ConfluenceService: {
			ServicePort:     8090,
			ServiceProtocol: tcpProtocol,
			IgnoreHighPort:  true,
		},
		ConsulService: {
			ServicePort:     8500,
			ServiceProtocol: tcpProtocol,
		},
		DovecotService: {
			ServicePort:     143,
			ServiceProtocol: tcpProtocol,
		},
		ElasticSearchService: {
			ServicePort:     9200,
			ServiceProtocol: tcpProtocol,
			IgnoreHighPort:  true,
		},
		EjabberService: {
			ServicePort:     5222,
			ServiceProtocol: tcpProtocol,
			IgnoreHighPort:  true,
		},
		EximService: {
			ServicePort:     25,
			ServiceProtocol: tcpProtocol,
		},
		Fail2banService: {},
		HAProxyService: {
			IgnoreHighPort:  true, // HAProxy use a random high-port when Syslog over-UDP is enabled.
			ServiceProtocol: tcpProtocol,
		},
		InfluxDBService: {
			// 3.x's port, which its own CLI defaults to, for the "influxdb3" binary and
			// for anything unrecognised. 1.x and 2.x serve 8086 from "influxd", and are
			// told apart from 3.x by that name alone -- their own command lines are
			// identical to each other.
			ServicePort:      8181,
			ServicePortByExe: map[string]int{"influxd": 8086},
			AltServicePorts:  []int{8086},
			ServiceProtocol:  tcpProtocol,
		},
		JenkinsService: {
			ServicePort:     8080,
			ServiceProtocol: tcpProtocol,
			IgnoreHighPort:  true,
		},
		JIRAService: {
			ServicePort:     8080,
			ServiceProtocol: tcpProtocol,
			IgnoreHighPort:  true,
		},
		KafkaService: {
			ServicePort:     9092,
			ServiceProtocol: tcpProtocol,
			IgnoreHighPort:  true,
		},
		MariaDBService: {
			ServicePort:     3306,
			ServiceProtocol: tcpProtocol,
		},
		MemcachedService: {
			ServicePort:     11211,
			ServiceProtocol: tcpProtocol,
		},
		MongoDBService: {
			ServicePort:     27017,
			ServiceProtocol: tcpProtocol,
		},
		MosquittoService: {
			ServicePort:     1883,
			ServiceProtocol: tcpProtocol,
		},
		MySQLService: {
			ServicePort:     3306,
			ServiceProtocol: tcpProtocol,
		},
		NginxService: {
			ServicePort:     80,
			ServiceProtocol: tcpProtocol,
		},
		NatsService: {
			ServicePort:     4222,
			ServiceProtocol: tcpProtocol,
		},
		NfsService: {},
		NSQService: {
			ServicePort:     4151,
			ServiceProtocol: tcpProtocol,
		},
		NTPService: {
			ServicePort:     123,
			ServiceProtocol: udpProtocol,
		},
		OpenBaoService: {
			ServicePort:     8200,
			ServiceProtocol: tcpProtocol,
		},
		OpenLDAPService: {
			ServicePort:     389,
			ServiceProtocol: tcpProtocol,
		},
		OpenVPNService: {
			DisablePersistentConnection: true,
		},
		PHPFPMService: {
			ServiceProtocol: tcpProtocol,
		},
		PostfixService: {
			ServicePort:     25,
			ServiceProtocol: tcpProtocol,
		},
		PostgreSQLService: {
			ServicePort:     5432,
			ServiceProtocol: tcpProtocol,
		},
		PgBouncerService: {
			ServicePort:     6432,
			ServiceProtocol: tcpProtocol,
		},
		RabbitMQService: {
			ServicePort:     5672,
			ServiceProtocol: tcpProtocol,
			IgnoreHighPort:  true,
		},
		RedisService: {
			ServicePort:     6379,
			ServiceProtocol: tcpProtocol,
		},
		SaltMasterService: {
			ServicePort:     4505,
			ServiceProtocol: tcpProtocol,
		},
		SquidService: {
			ServicePort:     3128,
			ServiceProtocol: tcpProtocol,
		},
		TomcatService: {
			ServicePort:     8080,
			ServiceProtocol: tcpProtocol,
			IgnoreHighPort:  true,
		},
		UPSDService: {
			ServicePort:     3493,
			ServiceProtocol: tcpProtocol,
		},
		UWSGIService: {
			IgnoreHighPort: true,
		},
		ValkeyService: {
			ServicePort:     6379,
			ServiceProtocol: tcpProtocol,
		},
		VarnishService: {
			ServicePort:     6082,
			ServiceProtocol: tcpProtocol,
		},
		VaultService: {
			ServicePort:     8200,
			ServiceProtocol: tcpProtocol,
		},
		ZookeeperService: {
			ServicePort:     2181,
			ServiceProtocol: tcpProtocol,
			IgnoreHighPort:  true,
		},

		CustomService: {},
	}
)

type discoveryInfo struct {
	ServicePort int
	// ServicePortByExe overrides ServicePort when the service's executable has one of
	// these base names, for a service whose port depends on which version is running.
	// It is keyed on the executable because that is the one thing the command line does
	// tell apart: InfluxDB 1.x and 2.x serve 8086 from a binary called "influxd", where
	// 3.x serves 8181 from "influxdb3".
	ServicePortByExe map[string]int
	// AltServicePorts are other ports this service type is known to serve. They are tried
	// when it is not listening on the one chosen above, which is what happens when the
	// executable is unknown -- a manually configured service, or a container whose process
	// Glouton cannot see -- and the version therefore cannot be told from it.
	AltServicePorts             []int
	ServiceProtocol             string // "tcp", "udp" or "unix"
	IgnoreHighPort              bool
	DisablePersistentConnection bool
	DefaultIgnoredPorts         map[int]bool
}

func serviceListToMap(services []Service) map[NameInstance]Service {
	servicesMap := make(map[NameInstance]Service, len(services))

	for _, service := range services {
		key := NameInstance{
			Name:     service.Name,
			Instance: service.Instance,
		}

		servicesMap[key] = service
	}

	return servicesMap
}
