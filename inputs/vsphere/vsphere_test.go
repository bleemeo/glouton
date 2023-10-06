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
	"crypto/tls"
	"glouton/config"
	"glouton/inputs/internal"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	dto "github.com/prometheus/client_model/go"
	"github.com/vmware/govmomi/simulator"
)

func TestRenameMetrics(t *testing.T) {
	cases := []struct {
		measurement         string
		metricName          string
		expectedMeasurement string
		expectedName        string
	}{
		{
			"vsphere_host_mem",
			"swapout_average",
			"vsphere_host_swap",
			"out",
		},
		{
			"vsphere_host_mem",
			"totalCapacity_average",
			"vsphere_host_mem",
			"total",
		},
		{
			"vsphere_vm_disk",
			"read_average",
			"vsphere_vm_io",
			"read_bytes",
		},
		{
			"vsphere_vm_net",
			"transmitted_average",
			"vsphere_vm_net",
			"bits_sent",
		},
	}

	for _, testCase := range cases {
		currentContext := internal.GatherContext{Measurement: testCase.measurement}

		newMeasurement, newMetricName := renameMetrics(currentContext, testCase.metricName)
		if newMeasurement != testCase.expectedMeasurement {
			t.Errorf("Unexpected measurement result of renameMetrics(%q, %q): want %q got %q.", testCase.measurement, testCase.metricName, testCase.expectedMeasurement, newMeasurement)
		}

		if newMetricName != testCase.expectedName {
			t.Errorf("Unexpected result of renameMetrics(%q, %q): want %q, got %q", testCase.measurement, testCase.metricName, testCase.expectedName, newMetricName)
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
				"swapout_average",
				78,
				78000,
			},
		},
		"vsphere_host_net": {
			{
				"transmitted_average",
				0.91,
				7280,
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

func setupVSphereTest(t *testing.T, hostsCount int) (vsphereGatherer *vSphereGatherer, deferFn func()) {
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

	vSphere := &vSphere{
		opts: config.VSphere{
			URL:                server.URL.String(),
			Username:           "user",
			Password:           "pass",
			InsecureSkipVerify: true,
			MonitorVMs:         true,
		},
	}

	gatherer, _, err := vSphere.makeGatherer()
	if err != nil {
		t.Fatal("Failed to create vSphere gatherer:", err)
	}

	vsphereGatherer = gatherer.(*vSphereGatherer) //nolint:forcetypeassert

	return vsphereGatherer, func() {
		model.Remove()
		server.Close()
	}
}

func TestVSphereInputNoHost(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	input, deferFn := setupVSphereTest(t, 0)
	defer deferFn()

	mfs, err := input.Gather()
	if err != nil {
		t.Fatal("Failed to gather from vSphere gatherer:", err)
	}

	expectedMfs := []*dto.MetricFamily{}

	// Field values are random, so we exclude them from diffing by ignoring int64 and floats.
	if diff := cmp.Diff(expectedMfs, mfs, cmpopts.IgnoreTypes(int64(0), float64(0))); diff != "" {
		t.Errorf("Unexpected fields:\n%v", diff)
	}
}

func TestVSphereInputMultipleHosts(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	input, deferFn := setupVSphereTest(t, 2)
	defer deferFn()

	mfs, err := input.Gather()
	if err != nil {
		t.Fatal("Failed to gather from vSphere gatherer:", err)
	}

	expectedMfs := []*dto.MetricFamily{}

	// Field values are random, so we exclude them from diffing by ignoring int64 and floats.
	if diff := cmp.Diff(expectedMfs, mfs, cmpopts.IgnoreTypes(int64(0), float64(0))); diff != "" {
		t.Errorf("Unexpected fields:\n%v", diff)
	}
}
