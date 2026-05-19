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
	"crypto/tls"
	"net/url"
	"sort"
	"strings"
	"testing"

	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/prometheus/registry"
	"github.com/bleemeo/glouton/types"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/vmware/govmomi/simulator"
)

// Test-only constants for repeated values.
const (
	testStatePoweredOn = "poweredOn"
	testStateGreen     = "green"
)

// Test fact key constants.
const (
	testFactKeyDomain      = "domain"
	testFactKeyIPv6Enabled = "ipv6_enabled"
	testFactKeyCPUModel    = "cpu_model_name"
	testFactKeyProductName = "product_name"
	testFactKeySysVendor   = "system_vendor"
)

// Test fact value constants shared across test files.
const (
	testDatastore25    = "datastore-25"
	testCPUModelI7     = "Intel(R) Core(TM) i7-3615QM CPU @ 2.30GHz"
	testFalse          = "false"
	testMem4GB         = "4.00 GB"
	testOSVmnixX86     = "vmnix-x86"
	testIP127001       = "127.0.0.1"
	testProductVMW     = "VMware Virtual Platform"
	testVendorGovmomi  = "VMware, Inc. (govmomi simulator)"
	testVersion650     = "6.5.0"
	testDomainLocal    = "localdomain"
	testVMVersionVmx13 = "vmx-13"
	testDisk202_0      = "disk-202-0"
	testEthernet0      = "ethernet-0"
	testResources      = "Resources"
	testDisk1000_0     = "disk-1000-0"
	testMOIDVM74       = "vm-74"
	testVMAppHaproxy2  = "app-haproxy2"
	testVMVcenterVmx   = "vcenter.vmx"
	testVMLunar        = "lunar"
	testSCSI00         = "scsi0:0"
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
		SkipMonitorVMs:     false,
	}

	return vSphereCfg, func() {
		model.Remove()
		server.Close()
	}
}

// TestVSphereSteps lists and describes the devices of a vCenter,
// by calling individually each method needed.
func TestVSphereSteps(t *testing.T) {
	vSphereCfg, deferFn := setupVSphereAPITest(t, "vcenter_1")
	defer deferFn()

	vSphereURL, err := url.Parse(vSphereCfg.URL)
	if err != nil {
		t.Fatalf("Failed to parse vSphere URL %q: %v", vSphereCfg.URL, err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), commonTimeout)
	defer cancel()

	finder, client, err := newDeviceFinder(ctx, vSphereCfg)
	if err != nil {
		t.Fatal(err)
	}

	clusters, _, resourcePools, hosts, vms, err := findDevices(ctx, finder, false)
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

	const scraperFQDN = "scraper FQDN"

	providedFacts := map[string]string{factFQDN: scraperFQDN}
	dummyVSphere := newVSphere(vSphereURL.Host, vSphereCfg, nil, facts.NewMockFacter(providedFacts))

	err = dummyVSphere.hierarchy.Refresh(ctx, clusters, resourcePools, hosts, vms, dummyVSphere.devicePropsCache.vmCache)
	if err != nil {
		t.Fatalf("Got an error refreshing the vSphere hierarchy: %v", err)
	}

	devices, err := dummyVSphere.describeClusters(ctx, client, clusters, providedFacts)
	if err != nil {
		t.Fatalf("Got an error while describing clusters: %v", err)
	}

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
			moid:   testMOIDDomainC16,
			name:   testClusterDC0C0,
			facts: map[string]string{
				factCPUCores:    "2",
				factFQDN:        testClusterDC0C0,
				factScraperFQDN: scraperFQDN,
			},
			state: testStateGreen,
		},
		datastores: []string{testDatastore25},
	}
	if diff := cmp.Diff(expectedCluster, *cluster, cmp.AllowUnexported(Cluster{}, device{})); diff != "" {
		t.Fatalf("Unexpected host description (-want +got):\n%s", diff)
	}

	devices, err = dummyVSphere.describeHosts(ctx, client, hosts, providedFacts)
	if err != nil {
		t.Fatalf("Got an error while describing hosts: %v", err)
	}

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
			moid:   testMOIDHost23,
			name:   testHostDC0C0H0,
			facts: map[string]string{
				factCPUCores:              "2",
				testFactKeyCPUModel:       testCPUModelI7,
				testFactKeyDomain:         testDomainLocal,
				factFQDN:                  "DC0_C0_H0.localdomain",
				factHostname:              testHostDC0C0H0,
				testFactKeyIPv6Enabled:    testFalse,
				factMemory:                testMem4GB,
				factOSPrettyName:          testOSVmnixX86,
				factPrimaryAddress:        testIP127001,
				testFactKeyProductName:    testProductVMW,
				factScraperFQDN:           scraperFQDN,
				testFactKeySysVendor:      testVendorGovmomi,
				factVSphereHostVersion:    testVersion650,
				factVSphereVMotionEnabled: testFalse,
			},
			state: testStatePoweredOn,
		},
	}
	if diff := cmp.Diff(expectedHost, *host, cmp.AllowUnexported(HostSystem{}, device{})); diff != "" {
		t.Fatalf("Unexpected host description (-want +got):\n%s", diff)
	}

	devices, vmLabelsMetadata, err := dummyVSphere.describeVMs(ctx, client, vms, providedFacts)
	if err != nil {
		t.Fatalf("Got an error while describing vms: %v", err)
	}

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
			moid:   testMOIDVM28,
			name:   testVMDC0C0RP0VM0,
			facts: map[string]string{
				factCPUCores:            "1",
				factFQDN:                testVMDC0C0RP0VM0,
				factHostname:            testVMDC0C0RP0VM0,
				factMemory:              "32.00 MB",
				factOSPrettyName:        "",
				factScraperFQDN:         scraperFQDN,
				factVSphereHost:         testHostDC0C0H0,
				factVSphereResourcePool: testResources,
				factVSphereVMName:       testVMDC0C0RP0VM0,
				factVSphereVMVersion:    testVMVersionVmx13,
			},
			state: testStatePoweredOn,
		},
	}
	if diff := cmp.Diff(expectedVM, *vm, cmp.AllowUnexported(VirtualMachine{}, device{})); diff != "" {
		t.Fatalf("Unexpected VM description (-want +got):\n%s", diff)
	}

	expectedLabelsMetadata := labelsMetadata{
		disksPerVM:         map[string]map[string]string{testMOIDVM28: {testSCSI00: testDisk202_0}},
		netInterfacesPerVM: map[string]map[string]string{testMOIDVM28: {"4000": testEthernet0}},
	}
	if diff := cmp.Diff(expectedLabelsMetadata, vmLabelsMetadata, cmp.AllowUnexported(labelsMetadata{})); diff != "" {
		t.Fatalf("Unexpected labels metadata (-want +got):\n%s", diff)
	}
}

