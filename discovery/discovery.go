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

package discovery

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/crashreport"
	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/prometheus/registry"
	"github.com/bleemeo/glouton/task"
	"github.com/bleemeo/glouton/types"
	"github.com/bleemeo/glouton/utils/gloutonexec"

	"dario.cat/mergo"
	"github.com/influxdata/telegraf"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/model"
	"gopkg.in/yaml.v3"
)

var errNoCheckAssociated = errors.New("there is no check associated with the container")

const localhostIP = "127.0.0.1"

// discoveryTimeout is the time limit for a discovery.
const discoveryTimeout = time.Minute

// Discovery implements the full discovery mechanism.
// It will take information from both the dynamic discovery
// (service currently running) and previously detected services.
// It will configure metrics input and add them to a Collector.
type Discovery struct {
	l sync.Mutex

	commandRunner    *gloutonexec.Runner
	dynamicDiscovery Discoverer

	lastDynamicService    []Service
	discoveredServicesMap map[NameInstance]Service
	servicesMap           map[NameInstance]Service
	lastDiscoveryUpdate   time.Time

	triggerDiscovery      chan interface{}
	subscribers           []*subscriber
	lastConfigservicesMap map[NameInstance]Service
	activeCollector       map[NameInstance]collectorDetails
	activeCheck           map[NameInstance]CheckDetails
	metricRegistry        GathererRegistry
	containerInfo         containerInfoProvider
	state                 State
	servicesOverride      map[NameInstance]config.Service
	isServiceIgnored      func(Service) bool
	isCheckIgnored        func(Service) bool
	isInputIgnored        func(Service) bool
	isContainerIgnored    func(facts.Container) bool
	processFact           processFact

	absentServiceDeactivationDelay time.Duration
	logProcessingCfg               config.OpenTelemetry
}

type subscriber struct {
	closed  bool
	channel chan []Service
	l       sync.Mutex
}

// Registry will contains checks.
type Registry interface {
	AddTask(task task.Runner, shortName string) (int, error)
	RemoveTask(taskID int)
}

// GathererRegistry allow to register/unregister prometheus Gatherer.
type GathererRegistry interface {
	RegisterGatherer(opt registry.RegistrationOption, gatherer prometheus.Gatherer) (int, error)
	RegisterInput(opt registry.RegistrationOption, input telegraf.Input) (int, error)
	Unregister(id int) bool
}

// New returns a new Discovery and some warnings.
func New(
	dynamicDiscovery Discoverer,
	commandRunner *gloutonexec.Runner,
	metricRegistry GathererRegistry,
	state State,
	containerInfo containerInfoProvider,
	servicesOverride []config.Service,
	isServiceIgnored func(Service) bool,
	isCheckIgnored func(Service) bool,
	isInputIgnored func(Service) bool,
	isContainerIgnored func(c facts.Container) bool,
	processFact processFact,
	absentServiceDeactivationDelay time.Duration,
	logProcessingCfg config.OpenTelemetry,
) (*Discovery, prometheus.MultiError) {
	initialServices := servicesFromState(state)
	discoveredServicesMap := make(map[NameInstance]Service, len(initialServices))

	for _, v := range initialServices {
		key := NameInstance{
			Name:     v.Name,
			Instance: v.Instance,
		}
		discoveredServicesMap[key] = v
	}

	servicesOverrideMap, warnings := validateServices(servicesOverride, logProcessingCfg)

	discovery := &Discovery{
		dynamicDiscovery:               dynamicDiscovery,
		commandRunner:                  commandRunner,
		discoveredServicesMap:          discoveredServicesMap,
		metricRegistry:                 metricRegistry,
		containerInfo:                  containerInfo,
		activeCollector:                make(map[NameInstance]collectorDetails),
		activeCheck:                    make(map[NameInstance]CheckDetails),
		triggerDiscovery:               make(chan interface{}),
		state:                          state,
		servicesOverride:               servicesOverrideMap,
		isServiceIgnored:               isServiceIgnored,
		isCheckIgnored:                 isCheckIgnored,
		isInputIgnored:                 isInputIgnored,
		isContainerIgnored:             isContainerIgnored,
		processFact:                    processFact,
		absentServiceDeactivationDelay: absentServiceDeactivationDelay,
		logProcessingCfg:               logProcessingCfg,
	}

	return discovery, warnings
}

