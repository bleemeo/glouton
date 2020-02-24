// Copyright 2015-2019 Bleemeo
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

package common

import (
	bleemeoTypes "glouton/bleemeo/types"
	"glouton/types"
	"reflect"
	"testing"
	"time"
)

func TestMetricLookupFromList(t *testing.T) {
	input := []bleemeoTypes.Metric{
		{
			Labels: map[string]string{
				types.LabelName: "io_reads",
				"device":        "sda",
			},
			ID: "index-0",
		},
		{
			Labels: map[string]string{
				types.LabelName: "io_reads",
				"device":        "sda",
			},
			ID:            "index-1",
			DeactivatedAt: time.Now(),
		},
		{
			Labels: map[string]string{
				types.LabelName: "io_reads",
				"device":        "sdb",
			},
			ID:            "index-2",
			DeactivatedAt: time.Now(),
		},
		{
			Labels: map[string]string{
				types.LabelName: "io_reads",
				"device":        "sdb",
			},
			ID: "index-3",
		},
		{
			Labels: map[string]string{
				types.LabelName: "cpu_user",
				"device":        "",
			},
			ID: "index-4",
		},
		{
			Labels: map[string]string{
				types.LabelName: "cpu_system",
			},
			ID: "index-5",
		},
	}
	for i, v := range input {
		input[i].LabelsText = types.LabelsToText(v.Labels)
	}
	want := map[string]bleemeoTypes.Metric{
		input[0].LabelsText: input[0],
		input[3].LabelsText: input[3],
		input[4].LabelsText: input[4],
		input[5].LabelsText: input[5],
	}
	got := MetricLookupFromList(input)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("MetricLookupFromList(...) == %v, want %v", got, want)
	}
}
