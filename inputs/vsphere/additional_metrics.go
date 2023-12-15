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
	"glouton/logger"
	"maps"
	"time"

	"github.com/influxdata/telegraf"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/types"
)

func additionalClusterMetrics(ctx context.Context, client *vim25.Client, clusters []*object.ClusterComputeResource, datastores []*object.Datastore, caches *propsCaches, acc telegraf.Accumulator, retained retainedMetrics, h *Hierarchy, t0 time.Time) error {
	for _, cluster := range clusters {
		// Defining common tags for all metrics in this cluster,
		// we will clone this map in case extra tags need to be added.
		tags := map[string]string{
			"clustername": cluster.Name(),
			"dcname":      h.ParentDCName(cluster),
			"moid":        cluster.Reference().Value,
		}

		hosts, err := cluster.Hosts(ctx)
		if err != nil {
			return err
		}

		hostMOIDs := make(map[string]bool, len(hosts))

		for _, host := range hosts {
			hostMOIDs[host.Reference().Value] = true
		}

		errClusterCPU := additionalClusterCPU(maps.Clone(tags), cluster, hostMOIDs, caches.clusterCache, acc, retained, t0)
		if errClusterCPU != nil {
			return errClusterCPU
		}

		errClusterMemory := additionalClusterMemory(maps.Clone(tags), cluster, hostMOIDs, caches, acc, retained, t0)
		if errClusterMemory != nil {
			return errClusterMemory
		}

		errHostsCount := additionalClusterHostsCount(ctx, maps.Clone(tags), client, hosts, caches.hostCache, acc, t0)
		if errHostsCount != nil {
			return errHostsCount
		}
	}

	for _, datastore := range datastores {
		tags := map[string]string{
			"dsname": datastore.Name(),
			"moid":   datastore.Reference().Value,
		}

		hosts, err := datastore.AttachedHosts(ctx)
		if err != nil {
			return err
		}

		hostMOIDs := make(map[string]bool, len(hosts))

		for _, host := range hosts {
			hostMOIDs[host.Reference().Value] = true
		}

		logger.Printf("Host MOIDs of %s are %v", datastore.Name(), hostMOIDs)

		errDatastoreIO := additionalDatastoreIO(maps.Clone(tags), hostMOIDs, acc, retained, t0)
		if errDatastoreIO != nil {
			return errDatastoreIO
		}
	}

	return nil
}

func additionalClusterCPU(tags map[string]string, cluster *object.ClusterComputeResource, hostMOIDs map[string]bool, cache *propsCache[clusterLightProps], acc telegraf.Accumulator, retained retainedMetrics, t0 time.Time) error {
	hostUsagesMHz := retained.get("vsphere_host_cpu", "usagemhz_average", func(tags map[string]string, t []time.Time) bool {
		return hostMOIDs[tags["moid"]] && (len(t) == 0 || t[0].Equal(t0))
	})

	clusterProps, ok := cache.get(cluster.Reference().Value, true)
	if len(hostUsagesMHz) == 0 || !ok {
		return nil
	}

	var sum int64

	for _, value := range hostUsagesMHz {
		sum += value.(int64) //nolint: forcetypeassert
	}

	totalMHz := float64(clusterProps.ComputeResource.Summary.ComputeResourceSummary.TotalCpu)
	result := (float64(sum) / totalMHz) * 100

	tags["cpu"] = instanceTotal

	logger.Printf("Cluster CPU: (%d / %f) * 100 = %f", sum, totalMHz, result)

	if sum == 0 {
		logger.Printf("Sum is 0 because: %v", hostUsagesMHz)
	}

	acc.AddFields("vsphere_cluster_cpu", map[string]any{"usage_average": result}, tags, t0)

	return nil
}

func additionalClusterMemory(tags map[string]string, cluster *object.ClusterComputeResource, hostMOIDs map[string]bool, caches *propsCaches, acc telegraf.Accumulator, retained retainedMetrics, t0 time.Time) error {
	clusterProps, ok := caches.clusterCache.get(cluster.Reference().Value, true)
	if !ok {
		return nil
	}

	hostsUsageMB := retained.reduce("vsphere_host_mem", "usage_average", 0., func(acc, value any, tags map[string]string, t []time.Time) any {
		moid := tags["moid"]
		if !hostMOIDs[moid] || len(t) == 0 || !t[0].Equal(t0) {
			return acc
		}

		hostProps, ok := caches.hostCache.get(moid, true)
		if !ok {
			return acc
		}

		ac, val := acc.(float64), value.(float64) //nolint: forcetypeassert
		hostMemMB := val / 100 * float64(hostProps.Hardware.MemorySize)

		return ac + hostMemMB
	})

	if hostsUsageMB == nil {
		return nil
	}

	totalMB := float64(clusterProps.ComputeResource.Summary.ComputeResourceSummary.TotalMemory)
	result := (hostsUsageMB.(float64) / totalMB) * 100 //nolint: forcetypeassert

	logger.Printf("Cluster memory: (%f / %f) * 100 = %f", hostsUsageMB, totalMB, result)

	acc.AddFields("vsphere_cluster_mem", map[string]any{"used_perc": result}, tags, t0)

	return nil
}

func additionalClusterHostsCount(ctx context.Context, tags map[string]string, client *vim25.Client, hosts []*object.HostSystem, cache *propsCache[hostLightProps], acc telegraf.Accumulator, t0 time.Time) error {
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

func additionalDatastoreIO(tags map[string]string, hostMOIDs map[string]bool, acc telegraf.Accumulator, retained retainedMetrics, t0 time.Time) error {
	hostsReadKBps := retained.get("vsphere_host_datastore", "read_average", func(tags map[string]string, t []time.Time) bool {
		return hostMOIDs[tags["moid"]] && approxTimeEqual(t0, t)
	})
	hostsWriteKBps := retained.get("vsphere_host_datastore", "write_average", func(tags map[string]string, t []time.Time) bool {
		return hostMOIDs[tags["moid"]] && approxTimeEqual(t0, t)
	})

	logger.Printf("%s: HR=%v / HW=%v", tags["dsname"], hostsReadKBps, hostsWriteKBps)

	if hostsReadKBps == nil || hostsWriteKBps == nil {
		return nil
	}

	var readSum, writeSum int64

	for _, read := range hostsReadKBps {
		readSum += read.(int64)
	}

	for _, write := range hostsWriteKBps {
		writeSum += write.(int64)
	}

	fields := map[string]any{
		"read_average":  readSum,
		"write_average": writeSum,
	}

	logger.Printf("Datastore %q read/write: %d/%d", tags["dsname"], readSum, writeSum)

	acc.AddFields("vsphere_datastore_datastore", fields, tags, t0)

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

func approxTimeEqual(t0 time.Time, t []time.Time) bool {
	if len(t) == 0 {
		return true
	}

	return t0.Sub(t[0]) <= time.Minute
}
