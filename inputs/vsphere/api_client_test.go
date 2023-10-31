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
	"crypto/tls"
	bleemeoTypes "glouton/bleemeo/types"
	"glouton/config"
	"glouton/prometheus/registry"
	"net/url"
	"sort"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/vmware/govmomi/simulator"
)

func setupVSphereAPITest(t *testing.T, dirName string) (vSphereCfg config.VSphere, deferFn func()) {
	t.Helper()

	model := simulator.VPX()

	err := model.Create()
	if err != nil {
		t.Fatal("Model creation:", err)
	}

	err = model.Load("testdata/" + dirName)
	if err != nil {
		t.Fatal("Model loading:", err)
	}

	model.Service.TLS = new(tls.Config)

	server := model.Service.NewServer()

	vSphereCfg = config.VSphere{
		URL:                server.URL.String(),
		Username:           "user",
		Password:           "pass",
		InsecureSkipVerify: true,
		MonitorVMs:         true,
	}

	return vSphereCfg, func() {
		model.Remove()
		server.Close()
	}
}

// TestVCenterDescribing list and describes the devices of a vCenter,
// by calling individually each method needed.
func TestVCenterDescribing(t *testing.T) {
	vSphereCfg, deferFn := setupVSphereAPITest(t, "vcenter_1")
	defer deferFn()

	vSphereURL, err := url.Parse(vSphereCfg.URL)
	if err != nil {
		t.Fatalf("Failed to parse vSphere URL %q: %v", vSphereCfg.URL, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), commonTimeout)
	defer cancel()

	finder, client, err := newDeviceFinder(ctx, vSphereCfg)
	if err != nil {
		t.Fatal(err)
	}

	clusters, _, hosts, vms, err := findDevices(ctx, finder, false)
	if err != nil {
		t.Fatal(err)
	}

	fail := false

	if len(clusters) != 1 {
		t.Errorf("Expected 1 cluster, found %d.", len(clusters))

		fail = true
	}

	if len(hosts) != 1 {
		t.Errorf("Expected 1 host, found %d.", len(hosts))

		fail = true
	}

	if len(vms) != 1 {
		t.Errorf("Expected 1 VM, found %d.", len(vms))

		fail = true
	}

	if fail {
		t.FailNow()
	}

	dummyVSphere := newVSphere(vSphereURL.Host, vSphereCfg, nil)
	dummyVSphere.stat = NewStat()

	devices := dummyVSphere.describeClusters(ctx, client, clusters)
	if len(devices) != 1 {
		t.Fatalf("Expected 1 cluster to be described, but got %d.", len(devices))
	}

	cluster, ok := devices[0].(*Cluster)
	if !ok {
		t.Fatalf("Expected device to be a Cluster, but is %T.", devices[0])
	}

	expectedCluster := Cluster{
		device: device{
			source: vSphereURL.Host,
			moid:   "domain-c16",
			name:   "DC0_C0",
			facts: map[string]string{
				"cpu_cores": "2",
				"fqdn":      "DC0_C0",
			},
			state: "green",
		},
		datastores: []string{"datastore-25"},
	}
	if diff := cmp.Diff(expectedCluster, *cluster, cmp.AllowUnexported(Cluster{}, device{})); diff != "" {
		t.Fatalf("Unexpected host description (-want +got):\n%s", diff)
	}

	devices = dummyVSphere.describeHosts(ctx, client, hosts)
	if len(devices) != 1 {
		t.Fatalf("Expected 1 host to be described, but got %d.", len(devices))
	}

	host, ok := devices[0].(*HostSystem)
	if !ok {
		t.Fatalf("Expected device to be a HostSystem, but is %T.", devices[0])
	}

	expectedHost := HostSystem{
		device{
			source: vSphereURL.Host,
			moid:   "host-23",
			name:   "DC0_C0_H0",
			facts: map[string]string{
				"cpu_cores":               "2",
				"cpu_model_name":          "Intel(R) Core(TM) i7-3615QM CPU @ 2.30GHz",
				"fqdn":                    "DC0_C0_H0",
				"hostname":                "DC0_C0_H0",
				"ipv6_enabled":            "false",
				"memory":                  "4.00 GB",
				"os_pretty_name":          "vmnix-x86",
				"primary_address":         "127.0.0.1",
				"product_name":            "VMware Virtual Platform",
				"system_vendor":           "VMware, Inc. (govmomi simulator)",
				"vsphere_host_version":    "6.5.0",
				"vsphere_vmotion_enabled": "false",
			},
			state: "poweredOn",
		},
	}
	if diff := cmp.Diff(expectedHost, *host, cmp.AllowUnexported(HostSystem{}, device{})); diff != "" {
		t.Fatalf("Unexpected host description (-want +got):\n%s", diff)
	}

	devices, vmLabelsMetadata := dummyVSphere.describeVMs(ctx, client, vms)
	if len(devices) != 1 {
		t.Fatalf("Expected 1 VM to be described, but got %d.", len(devices))
	}

	vm, ok := devices[0].(*VirtualMachine)
	if !ok {
		t.Fatalf("Expected device to be a VirtualMachine, but is %T.", devices)
	}

	expectedVM := VirtualMachine{
		device: device{
			source: vSphereURL.Host,
			moid:   "vm-28",
			name:   "DC0_C0_RP0_VM0",
			facts: map[string]string{
				"cpu_cores":             "1",
				"fqdn":                  "DC0_C0_RP0_VM0",
				"hostname":              "DC0_C0_RP0_VM0",
				"memory":                "32.00 MB",
				"vsphere_host":          "host-23",
				"vsphere_resource_pool": "resgroup-15",
				"vsphere_vm_name":       "DC0_C0_RP0_VM0",
				"vsphere_vm_version":    "vmx-13",
			},
			state: "poweredOn",
		},
	}
	if diff := cmp.Diff(expectedVM, *vm, cmp.AllowUnexported(VirtualMachine{}, device{})); diff != "" {
		t.Fatalf("Unexpected VM description (-want +got):\n%s", diff)
	}

	expectedLabelsMetadata := labelsMetadata{
		disksPerVM:         map[string]map[string]string{"vm-28": {"scsi0:0": "disk-202-0"}},
		netInterfacesPerVM: map[string]map[string]string{"vm-28": {"4000": "ethernet-0"}},
	}
	if diff := cmp.Diff(expectedLabelsMetadata, vmLabelsMetadata, cmp.AllowUnexported(labelsMetadata{})); diff != "" {
		t.Fatalf("Unexpected labels metadata (-want +got):\n%s", diff)
	}
}

