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
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"glouton/facts"
	"glouton/inputs"
	"glouton/logger"
	"glouton/task"
	"glouton/types"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/influxdata/telegraf"
	"github.com/prometheus/client_golang/prometheus"
)

var errNoCheckAssociated = errors.New("there is no check associated with the container")

const localhostIP = "127.0.0.1"

// List of common ExtraAttributes supported by all services.
// This list + ExtraAttributes from discoveryInfo list all overidable settings.
const (
	nrpeExposedName = "nagios_nrpe_name"
	ignoredPorts    = "ignore_ports"
)

// Discovery implement the full discovery mecanisme. It will take informations
// from both the dynamic discovery (service currently running) and previously
// detected services.
// It will configure metrics input and add them to a Collector.
type Discovery struct {
	l sync.Mutex

	dynamicDiscovery Discoverer

	discoveredServicesMap map[NameContainer]Service
	servicesMap           map[NameContainer]Service
	lastDiscoveryUpdate   time.Time

	acc                   inputs.AnnotationAccumulator
	lastConfigservicesMap map[NameContainer]Service
	activeCollector       map[NameContainer]collectorDetails
	activeCheck           map[NameContainer]CheckDetails
	coll                  Collector
	taskRegistry          Registry
	metricRegistry        GathererRegistry
	containerInfo         containerInfoProvider
	state                 State
	servicesOverride      map[NameContainer]map[string]string
	isCheckIgnored        func(NameContainer) bool
	isInputIgnored        func(NameContainer) bool
	metricFormat          types.MetricFormat
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
	RegisterGatherer(gatherer prometheus.Gatherer, stopCallback func(), extraLabels map[string]string, pushPoints bool) (int, error)
	UnregisterGatherer(id int) bool
}

// New returns a new Discovery.
func New(dynamicDiscovery Discoverer, coll Collector, metricRegistry GathererRegistry, taskRegistry Registry, state State, acc inputs.AnnotationAccumulator, containerInfo containerInfoProvider, servicesOverride []map[string]string, isCheckIgnored func(NameContainer) bool, isInputIgnored func(NameContainer) bool, metricFormat types.MetricFormat) *Discovery {
	initialServices := servicesFromState(state)
	discoveredServicesMap := make(map[NameContainer]Service, len(initialServices))

	for _, v := range initialServices {
		key := NameContainer{
			Name:          v.Name,
			ContainerName: v.ContainerName,
		}
		discoveredServicesMap[key] = v
	}

	servicesOverrideMap := make(map[NameContainer]map[string]string)

	for _, fragment := range servicesOverride {
		fragmentCopy := make(map[string]string)

		for k, v := range fragment {
			if k == "id" || k == "instance" {
				continue
			}

			fragmentCopy[k] = v
		}

		key := NameContainer{
			fragment["id"],
			fragment["instance"],
		}
		servicesOverrideMap[key] = fragmentCopy
	}

	return &Discovery{
		dynamicDiscovery:      dynamicDiscovery,
		discoveredServicesMap: discoveredServicesMap,
		coll:                  coll,
		metricRegistry:        metricRegistry,
		taskRegistry:          taskRegistry,
		containerInfo:         containerInfo,
		acc:                   acc,
		activeCollector:       make(map[NameContainer]collectorDetails),
		activeCheck:           make(map[NameContainer]CheckDetails),
		state:                 state,
		servicesOverride:      servicesOverrideMap,
		isCheckIgnored:        isCheckIgnored,
		isInputIgnored:        isInputIgnored,
		metricFormat:          metricFormat,
	}
}

// Close stop & cleanup inputs & check created by the discovery.
func (d *Discovery) Close() {
	d.l.Lock()
	defer d.l.Unlock()

	_ = d.configureMetricInputs(d.servicesMap, nil)

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

// DiagnosticZip add to a zipfile useful diagnostic information.
//nolint: gocyclo
func (d *Discovery) DiagnosticZip(zipFile *zip.Writer) error {
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

		return services[i].Name < services[j].Name || (services[i].Name == services[j].Name && services[i].ContainerName < services[j].ContainerName)
	})

	for _, v := range services {
		fmt.Fprintf(file, "%s on IP %s, active=%v, containerName=%s, listenning on %v\n", v.Name, v.IPAddress, v.Active, v.ContainerName, v.ListenAddresses)
	}

	if dd, ok := d.dynamicDiscovery.(*DynamicDiscovery); ok {
		procs, err := dd.ps.Processes(context.Background(), time.Hour)
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

			return services[i].Name < services[j].Name || (services[i].Name == services[j].Name && services[i].ContainerName < services[j].ContainerName)
		})

		for _, v := range services {
			fmt.Fprintf(file, "%s on IP %s, active=%v, containerName=%s, listenning on %v\n", v.Name, v.IPAddress, v.Active, v.ContainerName, v.ListenAddresses)
		}
	}

	return nil
}

