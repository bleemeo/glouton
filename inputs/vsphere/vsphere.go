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
	bleemeoTypes "glouton/bleemeo/types"
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

	state bleemeoTypes.State

	gatherer *vSphereGatherer

	deviceCache      map[string]bleemeoTypes.VSphereDevice
	noMetricsSince   map[string]int
	lastStatuses     map[string]types.Status
	lastErrorMessage string
	consecutiveErr   int

	l sync.Mutex
}

func newVSphere(host string, cfg config.VSphere, state bleemeoTypes.State) *vSphere {
	return &vSphere{
		host:           host,
		opts:           cfg,
		state:          state,
		deviceCache:    make(map[string]bleemeoTypes.VSphereDevice),
		noMetricsSince: make(map[string]int),
		lastStatuses:   make(map[string]types.Status),
	}
}

// Takes the lock
func (vSphere *vSphere) setErr(err error) {
	vSphere.l.Lock()
	defer vSphere.l.Unlock()

	if err == nil {
		vSphere.lastErrorMessage = ""
		vSphere.consecutiveErr = 0
	} else {
		vSphere.lastErrorMessage = err.Error()
		vSphere.consecutiveErr++
	}
}

func (vSphere *vSphere) getStatus() (types.Status, string) {
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

func (vSphere *vSphere) devices(ctx context.Context, deviceChan chan<- bleemeoTypes.VSphereDevice) {
	soapCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	logger.Printf("Discovering devices for vSphere %q ...", vSphere.host) // TODO: remove

	finder, err := newDeviceFinder(soapCtx, vSphere.opts)
	if err != nil {
		vSphere.setErr(err)
		logger.V(1).Printf("Can't create vSphere client for %q: %v", vSphere.host, err) // TODO: V(2) ?

		return
	}

	clusters, hosts, vms, err := findDevices(soapCtx, finder)
	if err != nil {
		vSphere.setErr(err)
		logger.V(1).Printf("Can't find devices on vSphere %q: %v", vSphere.host, err)

		return
	}

	logger.Printf("Found %d hosts and %d vms.", len(hosts), len(vms))

	vSphere.describeClusters(soapCtx, clusters, deviceChan)
	vSphere.describeHosts(soapCtx, hosts, deviceChan)
	vSphere.describeVMs(soapCtx, vms, deviceChan)

	vSphere.setErr(nil)
}

func (vSphere *vSphere) describeClusters(ctx context.Context, rawClusters []*object.ClusterComputeResource, deviceChan chan<- bleemeoTypes.VSphereDevice) {
	for _, cluster := range rawClusters {
		var clusterProps mo.ClusterComputeResource

		moid := cluster.Reference().Value

		err := cluster.Properties(ctx, cluster.Reference(), relevantClusterProperties, &clusterProps)
		if err != nil {
			logger.Printf("Failed to fetch cluster props: %v", err) // TODO: remove

			if dev, ok := vSphere.deviceCache[moid]; ok {
				dev.(*Cluster).err = err //nolint:forcetypeassert
			}

			continue
		}

		devCluster := describeCluster(cluster, clusterProps)
		deviceChan <- devCluster

		vSphere.l.Lock()
		vSphere.deviceCache[moid] = devCluster
		vSphere.l.Unlock()
	}
}

func (vSphere *vSphere) describeHosts(ctx context.Context, rawHosts []*object.HostSystem, deviceChan chan<- bleemeoTypes.VSphereDevice) {
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

		vSphere.l.Lock()
		vSphere.deviceCache[moid] = devHost
		vSphere.l.Unlock()
	}
}

