package vsphere

import (
	"glouton/inputs/internal"
	"testing"

	"github.com/google/go-cmp/cmp"
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