// validateServices validates the service config.
// It returns the services as a map and some warnings.
func validateServices(services []config.Service, logProcessingCfg config.OpenTelemetry) (map[NameInstance]config.Service, prometheus.MultiError) {
	var warnings prometheus.MultiError

	serviceMap := make(map[NameInstance]config.Service, len(services))
	replacer := strings.NewReplacer(".", "_", "-", "_")

	for _, srv := range services {
		if srv.Type == "" {
			warning := fmt.Errorf("%w: the key \"type\" is missing in one of your service override", config.ErrInvalidValue)
			warnings.Append(warning)

			continue
		}

		if !model.IsValidMetricName(model.LabelValue(srv.Type)) {
			newServiceType := replacer.Replace(srv.Type)
			if !model.IsValidMetricName(model.LabelValue(newServiceType)) {
				warning := fmt.Errorf(
					"%w: service type \"%s\" can only contains letters, digits and underscore",
					config.ErrInvalidValue, srv.Type,
				)
				warnings.Append(warning)

				continue
			}

			warning := fmt.Errorf(
				"%w: service type \"%s\" can not contains dot (.) or dash (-). Changed to \"%s\"",
				config.ErrInvalidValue, srv.Type, newServiceType,
			)
			warnings.Append(warning)

			srv.Type = newServiceType
		}

		// SSL and StartTLS can't be used at the same time.
		if srv.SSL && srv.StartTLS {
			warning := fmt.Errorf(
				"%w: service '%s' can't set both SSL and StartTLS, StartTLS will be used",
				config.ErrInvalidValue, srv.Type,
			)
			warnings.Append(warning)

			srv.SSL = false
		}

		// StatsProtocol must be "http" or "tcp".
		switch srv.StatsProtocol {
		case "", "http", "tcp":
		default:
			warning := fmt.Errorf(
				"%w: service '%s' has an unsupported stats protocol: '%s'",
				config.ErrInvalidValue, srv.Type, srv.StatsProtocol,
			)
			warnings.Append(warning)

			// StatsProtocol is not required, so we leave it empty to
			// let the service decide on the default protocol to use.
			srv.StatsProtocol = ""
		}

		// Check for duplicated overrides.
		key := NameInstance{
			Name:     srv.Type,
			Instance: srv.Instance,
		}

		if _, ok := serviceMap[key]; ok {
			warning := fmt.Sprintf("a service override is duplicated for '%s'", srv.Type)

			if srv.Instance != "" {
				warning = fmt.Sprintf("%s on instance '%s'", warning, srv.Instance)
			}

			warnings.Append(fmt.Errorf("%w: %s", config.ErrInvalidValue, warning))
		}

		if logProcessingCfg.Enable && logProcessingCfg.AutoDiscovery {
			warns := validateServiceLogConfig(srv, logProcessingCfg.KnownLogFormats, logProcessingCfg.KnownLogFilters)
			for _, warn := range warns {
				if srv.Instance != "" {
					warnings.Append(fmt.Errorf("%w: service '%s'/'%s': %w", config.ErrInvalidValue, srv.Type, srv.Instance, warn))
				} else {
					warnings.Append(fmt.Errorf("%w: service '%s': %w", config.ErrInvalidValue, srv.Type, warn))
				}
			}
		}

		serviceMap[key] = srv
	}

	return serviceMap, warnings
}

// Close stop & cleanup inputs & check created by the discovery.
func (d *Discovery) Close() {
	d.l.Lock()
	defer d.l.Unlock()

	for key := range d.servicesMap {
		d.removeInput(key)
	}

	d.configureChecks(d.servicesMap, nil)
}

