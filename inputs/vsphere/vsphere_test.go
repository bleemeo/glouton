package vsphere

import (
	"crypto/tls"
	"fmt"
	"glouton/inputs/internal"
	"glouton/types"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/plugins/inputs/vsphere"
	"github.com/vmware/govmomi/simulator"
)

func TestRenameMetrics(t *testing.T) {
	cases := []struct {
		metricName string
		expected   string
	}{
		{
			"swapped_average",
			"swapped",
		},
		{
			"totalCapacity_average",
			"totalCapacity",
		},
		{
			"net_transmitted_average",
			"net_transmitted",
		},
	}

	currentContext := internal.GatherContext{Measurement: "vsphere_..."}

	for _, testCase := range cases {
		newMeasurement, newMetricName := renameMetrics(currentContext, testCase.metricName)
		if newMeasurement != currentContext.Measurement {
			t.Errorf("renameMetrics() did modified the measurement but shouldn't have.")
		}

		if newMetricName != testCase.expected {
			t.Errorf("Unexpected result of renameMetrics(%q): want %q, got %q", testCase.metricName, testCase.expected, newMetricName)
		}
	}
}

func TestTransformMetrics(t *testing.T) {
	cases := map[string][]struct {
		field         string
		value         float64
		expectedValue float64
	}{
		"vsphere_vm_mem": {
			{
				"swapped_average",
				185.64,
				185640,
			},
		},
		"vsphere_vm_disk": {
			{
				"read_average",
				35.432,
				35432,
			},
			{
				"write_average",
				36.68,
				36680,
			},
		},
		"vsphere_host_mem": {
			{
				"totalCapacity_average",
				4294.967296,
				4294967296,
			},
			{
				"mem.usage.average",
				78,
				78,
			},
		},
		"vsphere_host_net": {
			{
				"net_transmitted_average",
				0.91,
				910,
			},
		},
	}

	for measurement, testCases := range cases {
		currentContext := internal.GatherContext{Measurement: measurement}
		fields := make(map[string]float64, len(testCases))
		expected := make(map[string]float64, len(testCases))

		for _, testCase := range testCases {
			fields[testCase.field] = testCase.value
			expected[testCase.field] = testCase.expectedValue
		}

		resultFields := transformMetrics(currentContext, fields, nil)
		if diff := cmp.Diff(expected, resultFields); diff != "" {
			t.Errorf("Unexpected result of transformMetrics(%q, ...):\n%v", measurement, diff)
		}
	}
}

func setupVSphereTest(t *testing.T, hostsCount int) (vsphereInput *vsphere.VSphere, deferFn func()) {
	t.Helper()

	model := simulator.VPX()
	model.ClusterHost = 1
	model.Machine = 1
	model.Host = hostsCount

	err := model.Create()
	if err != nil {
		t.Fatal(err)
	}

	model.Service.TLS = new(tls.Config)

	server := model.Service.NewServer()

	lgr := &logHandler{}

	input, _, err := New(server.URL.String(), "user", "pass", true, true)
	if err != nil {
		t.Fatal("Failed to create vSphere input:", err)
	}

	vsphereInput = input.(*internal.Input).Input.(*vsphere.VSphere) //nolint:forcetypeassert
	vsphereInput.Log = lgr

	return vsphereInput, func() {
		model.Remove()
		server.Close()

		lgr.assertNoUnexpectedLogs(t)
	}
}

