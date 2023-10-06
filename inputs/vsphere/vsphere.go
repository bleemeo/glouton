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
	"fmt"
	"glouton/config"
	"glouton/inputs"
	"glouton/inputs/internal"
	"glouton/logger"
	"glouton/prometheus/registry"
	"glouton/types"
	"maps"
	"strings"
	"sync"
	"time"

	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/vsphere"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/mo"
	"google.golang.org/protobuf/proto"
)

const (
	vCenterConsecutiveErrorsStatusThreshold = 2
	noMetricsStatusThreshold                = 5
)

type vSphere struct {
	host string
	opts config.VSphere

	gatherer *vSphereGatherer

	deviceCache      map[string]Device
	noMetricsSince   map[string]int
	lastStatuses     map[string]types.Status
	lastErrorMessage string
	consecutiveErr   int
	errLock          sync.Mutex
}

func newVSphere(host string, cfg config.VSphere) *vSphere {
	return &vSphere{
		host:           host,
		opts:           cfg,
		deviceCache:    make(map[string]Device),
		noMetricsSince: make(map[string]int),
		lastStatuses:   make(map[string]types.Status),
	}
}

func (vSphere *vSphere) setErr(err error) {
	vSphere.errLock.Lock()
	defer vSphere.errLock.Unlock()

	if err == nil {
		vSphere.lastErrorMessage = ""
		vSphere.consecutiveErr = 0
	} else {
		vSphere.lastErrorMessage = err.Error()
		vSphere.consecutiveErr++
	}
}

func (vSphere *vSphere) getStatus() (types.Status, string) {
	vSphere.errLock.Lock()
	defer vSphere.errLock.Unlock()

	if vSphere.consecutiveErr >= vCenterConsecutiveErrorsStatusThreshold {
		return types.StatusCritical, vSphere.lastErrorMessage
	}

	if vSphere.gatherer.lastErr != nil {
		// Should this necessarily be critical ?
		return types.StatusCritical, "endpoint error: " + vSphere.gatherer.lastErr.Error()
	}

	return types.StatusOk, ""
}

func (vSphere *vSphere) String() string {
	return fmt.Sprintf("vSphere(%s)", vSphere.host)
}

func (vSphere *vSphere) devices(ctx context.Context, deviceChan chan<- Device) {
	soapCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	logger.Printf("Discovering devices for vSphere %q ...", vSphere.host) // TODO: remove

	finder, err := newDeviceFinder(soapCtx, vSphere.opts)
	if err != nil {
		vSphere.setErr(err)
		logger.V(1).Printf("Can't create vSphere client for %q: %v", vSphere.host, err) // TODO: V(2) ?

		return
	}

	hosts, vms, err := findDevices(soapCtx, finder)
	if err != nil {
		vSphere.setErr(err)
		logger.V(1).Printf("Can't find devices on vSphere %q: %v", vSphere.host, err)

		return
	}

	logger.Printf("Found %d hosts and %d vms.", len(hosts), len(vms))

	vSphere.describeHosts(soapCtx, hosts, deviceChan)
	vSphere.describeVMs(soapCtx, vms, deviceChan)

	vSphere.setErr(nil)
}

func (vSphere *vSphere) describeHosts(ctx context.Context, rawHosts []*object.HostSystem, deviceChan chan<- Device) {
	for _, host := range rawHosts {
		var hostProps mo.HostSystem

		moid := host.Reference().Value

		err := host.Properties(ctx, host.Reference(), relevantHostProperties, &hostProps)
		if err != nil {
			logger.Printf("Failed to fetch host props: %v", err) // TODO: remove

			if dev, ok := vSphere.deviceCache[moid]; ok {
				dev.(*HostSystem).err = err //nolint:forcetypeassert
			}

			continue
		}

		devHost := describeHost(host, hostProps)
		deviceChan <- devHost
		vSphere.deviceCache[moid] = devHost
	}
}

func (vSphere *vSphere) describeVMs(ctx context.Context, rawVMs []*object.VirtualMachine, deviceChan chan<- Device) {
	for _, vm := range rawVMs {
		var vmProps mo.VirtualMachine

		moid := vm.Reference().Value

		err := vm.Properties(ctx, vm.Reference(), relevantVMProperties, &vmProps)
		if err != nil {
			logger.Printf("Failed to fetch VM props:", err) // TODO: remove

			if dev, ok := vSphere.deviceCache[moid]; ok {
				dev.(*VirtualMachine).err = err //nolint:forcetypeassert
			}

			continue
		}

		devVM := describeVM(ctx, vm, vmProps)
		deviceChan <- devVM
		vSphere.deviceCache[moid] = devVM
	}
}

