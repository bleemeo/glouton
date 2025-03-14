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
	"net/url"
	"testing"

	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/facts"

	"github.com/google/go-cmp/cmp"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/types"
)

func TestPropsCaches(t *testing.T) { //nolint:maintidx
	vSphereCfg, deferFn := setupVSphereAPITest(t, "vcenter_1")
	defer deferFn()

	u, _ := url.Parse(vSphereCfg.URL)

	vSphere := newVSphere(u.Host, vSphereCfg, nil, facts.NewMockFacter(make(map[string]string)))
	devChan := make(chan bleemeoTypes.VSphereDevice)

	go func() {
		for range devChan { //nolint: revive
		}
	}()

	vSphere.devices(t.Context(), devChan)
	close(devChan)

	expectedClusters := map[string]clusterLightProps{
		"domain-c16": {
			ComputeResource: clusterLightComputeResource{
				ManagedEntity: clusterLightComputeResourceManagedEntity{
					OverallStatus: "green",
				},
				Datastore: []types.ManagedObjectReference{
					{
						Type:  "Datastore",
						Value: "datastore-25",
					},
				},
				Summary: &clusterLightComputeResourceSummary{
					ComputeResourceSummary: clusterLightComputeResourceSummaryComputeResourceSummary{
						NumCpuCores: 2,
						TotalCpu:    2294,
						TotalMemory: 4294430720,
					},
				},
			},
		},
	}
	expectedHosts := map[string]hostLightProps{
		"host-23": {
			ManagedEntity: hostLightManagedEntity{
				Parent: &types.ManagedObjectReference{
					Type:  "ClusterComputeResource",
					Value: "domain-c16",
				},
				Name: "DC0_C0_H0",
			},
			Runtime: hostLightRuntime{
				PowerState: "poweredOn",
			},
			Summary: hostLightSummary{
				Hardware: &hostLightSummaryHardware{
					Vendor:   "VMware, Inc. (govmomi simulator)",
					Model:    "VMware Virtual Platform",
					CpuModel: "Intel(R) Core(TM) i7-3615QM CPU @ 2.30GHz",
				},
				Config: hostLightSummaryConfig{
					Name:           "DC0_C0_H0",
					VmotionEnabled: false,
				},
			},
			Hardware: &hostLightHardware{
				CpuInfo: hostLightHardwareCpuInfo{
					NumCpuCores: 2,
				},
				MemorySize: 4294430720,
			},
			Config: &hostLightConfig{
				Product: hostLightConfigProduct{
					Version: "6.5.0",
					OsType:  "vmnix-x86",
				},
				Network: &hostLightConfigNetwork{
					Vnic: []hostLightConfigNetworkVnic{
						{
							Spec: hostLightConfigNetworkVnicSpec{
								Ip: &hostLightConfigNetworkVnicSpecIp{
									IpAddress: "127.0.0.1",
								},
							},
						},
					},
					DnsConfig: &types.HostDnsConfig{
						DomainName: "localdomain",
					},
					IpV6Enabled: ptr(false),
				},
				DateTimeInfo: nil,
			},
		},
	}
	expectedVMs := map[string]vmLightProps{
		"vm-28": {
			Config: &vmLightConfig{
				Name:          "DC0_C0_RP0_VM0",
				GuestFullName: "otherGuest",
				Version:       "vmx-13",
				Hardware: vmLightConfigHardware{
					NumCPU:   1,
					MemoryMB: 32,
					Device: object.VirtualDeviceList{
						&types.VirtualIDEController{
							VirtualController: types.VirtualController{
								VirtualDevice: types.VirtualDevice{Key: 200, DeviceInfo: &types.Description{
									Label:   "IDE 0",
									Summary: "IDE 0",
								}},
								BusNumber: 0,
							},
						},
						&types.VirtualIDEController{
							VirtualController: types.VirtualController{
								VirtualDevice: types.VirtualDevice{Key: 201, DeviceInfo: &types.Description{
									Label:   "IDE 1",
									Summary: "IDE 1",
								}},
								BusNumber: 1,
							},
						},
						&types.VirtualPS2Controller{
							VirtualController: types.VirtualController{
								VirtualDevice: types.VirtualDevice{Key: 300, DeviceInfo: &types.Description{
									Label:   "PS2 controller 0",
									Summary: "PS2 controller 0",
								}},
								Device:    []int32{600, 700},
								BusNumber: 0,
							},
						},
						&types.VirtualPCIController{
							VirtualController: types.VirtualController{
								VirtualDevice: types.VirtualDevice{Key: 100, DeviceInfo: &types.Description{
									Label:   "PCI controller 0",
									Summary: "PCI controller 0",
								}},
								Device:    []int32{500, 12000},
								BusNumber: 0,
							},
						},
						&types.VirtualSIOController{
							VirtualController: types.VirtualController{
								VirtualDevice: types.VirtualDevice{Key: 400, DeviceInfo: &types.Description{
									Label:   "SIO controller 0",
									Summary: "SIO controller 0",
								}},
								BusNumber: 0,
							},
						},
						&types.VirtualKeyboard{
							VirtualDevice: types.VirtualDevice{
								Key:           600,
								DeviceInfo:    &types.Description{Label: "Keyboard ", Summary: "Keyboard"},
								ControllerKey: 300,
								UnitNumber:    ptr[int32](0),
							},
						},
						&types.VirtualPointingDevice{
							VirtualDevice: types.VirtualDevice{
								Key:        700,
								DeviceInfo: &types.Description{Label: "Pointing device", Summary: "Pointing device; Device"},
								Backing: &types.VirtualPointingDeviceDeviceBackingInfo{
									VirtualDeviceDeviceBackingInfo: types.VirtualDeviceDeviceBackingInfo{
										UseAutoDetect: ptr(false),
									},
									HostPointingDevice: "autodetect",
								},
								ControllerKey: 300,
								UnitNumber:    ptr[int32](1),
							},
						},
						&types.VirtualMachineVideoCard{
							VirtualDevice: types.VirtualDevice{
								Key:           500,
								DeviceInfo:    &types.Description{Label: "Video card ", Summary: "Video card"},
								ControllerKey: 100,
								UnitNumber:    ptr[int32](0),
							},
							VideoRamSizeInKB:       4096,
							NumDisplays:            1,
							UseAutoDetect:          ptr(false),
							Enable3DSupport:        ptr(false),
							Use3dRenderer:          "automatic",
							GraphicsMemorySizeInKB: 262144,
						},
						&types.VirtualMachineVMCIDevice{
							VirtualDevice: types.VirtualDevice{
								Key: 12000,
								DeviceInfo: &types.Description{
									Label:   "VMCI device",
									Summary: "Device on the virtual machine PCI bus that provides support for the virtual machine communication interface",
								},
								ControllerKey: 100,
								UnitNumber:    ptr[int32](17),
							},
							Id:                             -1,
							AllowUnrestrictedCommunication: ptr(false),
							FilterEnable:                   ptr(true),
						},
						&types.ParaVirtualSCSIController{
							VirtualSCSIController: types.VirtualSCSIController{
								VirtualController: types.VirtualController{VirtualDevice: types.VirtualDevice{
									Key: 202,
									DeviceInfo: &types.Description{
										Label:   "pvscsi-202",
										Summary: "pvscsi-202",
									},
								}},
								SharedBus:          "noSharing",
								ScsiCtlrUnitNumber: 7,
							},
						},
						&types.VirtualCdrom{
							VirtualDevice: types.VirtualDevice{
								Key:        203,
								DeviceInfo: &types.Description{Label: "cdrom-203", Summary: "cdrom-203"},
								Backing: &types.VirtualCdromAtapiBackingInfo{VirtualDeviceDeviceBackingInfo: types.VirtualDeviceDeviceBackingInfo{
									DeviceName:    "cdrom--201-824635603088",
									UseAutoDetect: ptr(false),
								}},
								Connectable:   &types.VirtualDeviceConnectInfo{StartConnected: true, AllowGuestControl: true, Connected: true},
								ControllerKey: 202,
								UnitNumber:    ptr[int32](0),
							},
						},
						&types.VirtualDisk{
							VirtualDevice: types.VirtualDevice{
								Key:        204,
								DeviceInfo: &types.Description{Label: "disk-202-0", Summary: "10,485,760 KB"},
								Backing: &types.VirtualDiskFlatVer2BackingInfo{
									VirtualDeviceFileBackingInfo: types.VirtualDeviceFileBackingInfo{
										FileName: "[LocalDS_0] DC0_C0_RP0_VM0/disk1.vmdk",
										Datastore: &types.ManagedObjectReference{
											Type:  "Datastore",
											Value: "datastore-25",
										},
									},
									DiskMode:        "persistent",
									Split:           ptr(false),
									WriteThrough:    ptr(false),
									ThinProvisioned: ptr(true),
									EagerlyScrub:    ptr(false),
									Uuid:            "be8d2471-f32e-5c7e-a89b-22cb8e533890",
									DigestEnabled:   ptr(false),
								},
								ControllerKey: 202,
								UnitNumber:    ptr[int32](0),
							},
							CapacityInKB:        10485760,
							CapacityInBytes:     10737418240,
							StorageIOAllocation: &types.StorageIOAllocationInfo{Limit: ptr[int64](-1)},
						},
						&types.VirtualE1000{
							VirtualEthernetCard: types.VirtualEthernetCard{
								VirtualDevice: types.VirtualDevice{
									Key: 4000,
									DeviceInfo: &types.Description{
										Label:   "ethernet-0",
										Summary: "DVSwitch: fea97929-4b2d-5972-b146-930c6d0b4014",
									},
									Backing: &types.VirtualEthernetCardDistributedVirtualPortBackingInfo{
										Port: types.DistributedVirtualSwitchPortConnection{
											SwitchUuid:       "fea97929-4b2d-5972-b146-930c6d0b4014",
											PortgroupKey:     "dvportgroup-13",
											PortKey:          "",
											ConnectionCookie: 0,
										},
									},
									Connectable: &types.VirtualDeviceConnectInfo{
										MigrateConnect:    "",
										StartConnected:    true,
										AllowGuestControl: true,
										Connected:         true,
										Status:            "untried",
									},
									SlotInfo:      &types.VirtualDevicePciBusSlotInfo{PciSlotNumber: 32},
									ControllerKey: 100,
									UnitNumber:    ptr[int32](7),
								},
								AddressType:      "generated",
								MacAddress:       "00:0c:29:33:34:38",
								WakeOnLanEnabled: ptr(true),
								ResourceAllocation: &types.VirtualEthernetCardResourceAllocation{
									Reservation: ptr[int64](0),
									Share: types.SharesInfo{
										Shares: 50,
										Level:  "normal",
									},
									Limit: ptr(int64(-1)),
								},
							},
						},
					},
				},
				DatastoreUrl: nil,
			},
			ResourcePool: &types.ManagedObjectReference{
				Type:  "ResourcePool",
				Value: "resgroup-15",
			},
			Runtime: vmLightRuntime{
				Host: &types.ManagedObjectReference{
					Type:  "HostSystem",
					Value: "host-23",
				},
				PowerState: "poweredOn",
			},
			Guest:   &vmLightGuest{},
			Summary: vmLightSummary{},
		},
	}

	compareLightProps(t, expectedClusters, vSphere.devicePropsCache.clusterCache.m)
	compareLightProps(t, expectedHosts, vSphere.devicePropsCache.hostCache.m)
	compareLightProps(t, expectedVMs, vSphere.devicePropsCache.vmCache.m)
}

func compareLightProps[propsType any](t *testing.T, expected map[string]propsType, cacheMap map[string]cachedProp[propsType]) {
	t.Helper()

	for dev := range cacheMap {
		if _, found := expected[dev]; !found {
			t.Errorf("Found extra device in %T map: %q", *(new(propsType)), dev)
		}
	}

	for expectedDev, expectedProps := range expected {
		cacheProp, found := cacheMap[expectedDev]
		if !found {
			t.Errorf("Device %q not found in %T cache map", expectedDev, cacheProp)

			continue
		}

		if diff := cmp.Diff(expectedProps, cacheProp.value); diff != "" {
			t.Errorf("Incorrect cached properties (-want +got):\n%s", diff)
		}
	}
}