func TestVSphereInputNoHost(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	input, deferFn := setupVSphereTest(t, 0)
	defer deferFn()

	acc := shallowAcc{fields: make(msmsa)}

	err := input.Start(&acc)
	if err != nil {
		t.Fatal("Failed to start vSphere input:", err)
	}

	err = input.Gather(&acc)
	if err != nil {
		t.Fatal("Failed to gather from vSphere input:", err)
	}

	expectedFields := msmsa{
		"vsphere_datacenter_vmop": {
			`numChangeDS_latest__dcname="DC0",moid="datacenter-2",source="DC0"`:      int64(0),
			`numChangeHost_latest__dcname="DC0",moid="datacenter-2",source="DC0"`:    int64(0),
			`numCreate_latest__dcname="DC0",moid="datacenter-2",source="DC0"`:        int64(0),
			`numDestroy_latest__dcname="DC0",moid="datacenter-2",source="DC0"`:       int64(0),
			`numPoweroff_latest__dcname="DC0",moid="datacenter-2",source="DC0"`:      int64(0),
			`numPoweron_latest__dcname="DC0",moid="datacenter-2",source="DC0"`:       int64(0),
			`numRebootGuest_latest__dcname="DC0",moid="datacenter-2",source="DC0"`:   int64(0),
			`numReconfigure_latest__dcname="DC0",moid="datacenter-2",source="DC0"`:   int64(0),
			`numRegister_latest__dcname="DC0",moid="datacenter-2",source="DC0"`:      int64(0),
			`numReset_latest__dcname="DC0",moid="datacenter-2",source="DC0"`:         int64(0),
			`numSVMotion_latest__dcname="DC0",moid="datacenter-2",source="DC0"`:      int64(0),
			`numShutdownGuest_latest__dcname="DC0",moid="datacenter-2",source="DC0"`: int64(0),
			`numSuspend_latest__dcname="DC0",moid="datacenter-2",source="DC0"`:       int64(0),
			`numUnregister_latest__dcname="DC0",moid="datacenter-2",source="DC0"`:    int64(0),
			`numVMotion_latest__dcname="DC0",moid="datacenter-2",source="DC0"`:       int64(0),
		},
		"vsphere_host_cpu": {
			`usage_average__clustername="DC0_C0",cpu="*",dcname="DC0",esxhostname="DC0_C0_H0",moid="host-23",source="DC0_C0_H0"`: 0.,
		},
		"vsphere_host_disk": {
			`read_average__clustername="DC0_C0",dcname="DC0",disk="*",esxhostname="DC0_C0_H0",moid="host-23",source="DC0_C0_H0"`:  int64(0),
			`usage_average__clustername="DC0_C0",dcname="DC0",disk="*",esxhostname="DC0_C0_H0",moid="host-23",source="DC0_C0_H0"`: int64(0),
			`write_average__clustername="DC0_C0",dcname="DC0",disk="*",esxhostname="DC0_C0_H0",moid="host-23",source="DC0_C0_H0"`: int64(0),
		},
		"vsphere_host_mem": {
			`active_average__clustername="DC0_C0",dcname="DC0",esxhostname="DC0_C0_H0",instance="*",moid="host-23",source="DC0_C0_H0"`:        int64(0),
			`totalCapacity_average__clustername="DC0_C0",dcname="DC0",esxhostname="DC0_C0_H0",instance="*",moid="host-23",source="DC0_C0_H0"`: int64(0),
			`usage_average__clustername="DC0_C0",dcname="DC0",esxhostname="DC0_C0_H0",instance="*",moid="host-23",source="DC0_C0_H0"`:         0.,
		},
		"vsphere_host_net": {
			`received_average__clustername="DC0_C0",dcname="DC0",esxhostname="DC0_C0_H0",interface="*",moid="host-23",source="DC0_C0_H0"`:    int64(0),
			`transmitted_average__clustername="DC0_C0",dcname="DC0",esxhostname="DC0_C0_H0",interface="*",moid="host-23",source="DC0_C0_H0"`: int64(0),
			`usage_average__clustername="DC0_C0",dcname="DC0",esxhostname="DC0_C0_H0",interface="*",moid="host-23",source="DC0_C0_H0"`:       int64(0),
		},
		"vsphere_vm_cpu": {
			`latency_average__clustername="DC0_C0",cpu="*",dcname="DC0",esxhostname="DC0_C0_H0",guest="other",moid="vm-28",rpname="Resources",source="DC0_C0_RP0_VM0",uuid="cd0681bf-2f18-5c00-9b9b-8197c0095348",vmname="DC0_C0_RP0_VM0"`: 0.,
			`usage_average__clustername="DC0_C0",cpu="*",dcname="DC0",esxhostname="DC0_C0_H0",guest="other",moid="vm-28",rpname="Resources",source="DC0_C0_RP0_VM0",uuid="cd0681bf-2f18-5c00-9b9b-8197c0095348",vmname="DC0_C0_RP0_VM0"`:   0.,
		},
		"vsphere_vm_disk": {
			`read_average__clustername="DC0_C0",dcname="DC0",disk="*",esxhostname="DC0_C0_H0",guest="other",moid="vm-28",rpname="Resources",source="DC0_C0_RP0_VM0",uuid="cd0681bf-2f18-5c00-9b9b-8197c0095348",vmname="DC0_C0_RP0_VM0"`:  int64(0),
			`usage_average__clustername="DC0_C0",dcname="DC0",disk="*",esxhostname="DC0_C0_H0",guest="other",moid="vm-28",rpname="Resources",source="DC0_C0_RP0_VM0",uuid="cd0681bf-2f18-5c00-9b9b-8197c0095348",vmname="DC0_C0_RP0_VM0"`: int64(0),
			`write_average__clustername="DC0_C0",dcname="DC0",disk="*",esxhostname="DC0_C0_H0",guest="other",moid="vm-28",rpname="Resources",source="DC0_C0_RP0_VM0",uuid="cd0681bf-2f18-5c00-9b9b-8197c0095348",vmname="DC0_C0_RP0_VM0"`: int64(0),
		},
		"vsphere_vm_mem": {
			`usage_average__clustername="DC0_C0",dcname="DC0",esxhostname="DC0_C0_H0",guest="other",instance="*",moid="vm-28",rpname="Resources",source="DC0_C0_RP0_VM0",uuid="cd0681bf-2f18-5c00-9b9b-8197c0095348",vmname="DC0_C0_RP0_VM0"`: 0.,
		},
		"vsphere_vm_net": {
			`received_average__clustername="DC0_C0",dcname="DC0",esxhostname="DC0_C0_H0",guest="other",interface="*",moid="vm-28",rpname="Resources",source="DC0_C0_RP0_VM0",uuid="cd0681bf-2f18-5c00-9b9b-8197c0095348",vmname="DC0_C0_RP0_VM0"`:    int64(0),
			`transmitted_average__clustername="DC0_C0",dcname="DC0",esxhostname="DC0_C0_H0",guest="other",interface="*",moid="vm-28",rpname="Resources",source="DC0_C0_RP0_VM0",uuid="cd0681bf-2f18-5c00-9b9b-8197c0095348",vmname="DC0_C0_RP0_VM0"`: int64(0),
			`usage_average__clustername="DC0_C0",dcname="DC0",esxhostname="DC0_C0_H0",guest="other",interface="*",moid="vm-28",rpname="Resources",source="DC0_C0_RP0_VM0",uuid="cd0681bf-2f18-5c00-9b9b-8197c0095348",vmname="DC0_C0_RP0_VM0"`:       int64(0),
		},
	}

	// Field values are random, so we exclude them from diffing by ignoring int64 and floats.
	if diff := cmp.Diff(expectedFields, acc.fields, cmpopts.IgnoreTypes(int64(0), float64(0))); diff != "" {
		t.Errorf("Unexpected fields:\n%v", diff)
	}
}

