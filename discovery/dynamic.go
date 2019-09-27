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

// Package discovery contains function related to the service discovery.
package discovery

import (
	"context"
	"glouton/facts"
	"glouton/logger"
	"net"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/ini.v1"
)

// DynamicDiscovery implement the dynamic discovery. It will only return
// service dynamically discovery from processes list, containers running, ...
// It don't include manually configured service or previously detected services.
type DynamicDiscovery struct {
	l sync.Mutex

	ps            processFact
	netstat       netstatProvider
	containerInfo containerInfoProvider
	fileReader    fileReader
	defaultStack  string

	lastDiscoveryUpdate time.Time
	services            []Service
}

type container interface {
	Env() []string
	PrimaryAddress() string
	ListenAddresses() []facts.ListenAddress
	Labels() map[string]string
	Ignored() bool
}

type containerInfoProvider interface {
	Container(containerID string) (c container, found bool)
}

type fileReader interface {
	ReadFile(filename string) ([]byte, error)
}

// NewDynamic create a new dynamic service discovery which use information from
// processess and netstat to discovery services
func NewDynamic(ps processFact, netstat netstatProvider, containerInfo *facts.DockerProvider, fileReader fileReader, defaultStack string) *DynamicDiscovery {
	return &DynamicDiscovery{
		ps:            ps,
		netstat:       netstat,
		containerInfo: (*dockerWrapper)(containerInfo),
		fileReader:    fileReader,
		defaultStack:  defaultStack,
	}
}

// Discovery detect service running on the system and return a list of Service object.
func (dd *DynamicDiscovery) Discovery(ctx context.Context, maxAge time.Duration) (services []Service, err error) {
	dd.l.Lock()
	defer dd.l.Unlock()

	if time.Since(dd.lastDiscoveryUpdate) > maxAge {
		err = dd.updateDiscovery(ctx, maxAge)
		if err != nil {
			return
		}
	}

	return dd.services, nil
}

// LastUpdate return when the last update occurred
func (dd *DynamicDiscovery) LastUpdate() time.Time {
	dd.l.Lock()
	defer dd.l.Unlock()
	return dd.lastDiscoveryUpdate
}

// nolint:gochecknoglobals
var (
	knownProcesses = map[string]ServiceName{
		"apache2":      ApacheService,
		"asterisk":     AsteriskService,
		"dovecot":      DovecoteService,
		"exim4":        EximService,
		"exim":         EximService,
		"freeradius":   FreeradiusService,
		"haproxy":      HAProxyService,
		"httpd":        ApacheService,
		"influxd":      InfluxDBService,
		"libvirtd":     LibvirtService,
		"master":       PostfixService,
		"memcached":    MemcachedService,
		"mongod":       MongoDBService,
		"mosquitto":    MosquittoService, //nolint:misspell
		"mysqld":       MySQLService,
		"named":        BindService,
		"nginx:":       NginxService,
		"ntpd":         NTPService,
		"openvpn":      OpenVPNService,
		"php-fpm:":     PHPFPMService,
		"postgres":     PostgreSQLService,
		"redis-server": RedisService,
		"slapd":        OpenLDAPService,
		"squid3":       SquidService,
		"squid":        SquidService,
		"varnishd":     VarnishService,
		"uwsgi":        UWSGIService,
	}
	knownIntepretedProcess = []struct {
		CmdLineMustContains []string
		ServiceName         ServiceName
		Interpreter         string
	}{
		{
			CmdLineMustContains: []string{"org.apache.cassandra.service.CassandraDaemon"},
			ServiceName:         CassandraService,
			Interpreter:         "java",
		},
		{
			CmdLineMustContains: []string{"org.elasticsearch.bootstrap.Elasticsearch"},
			ServiceName:         ElasticSearchService,
			Interpreter:         "java",
		},
		{
			CmdLineMustContains: []string{"org.apache.zookeeper.server.quorum.QuorumPeerMain"},
			ServiceName:         ZookeeperService,
			Interpreter:         "java",
		},
		{
			CmdLineMustContains: []string{"-s rabbit"},
			ServiceName:         RabbitMQService,
			Interpreter:         "erlang",
		},
		{
			CmdLineMustContains: []string{"-s ejabberd"},
			ServiceName:         EjabberService,
			Interpreter:         "erlang",
		},
		{
			CmdLineMustContains: []string{"com.atlassian.stash.internal.catalina.startup.Bootstrap"},
			ServiceName:         BitBucketService,
			Interpreter:         "java",
		},
		{
			CmdLineMustContains: []string{"com.atlassian.bitbucket.internal.launcher.BitbucketServerLauncher"},
			ServiceName:         BitBucketService,
			Interpreter:         "java",
		},
		{
			CmdLineMustContains: []string{"org.apache.catalina.startup.Bootstrap", "jira"},
			ServiceName:         JIRAService,
			Interpreter:         "java",
		},
		{
			CmdLineMustContains: []string{"org.apache.catalina.startup.Bootstrap", "confluence"},
			ServiceName:         ConfluenceService,
			Interpreter:         "java",
		},
		{
			CmdLineMustContains: []string{"salt-master"},
			ServiceName:         SaltMasterService,
			Interpreter:         "python",
		},
	}
)

