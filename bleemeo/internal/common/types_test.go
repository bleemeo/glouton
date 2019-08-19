package common

import (
	"agentgo/bleemeo/types"
	"reflect"
	"testing"
	"time"
)

func TestMetricLookupFromList(t *testing.T) {
	input := []types.Metric{
		{Label: "io_reads", Labels: map[string]string{"item": "sda"}, ID: "index-0"},
		{Label: "io_reads", Labels: map[string]string{"item": "sda"}, ID: "index-1", DeactivatedAt: time.Now()},
		{Label: "io_reads", Labels: map[string]string{"item": "sdb"}, ID: "index-2", DeactivatedAt: time.Now()},
		{Label: "io_reads", Labels: map[string]string{"item": "sdb"}, ID: "index-3"},
		{Label: "cpu_user", Labels: map[string]string{}, ID: "index-4"},
		{Label: "cpu_system", ID: "index-5"},
	}
	want := map[MetricLabelItem]types.Metric{
		{Label: "io_reads", Item: "sda"}: input[0],
		{Label: "io_reads", Item: "sdb"}: input[3],
		{Label: "cpu_user", Item: ""}:    input[4],
		{Label: "cpu_system", Item: ""}:  input[5],
	}
	got := MetricLookupFromList(input)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("MetricLookupFromList(...) == %v, want %v", got, want)
	}
}