// GetLatestDiscovery return list of services detected on the system with date of latest update.
func (d *Discovery) GetLatestDiscovery() ([]Service, time.Time) {
	d.l.Lock()
	defer d.l.Unlock()

	services := make([]Service, 0, len(d.servicesMap))

	for _, v := range d.servicesMap {
		services = append(services, v)
	}

	return services, d.lastDiscoveryUpdate
}

// Subscribe return a channel that will receive all update of services after a
// discovery run.
//
// On subscribe a list of current discovered service will be sent, unless no discovery have yet run.
//
// The channel will be closed when ctx expire. You must drain the full channel or discovery could be blocked.
func (d *Discovery) Subscribe(ctx context.Context) <-chan []Service {
	sub := &subscriber{
		channel: make(chan []Service),
	}

	d.l.Lock()
	defer d.l.Unlock()

	d.subscribers = append(d.subscribers, sub)

	go func() {
		defer crashreport.ProcessPanic()

		services, lastUpdate := d.GetLatestDiscovery()
		if !lastUpdate.IsZero() {
			sub.channel <- services
		}

		<-ctx.Done()

		sub.l.Lock()

		close(sub.channel)
		sub.closed = true

		sub.l.Unlock()

		d.l.Lock()
		defer d.l.Unlock()

		i := 0

		for _, row := range d.subscribers {
			if row == sub {
				continue
			}

			d.subscribers[i] = row
			i++
		}

		// Make nil value remove to ensure channel get GC'ed
		for j := i; j < len(d.subscribers); j++ {
			d.subscribers[j] = nil
		}

		d.subscribers = d.subscribers[:i]
	}()

	return sub.channel
}

// TriggerUpdate ask for a new discovery to be run. This function as asynchronous
// and the discovery might not have finished when TriggerUpdate return.
//
// Use Subscribe to get notified after every change in discovery.
func (d *Discovery) TriggerUpdate() {
	d.triggerDiscovery <- nil
}

// Run execute the discovery thread that will update the discovery when needed.
func (d *Discovery) Run(ctx context.Context) error {
	var (
		wantedNextUpdate            time.Time
		consecutiveNonTriggerUpdate int
	)

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-d.triggerDiscovery:
			wantedNextUpdate = d.discoveryAndNotify(ctx, "trigger")
			consecutiveNonTriggerUpdate = 0
		case <-ticker.C:
			if wantedNextUpdate.IsZero() || time.Now().Before(wantedNextUpdate) {
				continue
			}

			wantedNextUpdate = d.discoveryAndNotify(ctx, "re-update for processes & container")
			consecutiveNonTriggerUpdate++

			if wantedNextUpdate.IsZero() {
				continue
			}

			switch consecutiveNonTriggerUpdate {
			case 1:
				// A discovery (due to a trigger) set the wantedNextUpdate.
				// That discovery due to the wantedNextUpdate once more wanted another update
				// honor it, BUT ensure at least 30 seconds elapse
				if time.Until(wantedNextUpdate) < 30*time.Second {
					wantedNextUpdate = time.Now().Add(30 * time.Second)
				}
			default:
				// Another case means that 2 or more update were done due to wantedNextUpdate.
				// To avoid infinite loop, skip them
				wantedNextUpdate = time.Time{}
			}
		}
	}
}