type processFact interface {
	Processes(ctx context.Context, maxAge time.Duration) (processes map[int]facts.Process, err error)
}

type netstatProvider interface {
	Netstat(ctx context.Context) (netstat map[int][]facts.ListenAddress, err error)
}

type dockerWrapper facts.DockerProvider

func (dw *dockerWrapper) Container(containerID string) (c container, found bool) {
	c, found = (*facts.DockerProvider)(dw).Container(containerID)
	return
}

func (dd *DynamicDiscovery) updateDiscovery(ctx context.Context, maxAge time.Duration) error {
	processes, err := dd.ps.Processes(ctx, maxAge)
	if err != nil {
		return err
	}
	netstat, err := dd.netstat.Netstat(ctx)
	if err != nil {
		return err
	}

	// Process PID present in netstat output before other PID, because
	// two processes may listen on same port (e.g. multiple Apache process)
	// but netstat only see one of them.
	allPids := make([]int, 0, len(processes)+len(netstat))
	for p := range netstat {
		allPids = append(allPids, p)
	}
	for p := range processes {
		allPids = append(allPids, p)
	}

	servicesMap := make(map[nameContainer]Service)
	for _, pid := range allPids {
		process, ok := processes[pid]
		if !ok {
			continue
		}
		serviceName, ok := serviceByCommand(process.CmdLineList)
		if !ok {
			continue
		}
		service := Service{
			Name:          serviceName,
			ContainerID:   process.ContainerID,
			ContainerName: process.ContainerName,
			ExePath:       process.Executable,
			Active:        true,
			Stack:         dd.defaultStack,
		}

		key := nameContainer{
			name:          service.Name,
			containerName: service.ContainerName,
		}
		if _, ok := servicesMap[key]; ok {
			continue
		}

		if service.ContainerID != "" {
			service.container, ok = dd.containerInfo.Container(service.ContainerID)
			if !ok {
				continue
			}
			if service.container.Ignored() {
				continue
			}
			if stack, ok := service.container.Labels()["bleemeo.stack"]; ok {
				service.Stack = stack
			}
			if stack, ok := service.container.Labels()["glouton.stack"]; ok {
				service.Stack = stack
			}
		}

		if service.ContainerID == "" {
			service.ListenAddresses = netstat[pid]
		} else {
			service.ListenAddresses = service.container.ListenAddresses()
		}
		if len(service.ListenAddresses) > 0 {
			service.HasNetstatInfo = true
		}

		di := servicesDiscoveryInfo[serviceName]

		dd.updateListenAddresses(&service, di)

		dd.fillExtraAttributes(&service)
		// TODO: jmx ?

		logger.V(2).Printf("Discovered service %v", service)
		servicesMap[key] = service
	}

	dd.lastDiscoveryUpdate = time.Now()
	services := make([]Service, 0, len(servicesMap))
	for _, v := range servicesMap {
		services = append(services, v)
	}
	dd.services = services
	return nil
}