func (d *Discovery) discovery(ctx context.Context, maxAge time.Duration) (services []Service, err error) {
	if time.Since(d.lastDiscoveryUpdate) >= maxAge {
		err := d.updateDiscovery(ctx, maxAge)
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
// This is useful to remove persited service that no longer run.
func (d *Discovery) RemoveIfNonRunning(ctx context.Context, services []Service) {
	d.l.Lock()
	defer d.l.Unlock()

	deleted := false

	for _, v := range services {
		key := NameContainer{Name: v.Name, ContainerName: v.ContainerName}
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

func (d *Discovery) updateDiscovery(ctx context.Context, maxAge time.Duration) error {
	r, err := d.dynamicDiscovery.Discovery(ctx, maxAge)
	if err != nil {
		return err
	}

	servicesMap := make(map[NameContainer]Service)

	for key, service := range d.discoveredServicesMap {
		if service.ContainerID != "" {
			if container, found := d.containerInfo.CachedContainer(service.ContainerID); !found || facts.ContainerIgnored(container) {
				service.Active = false
			} else if container.StoppedAndReplaced() {
				service.Active = false
			}
		} else if service.ExePath != "" {
			if _, err := os.Stat(service.ExePath); os.IsNotExist(err) {
				service.Active = false
			}
		}

		servicesMap[key] = service
	}

	for _, service := range r {
		key := NameContainer{
			Name:          service.Name,
			ContainerName: service.ContainerName,
		}

		if previousService, ok := servicesMap[key]; ok {
			if previousService.HasNetstatInfo && !service.HasNetstatInfo {
				service.ListenAddresses = previousService.ListenAddresses
				service.IPAddress = previousService.IPAddress
				service.HasNetstatInfo = previousService.HasNetstatInfo
			}
		}

		servicesMap[key] = service
	}

	d.discoveredServicesMap = servicesMap
	d.servicesMap = applyOveride(servicesMap, d.servicesOverride)

	d.ignoreServicesAndPorts()

	return nil
}

//nolint: gocyclo
func applyOveride(discoveredServicesMap map[NameContainer]Service, servicesOverride map[NameContainer]map[string]string) map[NameContainer]Service {
	servicesMap := make(map[NameContainer]Service)

	for k, v := range discoveredServicesMap {
		servicesMap[k] = v
	}

	for serviceKey, override := range servicesOverride {
		overrideCopy := make(map[string]string, len(override))

		for k, v := range override {
			overrideCopy[k] = v
		}

		service := servicesMap[serviceKey]
		if service.ServiceType == "" {
			if serviceKey.ContainerName != "" {
				logger.V(1).Printf(
					"Custom check for service %#v with a container (%#v) is not supported. Please unset the container",
					serviceKey.Name,
					serviceKey.ContainerName,
				)

				continue
			}

			service.ServiceType = CustomService
			service.Name = serviceKey.Name
			service.Active = true
		}

		if service.ExtraAttributes == nil {
			service.ExtraAttributes = make(map[string]string)
		}

		if value, ok := overrideCopy[ignoredPorts]; ok {
			values := strings.Split(value, ",")

			if service.IgnoredPorts == nil {
				service.IgnoredPorts = make(map[int]bool)
			}

			for _, s := range values {
				port, err := strconv.ParseInt(strings.TrimSpace(s), 10, 0)
				if err != nil {
					logger.V(1).Printf(
						"In %s for service %s: %s", ignoredPorts, serviceKey, err,
					)

					continue
				}

				service.IgnoredPorts[int(port)] = true
			}

			delete(overrideCopy, ignoredPorts)
		}

		di := servicesDiscoveryInfo[service.ServiceType]
		for _, name := range di.ExtraAttributeNames {
			if value, ok := overrideCopy[name]; ok {
				service.ExtraAttributes[name] = value

				delete(overrideCopy, name)
			}
		}

		if len(overrideCopy) > 0 {
			ignoredNames := make([]string, 0, len(overrideCopy))

			for k := range overrideCopy {
				// nrpeExposedName is not managed by us. See nrpe/responder.go
				if k != nrpeExposedName {
					ignoredNames = append(ignoredNames, k)
				}
			}

			if len(ignoredNames) != 0 {
				logger.V(1).Printf("Unknown field for service override on %v: %v", serviceKey, ignoredNames)
			}
		}

		if service.ServiceType == CustomService {
			if service.ExtraAttributes["port"] != "" {
				if service.ExtraAttributes["address"] == "" {
					service.ExtraAttributes["address"] = localhostIP
				}

				if _, port := service.AddressPort(); port == 0 {
					logger.V(1).Printf("Bad custom service definition for service %s, port %#v is invalid", service.Name)
					continue
				}
			}

			if service.ExtraAttributes["check_type"] == "" {
				service.ExtraAttributes["check_type"] = customCheckTCP
			}

			if service.ExtraAttributes["check_type"] == customCheckNagios && service.ExtraAttributes["check_command"] == "" {
				logger.V(1).Printf("Bad custom service definition for service %s, check_type is nagios but no check_command set", service.Name)
				continue
			}

			if service.ExtraAttributes["check_type"] != customCheckNagios && service.ExtraAttributes["port"] == "" {
				logger.V(1).Printf("Bad custom service definition for service %s, port is unknown so I don't known how to check it", service.Name)
				continue
			}
		}

		servicesMap[serviceKey] = service
	}

	return servicesMap
}

func (d *Discovery) ignoreServicesAndPorts() {
	servicesMap := d.servicesMap
	for nameContainer, service := range servicesMap {
		if d.isCheckIgnored != nil {
			service.CheckIgnored = d.isCheckIgnored(nameContainer)
		}

		if d.isInputIgnored != nil {
			service.MetricsIgnored = d.isInputIgnored(nameContainer)
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

// GetCheckNow returns the GetCheckNow function associated to a NameContainer.
func (d *Discovery) GetCheckNow(nameContainer NameContainer) (CheckNow, error) {
	CheckDetails, ok := d.activeCheck[nameContainer]
	if !ok {
		return nil, fmt.Errorf("%w %s", errNoCheckAssociated, nameContainer.Name)
	}

	return CheckDetails.check.CheckNow, nil
}
