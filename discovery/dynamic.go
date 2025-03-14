// Copyright 2015-2025 Bleemeo
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
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/version"

	"dario.cat/mergo"
	"github.com/go-viper/mapstructure/v2"
	"gopkg.in/ini.v1"
)

const (
	mysqlDefaultUser            = "root"
	gloutonContainerLabelPrefix = "glouton."
)

type Option struct {
	PS                 processFact
	Netstat            netstatProvider
	ContainerInfo      containerInfoProvider
	IsContainerIgnored func(facts.Container) bool
	IsServiceIgnored   func(Service) bool
	FileReader         fileReader
	DefaultStack       string
}

// DynamicDiscovery implement the dynamic discovery. It will only return
// service dynamically discovery from processes list, containers running, ...
// It don't include manually configured service or previously detected services.
type DynamicDiscovery struct {
	l sync.Mutex

	option Option
	now    func() time.Time
}

type containerInfoProvider interface {
	Containers(ctx context.Context, maxAge time.Duration, includeIgnored bool) (containers []facts.Container, err error)
	CachedContainer(containerID string) (c facts.Container, found bool)
}

type fileReader interface {
	ReadFile(ctx context.Context, filename string) ([]byte, error)
}

// NewDynamic create a new dynamic service discovery which use information from
// processes and netstat to discovery services.
func NewDynamic(opts Option) *DynamicDiscovery {
	dd := &DynamicDiscovery{
		now:    time.Now,
		option: opts,
	}

	return dd
}

// Discovery detect service running on the system and return a list of Service object.
// It also return a date for wanted next update.
func (dd *DynamicDiscovery) Discovery(ctx context.Context) ([]Service, time.Time, error) {
	if version.IsFreeBSD() {
		// Disable service discovery on FreeBSD for now. Glouton only support TrueNAS which don't have lots
		// of services (especially not lots of service we support).
		// Before re-enable this, we should fix our netstat on FreeBSD which isn't working:
		// * We don't use correct option in netstat
		// * The gopsutil Connections() isn't tested at all
		// * On TrueNAS service run in jail, so we must handle them (probably similar to what we do for Docker).
		return nil, time.Time{}, nil
	}

	dd.l.Lock()
	defer dd.l.Unlock()

	return dd.updateDiscovery(ctx)
}

// ProcessServiceInfo return the service & container a process belong based on its command line + pid & start time.
func (dd *DynamicDiscovery) ProcessServiceInfo(cmdLine []string, pid int, createTime time.Time) (serviceName ServiceName, containerName string) {
	serviceType, ok := serviceByCommand(cmdLine)
	if !ok {
		return "", ""
	}

	processes := dd.option.PS.GetLatest()

	// gopsutil round create time to second. So we do equality at second precision only
	createTime = createTime.Truncate(time.Second)

	for _, p := range processes {
		if p.PID == pid && p.CreateTime.Truncate(time.Second).Equal(createTime) {
			if p.ContainerID != "" {
				container, ok := dd.option.ContainerInfo.CachedContainer(p.ContainerID)
				if !ok || dd.option.IsContainerIgnored(container) {
					return "", ""
				}
			}

			return serviceType, p.ContainerName
		}
	}

	return "", ""
}