// TestVSphereLifecycle lists and describes devices across multiple test cases,
// but by calling the higher-level method Manager.Devices.
func TestVSphereLifecycle(t *testing.T) { //nolint:maintidx
	const scraperFQDN = "scraper FQDN"

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
						name: testHostESXI,
						facts: map[string]string{
							factCPUCores:              "4",
							testFactKeyCPUModel:       "Intel(R) Core(TM) i7-8550U CPU @ 1.80GHz",
							testFactKeyDomain:         "test",
							factFQDN:                  "esxi.test.test",
							factHostname:              testHostESXI,
							testFactKeyIPv6Enabled:    testFalse,
							factMemory:                testMem4GB,
							factOSPrettyName:          testOSVmnixX86,
							factPrimaryAddress:        "192.168.121.241",
							testFactKeyProductName:    "Standard PC (i440FX + PIIX, 1996)",
							factScraperFQDN:           scraperFQDN,
							testFactKeySysVendor:      "QEMU",
							"timezone":                "UTC",
							factVSphereHostVersion:    "8.0.1",
							factVSphereVMotionEnabled: testFalse,
						},
						state: testStatePoweredOn,
					},
				},
			},
			expectedVMs: []*VirtualMachine{
				{
					device: device{
						moid: "1",
						name: testVMVcenterVmx,
						facts: map[string]string{
							factFQDN:                testVMVcenterVmx,
							factHostname:            testVMVcenterVmx,
							factOSPrettyName:        "",
							factScraperFQDN:         scraperFQDN,
							factVSphereHost:         testHostESXI,
							factVSphereResourcePool: testResources,
						},
						state: "poweredOff",
					},
				},
				{
					device: device{
						moid: "8",
						name: testVMLunar,
						facts: map[string]string{
							factCPUCores:            "2",
							factFQDN:                testVMLunar,
							factHostname:            testVMLunar,
							factMemory:              "1.00 GB",
							factOSPrettyName:        "Ubuntu Linux (64-bit)",
							factScraperFQDN:         scraperFQDN,
							factVSphereDatastore:    "datastore1",
							factVSphereHost:         testHostESXI,
							factVSphereResourcePool: testResources,
							factVSphereVMName:       testVMLunar,
							factVSphereVMVersion:    "vmx-10",
						},
						state: "poweredOff",
					},
				},
				{
					device: device{
						moid: "10",
						name: testVMAlp1,
						facts: map[string]string{
							factCPUCores:            "1",
							factFQDN:                testVMAlp1,
							factHostname:            "alpine",
							factMemory:              "512.00 MB",
							factOSPrettyName:        "Linux 5.15.71-0-virt Alpine Linux v3.15 Alpine Linux 3.15.6",
							factPrimaryAddress:      "192.168.121.117",
							factScraperFQDN:         scraperFQDN,
							factVSphereDatastore:    "datastore1",
							factVSphereHost:         testHostESXI,
							factVSphereResourcePool: testResources,
							factVSphereVMName:       testVMAlp1,
							factVSphereVMVersion:    "vmx-14",
						},
						state: testStatePoweredOn,
					},
				},
			},
			expectedLabelsMetadata: map[string]labelsMetadata{
				testIP127001: {
					datastorePerLUN: map[string]string{},
					disksPerVM: map[string]map[string]string{
						"1": nil,
						"10": {
							"nvme0:0":  "disk-31000-0",
							"sata0:0":  "disk-15000-0",
							testSCSI00: testDisk1000_0,
						},
						"8": {testSCSI00: testDisk1000_0},
					},
					netInterfacesPerVM: map[string]map[string]string{"1": nil, "10": {"4000": testEthernet0}, "8": {"4000": testEthernet0}},
				},
			},
		},
		{
			name:    "vCenter vcsim'ulated",
			dirName: "vcenter_1",
			expectedClusters: []*Cluster{
				{
					device: device{
						moid: testMOIDDomainC16,
						name: testClusterDC0C0,
						facts: map[string]string{
							factCPUCores:    "2",
							factFQDN:        testClusterDC0C0,
							factScraperFQDN: scraperFQDN,
						},
						state: testStateGreen,
					},
					datastores: []string{testDatastore25},
				},
			},
			expectedHosts: []*HostSystem{
				{
					device: device{
						moid: testMOIDHost23,
						name: testHostDC0C0H0,
						facts: map[string]string{
							factCPUCores:              "2",
							testFactKeyCPUModel:       testCPUModelI7,
							testFactKeyDomain:         testDomainLocal,
							factFQDN:                  "DC0_C0_H0.localdomain",
							factHostname:              testHostDC0C0H0,
							testFactKeyIPv6Enabled:    testFalse,
							factMemory:                testMem4GB,
							factOSPrettyName:          testOSVmnixX86,
							factPrimaryAddress:        testIP127001,
							testFactKeyProductName:    testProductVMW,
							factScraperFQDN:           scraperFQDN,
							testFactKeySysVendor:      testVendorGovmomi,
							factVSphereHostVersion:    testVersion650,
							factVSphereVMotionEnabled: testFalse,
						},
						state: testStatePoweredOn,
					},
				},
			},
			expectedVMs: []*VirtualMachine{
				{
					device: device{
						moid: testMOIDVM28,
						name: testVMDC0C0RP0VM0,
						facts: map[string]string{
							factCPUCores:            "1",
							factFQDN:                testVMDC0C0RP0VM0,
							factHostname:            testVMDC0C0RP0VM0,
							factMemory:              "32.00 MB",
							factOSPrettyName:        "",
							factScraperFQDN:         scraperFQDN,
							factVSphereHost:         testHostDC0C0H0,
							factVSphereResourcePool: testResources,
							factVSphereVMName:       testVMDC0C0RP0VM0,
							factVSphereVMVersion:    testVMVersionVmx13,
						},
						state: testStatePoweredOn,
					},
				},
			},
			expectedLabelsMetadata: map[string]labelsMetadata{
				testIP127001: {
					datastorePerLUN:    map[string]string{},
					disksPerVM:         map[string]map[string]string{testMOIDVM28: {testSCSI00: testDisk202_0}},
					netInterfacesPerVM: map[string]map[string]string{testMOIDVM28: {"4000": testEthernet0}},
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
						moid: testMOIDVM74,
						name: testVMAppHaproxy2,
						facts: map[string]string{
							factCPUCores:            "2",
							factFQDN:                testVMAppHaproxy2,
							factHostname:            testVMAppHaproxy2,
							factMemory:              "2.00 GB",
							factVSphereHost:         "host-11",    // TODO improve this
							factVSphereResourcePool: "resgroup-8", // TODO improve this
							factOSPrettyName:        "Debian GNU/Linux 11 (64-bit)",
							factPrimaryAddress:      "192.168.0.2",
							factScraperFQDN:         scraperFQDN,
							factVSphereDatastore:    "Datastore001",
							factVSphereVMName:       testVMAppHaproxy2,
							factVSphereVMVersion:    "vmx-07",
						},
						state: testStatePoweredOn,
					},
				},
			},
			expectedLabelsMetadata: map[string]labelsMetadata{
				testIP127001: {
					datastorePerLUN:    map[string]string{},
					disksPerVM:         map[string]map[string]string{testMOIDVM74: {testSCSI00: testDisk1000_0}},
					netInterfacesPerVM: map[string]map[string]string{testMOIDVM74: {"4000": testEthernet0}},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) { //nolint: wsl
			// govmomi simulator doesn't seem to like having multiple instances in parallel.
			vSphereCfg, deferFn := setupVSphereAPITest(t, tc.dirName)
			defer deferFn()

			ctx, cancel := context.WithTimeout(t.Context(), commonTimeout)
			defer cancel()

			manager := new(Manager)
			manager.RegisterGatherers(
				ctx,
				[]config.VSphere{vSphereCfg},
				func(_ registry.RegistrationOption, _ prometheus.Gatherer) (types.Registration, error) {
					return nil, nil //nolint: nilnil
				},
				nil,
				facts.NewMockFacter(map[string]string{factFQDN: scraperFQDN}),
			)

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

			labelsMetadataByVSphere := mapMap(manager.vSpheres, func(host string, vSphere *vSphere) (string, labelsMetadata) {
				return strings.Split(host, ":")[0], vSphere.labelsMetadata
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