// DiagnosticArchive add to a zipfile useful diagnostic information.
func (d *Discovery) DiagnosticArchive(_ context.Context, zipFile types.ArchiveWriter) error {
	d.l.Lock()
	defer d.l.Unlock()

	file, err := zipFile.Create("discovery.txt")
	if err != nil {
		return err
	}

	fmt.Fprintf(file, "# Discovered service (count=%d, last update=%s)\n", len(d.servicesMap), d.lastDiscoveryUpdate.Format(time.RFC3339))
	services := make([]Service, 0, len(d.servicesMap))

	for _, v := range d.servicesMap {
		if v.Config.JMXPassword != "" {
			v.Config.JMXPassword = "*****"
		}

		if v.Config.Password != "" {
			v.Config.Password = "*****"
		}

		services = append(services, v)
	}

	sort.Slice(services, func(i, j int) bool {
		if services[j].Active != services[i].Active && services[i].Active {
			return true
		}

		if services[j].Active != services[i].Active && services[j].Active {
			return false
		}

		return services[i].Name < services[j].Name || (services[i].Name == services[j].Name && services[i].Instance < services[j].Instance)
	})

	for _, v := range services {
		fmt.Fprintf(file, "%s on IP %s, active=%v, instance=%s, container=%s, listening on %v, type=%s\n", v.Name, v.IPAddress, v.Active, v.Instance, v.ContainerName, v.ListenAddresses, v.ServiceType)
	}

	if dd, ok := d.dynamicDiscovery.(*DynamicDiscovery); ok {
		procs := dd.option.PS.GetLatest()

		fmt.Fprintf(file, "\n# Processes (filteted to only show ones associated with a service)\n")

		for _, p := range procs {
			serviceType, ok := serviceByCommand(p.CmdLineList)
			if !ok {
				continue
			}

			fmt.Fprintf(file, "PID %d with service %v on container %s\n", p.PID, serviceType, p.ContainerName)
		}

		fmt.Fprintf(file, "\n# Last dynamic discovery (count=%d)\n", len(d.lastDynamicService))

		services := make([]Service, len(d.lastDynamicService))

		copy(services, d.lastDynamicService)

		sort.Slice(services, func(i, j int) bool {
			if services[j].Active != services[i].Active && services[i].Active {
				return true
			}

			if services[j].Active != services[i].Active && services[j].Active {
				return false
			}

			return services[i].Name < services[j].Name || (services[i].Name == services[j].Name && services[i].Instance < services[j].Instance)
		})

		for _, v := range services {
			fmt.Fprintf(file, "%s on IP %s, active=%v, instance=%s, container=%s, listening on %v\n", v.Name, v.IPAddress, v.Active, v.Instance, v.ContainerName, v.ListenAddresses)
		}
	}

	file, err = zipFile.Create("discovery.yaml")
	if err != nil {
		return err
	}

	enc := yaml.NewEncoder(file)

	enc.SetIndent(4)

	err = enc.Encode(services)
	if err != nil {
		return err
	}

	err = enc.Close()
	if err != nil {
		return err
	}

	return nil
}

func (d *Discovery) discoveryAndNotify(ctx context.Context, updateReason string) time.Time {
	startedAt := time.Now()

	services, nextUpdate, err := d.discovery(ctx)
	if err != nil {
		logger.V(1).Printf("error during discovery: %v", err)
	}

	activeServices := 0

	for _, srv := range services {
		if srv.Active {
			activeServices++
		}
	}

	d.l.Lock()
	subCopy := slices.Clone(d.subscribers)
	d.l.Unlock()

	for _, sub := range subCopy {
		sub.l.Lock()

		if !sub.closed {
			sub.channel <- services
		}

		sub.l.Unlock()
	}

	extraMessage := ""

	if !nextUpdate.IsZero() {
		extraMessage = fmt.Sprintf(" want another discovery at %s", nextUpdate)
	}

	logger.V(2).Printf(
		"discovery run due to %s in %s and found %d active services%s",
		updateReason,
		time.Since(startedAt),
		activeServices,
		extraMessage,
	)

	return nextUpdate
}

func (d *Discovery) discovery(ctx context.Context) ([]Service, time.Time, error) {
	ctx, cancel := context.WithTimeout(ctx, discoveryTimeout)
	defer cancel()

	nextUpdate, err := d.updateDiscovery(ctx, time.Now())

	d.l.Lock()
	defer d.l.Unlock()

	if err != nil {
		return nil, nextUpdate, err
	}

	if ctx.Err() == nil {
		saveState(d.state, d.discoveredServicesMap)
		d.reconfigure()

		d.lastDiscoveryUpdate = time.Now()
	}

	services := make([]Service, 0, len(d.servicesMap))

	for _, v := range d.servicesMap {
		services = append(services, v)
	}

	return services, nextUpdate, ctx.Err()
}