func (vSphere *vSphere) describeVMs(ctx context.Context, rawVMs []*object.VirtualMachine, deviceChan chan<- bleemeoTypes.VSphereDevice) {
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

		vSphere.l.Lock()
		vSphere.deviceCache[moid] = devVM
		vSphere.l.Unlock()
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
	vsphereInput.HostInstances = true
	vsphereInput.DatastoreInstances = true
	vsphereInput.ClusterInstances = true

	vsphereInput.VMMetricInclude = []string{
		"cpu.usage.average",
		"cpu.latency.average",
		"mem.usage.average",
		"mem.swapped.average",
		"net.transmitted.average",
		"net.received.average",
		"virtualDisk.read.average",
		"virtualDisk.write.average",
	}
	vsphereInput.HostMetricInclude = []string{
		"cpu.usage.average",
		"mem.totalCapacity.average",
		"mem.usage.average",
		"mem.swapin.average",
		"mem.swapout.average",
		"datastore.read.average",
		"datastore.write.average",
		"net.transmitted.average",
		"net.received.average",
	}
	vsphereInput.DatastoreMetricInclude = []string{
		"datastore.write.average",
		"datastore.read.average",
		"disk.used.latest",
		"disk.capacity.latest",
	}
	vsphereInput.ClusterMetricInclude = []string{
		"cpu.usage.average",
		"mem.usage.average",
		"mem.swapused.average",
	}
	vsphereInput.DatacenterMetricExclude = []string{"*"}
	vsphereInput.ResourcePoolMetricExclude = []string{"*"}

	vsphereInput.InsecureSkipVerify = vSphere.opts.InsecureSkipVerify

	vsphereInput.Log = logger.NewTelegrafLog(vSphere.String())

	acc := &internal.Accumulator{
		RenameMetrics:    renameMetrics,
		TransformMetrics: transformMetrics,
		RenameGlobal:     vSphere.renameGlobal,
	}

	gatherer, err := newGatherer(vSphere.opts, vsphereInput, acc)
	if err != nil {
		return nil, registry.RegistrationOption{}, err
	}

	vSphere.gatherer = gatherer

	opt := registry.RegistrationOption{
		Description:         vSphere.String(),
		MinInterval:         time.Minute,
		StopCallback:        gatherer.stop,
		ApplyDynamicRelabel: true,
		GatherModifier:      vSphere.gatherModifier,
	}

	return gatherer, opt, nil
}

