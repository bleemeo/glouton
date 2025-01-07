// Copyright 2015-2025 Bleemeo
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

func additionalClusterMetrics(ctx context.Context, client *vim25.Client, clusters []*object.ClusterComputeResource, datastores []*object.Datastore, caches *propsCaches, acc telegraf.Accumulator, retained retainedMetrics, h *Hierarchy, t0 time.Time) error {
	retained.sort() // Pre-sort points according to timestamp in ascending order

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

		errClusterCPU := additionalClusterCPU(maps.Clone(tags), cluster, hostMOIDs, caches.clusterCache, acc, retained)
		if errClusterCPU != nil {
			return errClusterCPU
		}

		errClusterMemory := additionalClusterMemory(maps.Clone(tags), cluster, hostMOIDs, caches, acc, retained)
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

		errDatastoreIO := additionalDatastoreIO(maps.Clone(tags), hostMOIDs, acc, retained)
		if errDatastoreIO != nil {
			return errDatastoreIO
		}
	}

	return nil
}

func additionalClusterCPU(tags map[string]string, cluster *object.ClusterComputeResource, hostMOIDs map[string]bool, cache *propsCache[clusterLightProps], acc telegraf.Accumulator, retained retainedMetrics) error {
	clusterProps, ok := cache.get(cluster.Reference().Value, true)
	if !ok {
		return nil
	}

	hostUsagesMHzPerTS := retained.get("vsphere_host_cpu", "usagemhz_average", func(tags map[string]string) bool {
		return hostMOIDs[tags["moid"]]
	})

	for ts, hostUsagesMHz := range hostUsagesMHzPerTS {
		var sum int64

		for _, value := range hostUsagesMHz {
			sum += value.(int64) //nolint: forcetypeassert
		}

		totalMHz := float64(clusterProps.ComputeResource.Summary.ComputeResourceSummary.TotalCpu)
		result := (float64(sum) / totalMHz) * 100

		tags["cpu"] = instanceTotal

		acc.AddFields("vsphere_cluster_cpu", map[string]any{"usage_average": result}, tags, time.Unix(ts, 0))
	}

	return nil
}

func additionalClusterMemory(tags map[string]string, cluster *object.ClusterComputeResource, hostMOIDs map[string]bool, caches *propsCaches, acc telegraf.Accumulator, retained retainedMetrics) error {
	clusterProps, ok := caches.clusterCache.get(cluster.Reference().Value, true)
	if !ok {
		return nil
	}

	hostsUsageMBPerTS := retained.reduce("vsphere_host_mem", "usage_average", nil, func(acc any, value any, tags map[string]string, t time.Time) any {
		moid := tags["moid"]
		if !hostMOIDs[moid] {
			return acc
		}

		hostProps, ok := caches.hostCache.get(moid, true)
		if !ok {
			return acc
		}

		if acc == nil {
			acc = make(map[int64]float64) // acc was nil until here, in case no valid values were found
		}

		ac, val := acc.(map[int64]float64), value.(float64) //nolint: forcetypeassert
		ac[t.Unix()] = val / 100 * float64(hostProps.Hardware.MemorySize)

		return ac
	})

	if hostsUsageMBPerTS == nil {
		return nil
	}

	for ts, hostsUsageMB := range hostsUsageMBPerTS.(map[int64]float64) { //nolint: forcetypeassert
		totalMB := float64(clusterProps.ComputeResource.Summary.ComputeResourceSummary.TotalMemory)
		result := (hostsUsageMB / totalMB) * 100

		acc.AddFields("vsphere_cluster_mem", map[string]any{"used_perc": result}, tags, time.Unix(ts, 0))
	}

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

func additionalDatastoreIO(tags map[string]string, hostMOIDs map[string]bool, acc telegraf.Accumulator, retained retainedMetrics) error {
	allTimestamps := make(map[int64]struct{})
	reducer := func(acc, value any, tags map[string]string, t time.Time) any {
		if hostMOIDs[tags["moid"]] {
			ac := acc.(map[int64]map[string]any) //nolint: forcetypeassert
			ts := t.Unix()

			usagePerHost, ok := ac[ts]
			if !ok {
				usagePerHost = make(map[string]any)
			}

			usagePerHost[tags["moid"]] = value
			ac[ts] = usagePerHost
			allTimestamps[ts] = struct{}{}

			return ac
		}

		return acc
	}

	// Reducing the points to a map to get the value of each metric at each timestamp
	hostsReadKBpsPerTS := retained.reduce("vsphere_host_datastore", "read_average", make(map[int64]map[string]any), reducer)
	hostsWriteKBpsPerTS := retained.reduce("vsphere_host_datastore", "write_average", make(map[int64]map[string]any), reducer)

	for ts := range allTimestamps {
		hostsReadKBps, readOk := hostsReadKBpsPerTS.(map[int64]map[string]any)[ts]
		hostsWriteKBps, writeOk := hostsWriteKBpsPerTS.(map[int64]map[string]any)[ts]

		if !readOk || !writeOk {
			return nil
		}

		var readSum, writeSum int64

		for _, read := range hostsReadKBps {
			readSum += read.(int64) //nolint: forcetypeassert
		}

		for _, write := range hostsWriteKBps {
			writeSum += write.(int64) //nolint: forcetypeassert
		}

		fields := map[string]any{
			"read_average":  readSum,
			"write_average": writeSum,
		}

		acc.AddFields("vsphere_datastore_datastore", fields, tags, time.Unix(ts, 0))
	}

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