func TestVSphereInputMultipleHosts(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	input, deferFn := setupVSphereTest(t, 2)
	defer deferFn()

	acc := shallowAcc{fields: make(msmsa)}

	err := input.Start(&acc)
	if err != nil {
		t.Fatal("Failed to start vSphere input:", err)
	}

	err = input.Gather(&acc)
	if err != nil {
		t.Fatal("Failed to gather from vSphere input:", err)
	}

	expectedFields := msmsa{
		"vsphere_datacenter_vmop": {
			`numChangeDS_latest__dcname="DC0",moid="datacenter-2",source="DC0"`:      int64(0),
			`numChangeHost_latest__dcname="DC0",moid="datacenter-2",source="DC0"`:    int64(0),
			`numCreate_latest__dcname="DC0",moid="datacenter-2",source="DC0"`:        int64(0),
			`numDestroy_latest__dcname="DC0",moid="datacenter-2",source="DC0"`:       int64(0),
			`numPoweroff_latest__dcname="DC0",moid="datacenter-2",source="DC0"`:      int64(0),
			`numPoweron_latest__dcname="DC0",moid="datacenter-2",source="DC0"`:       int64(0),
			`numRebootGuest_latest__dcname="DC0",moid="datacenter-2",source="DC0"`:   int64(0),
			`numReconfigure_latest__dcname="DC0",moid="datacenter-2",source="DC0"`:   int64(0),
			`numRegister_latest__dcname="DC0",moid="datacenter-2",source="DC0"`:      int64(0),
			`numReset_latest__dcname="DC0",moid="datacenter-2",source="DC0"`:         int64(0),
			`numSVMotion_latest__dcname="DC0",moid="datacenter-2",source="DC0"`:      int64(0),
			`numShutdownGuest_latest__dcname="DC0",moid="datacenter-2",source="DC0"`: int64(0),
			`numSuspend_latest__dcname="DC0",moid="datacenter-2",source="DC0"`:       int64(0),
			`numUnregister_latest__dcname="DC0",moid="datacenter-2",source="DC0"`:    int64(0),
			`numVMotion_latest__dcname="DC0",moid="datacenter-2",source="DC0"`:       int64(0),
		},
		"vsphere_host_cpu": {
			`usage_average__clustername="DC0_C0",cpu="*",dcname="DC0",esxhostname="DC0_C0_H0",moid="host-45",source="DC0_C0_H0"`: 0.,
			`usage_average__cpu="*",dcname="DC0",esxhostname="DC0_H0",moid="host-21",source="DC0_H0"`:                            0.,
			`usage_average__cpu="*",dcname="DC0",esxhostname="DC0_H1",moid="host-32",source="DC0_H1"`:                            0.,
		},
		"vsphere_host_disk": {
			`read_average__clustername="DC0_C0",dcname="DC0",disk="*",esxhostname="DC0_C0_H0",moid="host-45",source="DC0_C0_H0"`:  int64(0),
			`read_average__dcname="DC0",disk="*",esxhostname="DC0_H0",moid="host-21",source="DC0_H0"`:                             int64(0),
			`read_average__dcname="DC0",disk="*",esxhostname="DC0_H1",moid="host-32",source="DC0_H1"`:                             int64(0),
			`usage_average__clustername="DC0_C0",dcname="DC0",disk="*",esxhostname="DC0_C0_H0",moid="host-45",source="DC0_C0_H0"`: int64(0),
			`usage_average__dcname="DC0",disk="*",esxhostname="DC0_H0",moid="host-21",source="DC0_H0"`:                            int64(0),
			`usage_average__dcname="DC0",disk="*",esxhostname="DC0_H1",moid="host-32",source="DC0_H1"`:                            int64(0),
			`write_average__clustername="DC0_C0",dcname="DC0",disk="*",esxhostname="DC0_C0_H0",moid="host-45",source="DC0_C0_H0"`: int64(0),
			`write_average__dcname="DC0",disk="*",esxhostname="DC0_H0",moid="host-21",source="DC0_H0"`:                            int64(0),
			`write_average__dcname="DC0",disk="*",esxhostname="DC0_H1",moid="host-32",source="DC0_H1"`:                            int64(0),
		},
		"vsphere_host_mem": {
			`active_average__clustername="DC0_C0",dcname="DC0",esxhostname="DC0_C0_H0",instance="*",moid="host-45",source="DC0_C0_H0"`:        int64(0),
			`active_average__dcname="DC0",esxhostname="DC0_H0",instance="*",moid="host-21",source="DC0_H0"`:                                   int64(0),
			`active_average__dcname="DC0",esxhostname="DC0_H1",instance="*",moid="host-32",source="DC0_H1"`:                                   int64(0),
			`totalCapacity_average__clustername="DC0_C0",dcname="DC0",esxhostname="DC0_C0_H0",instance="*",moid="host-45",source="DC0_C0_H0"`: int64(0),
			`totalCapacity_average__dcname="DC0",esxhostname="DC0_H0",instance="*",moid="host-21",source="DC0_H0"`:                            int64(0),
			`totalCapacity_average__dcname="DC0",esxhostname="DC0_H1",instance="*",moid="host-32",source="DC0_H1"`:                            int64(0),
			`usage_average__clustername="DC0_C0",dcname="DC0",esxhostname="DC0_C0_H0",instance="*",moid="host-45",source="DC0_C0_H0"`:         0.,
			`usage_average__dcname="DC0",esxhostname="DC0_H0",instance="*",moid="host-21",source="DC0_H0"`:                                    0.,
			`usage_average__dcname="DC0",esxhostname="DC0_H1",instance="*",moid="host-32",source="DC0_H1"`:                                    0.,
		},
		"vsphere_host_net": {
			`received_average__clustername="DC0_C0",dcname="DC0",esxhostname="DC0_C0_H0",interface="*",moid="host-45",source="DC0_C0_H0"`:    int64(0),
			`received_average__dcname="DC0",esxhostname="DC0_H0",interface="*",moid="host-21",source="DC0_H0"`:                               int64(0),
			`received_average__dcname="DC0",esxhostname="DC0_H1",interface="*",moid="host-32",source="DC0_H1"`:                               int64(0),
			`transmitted_average__clustername="DC0_C0",dcname="DC0",esxhostname="DC0_C0_H0",interface="*",moid="host-45",source="DC0_C0_H0"`: int64(0),
			`transmitted_average__dcname="DC0",esxhostname="DC0_H0",interface="*",moid="host-21",source="DC0_H0"`:                            int64(0),
			`transmitted_average__dcname="DC0",esxhostname="DC0_H1",interface="*",moid="host-32",source="DC0_H1"`:                            int64(0),
			`usage_average__clustername="DC0_C0",dcname="DC0",esxhostname="DC0_C0_H0",interface="*",moid="host-45",source="DC0_C0_H0"`:       int64(0),
			`usage_average__dcname="DC0",esxhostname="DC0_H0",interface="*",moid="host-21",source="DC0_H0"`:                                  int64(0),
			`usage_average__dcname="DC0",esxhostname="DC0_H1",interface="*",moid="host-32",source="DC0_H1"`:                                  int64(0),
		},
		"vsphere_vm_cpu": {
			`latency_average__clustername="DC0_C0",cpu="*",dcname="DC0",esxhostname="DC0_C0_H0",guest="other",moid="vm-56",rpname="Resources",source="DC0_C0_RP0_VM0",uuid="cd0681bf-2f18-5c00-9b9b-8197c0095348",vmname="DC0_C0_RP0_VM0"`: 0.,
			`latency_average__cpu="*",dcname="DC0",esxhostname="DC0_H0",guest="other",moid="vm-50",rpname="Resources",source="DC0_H0_VM0",uuid="265104de-1472-547c-b873-6dc7883fb6cb",vmname="DC0_H0_VM0"`:                                 0.,
			`latency_average__cpu="*",dcname="DC0",esxhostname="DC0_H1",guest="other",moid="vm-53",rpname="Resources",source="DC0_H1_VM0",uuid="4d341ac4-941f-5827-a28b-9550db7729f5",vmname="DC0_H1_VM0"`:                                 0.,
			`usage_average__clustername="DC0_C0",cpu="*",dcname="DC0",esxhostname="DC0_C0_H0",guest="other",moid="vm-56",rpname="Resources",source="DC0_C0_RP0_VM0",uuid="cd0681bf-2f18-5c00-9b9b-8197c0095348",vmname="DC0_C0_RP0_VM0"`:   0.,
			`usage_average__cpu="*",dcname="DC0",esxhostname="DC0_H0",guest="other",moid="vm-50",rpname="Resources",source="DC0_H0_VM0",uuid="265104de-1472-547c-b873-6dc7883fb6cb",vmname="DC0_H0_VM0"`:                                   0.,
			`usage_average__cpu="*",dcname="DC0",esxhostname="DC0_H1",guest="other",moid="vm-53",rpname="Resources",source="DC0_H1_VM0",uuid="4d341ac4-941f-5827-a28b-9550db7729f5",vmname="DC0_H1_VM0"`:                                   0.,
		},
		"vsphere_vm_disk": {
			`read_average__clustername="DC0_C0",dcname="DC0",disk="*",esxhostname="DC0_C0_H0",guest="other",moid="vm-56",rpname="Resources",source="DC0_C0_RP0_VM0",uuid="cd0681bf-2f18-5c00-9b9b-8197c0095348",vmname="DC0_C0_RP0_VM0"`:  int64(0),
			`read_average__dcname="DC0",disk="*",esxhostname="DC0_H0",guest="other",moid="vm-50",rpname="Resources",source="DC0_H0_VM0",uuid="265104de-1472-547c-b873-6dc7883fb6cb",vmname="DC0_H0_VM0"`:                                  int64(0),
			`read_average__dcname="DC0",disk="*",esxhostname="DC0_H1",guest="other",moid="vm-53",rpname="Resources",source="DC0_H1_VM0",uuid="4d341ac4-941f-5827-a28b-9550db7729f5",vmname="DC0_H1_VM0"`:                                  int64(0),
			`usage_average__clustername="DC0_C0",dcname="DC0",disk="*",esxhostname="DC0_C0_H0",guest="other",moid="vm-56",rpname="Resources",source="DC0_C0_RP0_VM0",uuid="cd0681bf-2f18-5c00-9b9b-8197c0095348",vmname="DC0_C0_RP0_VM0"`: int64(0),
			`usage_average__dcname="DC0",disk="*",esxhostname="DC0_H0",guest="other",moid="vm-50",rpname="Resources",source="DC0_H0_VM0",uuid="265104de-1472-547c-b873-6dc7883fb6cb",vmname="DC0_H0_VM0"`:                                 int64(0),
			`usage_average__dcname="DC0",disk="*",esxhostname="DC0_H1",guest="other",moid="vm-53",rpname="Resources",source="DC0_H1_VM0",uuid="4d341ac4-941f-5827-a28b-9550db7729f5",vmname="DC0_H1_VM0"`:                                 int64(0),
			`write_average__clustername="DC0_C0",dcname="DC0",disk="*",esxhostname="DC0_C0_H0",guest="other",moid="vm-56",rpname="Resources",source="DC0_C0_RP0_VM0",uuid="cd0681bf-2f18-5c00-9b9b-8197c0095348",vmname="DC0_C0_RP0_VM0"`: int64(0),
			`write_average__dcname="DC0",disk="*",esxhostname="DC0_H0",guest="other",moid="vm-50",rpname="Resources",source="DC0_H0_VM0",uuid="265104de-1472-547c-b873-6dc7883fb6cb",vmname="DC0_H0_VM0"`:                                 int64(0),
			`write_average__dcname="DC0",disk="*",esxhostname="DC0_H1",guest="other",moid="vm-53",rpname="Resources",source="DC0_H1_VM0",uuid="4d341ac4-941f-5827-a28b-9550db7729f5",vmname="DC0_H1_VM0"`:                                 int64(0),
		},
		"vsphere_vm_mem": {
			`usage_average__clustername="DC0_C0",dcname="DC0",esxhostname="DC0_C0_H0",guest="other",instance="*",moid="vm-56",rpname="Resources",source="DC0_C0_RP0_VM0",uuid="cd0681bf-2f18-5c00-9b9b-8197c0095348",vmname="DC0_C0_RP0_VM0"`: 0.,
			`usage_average__dcname="DC0",esxhostname="DC0_H0",guest="other",instance="*",moid="vm-50",rpname="Resources",source="DC0_H0_VM0",uuid="265104de-1472-547c-b873-6dc7883fb6cb",vmname="DC0_H0_VM0"`:                                 0.,
			`usage_average__dcname="DC0",esxhostname="DC0_H1",guest="other",instance="*",moid="vm-53",rpname="Resources",source="DC0_H1_VM0",uuid="4d341ac4-941f-5827-a28b-9550db7729f5",vmname="DC0_H1_VM0"`:                                 0.,
		},
		"vsphere_vm_net": {
			`received_average__clustername="DC0_C0",dcname="DC0",esxhostname="DC0_C0_H0",guest="other",interface="*",moid="vm-56",rpname="Resources",source="DC0_C0_RP0_VM0",uuid="cd0681bf-2f18-5c00-9b9b-8197c0095348",vmname="DC0_C0_RP0_VM0"`:    int64(0),
			`received_average__dcname="DC0",esxhostname="DC0_H0",guest="other",interface="*",moid="vm-50",rpname="Resources",source="DC0_H0_VM0",uuid="265104de-1472-547c-b873-6dc7883fb6cb",vmname="DC0_H0_VM0"`:                                    int64(0),
			`received_average__dcname="DC0",esxhostname="DC0_H1",guest="other",interface="*",moid="vm-53",rpname="Resources",source="DC0_H1_VM0",uuid="4d341ac4-941f-5827-a28b-9550db7729f5",vmname="DC0_H1_VM0"`:                                    int64(0),
			`transmitted_average__clustername="DC0_C0",dcname="DC0",esxhostname="DC0_C0_H0",guest="other",interface="*",moid="vm-56",rpname="Resources",source="DC0_C0_RP0_VM0",uuid="cd0681bf-2f18-5c00-9b9b-8197c0095348",vmname="DC0_C0_RP0_VM0"`: int64(0),
			`transmitted_average__dcname="DC0",esxhostname="DC0_H0",guest="other",interface="*",moid="vm-50",rpname="Resources",source="DC0_H0_VM0",uuid="265104de-1472-547c-b873-6dc7883fb6cb",vmname="DC0_H0_VM0"`:                                 int64(0),
			`transmitted_average__dcname="DC0",esxhostname="DC0_H1",guest="other",interface="*",moid="vm-53",rpname="Resources",source="DC0_H1_VM0",uuid="4d341ac4-941f-5827-a28b-9550db7729f5",vmname="DC0_H1_VM0"`:                                 int64(0),
			`usage_average__clustername="DC0_C0",dcname="DC0",esxhostname="DC0_C0_H0",guest="other",interface="*",moid="vm-56",rpname="Resources",source="DC0_C0_RP0_VM0",uuid="cd0681bf-2f18-5c00-9b9b-8197c0095348",vmname="DC0_C0_RP0_VM0"`:       int64(0),
			`usage_average__dcname="DC0",esxhostname="DC0_H0",guest="other",interface="*",moid="vm-50",rpname="Resources",source="DC0_H0_VM0",uuid="265104de-1472-547c-b873-6dc7883fb6cb",vmname="DC0_H0_VM0"`:                                       int64(0),
			`usage_average__dcname="DC0",esxhostname="DC0_H1",guest="other",interface="*",moid="vm-53",rpname="Resources",source="DC0_H1_VM0",uuid="4d341ac4-941f-5827-a28b-9550db7729f5",vmname="DC0_H1_VM0"`:                                       int64(0),
		},
	}

	// Field values are random, so we exclude them from diffing by ignoring int64 and floats.
	if diff := cmp.Diff(expectedFields, acc.fields, cmpopts.IgnoreTypes(int64(0), float64(0))); diff != "" {
		t.Errorf("Unexpected fields:\n%v", diff)
	}
}

