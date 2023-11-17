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
	"errors"
	"fmt"
	"glouton/config"
	"glouton/facts"
	"glouton/logger"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/influxdata/telegraf"
	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/soap"
	"github.com/vmware/govmomi/vim25/types"
)

func newDeviceFinder(ctx context.Context, vSphereCfg config.VSphere) (*find.Finder, *vim25.Client, error) {
	u, err := soap.ParseURL(vSphereCfg.URL)
	if err != nil {
		return nil, nil, err
	}

	u.User = url.UserPassword(vSphereCfg.Username, vSphereCfg.Password)

	c, err := govmomi.NewClient(ctx, u, vSphereCfg.InsecureSkipVerify)
	if err != nil {
		return nil, nil, err
	}

	f := find.NewFinder(c.Client, true)

	dc, err := f.DefaultDatacenter(ctx)
	if err != nil {
		return nil, nil, err
	}

	// Make future calls to the local datacenter
	f.SetDatacenter(dc)

	return f, c.Client, nil
}

func findDevices(ctx context.Context, finder *find.Finder, listDatastores bool) (clusters []*object.ClusterComputeResource, datastores []*object.Datastore, hosts []*object.HostSystem, vms []*object.VirtualMachine, err error) {
	// The find.NotFoundError is thrown when no devices are found,
	// even if the path is not restrictive to a particular device.
	var notFoundError *find.NotFoundError

	clusters, err = finder.ClusterComputeResourceList(ctx, "*")
	if err != nil && !errors.As(err, &notFoundError) {
		return nil, nil, nil, nil, err
	}

	if listDatastores {
		datastores, err = finder.DatastoreList(ctx, "*")
		if err != nil && !errors.As(err, &notFoundError) {
			return nil, nil, nil, nil, err
		}
	}

	hosts, err = finder.HostSystemList(ctx, "*")
	if err != nil && !errors.As(err, &notFoundError) {
		return nil, nil, nil, nil, err
	}

	vms, err = finder.VirtualMachineList(ctx, "*")
	if err != nil && !errors.As(err, &notFoundError) {
		return nil, nil, nil, nil, err
	}

	return clusters, datastores, hosts, vms, nil
}

func describeCluster(source string, rfName refName, clusterProps clusterLightProps) *Cluster {
	clusterFacts := make(map[string]string)

	if resourceSummary := clusterProps.ComputeResource.Summary; resourceSummary != nil {
		clusterFacts["cpu_cores"] = str(resourceSummary.ComputeResourceSummary.NumCpuCores)
	}

	datastores := make([]string, len(clusterProps.ComputeResource.Datastore))

	for i, ds := range clusterProps.ComputeResource.Datastore {
		datastores[i] = ds.Value
	}

	dev := device{
		source: source,
		moid:   rfName.Reference().Value,
		name:   rfName.Name(),
		facts:  clusterFacts,
		state:  string(clusterProps.ComputeResource.ManagedEntity.OverallStatus),
	}

	dev.facts["fqdn"] = dev.FQDN()

	return &Cluster{
		device:     dev,
		datastores: datastores,
	}
}