// RemoveIfNonRunning remove a service if the service is not running.
//
// This is useful to remove persisted service that no longer run.
// The service will immediately be remove from next call to GetLatestDiscovery and a
// next discovery will be run asynchroniously if from entry were deleted.
func (d *Discovery) RemoveIfNonRunning(services []Service) {
	d.l.Lock()
	defer d.l.Unlock()

	deleted := false

	for _, v := range services {
		key := NameInstance{Name: v.Name, Instance: v.Instance}
		if _, ok := d.servicesMap[key]; ok {
			deleted = true
		}

		delete(d.servicesMap, key)
		delete(d.discoveredServicesMap, key)
	}

	if deleted {
		d.TriggerUpdate()
	}
}

func (d *Discovery) reconfigure() {
	err := d.configureMetricInputs(d.lastConfigservicesMap, d.servicesMap)
	if err != nil {
		logger.Printf("Unable to update metric inputs: %v", err)
	}

	d.configureChecks(d.lastConfigservicesMap, d.servicesMap)

	d.lastConfigservicesMap = d.servicesMap
}

// Only one updateDiscovery should be running at a time (since discovery is called for Run gorouting the
// requirement is fulfilled).
// The lock should not be held, updateDiscovery take care of taking lock before access to mutable fields.
func (d *Discovery) updateDiscovery(ctx context.Context, now time.Time) (time.Time, error) {
	// Make sure we have a container list. This is important for startup, so
	// that previously known service could get associated with container.
	// Without this, a service in a stopped container (which should be shown
	// as critical with "Container Stopped" reason) might disappear.
	_, err := d.containerInfo.Containers(ctx, 0, false)
	if err != nil {
		logger.V(1).Printf("error while updating containers: %v", err)
	}

	r, nextUpdate, err := d.dynamicDiscovery.Discovery(ctx)
	if err != nil {
		return nextUpdate, err
	}

	servicesMap := make(map[NameInstance]Service)

	for key, service := range d.discoveredServicesMap {
		service = d.setServiceActiveAndContainer(service)

		servicesMap[key] = service
	}

	for _, service := range r {
		if d.isServiceIgnored != nil && d.isServiceIgnored(service) {
			logger.V(2).Printf("Fully ignoring service %s/%s", service.Name, service.Instance)

			continue
		}

		key := NameInstance{
			Name:     service.Name,
			Instance: service.Instance,
		}

		if previousService, ok := servicesMap[key]; ok {
			if usePreviousNetstat(now, previousService, service) {
				service.ListenAddresses = previousService.ListenAddresses
				service.IPAddress = previousService.IPAddress
				service.HasNetstatInfo = previousService.HasNetstatInfo
				service.LastNetstatInfo = previousService.LastNetstatInfo
			}
		}

		servicesMap[key] = service
	}

	if ctx.Err() != nil {
		return nextUpdate, ctx.Err()
	}

	d.l.Lock()
	defer d.l.Unlock()

	d.lastDynamicService = r
	d.discoveredServicesMap = servicesMap
	serviceMapWithOverride := copyAndMergeServiceWithOverride(servicesMap, d.servicesOverride)

	applyOverrideInPlace(serviceMapWithOverride)

	d.servicesMap = serviceMapWithOverride

	d.ignoreServicesAndPorts()

	if d.logProcessingCfg.Enable && d.logProcessingCfg.AutoDiscovery {
		for key, service := range d.servicesMap {
			d.servicesMap[key] = inferLogProcessingConfig(service, d.logProcessingCfg.KnownLogFormats, d.logProcessingCfg.KnownLogFilters)
		}
	}

	return nextUpdate, nil
}

func usePreviousNetstat(now time.Time, previousService Service, newService Service) bool {
	// We want to use previous discovered information to cover case where the service restarted and
	// netstat information isn't yet up-to-date.
	// But we stop using old information in the following cases:
	// * The new service has netstat (or old didn't had netstat)
	// * the associated container changed its IP
	// * the previous discovered netstat is too old (more than 1 hour, netstat is gathered every hour)
	if newService.HasNetstatInfo || !previousService.HasNetstatInfo {
		return false
	}

	if newService.ContainerID != "" && newService.IPAddress != previousService.IPAddress {
		return false
	}

	if now.Sub(previousService.LastNetstatInfo) > time.Hour {
		return false
	}

	return true
}