// TestESXIDescribing lists and describes devices across multiple test cases,
// but by calling the higher-level method Manager.Devices.
func TestESXIDescribing(t *testing.T) { //nolint:maintidx
	testCases := []struct {
		name                   string
		dirName                string
		expectedClusters       []*Cluster
		expectedHosts          []*HostSystem
		expectedVMs            []*VirtualMachine
		expectedLabelsMetadata map[string]labelsMetadata
	}{
		{
			name:    "ESXI running on QEMU",
			dirName: "esxi_1",
			expectedHosts: []*HostSystem{
				{
					device{
						moid: "ha-host",
						name: "esxi.test",
						facts: map[string]string{
							"cpu_cores":               "4",
							"cpu_model_name":          "Intel(R) Core(TM) i7-8550U CPU @ 1.80GHz",
							"fqdn":                    "esxi.test",
							"hostname":                "esxi.test",
							"ipv6_enabled":            "false",
							"memory":                  "4.00 GB",
							"os_pretty_name":          "vmnix-x86",
							"primary_address":         "192.168.121.241",
							"product_name":            "Standard PC (i440FX + PIIX, 1996)",
							"system_vendor":           "QEMU",
							"timezone":                "UTC",
							"vsphere_host_version":    "8.0.1",
							"vsphere_vmotion_enabled": "false",
						},
						state: "poweredOn",
					},
				},
			},
			expectedVMs: []*VirtualMachine{
				{
					device: device{
						moid: "1",
						name: "vcenter.vmx",
						facts: map[string]string{
							"fqdn":                  "vcenter.vmx",
							"hostname":              "vcenter.vmx",
							"vsphere_host":          "ha-host",
							"vsphere_resource_pool": "ha-root-pool",
						},
						state: "poweredOff",
					},
				},
				{
					device: device{
						moid: "8",
						name: "lunar",
						facts: map[string]string{
							"cpu_cores":             "2",
							"fqdn":                  "lunar",
							"hostname":              "lunar",
							"memory":                "1.00 GB",
							"os_pretty_name":        "Ubuntu Linux (64-bit)",
							"vsphere_datastore":     "datastore1",
							"vsphere_host":          "ha-host",
							"vsphere_resource_pool": "ha-root-pool",
							"vsphere_vm_name":       "lunar",
							"vsphere_vm_version":    "vmx-10",
						},
						state: "poweredOff",
					},
				},
				{
					device: device{
						moid: "10",
						name: "alp1",
						facts: map[string]string{
							"cpu_cores":             "1",
							"fqdn":                  "alp1",
							"hostname":              "alpine",
							"memory":                "512.00 MB",
							"os_pretty_name":        "Other (32-bit)",
							"primary_address":       "192.168.121.117",
							"vsphere_datastore":     "datastore1",
							"vsphere_host":          "ha-host",
							"vsphere_resource_pool": "ha-root-pool",
							"vsphere_vm_name":       "alp1",
							"vsphere_vm_version":    "vmx-14",
						},
						state: "poweredOn",
					},
				},
			},
			expectedLabelsMetadata: map[string]labelsMetadata{
				"127.0.0.1": {
					datastorePerLUN: map[string]string{},
					disksPerVM: map[string]map[string]string{
						"1": nil,
						"10": {
							"nvme0:0": "disk-31000-0",
							"sata0:0": "disk-15000-0",
							"scsi0:0": "disk-1000-0",
						},
						"8": {"scsi0:0": "disk-1000-0"},
					},
					netInterfacesPerVM: map[string]map[string]string{"1": nil, "10": {"4000": "ethernet-0"}, "8": {"4000": "ethernet-0"}},
				},
			},
		},
		{
			name:    "vCenter vcsim'ulated",
			dirName: "vcenter_1",
			expectedClusters: []*Cluster{
				{
					device: device{
						moid: "domain-c16",
						name: "DC0_C0",
						facts: map[string]string{
							"cpu_cores": "2",
							"fqdn":      "DC0_C0",
						},
						state: "green",
					},
					datastores: []string{"datastore-25"},
				},
			},
			expectedHosts: []*HostSystem{
				{
					device: device{
						moid: "host-23",
						name: "DC0_C0_H0",
						facts: map[string]string{
							"cpu_cores":               "2",
							"cpu_model_name":          "Intel(R) Core(TM) i7-3615QM CPU @ 2.30GHz",
							"fqdn":                    "DC0_C0_H0",
							"hostname":                "DC0_C0_H0",
							"ipv6_enabled":            "false",
							"memory":                  "4.00 GB",
							"os_pretty_name":          "vmnix-x86",
							"primary_address":         "127.0.0.1",
							"product_name":            "VMware Virtual Platform",
							"system_vendor":           "VMware, Inc. (govmomi simulator)",
							"vsphere_host_version":    "6.5.0",
							"vsphere_vmotion_enabled": "false",
						},
						state: "poweredOn",
					},
				},
			},
			expectedVMs: []*VirtualMachine{
				{
					device: device{
						moid: "vm-28",
						name: "DC0_C0_RP0_VM0",
						facts: map[string]string{
							"cpu_cores":             "1",
							"fqdn":                  "DC0_C0_RP0_VM0",
							"hostname":              "DC0_C0_RP0_VM0",
							"memory":                "32.00 MB",
							"vsphere_host":          "host-23",
							"vsphere_resource_pool": "resgroup-15",
							"vsphere_vm_name":       "DC0_C0_RP0_VM0",
							"vsphere_vm_version":    "vmx-13",
						},
						state: "poweredOn",
					},
				},
			},
			expectedLabelsMetadata: map[string]labelsMetadata{
				"127.0.0.1": {
					datastorePerLUN:    map[string]string{},
					disksPerVM:         map[string]map[string]string{"vm-28": {"scsi0:0": "disk-202-0"}},
					netInterfacesPerVM: map[string]map[string]string{"vm-28": {"4000": "ethernet-0"}},
				},
			},
		},
		{
			name:          "vCenter",
			dirName:       "vcenter_2",
			expectedHosts: []*HostSystem{},
			expectedVMs: []*VirtualMachine{
				{
					device: device{
						moid: "vm-74",
						name: "app-haproxy2",
						facts: map[string]string{
							"cpu_cores":             "2",
							"fqdn":                  "app-haproxy2",
							"hostname":              "app-haproxy2",
							"memory":                "2.00 GB",
							"vsphere_host":          "host-11",                     // TODO improve this
							"vsphere_resource_pool": "resgroup-8",                  // TODO improve this
							"os_pretty_name":        "Debian GNU/Linux 5 (64-bit)", // TODO: we want "Debian GNU/Linux 11 (64-bit)"
							"primary_address":       "192.168.0.2",
							"vsphere_datastore":     "Datastore001",
							"vsphere_vm_name":       "app-haproxy2",
							"vsphere_vm_version":    "vmx-07",
						},
						state: "poweredOn",
					},
				},
			},
			expectedLabelsMetadata: map[string]labelsMetadata{
				"127.0.0.1": {
					datastorePerLUN:    map[string]string{},
					disksPerVM:         map[string]map[string]string{"vm-74": {"scsi0:0": "disk-1000-0"}},
					netInterfacesPerVM: map[string]map[string]string{"vm-74": {"4000": "ethernet-0"}},
				},
			},
		},
	}

	for _, testCase := range testCases {
		tc := testCase

		t.Run(tc.name, func(t *testing.T) {
			// govmomi simulator doesn't seem to like having multiple instances in parallel.

			vSphereCfg, deferFn := setupVSphereAPITest(t, tc.dirName)
			defer deferFn()

			ctx, cancel := context.WithTimeout(context.Background(), commonTimeout)
			defer cancel()

			manager := new(Manager)
			manager.RegisterGatherers([]config.VSphere{vSphereCfg}, func(opt registry.RegistrationOption, gatherer prometheus.Gatherer) (int, error) { return 0, nil }, nil)

			devices := manager.Devices(ctx, 0)

			var (
				clusters []*Cluster
				hosts    []*HostSystem
				vms      []*VirtualMachine
			)

			for _, dev := range devices {
				switch kind := dev.Kind(); kind {
				case KindCluster:
					clusters = append(clusters, dev.(*Cluster)) //nolint: forcetypeassert
				case KindHost:
					hosts = append(hosts, dev.(*HostSystem)) //nolint: forcetypeassert
				case KindVM:
					vms = append(vms, dev.(*VirtualMachine)) //nolint: forcetypeassert
				default:
					// If this error is triggered, the test will (normally) stop at the if right below.
					t.Errorf("Unexpected device kind: %q", kind)
				}
			}

			if len(clusters) != len(tc.expectedClusters) || len(hosts) != len(tc.expectedHosts) || len(vms) != len(tc.expectedVMs) {
				t.Fatalf("Expected %d cluster(s), %d host(s) and %d VM(s), got %d cluster(s), %d host(s) and %d VM(s).",
					len(tc.expectedClusters), len(tc.expectedHosts), len(tc.expectedVMs), len(clusters), len(hosts), len(vms))
			}

			noSourceCmp := cmpopts.IgnoreFields(device{}, "source")

			sortDevices(tc.expectedClusters)
			sortDevices(clusters)
			// We need to compare the clusters, hosts and VMs one by one, otherwise the diff is way harder to analyze.
			for i, expectedCluster := range tc.expectedClusters {
				if diff := cmp.Diff(expectedCluster, clusters[i], cmp.AllowUnexported(Cluster{}, device{}), noSourceCmp); diff != "" {
					t.Errorf("Unexpected cluster description (-want +got):\n%s", diff)
				}
			}

			sortDevices(tc.expectedHosts)
			sortDevices(hosts)
			for i, expectedHost := range tc.expectedHosts {
				if diff := cmp.Diff(expectedHost, hosts[i], cmp.AllowUnexported(HostSystem{}, device{}), noSourceCmp); diff != "" {
					t.Errorf("Unexpected host description (-want +got):\n%s", diff)
				}
			}

			sortDevices(tc.expectedVMs)
			sortDevices(vms)

			for i, expectedVM := range tc.expectedVMs {
				if diff := cmp.Diff(expectedVM, vms[i], cmp.AllowUnexported(VirtualMachine{}, device{}), noSourceCmp); diff != "" {
					t.Errorf("Unexpected VM description (-want +got):\n%s", diff)
				}
			}

			labelsMetadataByVSphere := mapMap(manager.vSpheres, func(ki string, vi *vSphere) (string, labelsMetadata) {
				return strings.Split(ki, ":")[0], vi.labelsMetadata
			})
			if diff := cmp.Diff(tc.expectedLabelsMetadata, labelsMetadataByVSphere, cmp.AllowUnexported(labelsMetadata{})); diff != "" {
				t.Errorf("Unexpected labels metadata (-want +got):\n%s", diff)
			}
		})
	}
}

// sortDevices sorts the given VSphereDevice slice in place, according to device MOIDs.
func sortDevices[D bleemeoTypes.VSphereDevice](devices []D) {
	sort.Slice(devices, func(i, j int) bool {
		return devices[i].MOID() < devices[j].MOID()
	})
}

// KI/KO: Input/Output Keys | VI/VO: Input/Output Values.
func mapMap[KI, KO comparable, VI, VO any](m map[KI]VI, f func(KI, VI) (KO, VO)) map[KO]VO {
	result := make(map[KO]VO, len(m))

	for ki, vi := range m {
		ko, vo := f(ki, vi)
		result[ko] = vo
	}

	return result
}
