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
	"glouton/types"
	"strings"
	"time"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/vsphere"
)

type vSphere struct {
	host string
	opts config.VSphere
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
		logger.V(1).Printf("Can't create vSphere client for %q: %v", vSphere.host, err)

		return
	}

	hosts, vms, err := findDevices(soapCtx, finder)
	if err != nil {
		logger.V(1).Printf("Can't find devices on vSphere %q: %v", vSphere.host, err)

		return
	}

	logger.Printf("Found %d hosts and %d vms.", len(hosts), len(vms))

	describeHosts(soapCtx, hosts, deviceChan)
	describeVMs(soapCtx, vms, deviceChan)
}

func (vSphere *vSphere) makeInput() (telegraf.Input, *inputs.GathererOptions, error) {
	input, ok := telegraf_inputs.Inputs["vsphere"]
	if !ok {
		return nil, nil, inputs.ErrDisabledInput
	}

	vsphereInput, ok := input().(*vsphere.VSphere)
	if !ok {
		return nil, nil, inputs.ErrUnexpectedType
	}

	vsphereInput.Vcenters = []string{vSphere.opts.URL}
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

	internalInput := &internal.Input{
		Input: vsphereInput,
		Accumulator: internal.Accumulator{
			RenameMetrics:    renameMetrics,
			TransformMetrics: transformMetrics,
			RenameGlobal:     vSphere.renameGlobal,
		},
		Name: "vSphere",
	}
	opts := &inputs.GathererOptions{
		MinInterval:         time.Minute,
		ApplyDynamicRelabel: true,
	}

	// As we only have one vCenter per telegraf input, perhaps we should
	// build our own input designed for one vCenter; that would allow us
	// to pass context to the collecting method, as well as only starting
	// one goroutine per vCenter, instead of two now.
	// Note: our input would still rely on telegraf's vSphere endpoint,
	// but endpoints may be managed by us instead of telegraf's input.

	return internalInput, opts, nil
}

func (vSphere *vSphere) renameGlobal(gatherContext internal.GatherContext) (result internal.GatherContext, drop bool) {
	gatherContext.Tags[types.LabelMetaVSphere] = vSphere.host
	gatherContext.Tags[types.LabelMetaVSphereMOID] = gatherContext.Tags["moid"]

	if _, ok := gatherContext.Tags["moid"]; !ok {
		logger.Printf("No moid for %s/%v/%v", gatherContext.Measurement, gatherContext.OriginalFields, gatherContext.OriginalTags)
	}

	delete(gatherContext.Tags, "moid")
	delete(gatherContext.Tags, "rpname")
	delete(gatherContext.Tags, "guest")
	delete(gatherContext.Tags, "source")
	delete(gatherContext.Tags, "vmname")
	delete(gatherContext.Tags, "vcenter")

	if gatherContext.Tags["interface"] == "*" {
		delete(gatherContext.Tags, "interface")
	}

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
