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

package metricutils

import (
	"testing"

	"github.com/bleemeo/glouton/types"
)

func TestMetricOnlyHasItem(t *testing.T) {
	const agentID = "5f396bca-6dfd-4427-be12-c4107b076459"

	tests := []struct {
		name    string
		labels  map[string]string
		agentID string
		want    bool
	}{
		{
			name: "Bleemeo cpu",
			labels: map[string]string{
				types.LabelName: "cpu_used",
			},
			agentID: agentID,
			want:    true,
		},
		{
			name: "Bleemeo cpu instance",
			labels: map[string]string{
				types.LabelName:         "cpu_used",
				types.LabelInstanceUUID: agentID,
			},
			agentID: agentID,
			want:    true,
		},
		{
			name: "Bleemeo cpu another instance",
			labels: map[string]string{
				types.LabelName:         "cpu_used",
				types.LabelInstanceUUID: "16b5d368-4a6b-4e07-bb14-d1ac5478226d",
			},
			agentID: agentID,
			want:    false,
		},
		{
			name: "snmp metrics",
			labels: map[string]string{
				types.LabelName:         "snmp_device_status",
				types.LabelSNMPTarget:   "1.2.3.4",
				types.LabelInstanceUUID: "16b5d368-4a6b-4e07-bb14-d1ac5478226d",
			},
			agentID: agentID,
			want:    false,
		},
		{
			name: "prometheus scrapper",
			labels: map[string]string{
				types.LabelName:         "process_cpu_seconds_total",
				types.LabelScrapeJob:    "myjob",
				types.LabelInstanceUUID: agentID,
			},
			agentID: agentID,
			want:    false,
		},
		{
			name: "instance_uuid ignored",
			labels: map[string]string{
				types.LabelName:         "cpu_used",
				types.LabelInstanceUUID: agentID,
			},
			agentID: agentID,
			want:    true,
		},
		{
			name: "instance_uuid ignored 2",
			labels: map[string]string{
				types.LabelName:         "disk_used",
				types.LabelItem:         "/home",
				types.LabelInstanceUUID: agentID,
			},
			agentID: agentID,
			want:    true,
		},
		{
			name: "instance_uuid ignored 3",
			labels: map[string]string{
				types.LabelName:         "disk_used",
				types.LabelItem:         "/home",
				types.LabelInstanceUUID: agentID,
			},
			agentID: agentID,
			want:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MetricOnlyHasItem(tt.labels, tt.agentID); got != tt.want {
				t.Errorf("MetricOnlyHasItem() = %v, want %v", got, tt.want)
			}
		})
	}
}
