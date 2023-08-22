package vsphere

import (
	"glouton/inputs"
	"glouton/inputs/internal"
	"strings"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/vsphere"
)

func New(urls []string, username, password string) (telegraf.Input, *inputs.GathererOptions, error) {
	input, ok := telegraf_inputs.Inputs["vsphere"]
	if !ok {
		return nil, nil, inputs.ErrDisabledInput
	}

	vsphereInput, ok := input().(*vsphere.VSphere)
	if !ok {
		return nil, nil, inputs.ErrUnexpectedType
	}

	vsphereInput.Vcenters = urls
	vsphereInput.Username = username
	vsphereInput.Password = password

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
		"mem.usage.average",
		"mem.swapused.average",
		"disk.throughput.contention.average",
		"disk.throughput.usage.average",
		"net.throughput.usage.average",
	}
	vsphereInput.ResourcePoolMetricExclude = []string{"*"}
	vsphereInput.ClusterMetricExclude = []string{"*"}
	vsphereInput.DatastoreMetricExclude = []string{"*"}

	vsphereInput.InsecureSkipVerify = true

	internalInput := &internal.Input{
		Input: vsphereInput,
		Accumulator: internal.Accumulator{
			RenameMetrics:    renameMetrics,
			TransformMetrics: transformMetrics,
		},
		Name: "vSphere",
	}

	return internalInput, &inputs.GathererOptions{}, nil
}

func renameMetrics(currentContext internal.GatherContext, metricName string) (newMeasurement string, newMetricName string) {
	newMeasurement = currentContext.Measurement
	newMetricName = strings.TrimSuffix(metricName, "_average")

	return
}

func transformMetrics(currentContext internal.GatherContext, fields map[string]float64, originalFields map[string]interface{}) map[string]float64 {
	_, _ = currentContext, originalFields

	factors := map[string]float64{
		"vsphere_vm_mem_swapped_average":                  1000, // KB to B
		"vsphere_vm_disk_read_average":                    1000, // KB to B
		"vsphere_vm_disk_write_average":                   1000, // KB/s to B/s
		"vsphere_vm_disk_usage_average":                   1000, // KB/s to B/s
		"vsphere_vm_net_transmitted_average":              1000, // KB/s to B/s
		"vsphere_vm_net_received_average":                 1000, // KB/s to B/s
		"vsphere_vm_net_usage_average":                    1000, // KB/s to B/s
		"vsphere_host_mem_swapused_average":               1000, // KB to B
		"vsphere_host_disk_throughput_contention_average": 1e-3, // ms to s
		"vsphere_host_disk_throughput_usage_average":      1000, // KB/s to B/s
		"vsphere_host_net_throughput_usage_average":       1000, // KB/s to B/s
	}

	for field := range fields {
		factor, ok := factors[field]
		if ok {
			fields[field] *= factor
		}
	}

	return fields
}
