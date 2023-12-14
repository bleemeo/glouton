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
	"maps"
	"time"

	"github.com/influxdata/telegraf"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/types"
)

func additionalClusterMetrics(ctx context.Context, client *vim25.Client, clusters []*object.ClusterComputeResource, caches *propsCaches, acc telegraf.Accumulator, retained retainedMetrics, h *Hierarchy, t0 time.Time) error {
	for _, cluster := range clusters {
		tags := map[string]string{
			"clustername": cluster.Name(),
			"dcname":      h.ParentDCName(cluster),
			"moid":        cluster.Reference().Value,
		}

		additionalClusterCPU(tags, cluster, caches.clusterCache, acc, retained, t0)

		errHostsCount := additionalClusterHostsCount(ctx, tags, client, cluster, caches.hostCache, acc, t0)
		if errHostsCount != nil {
			return errHostsCount
		}
	}

	return nil
}

func additionalClusterCPU(tags map[string]string, cluster *object.ClusterComputeResource, cache *propsCache[clusterLightProps], acc telegraf.Accumulator, retained retainedMetrics, t0 time.Time) {
	usagesMHz := retained.get("vsphere_cluster_cpu", "usagemhz_average", func(tags map[string]string, t []time.Time) bool {
		return tags["moid"] == cluster.Reference().Value && (len(t) == 0 || t[0].Equal(t0))
	})

	clusterProps, ok := cache.get(cluster.Reference().Value, true)
	if len(usagesMHz) == 0 || !ok {
		return
	}

	var sum int64

	for _, value := range usagesMHz {
		sum += value.(int64) //nolint: forcetypeassert
	}

	avg := float64(sum) / float64(len(usagesMHz))
	totalMHz := float64(clusterProps.ComputeResource.Summary.ComputeResourceSummary.TotalCpu)
	result := (avg / totalMHz) * 100

	cpuTags := maps.Clone(tags)
	cpuTags["cpu"] = instanceTotal

	acc.AddFields("vsphere_cluster_cpu", map[string]any{"usage_average": result}, cpuTags, t0)
}

func additionalClusterHostsCount(ctx context.Context, tags map[string]string, client *vim25.Client, cluster *object.ClusterComputeResource, cache *propsCache[hostLightProps], acc telegraf.Accumulator, t0 time.Time) error {
	hosts, err := cluster.Hosts(ctx)
	if err != nil {
		return err
	}

	hostProps, err := retrieveProps(ctx, client, hosts, relevantHostProperties, cache)
	if err != nil {
		return err
	}

	var running, stopped int

	for _, props := range hostProps {
		if props.Runtime.PowerState == types.HostSystemPowerStatePoweredOn {
			running++
		} else {
			stopped++
		}
	}

	fields := map[string]any{
		"running_count": running,
		"stopped_count": stopped,
	}

	acc.AddFields("hosts", fields, tags, t0)

	return nil
}

func additionalHostMetrics(_ context.Context, _ *vim25.Client, hosts []*object.HostSystem, acc telegraf.Accumulator, h *Hierarchy, vmStatesPerHost map[string][]bool, t0 time.Time) error {
	for _, host := range hosts {
		moid := host.Reference().Value
		if vmStates, ok := vmStatesPerHost[moid]; ok {
			var running, stopped int

			for _, isRunning := range vmStates {
				if isRunning {
					running++
				} else {
					stopped++
				}
			}

			fields := map[string]any{
				"running_count": running,
				"stopped_count": stopped,
			}

			tags := map[string]string{
				"clustername": h.ParentClusterName(host),
				"dcname":      h.ParentDCName(host),
				"esxhostname": host.Name(),
				"moid":        moid,
			}

			acc.AddFields("vms", fields, tags, t0)
		}
	}

	return nil
}

func additionalVMMetrics(ctx context.Context, client *vim25.Client, vms []*object.VirtualMachine, cache *propsCache[vmLightProps], acc telegraf.Accumulator, h *Hierarchy, vmStatePerHost map[string][]bool, t0 time.Time) error {
	vmProps, err := retrieveProps(ctx, client, vms, relevantVMProperties, cache)
	if err != nil {
		return err
	}

	for vm, props := range vmProps {
		if props.Runtime.Host != nil {
			host := props.Runtime.Host.Value
			vmState := props.Runtime.PowerState == types.VirtualMachinePowerStatePoweredOn // true for running, false for stopped

			if states, ok := vmStatePerHost[host]; ok {
				vmStatePerHost[host] = append(states, vmState)
			} else {
				vmStatePerHost[host] = []bool{vmState}
			}
		}

		if props.Guest != nil {
			for _, disk := range props.Guest.Disk {
				usage := 100 - (float64(disk.FreeSpace)*100)/float64(disk.Capacity) // Percentage of disk used

				tags := map[string]string{
					"clustername": h.ParentClusterName(vm),
					"dcname":      h.ParentDCName(vm),
					"esxhostname": h.ParentHostName(vm),
					"item":        disk.DiskPath,
					"moid":        vm.Reference().Value,
					"vmname":      vm.Name(),
				}

				acc.AddFields("vsphere_vm_disk", map[string]any{"used_perc": usage}, tags, t0)
			}
		}
	}

	return nil
}