func describeHost(source string, rfName refName, hostProps hostLightProps) *HostSystem {
	hostFacts := map[string]string{
		"hostname": hostProps.Summary.Config.Name,
		// custom
		"vsphere_vmotion_enabled": str(hostProps.Summary.Config.VmotionEnabled),
	}

	if hostProps.Summary.Hardware != nil {
		hostFacts["cpu_model_name"] = hostProps.Summary.Hardware.CpuModel
		hostFacts["product_name"] = hostProps.Summary.Hardware.Model
		hostFacts["system_vendor"] = hostProps.Summary.Hardware.Vendor
	}

	if hostProps.Hardware != nil {
		hostFacts["cpu_cores"] = str(hostProps.Hardware.CpuInfo.NumCpuCores)
		hostFacts["memory"] = facts.ByteCountDecimal(uint64(hostProps.Hardware.MemorySize))
	}

	if hostProps.Config != nil {
		hostFacts["os_pretty_name"] = hostProps.Config.Product.OsType
		hostFacts["ipv6_enabled"] = str(*hostProps.Config.Network.IpV6Enabled)
		hostFacts["vsphere_host_version"] = hostProps.Config.Product.Version

		if hostProps.Config.DateTimeInfo != nil {
			hostFacts["timezone"] = hostProps.Config.DateTimeInfo.TimeZone.Name
		}

		if hostProps.Config.Network != nil {
			if dns := hostProps.Config.Network.DnsConfig; dns != nil {
				if cfg := dns.GetHostDnsConfig(); cfg != nil {
					hostFacts["domain"] = cfg.DomainName
				}
			}

			if vnic := hostProps.Config.Network.Vnic; len(vnic) > 0 {
				if vnic[0].Spec.Ip != nil {
					hostFacts["primary_address"] = vnic[0].Spec.Ip.IpAddress
				}
			}
		}

		if hostFacts["hostname"] == "" {
			hostFacts["hostname"] = hostProps.ManagedEntity.Name
		}
	}

	dev := device{
		source: source,
		moid:   rfName.Reference().Value,
		name:   fallback(hostFacts["hostname"], rfName.Name()),
		facts:  hostFacts,
		state:  string(hostProps.Runtime.PowerState),
	}

	dev.facts["fqdn"] = dev.FQDN()

	return &HostSystem{dev}
}

func describeVM(source string, rfName refName, vmProps vmLightProps) (*VirtualMachine, map[string]string, map[string]string) {
	vmFacts := make(map[string]string)

	var (
		disks         map[string]string
		netInterfaces map[string]string
	)

	if vmProps.Config != nil {
		vmFacts["cpu_cores"] = str(vmProps.Config.Hardware.NumCPU)
		vmFacts["memory"] = facts.ByteCountDecimal(uint64(vmProps.Config.Hardware.MemoryMB) * 1 << 20) // MB to B
		vmFacts["vsphere_vm_version"] = vmProps.Config.Version
		vmFacts["vsphere_vm_name"] = vmProps.Config.Name

		if vmProps.Config.GuestFullName != "otherGuest" {
			vmFacts["os_pretty_name"] = vmProps.Config.GuestFullName
		}

		if vmProps.Summary.Config.Product != nil {
			vmFacts["product_name"] = vmProps.Summary.Config.Product.Name
			vmFacts["system_vendor"] = vmProps.Summary.Config.Product.Vendor
		}

		if datastores := vmProps.Config.DatastoreUrl; len(datastores) > 0 {
			dsNames := make([]string, len(datastores))
			for i, datastore := range vmProps.Config.DatastoreUrl {
				dsNames[i] = datastore.Name
			}

			vmFacts["vsphere_datastore"] = strings.Join(dsNames, ", ")
		}

		disks, netInterfaces = getVMLabelsMetadata(vmProps.Config.Hardware.Device)
	}

	if vmProps.Runtime.Host != nil {
		vmFacts["vsphere_host"] = vmProps.Runtime.Host.Value
	}

	if vmProps.ResourcePool != nil {
		vmFacts["vsphere_resource_pool"] = vmProps.ResourcePool.Value
	}

	if vmProps.Guest != nil && vmProps.Guest.IpAddress != "" {
		vmFacts["primary_address"] = vmProps.Guest.IpAddress
	}

	switch {
	case vmProps.Guest != nil && vmProps.Guest.HostName != "":
		vmFacts["hostname"] = vmProps.Guest.HostName
	case vmProps.Summary.Vm != nil:
		vmFacts["hostname"] = vmProps.Summary.Vm.Value
	default:
		vmFacts["hostname"] = rfName.Name()
	}

	dev := device{
		source: source,
		moid:   rfName.Reference().Value,
		name:   fallback(vmFacts["vsphere_vm_name"], rfName.Name()),
		facts:  vmFacts,
		state:  string(vmProps.Runtime.PowerState),
	}

	dev.facts["fqdn"] = dev.FQDN()

	return &VirtualMachine{dev}, disks, netInterfaces
}