//nolint:gochecknoglobals
var (
	knownProcesses = map[string]ServiceName{
		"apache2":       ApacheService,
		"asterisk":      AsteriskService,
		"dovecot":       DovecotService,
		"exim4":         EximService,
		"exim":          EximService,
		"freeradius":    FreeradiusService,
		"haproxy":       HAProxyService,
		"httpd":         ApacheService,
		"influxd":       InfluxDBService,
		"libvirtd":      LibvirtService,
		"master":        PostfixService,
		"memcached":     MemcachedService,
		"mongod":        MongoDBService,
		"mosquitto":     MosquittoService, //nolint:misspell
		"mysqld":        MySQLService,
		"named":         BindService,
		"nats-server":   NatsService,
		"nfsiod":        NfsService,
		"nginx":         NginxService,
		"ntpd":          NTPService,
		"openvpn":       OpenVPNService,
		"php-fpm":       PHPFPMService,
		"postgres":      PostgreSQLService,
		"redis-server":  RedisService,
		"slapd":         OpenLDAPService,
		"squid3":        SquidService,
		"squid":         SquidService,
		"upsd":          UPSDService,
		"uwsgi":         UWSGIService,
		"uWSGI":         UWSGIService,
		"valkey-server": ValkeyService,
		"varnishd":      VarnishService,
	}
	knownInterpretedProcess = []struct {
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
			CmdLineMustContains: []string{"jenkins.war"},
			ServiceName:         JenkinsService,
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
			CmdLineMustContains: []string{"kafka.Kafka", "server.properties"},
			ServiceName:         KafkaService,
			Interpreter:         "java",
		},
		{
			CmdLineMustContains: []string{"salt-master"},
			ServiceName:         SaltMasterService,
			Interpreter:         "python",
		},
		{
			CmdLineMustContains: []string{"fail2ban-server"},
			ServiceName:         Fail2banService,
			Interpreter:         "python",
		},
	}
)

type processFact interface {
	GetLatest() map[int]facts.Process
	UpdateProcesses(ctx context.Context) (processes map[int]facts.Process, nextUpdate time.Time, err error)
}

type netstatProvider interface {
	Netstat(ctx context.Context, processes map[int]facts.Process) (netstat map[int][]facts.ListenAddress, err error)
}

