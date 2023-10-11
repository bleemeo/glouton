// Copyright 2015-2023 Bleemeo
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

package vsphere

import (
	"context"
	"encoding/json"
	bleemeoTypes "glouton/bleemeo/types"
	"glouton/config"
	"glouton/crashreport"
	"glouton/logger"
	"glouton/prometheus/registry"
	"glouton/types"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	KindHost = "HostSystem"
	KindVM   = "VirtualMachine"
)

type Manager struct {
	vSpheres map[string]*vSphere

	l                 sync.Mutex
	lastDevices       []bleemeoTypes.VSphereDevice
	lastDevicesUpdate time.Time

	lastChange time.Time
}

func NewManager() *Manager {
	return &Manager{}
}

func (m *Manager) LastChange(ctx context.Context) time.Time {
	m.Devices(ctx, 2*time.Minute)

	return m.lastChange
}

func (m *Manager) EndpointsInError() map[string]struct{} {
	endpoints := make(map[string]struct{})

	for _, vSphere := range m.vSpheres {
		if vSphere.gatherer == nil || vSphere.consecutiveErr > 0 {
			endpoints[vSphere.host] = struct{}{}
		}
	}

	return endpoints
}

func (m *Manager) RegisterGatherers(vSphereCfgs []config.VSphere, registerGatherer func(opt registry.RegistrationOption, gatherer prometheus.Gatherer) (int, error), state bleemeoTypes.State) {
	m.vSpheres = make(map[string]*vSphere)

	for _, vSphereCfg := range vSphereCfgs {
		u, err := url.Parse(vSphereCfg.URL)
		if err != nil {
			logger.V(1).Printf("Failed to parse vSphere URL %q: %v", vSphereCfg.URL, err)

			continue
		}

		if _, alreadyExists := m.vSpheres[u.Host]; alreadyExists {
			continue
		}

		vSphere := newVSphere(u.Host, vSphereCfg, state)

		gatherer, opt, err := vSphere.makeGatherer()
		if err != nil {
			logger.Printf("Failed to create gatherer for %s: %v", vSphere.String(), err)

			continue
		}

		_, err = registerGatherer(opt, gatherer)
		if err != nil {
			logger.Printf("Failed to register gatherer for %s: %v", vSphere.String(), err)

			continue
		}

		m.vSpheres[u.Host] = vSphere
	}
}

func (m *Manager) Devices(ctx context.Context, maxAge time.Duration) []bleemeoTypes.VSphereDevice {
	m.l.Lock()
	defer m.l.Unlock()

	if time.Since(m.lastDevicesUpdate) < maxAge {
		return m.lastDevices
	}

	startTime := time.Now()

	deviceChan := make(chan bleemeoTypes.VSphereDevice)
	wg := new(sync.WaitGroup)

	for _, vSphere := range m.vSpheres {
		vSphere := vSphere

		wg.Add(1)

		go func() {
			defer crashreport.ProcessPanic()

			vSphere.devices(ctx, deviceChan)
			wg.Done()
		}()
	}

	go func() { wg.Wait(); close(deviceChan) }()

	var devices []bleemeoTypes.VSphereDevice //nolint:prealloc
	var moids []string                       //nolint:prealloc,wsl // TODO: remove

	for device := range deviceChan {
		devices = append(devices, device)
		moids = append(moids, device.MOID()) // TODO: remove
	}

	logger.Printf("Found devices: %s", strings.Join(moids, ", ")) // TODO: remove

	if !reflect.DeepEqual(devices, m.lastDevices) {
		m.lastChange = time.Now()

		logger.Printf("vSphere devices changed") // TODO: remove
	}

	m.lastDevices = devices
	m.lastDevicesUpdate = time.Now()

	logger.Printf("vSphere devices discovery done in %s", time.Since(startTime)) // TODO: V(2)

	return devices
}