func getVMLabelsMetadata(devices object.VirtualDeviceList) (map[string]string, map[string]string) {
	disks := make(map[string]string)
	netInterfaces := make(map[string]string)

	for _, device := range devices {
		switch dev := device.(type) {
		case *types.VirtualDisk:
			if dev.UnitNumber == nil {
				continue
			}

			switch controller := devices.FindByKey(dev.ControllerKey).(type) {
			case types.BaseVirtualSCSIController:
				disks[fmt.Sprintf("scsi%d:%d", controller.GetVirtualSCSIController().BusNumber, *dev.UnitNumber)] = devices.Name(dev)
			case types.BaseVirtualSATAController:
				disks[fmt.Sprintf("sata%d:%d", controller.GetVirtualSATAController().BusNumber, *dev.UnitNumber)] = devices.Name(dev)
			case *types.VirtualNVMEController:
				disks[fmt.Sprintf("nvme%d:%d", controller.BusNumber, *dev.UnitNumber)] = devices.Name(dev)
			default:
				logger.V(2).Printf("Unknown disk controller: %T", controller)
			}
		case types.BaseVirtualEthernetCard: // VirtualVmxnet, VirtualE1000, ...
			ethernetCard := dev.GetVirtualEthernetCard()

			netInterfaces[strconv.Itoa(int(ethernetCard.Key))] = devices.Name(ethernetCard)
		}
	}

	return disks, netInterfaces
}

func str(v any) string { return fmt.Sprint(v) }

func fallback(v string, otherwise string) string {
	if v != "" {
		return v
	}

	return otherwise
}

type commonObject interface {
	Reference() types.ManagedObjectReference
	Name() string
}

var isolateLUN = regexp.MustCompile(`.*/([^/]+)/?$`)

func getDatastorePerLUN(ctx context.Context, client *vim25.Client, datastores []*object.Datastore, cache *propsCache[datastoreLightProps]) (map[string]string, error) {
	dsProps, err := retrieveProps(ctx, client, datastores, relevantDatastoreProperties, cache)
	if err != nil {
		logger.V(1).Printf("Failed to retrieve datastore props of %s: %v", client.URL().Host, err)

		return map[string]string{}, err
	}

	dsPerLUN := make(map[string]string, len(dsProps))

	for ds, props := range dsProps {
		info := props.Info.GetDatastoreInfo()
		if info == nil {
			continue
		}

		matches := isolateLUN.FindStringSubmatch(info.Url)
		if matches == nil {
			continue
		}

		dsPerLUN[matches[1]] = ds.Name()
	}

	return dsPerLUN, nil
}

func additionalClusterMetrics(ctx context.Context, client *vim25.Client, clusters []*object.ClusterComputeResource, cache *propsCache[hostLightProps], acc telegraf.Accumulator, h *hierarchy) error {
	for _, cluster := range clusters {
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

		tags := map[string]string{
			"clustername": cluster.Name(),
			"dcname":      h.ParentDCName(cluster),
			"moid":        cluster.Reference().Value,
		}

		acc.AddFields("hosts", fields, tags)
	}

	return nil
}

func additionalHostMetrics(_ context.Context, _ *vim25.Client, hosts []*object.HostSystem, acc telegraf.Accumulator, h *hierarchy, vmStatesPerHost map[string][]bool) error {
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

			acc.AddFields("vms", fields, tags)
		}
	}

	return nil
}

func additionalVMMetrics(ctx context.Context, client *vim25.Client, vms []*object.VirtualMachine, cache *propsCache[vmLightProps], acc telegraf.Accumulator, h *hierarchy, vmStatePerHost map[string][]bool) error {
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

				acc.AddFields("vsphere_vm_disk", map[string]any{"used_perc": usage}, tags)
			}
		}
	}

	return nil
}