type msmsa map[string]map[string]any

type shallowAcc struct {
	fields msmsa
	l      sync.Mutex
}

func (sa *shallowAcc) AddFields(measurement string, fields map[string]interface{}, tags map[string]string, _ ...time.Time) {
	sa.l.Lock()
	defer sa.l.Unlock()

	// The vcenter tag contains the port the simulator is listening to,
	// but as this port is randomly picked at each simulator startup,
	// we must prevent cmp.Diff from comparing it.
	delete(tags, "vcenter")

	if _, ok := sa.fields[measurement]; !ok {
		sa.fields[measurement] = make(map[string]any)
	}

	for field, v := range fields {
		sa.fields[measurement][field+"__"+types.LabelsToText(tags)] = v
	}
}

func (sa *shallowAcc) AddGauge(string, map[string]interface{}, map[string]string, ...time.Time) {
}

func (sa *shallowAcc) AddCounter(string, map[string]interface{}, map[string]string, ...time.Time) {
}

func (sa *shallowAcc) AddSummary(string, map[string]interface{}, map[string]string, ...time.Time) {
}

func (sa *shallowAcc) AddHistogram(string, map[string]interface{}, map[string]string, ...time.Time) {
}

func (sa *shallowAcc) AddFieldsWithAnnotations(measurement string, fields map[string]interface{}, tags map[string]string, _ types.MetricAnnotations, t ...time.Time) {
	sa.AddFields(measurement, fields, tags, t...)
}