// FindDevice returns the device from the given vSphere that has the given MOID.
// If no matching device is found, it returns nil.
func (m *Manager) FindDevice(ctx context.Context, vSphereHost, moid string) bleemeoTypes.VSphereDevice {
	// We specify a small max age here, because as metric gathering is done every minute,
	// there's a good chance to discover new vSphere VMs from the metric gathering.
	devices := m.Devices(ctx, 5*time.Minute)

	for _, dev := range devices {
		if dev.Source() == vSphereHost && dev.MOID() == moid {
			return dev
		}
	}

	return nil
}

type device struct {
	// The source is the host address of the vCenter/ESXI from which this device was described.
	source     string
	moid       string
	name       string
	facts      map[string]string
	powerState string
	err        error
}

func (dev *device) FQDN() string {
	var domain string

	if dev.facts["domain"] != "" {
		domain = "." + dev.facts["domain"]
	}

	return dev.name + domain // TODO: slugify ?
}

func (dev *device) Source() string {
	return dev.source
}

func (dev *device) MOID() string {
	return dev.moid
}

func (dev *device) Name() string {
	return dev.name
}

// Facts returns the facts of this device, but ignores the given maxAge
// since the facts had been gathered at the same as the device itself.
func (dev *device) Facts() map[string]string {
	return dev.facts
}

func (dev *device) IsPoweredOn() bool {
	return dev.powerState == "poweredOn"
}

func (dev *device) LatestError() error {
	return dev.err
}

type HostSystem struct {
	device
}

func (host *HostSystem) Kind() string {
	return KindHost
}

type VirtualMachine struct {
	device
	UUID string
}

func (vm *VirtualMachine) Kind() string {
	return KindVM
}

func (m *Manager) DiagnosticVSphere(ctx context.Context, archive types.ArchiveWriter, getAssociations func(ctx context.Context, devices []bleemeoTypes.VSphereDevice) (map[string]string, error)) error {
	file, err := archive.Create("vsphere.json")
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// 10min of max age to reuse devices found by the last metric collection,
	// while ensuring to display information close to reality.
	devices := m.Devices(ctx, 10*time.Minute)

	associations, err := getAssociations(ctx, devices)
	if err != nil {
		logger.V(1).Println("Failed to diagnostic vSphere associations:", err)
	}

	type device struct {
		Source                 string            `json:"source"`
		Kind                   string            `json:"kind"`
		MOID                   string            `json:"moid"`
		Name                   string            `json:"name"`
		AssociatedBleemeoAgent string            `json:"associated_bleemeo_agent,omitempty"`
		Error                  string            `json:"error,omitempty"`
		Facts                  map[string]string `json:"facts"`
	}

	finalDevices := make([]device, len(devices))

	for i, dev := range devices {
		var deviceError string

		if err := dev.LatestError(); err != nil {
			deviceError = err.Error()
		}

		finalDevices[i] = device{
			Source:                 dev.Source(),
			Kind:                   dev.Kind(),
			MOID:                   dev.MOID(),
			Name:                   dev.Name(),
			AssociatedBleemeoAgent: associations[dev.Source()+dev.MOID()],
			Error:                  deviceError,
			Facts:                  dev.Facts(),
		}
	}

	sort.Slice(finalDevices, func(i, j int) bool {
		// Sort by source, then by MOID
		if finalDevices[i].Source == finalDevices[j].Source {
			return finalDevices[i].MOID < finalDevices[j].MOID
		}

		return finalDevices[i].Source < finalDevices[j].Source
	})

	endpointStatuses := make(map[string]string, len(m.vSpheres))

	for host, vSphere := range m.vSpheres {
		status := "ok"

		if vSphere.lastErrorMessage != "" {
			status = vSphere.lastErrorMessage
		} else if vSphere.gatherer.endpoint == nil {
			status = vSphere.gatherer.lastErr.Error()
		}

		endpointStatuses[host] = status
	}

	diagnosticContent := struct {
		Endpoints map[string]string `json:"endpoints"`
		Devices   []device          `json:"devices"`
	}{
		Endpoints: endpointStatuses,
		Devices:   finalDevices,
	}

	return json.NewEncoder(file).Encode(diagnosticContent)
}