func (vSphere *vSphere) makeGatherer() (prometheus.Gatherer, registry.RegistrationOption, error) {
	input, ok := telegraf_inputs.Inputs["vsphere"]
	if !ok {
		return nil, registry.RegistrationOption{}, inputs.ErrDisabledInput
	}

	vsphereInput, ok := input().(*vsphere.VSphere)
	if !ok {
		return nil, registry.RegistrationOption{}, inputs.ErrUnexpectedType
	}

	vsphereInput.Username = vSphere.opts.Username
	vsphereInput.Password = vSphere.opts.Password

	vsphereInput.VMInstances = vSphere.opts.MonitorVMs
	vsphereInput.VMMetricInclude = []string{
		"cpu.usage.average",
		"cpu.latency.average",
		"mem.usage.average",
		"mem.swapped.average",
		"disk.read.average",
		"disk.write.average",
		"net.transmitted.average",
		"net.received.average",
	}
	vsphereInput.HostMetricInclude = []string{
		"cpu.usage.average",
		"mem.totalCapacity.average",
		"mem.usage.average",
		"mem.swapout.average",
		"disk.read.average",
		"disk.write.average",
		"net.transmitted.average",
		"net.received.average",
	}
	vsphereInput.DatacenterMetricExclude = []string{"*"}
	vsphereInput.ResourcePoolMetricExclude = []string{"*"}
	vsphereInput.ClusterMetricExclude = []string{"*"}
	vsphereInput.DatastoreMetricExclude = []string{"*"}

	vsphereInput.InsecureSkipVerify = vSphere.opts.InsecureSkipVerify

	vsphereInput.Log = logger.NewTelegrafLog(vSphere.String())

	acc := &internal.Accumulator{
		RenameMetrics:    renameMetrics,
		TransformMetrics: transformMetrics,
		RenameGlobal:     vSphere.renameGlobal,
	}

	gatherer, err := newGatherer(vSphere.opts.URL, vsphereInput, acc)
	if err != nil {
		return nil, registry.RegistrationOption{}, err
	}

	vSphere.gatherer = gatherer

	opt := registry.RegistrationOption{
		MinInterval:         time.Minute,
		StopCallback:        gatherer.stop,
		ApplyDynamicRelabel: true,
		GatherModifier:      vSphere.gatherModifier,
	}

	return gatherer, opt, nil
}

func (vSphere *vSphere) gatherModifier(mfs []*dto.MetricFamily) []*dto.MetricFamily {
	seenDevices := make(map[string]struct{})

	for _, mf := range mfs {
		for _, metric := range mf.Metric {
			for _, label := range metric.Label {
				if label.GetName() == types.LabelMetaVSphereMOID {
					seenDevices[label.GetValue()] = struct{}{}

					break
				}
			}
		}
	}

	vSphereStatus, vSphereMsg := vSphere.getStatus()

	for _, dev := range vSphere.deviceCache {
		var (
			deviceStatus types.Status
			deviceMsg    string
		)

		moid := dev.MOID()
		_, metricSeen := seenDevices[moid]

		switch {
		case vSphereStatus == types.StatusCritical:
			deviceStatus = types.StatusCritical
			deviceMsg = "vCenter is unreachable"

			if vSphereMsg != "" { // Maybe no error message
				deviceMsg += ": " + vSphereMsg
			}
		case !dev.isPoweredOn():
			deviceStatus = types.StatusCritical

			if err := dev.latestError(); err != nil {
				deviceMsg = err.Error()
			} else {
				deviceMsg = dev.Kind() + " is stopped"
			}
		case metricSeen:
			deviceStatus = types.StatusOk
			vSphere.noMetricsSince[moid] = 0
		case !metricSeen:
			vSphere.noMetricsSince[moid]++
			if vSphere.noMetricsSince[moid] >= noMetricsStatusThreshold {
				deviceStatus = types.StatusCritical
				deviceMsg = "No metrics seen since a long time"
			}
		}

		if deviceStatus == types.StatusOk || deviceStatus == types.StatusCritical && vSphere.lastStatuses[moid] != types.StatusCritical {
			vSphereDeviceStatus := &dto.MetricFamily{
				Name: proto.String("vsphere_device_status"),
				Type: dto.MetricType_GAUGE.Enum(),
				Metric: []*dto.Metric{
					{
						Label: []*dto.LabelPair{
							{Name: proto.String(types.LabelMetaCurrentStatus), Value: proto.String(deviceStatus.String())},
							{Name: proto.String(types.LabelMetaCurrentDescription), Value: proto.String(deviceMsg)},
							{Name: proto.String(types.LabelMetaVSphere), Value: proto.String(vSphere.host)},
							{Name: proto.String(types.LabelMetaVSphereMOID), Value: proto.String(moid)},
						},
						Gauge: &dto.Gauge{
							Value: proto.Float64(float64(deviceStatus.NagiosCode())),
						},
					},
				},
			}

			mfs = append(mfs, vSphereDeviceStatus)
		}

		vSphere.lastStatuses[moid] = deviceStatus
	}

	return mfs
}

