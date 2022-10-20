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
	"errors"
	"fmt"
	"glouton/config2"
	"glouton/facts"
	"glouton/inputs"
	"glouton/logger"
	"glouton/prometheus/registry"
	"glouton/task"
	"glouton/types"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/imdario/mergo"
	"github.com/influxdata/telegraf"
	"github.com/prometheus/client_golang/prometheus"
)

var errNoCheckAssociated = errors.New("there is no check associated with the container")

const localhostIP = "127.0.0.1"

// discoveryTimeout is the time limit for a discovery.
const discoveryTimeout = time.Minute

// Discovery implement the full discovery mecanisme. It will take informations
// from both the dynamic discovery (service currently running) and previously
// detected services.
// It will configure metrics input and add them to a Collector.
type Discovery struct {
	l sync.Mutex

	dynamicDiscovery Discoverer

	discoveredServicesMap map[NameInstance]Service
	servicesMap           map[NameInstance]Service
	lastDiscoveryUpdate   time.Time

	lastConfigservicesMap map[NameInstance]Service
	activeCollector       map[NameInstance]collectorDetails
	activeCheck           map[NameInstance]CheckDetails
	coll                  Collector
	metricRegistry        GathererRegistry
	containerInfo         containerInfoProvider
	state                 State
	servicesOverride      map[NameInstance]config2.Service
	isCheckIgnored        func(Service) bool
	isInputIgnored        func(Service) bool
	isContainerIgnored    func(facts.Container) bool
	metricFormat          types.MetricFormat
	processFact           processFact
}

// Collector will gather metrics for added inputs.
type Collector interface {
	AddInput(input telegraf.Input, shortName string) (int, error)
	RemoveInput(int)
}

// Registry will contains checks.
type Registry interface {
	AddTask(task task.Runner, shortName string) (int, error)
	RemoveTask(int)
}

// GathererRegistry allow to register/unregister prometheus Gatherer.
type GathererRegistry interface {
	RegisterGatherer(opt registry.RegistrationOption, gatherer prometheus.Gatherer) (int, error)
	Unregister(id int) bool
}