// Only one updateDiscovery should be running at a time.
func (dd *DynamicDiscovery) updateDiscovery(ctx context.Context) ([]Service, time.Time, error) {
	processes, nextUpdate, err := dd.option.PS.UpdateProcesses(ctx)
	if err != nil {
		return nil, nextUpdate, err
	}

	netstat, err := dd.option.Netstat.Netstat(ctx, processes)
	if err != nil && !os.IsNotExist(err) {
		logger.V(1).Printf("An error occurred while trying to retrieve netstat information: %v", err)
	}

	if ctx.Err() != nil {
		return nil, nextUpdate, ctx.Err()
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

	sort.Slice(allPids, func(i, j int) bool {
		// Kept netstat PID first, then sort by PID value
		pidI := allPids[i]
		pidJ := allPids[j]

		_, inNetstatI := netstat[pidI]
		_, inNetstatJ := netstat[pidJ]

		switch {
		case inNetstatI == inNetstatJ:
			return pidI < pidJ
		default:
			return inNetstatI
		}
	})

	servicesMap := make(map[NameInstance]Service)

	for _, pid := range allPids {
		process, ok := processes[pid]
		if !ok {
			continue
		}

		service, ok := dd.serviceFromProcess(ctx, process, netstat)
		if !ok {
			continue
		}

		key := NameInstance{
			Name:     service.Name,
			Instance: service.Instance,
		}

		if existingService, ok := servicesMap[key]; ok {
			service = existingService.merge(service)

			servicesMap[key] = service

			logger.V(2).Printf(
				"Update discovered service %v from PID %d (%s) with IP %s",
				service,
				process.PID,
				process.Name,
				service.IPAddress,
			)
		} else {
			servicesMap[key] = service

			logger.V(2).Printf(
				"Discovered service %v from PID %d (%s) with IP %s",
				service,
				process.PID,
				process.Name,
				service.IPAddress,
			)
		}
	}

	if ctx.Err() != nil {
		return nil, nextUpdate, ctx.Err()
	}

	// Resolve possible conflict of listen address. If two services are discovered in the same containers, it's
	// possible for two different service to have the same listening address... which is unlikely.
	// When a conflict occur, only kept port that are associated with the standard port of the service.
	services := fixListenAddressConflict(servicesMap)

	return services, nextUpdate, nil
}

func (dd *DynamicDiscovery) serviceFromProcess(ctx context.Context, process facts.Process, netstat map[int][]facts.ListenAddress) (Service, bool) {
	if process.Status == "zombie" {
		return Service{}, false
	}

	serviceType, ok := serviceByCommand(process.CmdLineList)
	if !ok {
		return Service{}, false
	}

	service := Service{
		ServiceType:   serviceType,
		Name:          string(serviceType),
		ContainerID:   process.ContainerID,
		ContainerName: process.ContainerName,
		Instance:      process.ContainerName,
		ExePath:       process.Executable,
		Active:        true,
		LastTimeSeen:  dd.now(),
	}

	if service.ContainerID != "" {
		service.container, ok = dd.option.ContainerInfo.CachedContainer(service.ContainerID)
		if !ok || dd.option.IsContainerIgnored(service.container) {
			return Service{}, false
		}
	}

	if dd.option.IsServiceIgnored != nil && dd.option.IsServiceIgnored(service) {
		return Service{}, false
	}

	di := getDiscoveryInfo(dd.now(), &service, netstat, process.PID)

	dd.updateListenAddresses(&service, di)

	dd.fillConfig(ctx, &service)
	dd.fillConfigFromLabels(&service)
	dd.discoveryFromLabels(&service)
	dd.guessJMX(&service, process.CmdLineList)

	return service, true
}

func getDiscoveryInfo(now time.Time, service *Service, netstat map[int][]facts.ListenAddress, pid int) discoveryInfo {
	if service.ContainerID == "" {
		service.ListenAddresses = netstat[pid]
	} else {
		service.ListenAddresses = service.container.ListenAddresses()
		service.IgnoredPorts = facts.ContainerIgnoredPorts(service.container)

		service.ListenAddresses = excludeEmptyAddress(service.ListenAddresses)

		if len(service.ListenAddresses) == 0 {
			service.ListenAddresses = netstat[pid]
		}
	}

	if len(service.ListenAddresses) > 0 {
		service.HasNetstatInfo = true
		service.LastNetstatInfo = now
	}

	di := servicesDiscoveryInfo[service.ServiceType]

	for port, ignore := range di.DefaultIgnoredPorts {
		if !ignore {
			continue
		}

		if _, ok := service.IgnoredPorts[port]; !ok {
			if service.IgnoredPorts == nil {
				service.IgnoredPorts = make(map[int]bool)
			}

			service.IgnoredPorts[port] = ignore
		}
	}

	return di
}

func (dd *DynamicDiscovery) updateListenAddresses(service *Service, di discoveryInfo) {
	defaultAddress := localhostIP

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

// fillConfig fills the service config with information found inside the container.
func (dd *DynamicDiscovery) fillConfig(ctx context.Context, service *Service) {
	if service.ServiceType == MySQLService {
		if service.container != nil {
			for k, v := range service.container.Environment() {
				if k == "MYSQL_ROOT_PASSWORD" {
					service.Config.Username = mysqlDefaultUser
					service.Config.Password = v
				}
			}
		} else if dd.option.FileReader != nil {
			if debianCnfRaw, err := dd.option.FileReader.ReadFile(ctx, "/etc/mysql/debian.cnf"); err == nil {
				if debianCnf, err := ini.Load(debianCnfRaw); err == nil {
					section := debianCnf.Section("client")
					if section != nil {
						service.Config.Username = iniSafeString(section.Key("user"))
						service.Config.Password = iniSafeString(section.Key("password"))
						service.Config.MetricsUnixSocket = iniSafeString(section.Key("socket"))
					}
				}
			}
		}
	}

	if service.ServiceType == PostgreSQLService {
		if service.container != nil {
			for k, v := range service.container.Environment() {
				if k == "POSTGRES_PASSWORD" {
					service.Config.Password = v
				}

				if k == "POSTGRES_USER" {
					service.Config.Username = v
				}
			}
		}
	}
}

// fillConfigFromLabels look for "glouton.*" labels on containers to override the service configuration.
func (dd *DynamicDiscovery) fillConfigFromLabels(service *Service) {
	if service.container == nil {
		return
	}

	// Make a map of container labels and values with the "glouton." prefix trimmed.
	labels := make(map[string]string)

	for k, v := range facts.LabelsAndAnnotations(service.container) {
		if !strings.HasPrefix(k, gloutonContainerLabelPrefix) {
			continue
		}

		k = strings.TrimPrefix(k, gloutonContainerLabelPrefix)

		labels[k] = v
	}

	if len(labels) == 0 {
		return
	}

	// Decode the map in the service struct.
	var override config.Service

	decoderConfig := &mapstructure.DecoderConfig{
		DecodeHook: mapstructure.ComposeDecodeHookFunc(
			mapstructure.StringToSliceHookFunc(","),
			config.StringToIntSliceHookFunc(","),
		),
		Result:           &override,
		WeaklyTypedInput: true,
		TagName:          config.Tag,
	}

	d, err := mapstructure.NewDecoder(decoderConfig)
	if err != nil {
		logger.V(1).Printf("Failed to create decoder for container labels: %s", err)

		return
	}

	if err := d.Decode(labels); err != nil {
		logger.Printf("Labels on container %s could not be decoded: %s", service.ContainerName, err)
	}

	// Override the current config with the labels from the container.
	err = mergo.Merge(&service.Config, override, mergo.WithOverride)
	if err != nil {
		logger.V(1).Printf("Failed to merge service and override: %s", err)
	}
}

// discoveryFromLabels look labels on containers that add information on service.
func (dd *DynamicDiscovery) discoveryFromLabels(service *Service) {
	if service.container == nil {
		return
	}

	labels := facts.LabelsAndAnnotations(service.container)

	if composeProject := labels["com.docker.compose.project"]; composeProject != "" {
		service.Applications = append(service.Applications, Application{
			Name: composeProject,
			Type: ApplicationDockerCompose,
		})
	}
}

func (dd *DynamicDiscovery) guessJMX(service *Service, cmdLine []string) {
	jmxOptions := []string{
		"-Dcom.sun.management.jmxremote.port=",
		"-Dcassandra.jmx.remote.port=",
	}
	if service.IPAddress == localhostIP {
		jmxOptions = append(jmxOptions, "-Dcassandra.jmx.local.port=")
	}

	switch service.ServiceType { //nolint:exhaustive
	case CassandraService, ElasticSearchService, ZookeeperService, BitBucketService,
		JIRAService, ConfluenceService, KafkaService:
		for _, arg := range cmdLine {
			for _, opt := range jmxOptions {
				if !strings.HasPrefix(arg, opt) {
					continue
				}

				portStr := strings.TrimPrefix(arg, opt)

				port, err := strconv.ParseInt(portStr, 10, 0)
				if err != nil {
					continue
				}

				service.Config.JMXPort = int(port)

				return
			}
		}
	}
}

func serviceByCommand(cmdLine []string) (serviceName ServiceName, found bool) {
	if len(cmdLine) == 0 {
		return "", false
	}

	name := filepath.Base(cmdLine[0])

	if runtime.GOOS == "windows" {
		name = strings.ToLower(name)
		name = strings.TrimSuffix(name, ".exe")
	}

	if name == "" {
		return "", false
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
	if len(alteredName) > 0 && alteredName[len(alteredName)-1] == ':' {
		if serviceName, ok := knownProcesses[alteredName[:len(alteredName)-1]]; ok {
			return serviceName, ok
		}
	}

	serviceName, ok := serviceByInterpreter(name, cmdLine)

	if ok {
		return serviceName, ok
	}

	serviceName, ok = knownProcesses[name]

	return serviceName, ok
}

func serviceByInterpreter(name string, cmdLine []string) (serviceName ServiceName, found bool) {
	// For now, special case for java, erlang or python process.
	// Need a more general way to manage those case. Every interpreter/VM
	// language are affected.
	if name == "java" || strings.HasPrefix(name, "python") || name == "erl" || strings.HasPrefix(name, "beam") {
		for _, candidate := range knownInterpretedProcess {
			switch candidate.Interpreter {
			case "erlang":
				if name != "erl" && !strings.HasPrefix(name, "beam") {
					continue
				}
			case "python":
				if !strings.HasPrefix(name, "python") {
					continue
				}
			default:
				if name != candidate.Interpreter {
					continue
				}
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

	return "", false
}

// excludeEmptyAddress exclude entry with empty Address IP. It will modify input.
func excludeEmptyAddress(addresses []facts.ListenAddress) []facts.ListenAddress {
	n := 0

	for _, x := range addresses {
		if x.Address != "" {
			addresses[n] = x
			n++
		}
	}

	addresses = addresses[:n]

	return addresses
}

func iniSafeString(key *ini.Key) string {
	if key == nil {
		return ""
	}

	return key.String()
}

// fixListenAddressConflict resolve conflict when two service listen on the same address.
func fixListenAddressConflict(servicesMap map[NameInstance]Service) []Service {
	services := make([]Service, 0, len(servicesMap))
	addressesMap := make(map[facts.ListenAddress][]NameInstance)

	for k, v := range servicesMap {
		for _, address := range v.ListenAddresses {
			addressesMap[address] = append(addressesMap[address], k)
		}
	}

	for _, v := range servicesMap {
		v = filterConflictListenAddress(v, servicesMap, addressesMap)

		services = append(services, v)
	}

	return services
}

// filterConflictListenAddress remove address that have a conflict. It might mutate service input.
func filterConflictListenAddress(service Service, servicesMap map[NameInstance]Service, addressesMap map[facts.ListenAddress][]NameInstance) Service {
	var newListenAddress []facts.ListenAddress //nolint:prealloc // prealloc is used

	key := NameInstance{service.Name, service.Instance}

	for i, addr := range service.ListenAddresses {
		if len(addressesMap[addr]) == 1 && newListenAddress == nil {
			// fast path: if there is no address conflict, do no copy and no mutation
			continue
		}

		if len(addressesMap[addr]) == 1 {
			newListenAddress = append(newListenAddress, addr)

			continue
		}

		if newListenAddress == nil {
			// First conflict for this service. service.ListenAddresses need to be modified but a copy must be
			// done (as this slice is shared with all services in the same containers).
			// Do a copy now.
			newListenAddress = make([]facts.ListenAddress, i)
			copy(newListenAddress, service.ListenAddresses)
		}

		// There is a conflict. Two case:
		// * either addr is using the standard port of current service AND
		//   it isn't the standard port of any other service. Then we kept the address
		// * or we don't kept the address because we aren't confident enough that this address belong to this service and not
		//   another one.

		if addr.Port != servicesDiscoveryInfo[service.ServiceType].ServicePort {
			continue
		}

		isStandardPortOfAnotherService := false

		for _, otherServiceKey := range addressesMap[addr] {
			if otherServiceKey == key {
				continue
			}

			otherService := servicesMap[otherServiceKey]

			if addr.Port == servicesDiscoveryInfo[otherService.ServiceType].ServicePort {
				isStandardPortOfAnotherService = true

				break
			}
		}

		if isStandardPortOfAnotherService {
			continue
		}

		newListenAddress = append(newListenAddress, addr) //nolint:makezero // but we fill the non-zero length part with a copy just after preallocation
	}

	if newListenAddress == nil {
		return service
	}

	if len(service.ListenAddresses) > 0 && len(newListenAddress) == 0 {
		// clear has netstat info. The netstat information didn't allowed to known port on which this
		// service listen (if it does).
		service.HasNetstatInfo = false
		service.LastNetstatInfo = time.Time{}
	}

	service.ListenAddresses = newListenAddress

	return service
}