func (d *Discovery) setServiceActiveAndContainer(service Service) Service {
	switch {
	case service.ContainerID != "":
		container, found := d.containerInfo.CachedContainer(service.ContainerID)

		if found {
			service.container = container
		}

		if !found || d.isContainerIgnored(container) {
			service.Active = false
		} else if container.StoppedAndReplaced() {
			service.Active = false
		}
	case service.ExePath != "":
		if _, err := os.Stat(service.ExePath); os.IsNotExist(err) {
			service.Active = false
		}
	case service.ExePath == "" && service.Active:
		if since := time.Since(service.LastTimeSeen); since > d.absentServiceDeactivationDelay {
			logger.V(2).Printf("Service %q hasn't been seen since %s, deactivating it.", service.Name, since.Round(time.Second))

			service.Active = false
		}
	}

	return service
}

// copyAndMergeServiceWithOverride returns a copy of servicesMap with overrides merged into service.Config.
// It didn't yet apply the override, use applyOverrideInPlace for that.
func copyAndMergeServiceWithOverride(servicesMap map[NameInstance]Service, overrides map[NameInstance]config.Service) map[NameInstance]Service {
	serviceMapWithOverride := make(map[NameInstance]Service)

	for k, v := range servicesMap {
		serviceMapWithOverride[k] = v
	}

	for _, v := range overrides {
		key := NameInstance{
			Name:     v.Type,
			Instance: v.Instance,
		}
		service := serviceMapWithOverride[key]

		// Override config.
		err := mergo.Merge(&service.Config, v, mergo.WithOverride)
		if err != nil {
			logger.V(1).Printf("Failed to merge service and override: %s", err)
		}

		serviceMapWithOverride[key] = service
	}

	return serviceMapWithOverride
}

