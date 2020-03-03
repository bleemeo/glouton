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
	"glouton/bleemeo/types"
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