func (vSphere *vSphere) gatherModifier(mfs []*dto.MetricFamily, gatherErr error) []*dto.MetricFamily {
	vSphere.l.Lock()
	defer vSphere.l.Unlock()

	if len(vSphere.deviceCache) == 0 && (gatherErr != nil || vSphere.consecutiveErr > 0) {
		return append(mfs, vSphere.statusesWhenNoDevices(gatherErr)...)
	}

	seenDevices := make(map[string]bool)

	for _, mf := range mfs {
		for _, metric := range mf.GetMetric() {
			for _, label := range metric.GetLabel() {
				if label.GetName() == types.LabelMetaVSphereMOID {
					seenDevices[label.GetValue()] = true

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
		metricSeen := seenDevices[moid]

		switch {
		case vSphereStatus == types.StatusCritical:
			deviceStatus = types.StatusCritical
			deviceMsg = "vSphere is unreachable"

			if vSphereMsg != "" {
				// This telegraf/govmomi error isn't really problematic.
				if !strings.Contains(vSphereMsg, "A specified parameter was not correct: querySpec[0]") {
					deviceMsg += ": " + vSphereMsg
				}
			}
		case !dev.IsPoweredOn():
			deviceStatus = types.StatusCritical

			if err := dev.LatestError(); err != nil {
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

		// We only want to publish a critical status when it's new, not to store points for all offline devices.
		if deviceStatus == types.StatusOk || deviceStatus == types.StatusCritical && vSphere.lastStatuses[moid] != types.StatusCritical {
			vSphereDeviceStatus := &dto.MetricFamily{
				Name: proto.String("agent_status"),
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

func (vSphere *vSphere) statusesWhenNoDevices(gatherErr error) []*dto.MetricFamily {
	logger.Printf("No devices on %s", vSphere.String()) // TODO: remove

	associations, err := vSphere.state.GetByPrefix("bleemeo:vsphere:", map[string]string{})
	if err != nil {
		logger.Printf("Can't give status for %s: can't lookup state: %v", err) // TODO: V(2)

		return nil
	}

	deviceStatus := types.StatusCritical
	deviceMsg := "vSphere is unreachable: "

	if gatherErr != nil {
		deviceMsg += gatherErr.Error()
	} else {
		_, vSphereMessage := vSphere.getStatus()
		deviceMsg += vSphereMessage
	}

	var mfs []*dto.MetricFamily //nolint:prealloc

	for _, value := range associations {
		asso, ok := value.(map[string]string)
		if !ok {
			continue
		}

		if asso["Source"] != vSphere.host {
			continue
		}

		moid, ok := asso["MOID"]
		if !ok {
			continue
		}

		if vSphere.lastStatuses[moid] == types.StatusCritical {
			// We already sent this status
			continue
		}

		statusMetric := &dto.MetricFamily{
			Name: proto.String("agent_status"),
			Type: dto.MetricType_GAUGE.Enum(),
			Metric: []*dto.Metric{
				{
					Label: []*dto.LabelPair{
						{Name: proto.String(types.LabelMetaCurrentStatus), Value: proto.String(deviceStatus.String())},
						{Name: proto.String(types.LabelMetaCurrentDescription), Value: proto.String(deviceMsg)},
						{Name: proto.String(types.LabelMetaVSphere), Value: proto.String(vSphere.host)},
						{Name: proto.String(types.LabelMetaVSphereMOID), Value: proto.String(moid)},
						// Instead of the giving the MOID, which would be processed by RelabelHook(),
						// we directly set the agent ID (which is actually easier to have from here).
						// {Name: proto.String(types.LabelMetaBleemeoTargetAgentUUID), Value: proto.String(agentID)},
					},
					Gauge: &dto.Gauge{
						Value: proto.Float64(float64(deviceStatus.NagiosCode())),
					},
				},
			},
		}

		mfs = append(mfs, statusMetric)
		vSphere.lastStatuses[moid] = deviceStatus
	}

	return mfs
}

func (vSphere *vSphere) renameGlobal(gatherContext internal.GatherContext) (result internal.GatherContext, drop bool) {
	tags := maps.Clone(gatherContext.Tags) // Prevents labels from being unexpectedly removed

	tags[types.LabelMetaVSphere] = vSphere.host
	tags[types.LabelMetaVSphereMOID] = tags["moid"]

	delete(tags, "guest")
	delete(tags, "guesthostname")
	delete(tags, "moid")
	delete(tags, "rpname")
	delete(tags, "source")
	delete(tags, "uuid")
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
	newMetricName = strings.TrimSuffix(newMetricName, "_latest")

	// We remove the prefix "vsphere_(vm|host|datastore|cluster)_", except for "vsphere_vm_cpu latency"
	if newMetricName != "latency" {
		newMeasurement = strings.TrimPrefix(newMeasurement, "vsphere_")
		newMeasurement = strings.TrimPrefix(newMeasurement, "vm_")
	}

	newMeasurement = strings.TrimPrefix(newMeasurement, "host_")
	newMeasurement = strings.TrimPrefix(newMeasurement, "datastore_")
	newMeasurement = strings.TrimPrefix(newMeasurement, "cluster_")

	switch newMeasurement {
	case "cpu", "vsphere_vm_cpu":
		newMetricName = strings.Replace(newMetricName, "usage", "used", 1)
		if newMetricName == "latency" {
			newMetricName = "latency_perc"
		}
	case "mem":
		switch newMetricName {
		case "swapped", "swapused":
			newMeasurement = "swap" //nolint:goconst
			newMetricName = "used"
		case "swapin":
			newMeasurement = "swap"
			newMetricName = "in"
		case "swapout":
			newMeasurement = "swap"
			newMetricName = "out"
		default:
			newMetricName = strings.Replace(newMetricName, "usage", "used_perc", 1)
			newMetricName = strings.Replace(newMetricName, "totalCapacity", "total", 1)
		}
	case "disk", "virtualDisk", "datastore":
		if newMetricName == "read" || newMetricName == "write" {
			newMeasurement = "io"
			newMetricName = strings.Replace(newMetricName, "read", "read_bytes", 1)
			newMetricName = strings.Replace(newMetricName, "write", "write_bytes", 1)
		} else if newMetricName == "capacity" {
			newMetricName = "total"
		}
	case "net":
		newMetricName = strings.Replace(newMetricName, "received", "bits_recv", 1)
		newMetricName = strings.Replace(newMetricName, "transmitted", "bits_sent", 1)
	}

	return newMeasurement, newMetricName
}

func transformMetrics(currentContext internal.GatherContext, fields map[string]float64, originalFields map[string]interface{}) map[string]float64 {
	_ = originalFields

	// map is: measurement -> field -> factor
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
			"swapin_average":        1000,    // KB to B
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
		// Datastore metrics
		"vsphere_datastore_datastore": {
			"write_average": 8000, // KB/s to b/s
			"read_average":  8000, // KB/s to b/s
		},
		"vsphere_datastore_disk": {
			"used_latest":     1000, // KB to B
			"capacity_latest": 1000, // KB to B
		},
		// Cluster metrics
		"vsphere_cluster_mem": {
			"swapused_average": 1000, // KB to B
		},
	}

	for field, factor := range factors[currentContext.Measurement] {
		if value, ok := fields[field]; ok {
			fields[field] = value * factor
		}
	}

	return fields
}
