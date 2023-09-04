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
	"glouton/inputs"
	"glouton/inputs/internal"
	"strings"
	"time"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/vsphere"
)

func New(url string, username, password string, insecureSkipVerify, monitorVMs bool) (telegraf.Input, *inputs.GathererOptions, error) {
	input, ok := telegraf_inputs.Inputs["vsphere"]
	if !ok {
		return nil, nil, inputs.ErrDisabledInput
	}

	vsphereInput, ok := input().(*vsphere.VSphere)
	if !ok {
		return nil, nil, inputs.ErrUnexpectedType
	}

	vsphereInput.Vcenters = []string{url}
	vsphereInput.Username = username
	vsphereInput.Password = password

	vsphereInput.VMInstances = monitorVMs
	vsphereInput.VMMetricInclude = []string{
		"cpu.usage.average",
		"cpu.latency.average",
		"mem.usage.average",
		"mem.swapped.average",
		"disk.read.average",
		"disk.write.average",
		"disk.usage.average",
		"net.transmitted.average",
		"net.received.average",
		"net.usage.average",
	}
	vsphereInput.HostMetricInclude = []string{
		"cpu.usage.average",
		"mem.active.average",
		"mem.swapused.average",
		"mem.totalCapacity.average",
		"mem.usage.average",
		"disk.usage.average",
		"net.transmitted.average",
	}
	vsphereInput.ResourcePoolMetricExclude = []string{"*"}
	vsphereInput.ClusterMetricExclude = []string{"*"}
	vsphereInput.DatastoreMetricExclude = []string{"*"}

	vsphereInput.InsecureSkipVerify = insecureSkipVerify

	internalInput := &internal.Input{
		Input: vsphereInput,
		Accumulator: internal.Accumulator{
			RenameMetrics:    renameMetrics,
			TransformMetrics: transformMetrics,
		},
		Name: "vSphere",
	}

	return internalInput, &inputs.GathererOptions{MinInterval: 20 * time.Second}, nil
}

func renameMetrics(currentContext internal.GatherContext, metricName string) (newMeasurement string, newMetricName string) {
	newMeasurement = currentContext.Measurement
	newMetricName = strings.TrimSuffix(metricName, "_average")

	return
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
			"usage_average": 1000, // KB/s to B/s
		},
		"vsphere_vm_net": {
			"transmitted_average": 1000, // KB/s to B/s
			"received_average":    1000, // KB/s to B/s
			"usage_average":       1000, // KB/s to B/s
		},
		// Host metrics
		"vsphere_host_mem": {
			"active_average":        1000,    // KB to B
			"swapused_average":      1000,    // KB to B
			"totalCapacity_average": 1000000, // MB to B
		},
		"vsphere_host_disk": {
			"usage_average": 1000, // KB/s to B/s
		},
		"vsphere_host_net": {
			"net_transmitted_average": 1000, // KB/s to B/s
		},
	}

	for field, factor := range factors[currentContext.Measurement] {
		if value, ok := fields[field]; ok {
			fields[field] = value * factor
		}
	}

	return fields
}
