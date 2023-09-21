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
	"glouton/config"
	"glouton/inputs"
	"net/url"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/influxdata/telegraf"
	"github.com/vmware/govmomi/simulator"
)

func setupVSphereAPITest(t *testing.T, dirName string) (vSphereCfg config.VSphere, deferFn func()) {
	t.Helper()

	model := simulator.VPX()

	err := model.Create()
	if err != nil {
		t.Fatal(err)
	}

	err = model.Load("testdata/" + dirName)
	if err != nil {
		t.Fatal(err)
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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	finder, err := newDeviceFinder(ctx, vSphereCfg)
	if err != nil {
		t.Fatal(err)
	}

	hosts, vms, err := findDevices(ctx, finder)
	if err != nil {
		t.Fatal(err)
	}

	fail := false

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

	deviceChan := make(chan Device, 1) // 1 of buffer because we only have 1 host, then 1 VM.

	describeHosts(ctx, hosts, deviceChan)

	if len(deviceChan) != 1 {
		t.Fatalf("Expected 1 host to be described, but got %d.", len(deviceChan))
	}

	devHost := <-deviceChan

	host, ok := devHost.(*HostSystem)
	if !ok {
		t.Fatalf("Expected device to be a HostSystem, but is %T.", devHost)
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
		},
	}
	if diff := cmp.Diff(expectedHost, *host, cmp.AllowUnexported(HostSystem{}, device{})); diff != "" {
		t.Fatalf("Unexpected host description:\n%s", diff)
	}

	describeVMs(ctx, vms, deviceChan)

	if len(deviceChan) != 1 {
		t.Fatalf("Expected 1 VM to be described, but got %d.", len(deviceChan))
	}

	devVM := <-deviceChan

	vm, ok := devVM.(*VirtualMachine)
	if !ok {
		t.Fatalf("Expected device to be a VirtualMachine, but is %T.", devVM)
	}

	expectedVM := VirtualMachine{
		device: device{
			source: vSphereURL.Host,
			moid:   "vm-28",
			name:   "DC0_C0_RP0_VM0",
			facts: map[string]string{
				"cpu_cores":             "1",
				"fqdn":                  "DC0_C0_RP0_VM0",
				"hostname":              "vm-28",
				"memory":                "32.00 MB",
				"os_pretty_name":        "otherGuest",
				"vsphere_host":          "host-23",
				"vsphere_resource_pool": "resgroup-15",
				"vsphere_vm_name":       "DC0_C0_RP0_VM0",
				"vsphere_vm_version":    "vmx-13",
			},
		},
		UUID: "cd0681bf-2f18-5c00-9b9b-8197c0095348",
	}
	if diff := cmp.Diff(expectedVM, *vm, cmp.AllowUnexported(VirtualMachine{}, device{})); diff != "" {
		t.Fatalf("Unexpected VM description:\n%s", diff)
	}
}

// TestESXIDescribing lists and describes the devices of an ESXI,
// but by calling the higher-level method Manager.Devices.
func TestESXIDescribing(t *testing.T) {
	vSphereCfg, deferFn := setupVSphereAPITest(t, "esxi_1")
	defer deferFn()

	vSphereURL, err := url.Parse(vSphereCfg.URL)
	if err != nil {
		t.Fatalf("Failed to parse vSphere URL %q: %v", vSphereCfg.URL, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	manager := new(Manager)
	manager.RegisterInputs([]config.VSphere{vSphereCfg}, func(string, telegraf.Input, *inputs.GathererOptions, error) {})

	devices := manager.Devices(ctx, 0)
	if len(devices) != 4 {
		t.Fatalf("Expected 4 devices to be described, got %d.", len(devices))
	}

	var (
		hosts []*HostSystem
		vms   []*VirtualMachine
	)

	for _, dev := range devices {
		switch kind := dev.Kind(); kind {
		case KindHost:
			hosts = append(hosts, dev.(*HostSystem)) //nolint: forcetypeassert
		case KindVM:
			vms = append(vms, dev.(*VirtualMachine)) //nolint: forcetypeassert
		default:
			// If this error is triggered, the test will (normally) stop at the if right below.
			t.Errorf("Unexpected device kind: %q", kind)
		}
	}

	if len(hosts) != 1 || len(vms) != 3 {
		t.Fatalf("Expected 1 host and 3 VMs, got %d host(s) and %d VM(s).", len(hosts), len(vms))
	}

	expectedHost := HostSystem{
		device{
			source: vSphereURL.Host,
			moid:   "ha-host",
			name:   "esxi.test",
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
		},
	}
	if diff := cmp.Diff(expectedHost, *hosts[0], cmp.AllowUnexported(HostSystem{}, device{})); diff != "" {
		t.Fatalf("Unexpected host description:\n%s", diff)
	}

	expectedVMs := map[string]VirtualMachine{
		"1": {
			device: device{
				source: vSphereURL.Host,
				moid:   "1",
				name:   "1",
				facts: map[string]string{
					"fqdn":                  "1",
					"hostname":              "1",
					"vsphere_host":          "ha-host",
					"vsphere_resource_pool": "ha-root-pool",
				},
			},
		},
		"4": {
			device: device{
				source: vSphereURL.Host,
				moid:   "4",
				name:   "v-center",
				facts: map[string]string{
					"cpu_cores":             "2",
					"fqdn":                  "v-center",
					"hostname":              "4",
					"memory":                "14.00 GB",
					"os_pretty_name":        "Other 3.x or later Linux (64-bit)",
					"vsphere_datastore":     "datastore1",
					"vsphere_host":          "ha-host",
					"vsphere_resource_pool": "ha-root-pool",
					"vsphere_vm_name":       "v-center",
					"vsphere_vm_version":    "vmx-10",
				},
			},
			UUID: "564d8859-9d98-c670-0aca-009149c3a8af",
		},
		"7": {
			device: device{
				source: vSphereURL.Host,
				moid:   "7",
				name:   "lunar",
				facts: map[string]string{
					"cpu_cores":             "2",
					"fqdn":                  "lunar",
					"hostname":              "7",
					"memory":                "1.00 GB",
					"os_pretty_name":        "Ubuntu Linux (64-bit)",
					"vsphere_datastore":     "datastore1",
					"vsphere_host":          "ha-host",
					"vsphere_resource_pool": "ha-root-pool",
					"vsphere_vm_name":       "lunar",
					"vsphere_vm_version":    "vmx-10",
				},
			},
			UUID: "564de3ab-988d-b51c-a5cb-5e1af6f5f313",
		},
	}

	for _, vm := range vms {
		expectedVM, found := expectedVMs[vm.moid]
		if !found {
			t.Errorf("Did not expected a VM with moid %q.", vm.moid)

			continue
		}

		if diff := cmp.Diff(expectedVM, *vm, cmp.AllowUnexported(VirtualMachine{}, device{})); diff != "" {
			t.Errorf("Unexpected VM description:\n%s", diff)
		}
	}
}