func (vSphere *vSphere) renameGlobal(gatherContext internal.GatherContext) (result internal.GatherContext, drop bool) {
	tags := maps.Clone(gatherContext.Tags) // Prevents some labels to be removed

	tags[types.LabelMetaVSphere] = vSphere.host
	tags[types.LabelMetaVSphereMOID] = tags["moid"]

	delete(tags, "moid")
	delete(tags, "rpname")
	delete(tags, "guest")
	delete(tags, "source")
	delete(tags, "vcenter")

	if tags["interface"] == "*" { // Get rid of this "useless" label
		delete(tags, "interface")
	}

	gatherContext.Tags = tags

	return gatherContext, false
}

func renameMetrics(currentContext internal.GatherContext, metricName string) (newMeasurement string, newMetricName string) {
	newMeasurement = currentContext.Measurement
	newMetricName = strings.TrimSuffix(metricName, "_average")

	switch newMeasurement {
	// VM metrics
	case "vsphere_vm_cpu":
		newMetricName = strings.Replace(newMetricName, "usage", "used", 1)
		newMetricName += "_perc" // For now, all VM CPU metrics are given as a percentage.
	case "vsphere_vm_mem":
		if newMetricName == "swapped" {
			newMeasurement = "vsphere_vm_swap"
			newMetricName = "used"
		} else {
			newMetricName = strings.Replace(newMetricName, "usage", "used_perc", 1)
		}
	case "vsphere_vm_disk":
		newMeasurement = "vsphere_vm_io"
		newMetricName = strings.Replace(newMetricName, "read", "read_bytes", 1)
		newMetricName = strings.Replace(newMetricName, "write", "write_bytes", 1)
	case "vsphere_vm_net":
		newMetricName = strings.Replace(newMetricName, "received", "bits_recv", 1)
		newMetricName = strings.Replace(newMetricName, "transmitted", "bits_sent", 1)
	// Host metrics
	case "vsphere_host_cpu":
		newMetricName = strings.Replace(newMetricName, "usage", "used_perc", 1)
	case "vsphere_host_mem":
		if newMetricName == "swapout" {
			newMeasurement = "vsphere_host_swap"
			newMetricName = "out"
		} else {
			newMetricName = strings.Replace(newMetricName, "totalCapacity", "total", 1)
			newMetricName = strings.Replace(newMetricName, "usage", "used_perc", 1)
		}
	case "vsphere_host_disk":
		newMeasurement = "vsphere_host_io"
		newMetricName = strings.Replace(newMetricName, "read", "read_bytes", 1)
		newMetricName = strings.Replace(newMetricName, "write", "write_bytes", 1)
	case "vsphere_host_net":
		newMetricName = strings.Replace(newMetricName, "received", "bits_recv", 1)
		newMetricName = strings.Replace(newMetricName, "transmitted", "bits_sent", 1)
	}

	return newMeasurement, newMetricName
}

func transformMetrics(currentContext internal.GatherContext, fields map[string]float64, originalFields map[string]interface{}) map[string]float64 {
	_ = originalFields

	// map measurement -> field -> factor
	factors := map[string]map[string]float64{
		// VM metrics
		"vsphere_vm_mem": {
			"swapped_average": 1000, // KB to B
		},
		"vsphere_vm_disk": {
			"read_average":  1000, // KB/s to B/s
			"write_average": 1000, // KB/s to B/s
		},
		"vsphere_vm_net": {
			"received_average":    8000, // KB/s to b/s
			"transmitted_average": 8000, // KB/s to b/s
		},
		// Host metrics
		"vsphere_host_mem": {
			"totalCapacity_average": 1000000, // MB to B
			"swapout_average":       1000,    // KB to B
		},
		"vsphere_host_disk": {
			"read_average":  1000, // KB/s to B/s
			"write_average": 1000, // KB/s to B/s
		},
		"vsphere_host_net": {
			"received_average":    8000, // KB/s to b/s
			"transmitted_average": 8000, // KB/s to b/s
		},
	}

	for field, factor := range factors[currentContext.Measurement] {
		if value, ok := fields[field]; ok {
			fields[field] = value * factor
		}
	}

	return fields
}
