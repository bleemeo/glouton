package vsphere

import (
	"context"
	"fmt"
	"glouton/config"
	"glouton/logger"
	"net/url"
	"strings"
	"time"

	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/soap"
)

//nolint:gochecknoglobals
var (
	relevantHostProperties = []string{
		"hardware.cpuInfo.numCpuCores",
		"summary.hardware.cpuModel",
		"hardware.memorySize",
		"config.product.osType",
		"summary.hardware.model",
		"summary.hardware.vendor",
		"config.dateTimeInfo.timeZone.name",
		// config.vmotion.ipConfig.ipAddress ?
		// config.ipmi.bmcIpAddress ?
		// config.ipmi.bmcMacAddress ?
		"config.network.ipV6Enabled",
		"runtime.hostMaxVirtualDiskCapacity",
		"config.product.version",
		"summary.config.vmotionEnabled",
	}
	relevantVMProperties = []string{
		"config.hardware.numCPU",
		"guest.hostName",
		"config.hardware.memoryMB",
		"config.guestFullName",
		"summary.config.product.name",   // Don't really know if the Product
		"summary.config.product.vendor", // section is sometime not null...
		"guest.ipAddress",
		"runtime.host",
		"resourcePool",
		"config.datastoreUrl",
		"config.version",
		"config.name",
	}
)

func newDeviceFinder(ctx context.Context, vSphereCfg config.VSphere) (*find.Finder, error) {
	u, err := soap.ParseURL(vSphereCfg.URL)
	if err != nil {
		return nil, err
	}

	u.User = url.UserPassword(vSphereCfg.Username, vSphereCfg.Password)

	c, err := govmomi.NewClient(ctx, u, vSphereCfg.InsecureSkipVerify)
	if err != nil {
		return nil, err
	}

	f := find.NewFinder(c.Client, true)

	dc, err := f.DefaultDatacenter(ctx)
	if err != nil {
		return nil, err
	}

	// Make future calls to the local datacenter
	f.SetDatacenter(dc)

	return f, nil
}

func findDevices(ctx context.Context, finder *find.Finder) (hosts []*object.HostSystem, vms []*object.VirtualMachine, err error) {
	hosts, err = finder.HostSystemList(ctx, "*")
	if err != nil {
		return nil, nil, err
	}

	vms, err = finder.VirtualMachineList(ctx, "*")
	if err != nil {
		return nil, nil, err
	}

	return hosts, vms, nil
}

func describeHosts(ctx context.Context, rawHosts []*object.HostSystem, deviceChan chan<- Device) {
	for _, host := range rawHosts {
		var hostProps mo.HostSystem

		err := host.Properties(ctx, host.Reference(), relevantHostProperties, &hostProps)
		if err != nil {
			logger.Printf("Failed to fetch host props: %v", err) // TODO: remove

			continue
		}

		deviceChan <- describeHost(host, hostProps)
	}
}

func describeHost(host *object.HostSystem, hostProps mo.HostSystem) *HostSystem {
	facts := map[string]string{
		"cpu_cores":      str(hostProps.Summary.Hardware.NumCpuCores),
		"cpu_model_name": hostProps.Summary.Hardware.CpuModel,
		"product_name":   hostProps.Summary.Hardware.Model,
		"system_vendor":  hostProps.Summary.Hardware.Vendor,
		// custom
		"boot_time":          hostProps.Runtime.BootTime.Format(time.RFC3339),
		"max_vdisk_capacity": str(hostProps.Runtime.HostMaxVirtualDiskCapacity),
		"vmotion_enabled":    str(hostProps.Summary.Config.VmotionEnabled),
		"reboot_required":    str(hostProps.Summary.RebootRequired),
	}

	if hostProps.Hardware != nil {
		facts["cpu_cores"] = str(hostProps.Hardware.CpuInfo.NumCpuCores)
		facts["memory"] = str(hostProps.Hardware.MemorySize)
	}

	if hostProps.Config != nil {
		facts["os_pretty_name"] = hostProps.Config.Product.OsType
		facts["timezone"] = hostProps.Config.DateTimeInfo.TimeZone.Name
		facts["ipv6_enabled"] = str(*hostProps.Config.Network.IpV6Enabled)
		facts["version"] = hostProps.Config.Product.Version
	}

	dev := device{
		source: host.Client().URL().Host,
		moid:   host.Reference().Value,
		facts:  facts,
	}

	dev.facts["fqdn"] = dev.FQDN()

	return &HostSystem{dev}
}

func describeVMs(ctx context.Context, rawVMs []*object.VirtualMachine, deviceChan chan<- Device) {
	for _, vm := range rawVMs {
		var vmProps mo.VirtualMachine

		err := vm.Properties(ctx, vm.Reference(), relevantVMProperties, &vmProps)
		if err != nil {
			logger.Printf("Failed to fetch VM props:", err) // TODO: remove

			continue
		}

		deviceChan <- describeVM(ctx, vm, vmProps)
	}
}

func describeVM(ctx context.Context, vm *object.VirtualMachine, vmProps mo.VirtualMachine) *VirtualMachine {
	facts := make(map[string]string)

	if vmProps.Config != nil {
		facts["cpu_cores"] = str(vmProps.Config.Hardware.NumCPU)
		facts["memory"] = str(vmProps.Config.Hardware.MemoryMB)
		facts["os_pretty_name"] = vmProps.Config.GuestFullName
		facts["version"] = vmProps.Config.Version
		facts["vm_name"] = vmProps.Config.Name

		if vmProps.Summary.Config.Product != nil {
			facts["product_name"] = vmProps.Summary.Config.Product.Name
			facts["system_vendor"] = vmProps.Summary.Config.Product.Vendor
		}

		if datastores := vmProps.Config.DatastoreUrl; len(datastores) > 0 {
			dsNames := make([]string, len(datastores))
			for i, datastore := range vmProps.Config.DatastoreUrl {
				dsNames[i] = datastore.Name
			}

			facts["datastore"] = strings.Join(dsNames, ", ")
		}
	}

	if vmProps.Runtime.BootTime != nil {
		facts["boot_time"] = vmProps.Runtime.BootTime.Format(time.RFC3339)
	}

	if vmProps.Runtime.Host != nil {
		facts["host"] = vmProps.Runtime.Host.Value
	}

	if vmProps.ResourcePool != nil {
		facts["resource_pool"] = vmProps.ResourcePool.Value
	}

	if vmProps.Guest != nil {
		facts["primary_address"] = vmProps.Guest.IpAddress
	}

	switch {
	case vmProps.Guest != nil && vmProps.Guest.HostName != "":
		facts["hostname"] = vmProps.Guest.HostName
	case vmProps.Summary.Vm != nil:
		facts["hostname"] = vmProps.Summary.Vm.Value
	default:
		facts["hostname"] = vm.Reference().Value
	}

	dev := device{
		source: vm.Client().URL().Host,
		moid:   vm.Reference().Value,
		facts:  facts,
	}

	dev.facts["fqdn"] = dev.FQDN()

	return &VirtualMachine{
		device: dev,
		UUID:   vm.UUID(ctx),
	}
}

func str(v any) string { return fmt.Sprint(v) }