// New returns a new Discovery.
func New(
	dynamicDiscovery Discoverer,
	coll Collector,
	metricRegistry GathererRegistry,
	taskRegistry Registry,
	state State,
	acc inputs.AnnotationAccumulator,
	containerInfo containerInfoProvider,
	servicesOverride []config2.Service,
	isCheckIgnored func(Service) bool,
	isInputIgnored func(Service) bool,
	isContainerIgnored func(c facts.Container) bool,
	metricFormat types.MetricFormat,
	processFact processFact,
) *Discovery {
	initialServices := servicesFromState(state)
	discoveredServicesMap := make(map[NameInstance]Service, len(initialServices))

	for _, v := range initialServices {
		key := NameInstance{
			Name:     v.Name,
			Instance: v.Instance,
		}
		discoveredServicesMap[key] = v
	}

	// Convert service overrides to a map.
	serviceMap := make(map[NameInstance]config2.Service, len(servicesOverride))

	for _, v := range servicesOverride {
		key := NameInstance{
			Name:     v.ID,
			Instance: v.Instance,
		}
		serviceMap[key] = v
	}

	return &Discovery{
		dynamicDiscovery:      dynamicDiscovery,
		discoveredServicesMap: discoveredServicesMap,
		coll:                  coll,
		metricRegistry:        metricRegistry,
		containerInfo:         containerInfo,
		activeCollector:       make(map[NameInstance]collectorDetails),
		activeCheck:           make(map[NameInstance]CheckDetails),
		state:                 state,
		servicesOverride:      serviceMap,
		isCheckIgnored:        isCheckIgnored,
		isInputIgnored:        isInputIgnored,
		isContainerIgnored:    isContainerIgnored,
		metricFormat:          metricFormat,
		processFact:           processFact,
	}
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

// Discovery detect service on the system and return a list of Service object.
//
// It may trigger an update of metric inputs present in the Collector.
func (d *Discovery) Discovery(ctx context.Context, maxAge time.Duration) (services []Service, err error) {
	d.l.Lock()
	defer d.l.Unlock()

	return d.discovery(ctx, maxAge)
}

// LastUpdate return when the last update occurred.
func (d *Discovery) LastUpdate() time.Time {
	d.l.Lock()
	defer d.l.Unlock()

	return d.lastDiscoveryUpdate
}

// DiagnosticArchive add to a zipfile useful diagnostic information.
func (d *Discovery) DiagnosticArchive(ctx context.Context, zipFile types.ArchiveWriter) error {
	d.l.Lock()
	defer d.l.Unlock()

	file, err := zipFile.Create("discovery.txt")
	if err != nil {
		return err
	}

	fmt.Fprintf(file, "# Discovered service (count=%d, last update=%s)\n", len(d.servicesMap), d.lastDiscoveryUpdate.Format(time.RFC3339))
	services := make([]Service, 0, len(d.servicesMap))

	for _, v := range d.servicesMap {
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
		fmt.Fprintf(file, "%s on IP %s, active=%v, instance=%s, container=%s, listenning on %v, type=%s\n", v.Name, v.IPAddress, v.Active, v.Instance, v.ContainerName, v.ListenAddresses, v.ServiceType)
	}

	if dd, ok := d.dynamicDiscovery.(*DynamicDiscovery); ok {
		procs, err := dd.ps.Processes(ctx, time.Hour)
		if err != nil {
			return err
		}

		fmt.Fprintf(file, "\n# Processes (filteted to only show ones associated with a service)\n")

		for _, p := range procs {
			serviceType, ok := serviceByCommand(p.CmdLineList)
			if !ok {
				continue
			}

			fmt.Fprintf(file, "PID %d with service %v on container %s\n", p.PID, serviceType, p.ContainerName)
		}

		fmt.Fprintf(file, "\n# Last dynamic discovery (count=%d, last update=%s)\n", len(dd.services), dd.lastDiscoveryUpdate.Format(time.RFC3339))

		services := make([]Service, len(dd.services))

		copy(services, dd.services)

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
			fmt.Fprintf(file, "%s on IP %s, active=%v, instance=%s, container=%s, listenning on %v\n", v.Name, v.IPAddress, v.Active, v.Instance, v.ContainerName, v.ListenAddresses)
		}
	}

	return nil
}

func (d *Discovery) discovery(ctx context.Context, maxAge time.Duration) (services []Service, err error) {
	ctx, cancel := context.WithTimeout(ctx, discoveryTimeout)
	defer cancel()

	if time.Since(d.lastDiscoveryUpdate) >= maxAge {
		err := d.updateDiscovery(ctx, time.Now())
		if err != nil {
			return nil, err
		}

		if ctx.Err() == nil {
			saveState(d.state, d.discoveredServicesMap)
			d.reconfigure()

			d.lastDiscoveryUpdate = time.Now()
		}
	}

	services = make([]Service, 0, len(d.servicesMap))

	for _, v := range d.servicesMap {
		services = append(services, v)
	}

	return services, ctx.Err()
}

