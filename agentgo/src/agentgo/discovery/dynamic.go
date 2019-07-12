// Package discovery contains function related to the service discovery.
package discovery

import (
	"agentgo/facts"
	"context"
	"fmt"
	"log"
	"net"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DynamicDiscovery implement the dynamic discovery. It will only return
// service dynamically discovery from processes list, containers running, ...
// It don't include manually configured service or previously detected services.
type DynamicDiscovery struct {
	l sync.Mutex

	ps            processFact
	netstat       netstatProvider
	containerInfo containerInfoProvider

	lastDiscoveryUpdate time.Time
	services            []Service
}

type containerInfoProvider interface {
	ContainerNetworkInfo(containerID string) (ipAddress string, listenAddresses []net.Addr)
	ContainerEnv(containerID string) []string
}

// NewDynamic create a new dynamic service discovery which use information from
// processess and netstat to discovery services
func NewDynamic(ps processFact, netstat netstatProvider, containerInfo containerInfoProvider) *DynamicDiscovery {
	return &DynamicDiscovery{
		ps:            ps,
		netstat:       netstat,
		containerInfo: containerInfo,
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

// nolint:gochecknoglobals
var (
	knownProcesses = map[string]string{
		"apache2":      "apache",
		"haproxy":      "haproxy",
		"httpd":        "apache",
		"memcached":    "memcached",
		"mongod":       "mongodb",
		"mysqld":       "mysql",
		"nginx:":       "nginx",
		"php-fpm:":     "phpfpm",
		"postgres":     "postgresql",
		"redis-server": "redis",
	}
	knownIntepretedProcess = []struct {
		CmdLineMustContains []string
		ServiceName         string
		Interpreter         string
	}{
		{
			CmdLineMustContains: []string{"org.elasticsearch.bootstrap.Elasticsearch"},
			ServiceName:         "elasticsearch",
			Interpreter:         "java",
		},
		{
			CmdLineMustContains: []string{"-s rabbit"},
			ServiceName:         "rabbitmq",
			Interpreter:         "erlang",
		},
	}
)

type listenAddress struct {
	network string
	address string
}

func (l listenAddress) Network() string {
	return l.network
}
func (l listenAddress) String() string {
	return l.address
}

type processFact interface {
	Processes(ctx context.Context, maxAge time.Duration) (processes map[int]facts.Process, err error)
}

type netstatProvider interface {
	Netstat(ctx context.Context) (netstat map[int][]net.Addr, err error)
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
		serviceName, ok := serviceByCommand(process.CmdLine)
		if !ok {
			continue
		}
		service := Service{
			Name:          serviceName,
			ContainerID:   process.ContainerID,
			ContainerName: process.ContainerName,
			ExePath:       process.Executable,
			Active:        true,
		}

		key := nameContainer{
			name:        service.Name,
			containerID: service.ContainerID,
		}
		if _, ok := servicesMap[key]; ok {
			continue
		}

		if service.ContainerID == "" {
			service.ListenAddresses = netstat[pid]
		} else {
			service.IPAddress, service.ListenAddresses = dd.containerInfo.ContainerNetworkInfo(service.ContainerID)
		}
		if len(service.ListenAddresses) > 0 {
			service.hasNetstatInfo = true
		}

		di := servicesDiscoveryInfo[serviceName]

		dd.updateListenAddresses(&service, di)

		dd.fillExtraAttributes(&service)
		// TODO: jmx ?

		log.Printf("DBG2: Discovered service %v", service)
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
	newListenAddresses := service.ListenAddresses[:0]
	for _, a := range service.ListenAddresses {
		if a.Network() == "unix" {
			newListenAddresses = append(newListenAddresses, a)
			continue
		}
		address, portStr, err := net.SplitHostPort(a.String())
		if err != nil {
			log.Printf("DBG: unable to split host/port for %#v: %v", a.String(), err)
			newListenAddresses = append(newListenAddresses, a)
			continue
		}
		port, err := strconv.ParseInt(portStr, 10, 0)
		if err != nil {
			log.Printf("DBG: unable to parse port %#v: %v", portStr, err)
			newListenAddresses = append(newListenAddresses, a)
			continue
		}
		if int(port) == di.ServicePort && a.Network() == di.ServiceProtocol && address != "0.0.0.0" {
			defaultAddress = address
		}
		if !di.IgnoreHighPort || port <= 32000 {
			newListenAddresses = append(newListenAddresses, a)
		}
	}
	service.ListenAddresses = newListenAddresses
	if service.ContainerID == "" {
		service.IPAddress = defaultAddress
	}
	if len(service.ListenAddresses) == 0 && di.ServicePort != 0 {
		// If netstat seems to have failed, always add the main service port
		service.ListenAddresses = append(service.ListenAddresses, listenAddress{network: di.ServiceProtocol, address: fmt.Sprintf("%s:%d", service.IPAddress, di.ServicePort)})
	}
}

func (dd *DynamicDiscovery) fillExtraAttributes(service *Service) {
	if service.ExtraAttributes == nil {
		service.ExtraAttributes = make(map[string]string)
	}
	if service.Name == "mysql" {
		if service.ContainerID != "" {
			for _, e := range dd.containerInfo.ContainerEnv(service.ContainerID) {
				if strings.HasPrefix(e, "MYSQL_ROOT_PASSWORD=") {
					service.ExtraAttributes["username"] = "root"
					service.ExtraAttributes["password"] = strings.TrimPrefix(e, "MYSQL_ROOT_PASSWORD=")
				}
			}
		} else {
			log.Printf("DBG2: TODO use sudo -n cat /etc/mysql/debian.cnf")
		}
	}
	if service.Name == "postgresql" {
		if service.ContainerID != "" {
			for _, e := range dd.containerInfo.ContainerEnv(service.ContainerID) {
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

func serviceByCommand(cmdLine []string) (serviceName string, found bool) {
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
