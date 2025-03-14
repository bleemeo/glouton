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
	"crypto/tls"
	"testing"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/inputs/internal"
	"github.com/bleemeo/glouton/types"

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
			"vsphere_vm_cpu",
			"latency_average",
			"vsphere_vm_cpu",
			"latency_perc",
		},
		{
			"vsphere_host_mem",
			"swapout_average",
			"swap",
			"out",
		},
		{
			"vsphere_host_mem",
			"totalCapacity_average",
			"mem",
			"total",
		},
		{
			"vsphere_vm_disk",
			"read_average",
			"io",
			"read_bytes",
		},
		{
			"vsphere_datastore_datastore",
			"write_average",
			"io",
			"write_bytes",
		},
		{
			"vsphere_vm_net",
			"transmitted_average",
			"net",
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
		tags          map[string]string
	}{
		"vsphere_vm_mem": {
			{
				field:         "active_average",
				value:         430080,
				expectedValue: 21,
				tags:          map[string]string{types.LabelMetaVSphereMOID: "vm-77"},
				// This case is a special case
			},
			{
				field:         "swapped_average",
				value:         185.64,
				expectedValue: 185640,
			},
		},
		"vsphere_vm_virtualDisk": {
			{
				field:         "read_average",
				value:         35.432,
				expectedValue: 35432,
			},
			{
				field:         "write_average",
				value:         36.68,
				expectedValue: 36680,
			},
		},
		"vsphere_host_mem": {
			{
				field:         "totalCapacity_average",
				value:         4294.967296,
				expectedValue: 4294967296,
			},
			{
				field:         "swapout_average",
				value:         78,
				expectedValue: 78000,
			},
		},
		"vsphere_host_net": {
			{
				field:         "transmitted_average",
				value:         0.91,
				expectedValue: 7454.72,
			},
		},
	}

	dummyVSphere := newVSphere("host", config.VSphere{}, nil, facts.NewMockFacter(make(map[string]string)))
	// The VM mem_used_perc (active_average) metric relies on the cache to evaluate its value.
	dummyVSphere.devicePropsCache.vmCache.set("vm-77", vmLightProps{Config: &vmLightConfig{Hardware: vmLightConfigHardware{MemoryMB: 2048}}})

	for measurement, testCases := range cases {
		currentContext := internal.GatherContext{Measurement: measurement}
		fields := make(map[string]float64, len(testCases))
		expected := make(map[string]float64, len(testCases))

		for _, testCase := range testCases {
			fields[testCase.field] = testCase.value
			expected[testCase.field] = testCase.expectedValue

			if testCase.tags != nil {
				currentContext.Tags = testCase.tags
			}
		}

		resultFields := dummyVSphere.transformMetrics(currentContext, fields, nil)
		if diff := cmp.Diff(expected, resultFields); diff != "" {
			t.Errorf("Unexpected result of transformMetrics(%q, ...):\n%v", measurement, diff)
		}
	}
}

func setupVSphereTest(t *testing.T, hostsCount int) (vsphereRealtimeGatherer, vsphereHistorical30minGatherer *vSphereGatherer, deferFn func()) {
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
			SkipMonitorVMs:     false,
		},
	}

	realtimeGatherer, _, err := vSphere.makeRealtimeGatherer(t.Context())
	if err != nil {
		t.Fatal("Failed to create vSphere realtime gatherer:", err)
	}

	historical30minGatherer, _, err := vSphere.makeHistorical30minGatherer(t.Context())
	if err != nil {
		t.Fatal("Failed to create vSphere historical 30min gatherer:", err)
	}

	vsphereRealtimeGatherer = realtimeGatherer.(*vSphereGatherer)               //nolint:forcetypeassert
	vsphereHistorical30minGatherer = historical30minGatherer.(*vSphereGatherer) //nolint:forcetypeassert

	return vsphereRealtimeGatherer, vsphereHistorical30minGatherer, func() {
		model.Remove()
		server.Close()
	}
}

func TestVSphereInputNoHost(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	realtimeInput, histo30mInput, deferFn := setupVSphereTest(t, 0)
	defer deferFn()

	realtimeMfs, err := realtimeInput.Gather()
	if err != nil {
		t.Fatal("Failed to gather from vSphere realtime gatherer:", err)
	}

	histo30minMfs, err := histo30mInput.Gather()
	if err != nil {
		t.Fatal("Failed to gather from vSphere historical (30min) gatherer:", err)
	}

	mfs := append(realtimeMfs, histo30minMfs...) //nolint: gocritic

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

	realtimeInput, histo30mInput, deferFn := setupVSphereTest(t, 2)
	defer deferFn()

	realtimeMfs, err := realtimeInput.Gather()
	if err != nil {
		t.Fatal("Failed to gather from vSphere realtime gatherer:", err)
	}

	histo30minMfs, err := histo30mInput.Gather()
	if err != nil {
		t.Fatal("Failed to gather from vSphere historical (30min) gatherer:", err)
	}

	mfs := append(realtimeMfs, histo30minMfs...) //nolint: gocritic

	expectedMfs := []*dto.MetricFamily{}

	// Field values are random, so we exclude them from diffing by ignoring int64 and floats.
	if diff := cmp.Diff(expectedMfs, mfs, cmpopts.IgnoreTypes(int64(0), float64(0))); diff != "" {
		t.Errorf("Unexpected fields:\n%v", diff)
	}
}