func applyOverrideInPlace(
	servicesMap map[NameInstance]Service,
) {
	serviceKeys := make([]NameInstance, 0, len(servicesMap))
	for k := range servicesMap {
		serviceKeys = append(serviceKeys, k)
	}

	// have stable order in services
	sort.Slice(serviceKeys, func(i, j int) bool {
		return serviceKeys[i].Name < serviceKeys[j].Name || (serviceKeys[i].Name == serviceKeys[j].Name && serviceKeys[i].Instance < serviceKeys[j].Instance)
	})

	var keyToDelete []NameInstance

	for _, serviceKey := range serviceKeys {
		service := servicesMap[serviceKey]

		if service.Name != service.Config.Type && service.Config.Type != "" {
			service.Name = service.Config.Type
			service.ServiceType = ""
		}

		if service.ServiceType == "" {
			if _, ok := servicesDiscoveryInfo[ServiceName(service.Name)]; ok {
				service.ServiceType = ServiceName(service.Name)
			} else {
				service.ServiceType = CustomService
			}

			if service.Config.Instance != "" {
				service.Instance = service.Config.Instance
			}

			service.Active = true
		}

		// If the address or the port is set explicitly in the config, override the listen address.
		if service.Config.Port != 0 || service.Config.Address != "" {
			address, port := service.AddressPort()

			if service.Config.Address != "" {
				address = service.Config.Address
			}

			if service.Config.Port != 0 {
				port = service.Config.Port

				if address == "" {
					address = service.IPAddress
				}
			}

			if address != "" && port != 0 {
				listenAddress := facts.ListenAddress{
					NetworkFamily: "tcp",
					Address:       address,
					Port:          port,
				}

				service.ListenAddresses = []facts.ListenAddress{listenAddress}

				// If an override set the port on a service, make sure this listenAddress isn't used on another service with
				// another instance (except if this would remove the last listen address).
				for otherKey, other := range servicesMap {
					if other.Name == service.Name && other.Instance != service.Instance && len(other.ListenAddresses) > 1 {
						other.ListenAddresses = filterListenAddress(other.ListenAddresses, listenAddress)

						servicesMap[otherKey] = other
					}
				}
			}
		}

		if service.Config.Address != "" {
			service.IPAddress = service.Config.Address
		}

		if service.Config.Interval != 0 {
			service.Interval = time.Duration(service.Config.Interval) * time.Second
		}

		if len(service.Config.IgnorePorts) > 0 {
			if service.IgnoredPorts == nil {
				service.IgnoredPorts = make(map[int]bool, len(service.Config.IgnorePorts))
			}

			for _, p := range service.Config.IgnorePorts {
				service.IgnoredPorts[p] = true
			}
		}

		if service.ServiceType == CustomService {
			// If the port is not set, use the JMX port.
			if service.Config.Port == 0 {
				service.Config.Port = service.Config.JMXPort
			}

			if service.Config.Port != 0 {
				if service.Config.Address == "" {
					service.Config.Address = localhostIP
				}

				if _, port := service.AddressPort(); port == 0 {
					logger.V(0).Printf("Bad custom service definition for service %s, port %#v is invalid", service.Name)

					keyToDelete = append(keyToDelete, serviceKey)

					continue
				}
			}

			if service.Config.CheckType == "" {
				service.Config.CheckType = customCheckTCP
			}

			if service.Config.CheckType == customCheckNagios && service.Config.CheckCommand == "" {
				logger.V(0).Printf("Bad custom service definition for service %s, check_type is nagios but no check_command set", service.Name)

				keyToDelete = append(keyToDelete, serviceKey)

				continue
			}

			if service.Config.CheckType == customCheckProcess && service.Config.MatchProcess == "" {
				logger.V(0).Printf("Bad custom service definition for service %s, check_type is process but no match_process set", service.Name)

				keyToDelete = append(keyToDelete, serviceKey)

				continue
			}

			if service.Config.CheckType != customCheckNagios && service.Config.CheckType != customCheckProcess && service.Config.Port == 0 {
				logger.V(0).Printf("Bad custom service definition for service %s, port is unknown so I don't known how to check it", service.Name)

				keyToDelete = append(keyToDelete, serviceKey)

				continue
			}
		}

		service.Tags = append(service.Tags, service.Config.Tags...)

		servicesMap[serviceKey] = service
	}

	for _, key := range keyToDelete {
		delete(servicesMap, key)
	}
}

func filterListenAddress(list []facts.ListenAddress, drop facts.ListenAddress) []facts.ListenAddress {
	i := 0

	for _, x := range list {
		if x != drop {
			list[i] = x
			i++
		}
	}

	return list[:i]
}

func (d *Discovery) ignoreServicesAndPorts() {
	servicesMap := d.servicesMap
	for nameContainer, service := range servicesMap {
		if d.isCheckIgnored != nil {
			service.CheckIgnored = d.isCheckIgnored(service)
		}

		if d.isInputIgnored != nil {
			service.MetricsIgnored = d.isInputIgnored(service)
		}

		if d.isServiceIgnored != nil && d.isServiceIgnored(service) {
			delete(d.servicesMap, nameContainer)

			continue
		}

		if len(service.IgnoredPorts) > 0 {
			n := 0

			for _, x := range service.ListenAddresses {
				if !service.IgnoredPorts[x.Port] {
					service.ListenAddresses[n] = x
					n++
				}
			}

			service.ListenAddresses = service.ListenAddresses[:n]
		}

		d.servicesMap[nameContainer] = service
	}
}

// CheckNow is type of check function.
type CheckNow func(ctx context.Context) types.StatusDescription

// GetCheckNow returns the GetCheckNow function associated to a NameInstance.
func (d *Discovery) GetCheckNow(nameInstance NameInstance) (CheckNow, error) {
	checkDetails, ok := d.activeCheck[nameInstance]
	if !ok {
		return nil, fmt.Errorf("%w %s", errNoCheckAssociated, nameInstance.Name)
	}

	return checkDetails.check.CheckNow, nil
}