// RemoveIfNonRunning remove a service if the service is not running
//
// This is useful to remove persisted service that no longer run.
func (d *Discovery) RemoveIfNonRunning(ctx context.Context, services []Service) {
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
		if _, err := d.discovery(ctx, 0); err != nil {
			logger.V(2).Printf("Error during discovery during RemoveIfNonRunning: %v", err)
		}
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

func (d *Discovery) updateDiscovery(ctx context.Context, now time.Time) error {
	// Make sure we have a container list. This is important for startup, so
	// that previously known service could get associated with container.
	// Without this, a service in a stopped container (which should be shown
	// as critical with "Container Stopped" reason) might disapear.
	_, err := d.containerInfo.Containers(ctx, 0, false)
	if err != nil {
		logger.V(1).Printf("error while updating containers: %v", err)
	}

	r, err := d.dynamicDiscovery.Discovery(ctx, 0)
	if err != nil {
		return err
	}

	servicesMap := make(map[NameInstance]Service)

	for key, service := range d.discoveredServicesMap {
		service = d.setServiceActiveAndContainer(service)

		servicesMap[key] = service
	}

	for _, service := range r {
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
		return ctx.Err()
	}

	d.discoveredServicesMap = servicesMap
	d.servicesMap = applyOverride(servicesMap, d.servicesOverride)

	d.ignoreServicesAndPorts()

	return nil
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
	if service.ContainerID != "" {
		container, found := d.containerInfo.CachedContainer(service.ContainerID)

		if found {
			service.container = container
		}

		if !found || d.isContainerIgnored(container) {
			service.Active = false
		} else if container.StoppedAndReplaced() {
			service.Active = false
		}
	} else if service.ExePath != "" {
		if _, err := os.Stat(service.ExePath); os.IsNotExist(err) {
			service.Active = false
		}
	}

	return service
}

func applyOverride(
	discoveredServicesMap map[NameInstance]Service,
	servicesOverride map[NameInstance]config2.Service,
) map[NameInstance]Service {
	servicesMap := make(map[NameInstance]Service)

	for k, v := range discoveredServicesMap {
		servicesMap[k] = v
	}

	for serviceKey, override := range servicesOverride {
		service := servicesMap[serviceKey]
		if service.ServiceType == "" {
			if _, ok := servicesDiscoveryInfo[ServiceName(serviceKey.Name)]; ok {
				service.ServiceType = ServiceName(serviceKey.Name)
			} else {
				service.ServiceType = CustomService
			}

			service.Name = serviceKey.Name
			service.Instance = serviceKey.Instance
			service.Active = true
		}

		// If the address or the port is set explicitly in the config, override the listen address.
		if override.Port != 0 || override.Address != "" {
			address, port := service.AddressPort()

			if override.Address != "" {
				address = override.Address
			}

			if override.Port != 0 {
				port = override.Port
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
				for _, other := range servicesMap {
					if other.Name == service.Name && other.Instance != service.Instance && len(other.ListenAddresses) > 1 {
						other.ListenAddresses = filterListenAddress(other.ListenAddresses, listenAddress)
					}
				}
			}
		}

		service.Interval = time.Duration(override.Interval) * time.Second

		if len(override.IgnorePorts) > 0 {
			if service.IgnoredPorts == nil {
				service.IgnoredPorts = make(map[int]bool, len(override.IgnorePorts))
			}

			for _, p := range override.IgnorePorts {
				service.IgnoredPorts[p] = true
			}
		}

		// Override config.
		mergo.Merge(&service.Config, override, mergo.WithOverride)

		if service.ServiceType == CustomService {
			if service.Config.Port != 0 {
				if service.Config.Address == "" {
					service.Config.Address = localhostIP
				}

				if _, port := service.AddressPort(); port == 0 {
					logger.V(0).Printf("Bad custom service definition for service %s, port %#v is invalid", service.Name)

					continue
				}
			}

			if service.Config.CheckType == "" {
				service.Config.CheckType = customCheckTCP
			}

			if service.Config.CheckType == customCheckNagios && service.Config.CheckCommand == "" {
				logger.V(0).Printf("Bad custom service definition for service %s, check_type is nagios but no check_command set", service.Name)

				continue
			}

			if service.Config.CheckType == customCheckProcess && service.Config.MatchProcess == "" {
				logger.V(0).Printf("Bad custom service definition for service %s, check_type is process but no match_process set", service.Name)

				continue
			}

			if service.Config.CheckType != customCheckNagios && service.Config.CheckType != customCheckProcess && service.Config.Port == 0 {
				logger.V(0).Printf("Bad custom service definition for service %s, port is unknown so I don't known how to check it", service.Name)

				continue
			}
		}

		servicesMap[serviceKey] = service
	}

	return servicesMap
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
