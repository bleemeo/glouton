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

package common

import (
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/types"
	"reflect"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

func TestMetricLookupFromList(t *testing.T) {
	input := []bleemeoTypes.Metric{
		{
			Labels: map[string]string{
				types.LabelName: "io_reads",
				"item":          "sda",
			},
			ID: "index-0",
		},
		{
			Labels: map[string]string{
				types.LabelName: "io_reads",
				"item":          "sda",
			},
			ID:            "index-1",
			DeactivatedAt: time.Now(),
		},
		{
			Labels: map[string]string{
				types.LabelName: "io_reads",
				"item":          "sdb",
			},
			ID:            "index-2",
			DeactivatedAt: time.Now(),
		},
		{
			Labels: map[string]string{
				types.LabelName: "io_reads",
				"item":          "sdb",
			},
			ID: "index-3",
		},
		{
			Labels: map[string]string{
				types.LabelName: "cpu_user",
				"item":          "",
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

func TestServiceLookupFromList(t *testing.T) {
	input := []bleemeoTypes.Service{
		{
			ID:           "id-1",
			Label:        "srv",
			Instance:     "S1",
			Active:       true,
			CreationDate: "2023-08-28T13:21:15.539941Z",
		},
		{
			ID:           "id-2",
			Label:        "srv",
			Instance:     "S2",
			Active:       true,
			CreationDate: "2023-08-28T14:12:45.647132Z",
		},
		{
			ID:           "id-3",
			Label:        "srv",
			Instance:     "S2",
			Active:       false,
			CreationDate: "2023-08-28T13:58:02.332047Z",
		},
		{
			ID:           "id-4",
			Label:        "other-srv",
			Instance:     "S",
			Active:       false,
			CreationDate: "2023-08-27T15:21:27.104098Z",
		},
		{
			ID:           "id-5",
			Label:        "other-srv",
			Instance:     "S",
			Active:       true,
			CreationDate: "2023-08-28T17:25:36.745169Z",
		},
		{
			ID:           "id-6",
			Label:        "other-srv",
			Instance:     "S",
			Active:       true,
			CreationDate: "2023-08-28T09:15:28.134825Z",
		},
		{
			ID:           "id-7",
			Label:        "service",
			Instance:     "S",
			Active:       false,
			CreationDate: "2023-08-29T13:21:15.539941Z",
		},
		{
			ID:           "id-8",
			Label:        "create_time_second",
			Instance:     "",
			Active:       false,
			CreationDate: "2023-08-28T09:15:28Z",
		},
		{
			ID:           "id-9",
			Label:        "create_time_second",
			Instance:     "",
			Active:       false,
			CreationDate: "2023-08-29T13:21:15Z",
		},
	}

	want := map[ServiceNameInstance]bleemeoTypes.Service{
		{"srv", "S1"}:              {ID: "id-1", Label: "srv", Instance: "S1", Active: true},
		{"srv", "S2"}:              {ID: "id-2", Label: "srv", Instance: "S2", Active: true},
		{"other-srv", "S"}:         {ID: "id-5", Label: "other-srv", Instance: "S", Active: true},
		{"service", "S"}:           {ID: "id-7", Label: "service", Instance: "S", Active: false},
		{"create_time_second", ""}: {ID: "id-9", Label: "create_time_second", Instance: "", Active: false},
	}
	got := ServiceLookupFromList(input)

	if diff := cmp.Diff(want, got, cmpopts.IgnoreFields(bleemeoTypes.Service{}, "CreationDate")); diff != "" {
		t.Fatalf("Unexpected output from ServiceLookupFromList():\n%v", diff)
	}
}