// hierarchy represents the structure of a vSphere in a way that suits us.
// It drops the folder levels to get a hierarchy with a shape like:
// VM -> Host -> Cluster -> Datacenter
// It also creates a map like device moid -> device name.
type hierarchy struct {
	deviceNamePerMOID  map[string]string
	parentPerChildMOID map[string]types.ManagedObjectReference
}

func hierarchyFrom(ctx context.Context, clusters []*object.ClusterComputeResource, hosts []*object.HostSystem, vms []*object.VirtualMachine, vmPropsCache *propsCache[vmLightProps]) (*hierarchy, error) {
	h := &hierarchy{
		deviceNamePerMOID:  make(map[string]string),
		parentPerChildMOID: make(map[string]types.ManagedObjectReference),
	}

	var vmClient *vim25.Client

	for _, vm := range vms {
		err := h.recurseDescribe(ctx, vm.Client(), vm.Reference())
		if err != nil {
			return nil, err
		}

		vmClient = vm.Client()
	}

	for _, host := range hosts {
		err := h.recurseDescribe(ctx, host.Client(), host.Reference())
		if err != nil {
			return nil, err
		}
	}

	for _, cluster := range clusters {
		err := h.recurseDescribe(ctx, cluster.Client(), cluster.Reference())
		if err != nil {
			return nil, err
		}
	}

	h.filterParents()

	if vmClient != nil {
		vmProps, err := retrieveProps(ctx, vmClient, vms, relevantVMProperties, vmPropsCache)
		if err != nil {
			return nil, err
		}

		h.fixVMParents(vmProps)
	}

	return h, nil
}

func (h *hierarchy) fixVMParents(vmProps map[refName]vmLightProps) {
	for vmRef, props := range vmProps {
		if host := props.Runtime.Host; host != nil {
			h.parentPerChildMOID[vmRef.Reference().Value] = *host
		}
	}
}

func (h *hierarchy) recurseDescribe(ctx context.Context, client *vim25.Client, objRef types.ManagedObjectReference) error {
	if _, alreadyExist := h.deviceNamePerMOID[objRef.Value]; alreadyExist {
		return nil
	}

	elements, err := mo.Ancestors(ctx, client, client.ServiceContent.PropertyCollector, objRef)
	if err != nil || len(elements) == 0 {
		return err
	}

	for _, e := range elements {
		h.deviceNamePerMOID[e.Reference().Value] = e.Name

		if e.Parent != nil {
			h.parentPerChildMOID[e.Reference().Value] = *e.Parent
		}

		err = h.recurseDescribe(ctx, client, e.Reference())
		if err != nil {
			return err
		}
	}

	return nil
}

// filterParents removes the folder elements in the hierarchy tree.
func (h *hierarchy) filterParents() {
	for child, parent := range h.parentPerChildMOID {
		needsUpdate := false

		for parent.Type == "Folder" {
			delete(h.deviceNamePerMOID, parent.Value) // We will never care about the name of a folder.

			if grandParent, ok := h.parentPerChildMOID[parent.Value]; ok {
				parent = grandParent
				needsUpdate = true
			} else {
				break
			}
		}

		if needsUpdate {
			h.parentPerChildMOID[child] = parent
		}
	}
}

func (h *hierarchy) findFirstParentOfType(child mo.Reference, typ string) string {
	if parent, ok := h.parentPerChildMOID[child.Reference().Value]; ok {
		if parent.Type == typ {
			return h.deviceNamePerMOID[parent.Value]
		}

		return h.findFirstParentOfType(parent, typ)
	}

	return ""
}

func (h *hierarchy) ParentHostName(child mo.Reference) string {
	return h.findFirstParentOfType(child, "HostSystem")
}

func (h *hierarchy) ParentClusterName(child mo.Reference) string {
	return h.findFirstParentOfType(child, "ComputeResource")
}

func (h *hierarchy) ParentDCName(child mo.Reference) string {
	return h.findFirstParentOfType(child, "Datacenter")
}