func (sa *shallowAcc) AddMetric(telegraf.Metric) {}

func (sa *shallowAcc) SetPrecision(time.Duration) {}

func (sa *shallowAcc) AddError(error) {}

func (sa *shallowAcc) WithTracking(int) telegraf.TrackingAccumulator { return nil }

// logHandler stores errors and warns, and discards infos and debugs.
type logHandler struct {
	errors, warns []string
}

func (l *logHandler) Errorf(format string, args ...interface{}) {
	l.errors = append(l.errors, fmt.Sprintf(format, args...))
}

func (l *logHandler) Error(args ...interface{}) {
	l.errors = append(l.errors, fmt.Sprint(args...))
}

func (l *logHandler) Warnf(format string, args ...interface{}) {
	l.warns = append(l.warns, fmt.Sprintf(format, args...))
}

func (l *logHandler) Warn(args ...interface{}) {
	l.warns = append(l.warns, fmt.Sprint(args...))
}

func (l *logHandler) Infof(string, ...interface{}) {}

func (l *logHandler) Info(...interface{}) {}

func (l *logHandler) Debugf(string, ...interface{}) {}

func (l *logHandler) Debug(...interface{}) {}

func (l *logHandler) assertNoUnexpectedLogs(t *testing.T) {
	t.Helper()

	if l.errors != nil {
		t.Errorf("Some errors were logged:\n- %s", strings.Join(l.errors, "\n- "))
	}

	if l.warns != nil {
		t.Errorf("Some warnings were logged:\n- %s", strings.Join(l.warns, "\n- "))
	}
}