func (dd *DynamicDiscovery) updateListenAddresses(service *Service, di discoveryInfo) {
	defaultAddress := "127.0.0.1"
	if service.container != nil {
		defaultAddress = service.container.PrimaryAddress()
	}
	newListenAddresses := service.ListenAddresses[:0]
	for _, a := range service.ListenAddresses {
		if a.Network() == "unix" {
			newListenAddresses = append(newListenAddresses, a)
			continue
		}
		address, portStr, err := net.SplitHostPort(a.String())
		if err != nil {
			logger.V(1).Printf("unable to split host/port for %#v: %v", a.String(), err)
			newListenAddresses = append(newListenAddresses, a)
			continue
		}
		port, err := strconv.ParseInt(portStr, 10, 0)
		if err != nil {
			logger.V(1).Printf("unable to parse port %#v: %v", portStr, err)
			newListenAddresses = append(newListenAddresses, a)
			continue
		}
		if int(port) == di.ServicePort && a.Network() == di.ServiceProtocol && address != net.IPv4zero.String() {
			defaultAddress = address
		}
		if !di.IgnoreHighPort || port <= 32000 {
			newListenAddresses = append(newListenAddresses, a)
		}
	}
	service.ListenAddresses = newListenAddresses
	service.IPAddress = defaultAddress
	if len(service.ListenAddresses) == 0 && di.ServicePort != 0 {
		// If netstat seems to have failed, always add the main service port
		service.ListenAddresses = append(service.ListenAddresses, facts.ListenAddress{NetworkFamily: di.ServiceProtocol, Address: service.IPAddress, Port: di.ServicePort})
	}
}

func (dd *DynamicDiscovery) fillExtraAttributes(service *Service) {
	if service.ExtraAttributes == nil {
		service.ExtraAttributes = make(map[string]string)
	}
	if service.Name == "mysql" {
		if service.container != nil {
			for _, e := range service.container.Env() {
				if strings.HasPrefix(e, "MYSQL_ROOT_PASSWORD=") {
					service.ExtraAttributes["username"] = "root"
					service.ExtraAttributes["password"] = strings.TrimPrefix(e, "MYSQL_ROOT_PASSWORD=")
				}
			}
		} else if dd.fileReader != nil {
			if debianCnfRaw, err := dd.fileReader.ReadFile("/etc/mysql/debian.cnf"); err == nil {
				if debianCnf, err := ini.Load(debianCnfRaw); err == nil {
					service.ExtraAttributes["username"] = debianCnf.Section("client").Key("user").String()
					service.ExtraAttributes["password"] = debianCnf.Section("client").Key("password").String()
				}
			}
		}
	}
	if service.Name == "postgresql" {
		if service.container != nil {
			for _, e := range service.container.Env() {
				if strings.HasPrefix(e, "POSTGRES_PASSWORD=") {
					service.ExtraAttributes["password"] = strings.TrimPrefix(e, "POSTGRES_PASSWORD=")
				}
				if strings.HasPrefix(e, "POSTGRES_USER=") {
					service.ExtraAttributes["user"] = strings.TrimPrefix(e, "POSTGRES_USER=")
				}
			}
		}
	}
}

func serviceByCommand(cmdLine []string) (serviceName ServiceName, found bool) {
	name := filepath.Base(cmdLine[0])
	if runtime.GOOS == "windows" {
		name = strings.ToLower(name)
		name = strings.TrimSuffix(name, ".exe")
	}

	if name == "" {
		found = false
		return
	}

	// Some process alter their name to add information. Redis, nginx or php-fpm do this.
	// Example for Redis: "/usr/bin/redis-server *:6379".
	// Example for nginx: "'nginx: master process /usr/sbin/nginx [...]"
	// To catch first (Redis), take first "word", since no currently supported
	// service include a space.
	name = strings.Split(name, " ")[0]
	// To catch second (nginx and php-fpm), check if command starts with one word
	// immediately followed by ":".
	alteredName := strings.Split(cmdLine[0], " ")[0]
	if alteredName[len(alteredName)-2] == ':' {
		if serviceName, ok := knownProcesses[alteredName]; ok {
			return serviceName, ok
		}
	}

	// For now, special case for java, erlang or python process.
	// Need a more general way to manage those case. Every interpreter/VM
	// language are affected.

	if name == "java" || name == "python" || name == "erl" || strings.HasPrefix(name, "beam") {
		for _, candidate := range knownIntepretedProcess {
			if candidate.Interpreter == "erlang" && name != "erl" && !strings.HasPrefix(name, "beam") {
				continue
			}
			if candidate.Interpreter != "erlang" && name != candidate.Interpreter {
				continue
			}
			match := true
			flatCmdLine := strings.Join(cmdLine, " ")
			for _, m := range candidate.CmdLineMustContains {
				if !strings.Contains(flatCmdLine, m) {
					match = false
					break
				}
			}
			if match {
				return candidate.ServiceName, true
			}
		}
	}

	serviceName, ok := knownProcesses[name]
	return serviceName, ok
}
