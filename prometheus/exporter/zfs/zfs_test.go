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

//nolint:dupl
package zfs

import (
	"github.com/bleemeo/glouton/types"
	"testing"
)

func Test_decodeZpool(t *testing.T) { //nolint:maintidx
	tests := []struct {
		name        string
		zpoolOutput string
		want        []types.MetricPoint
	}{
		{
			name:        "Ubuntu-onedisk",
			zpoolOutput: `mypool	ONLINE	4831838208	1259331584	3572506624`,
			want: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: "disk_used",
						types.LabelItem: "mypool",
					},
					Point: types.Point{
						Value: 1259331584,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "disk_total",
						types.LabelItem: "mypool",
					},
					Point: types.Point{
						Value: 4831838208,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "disk_free",
						types.LabelItem: "mypool",
					},
					Point: types.Point{
						Value: 3572506624,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "disk_used_perc",
						types.LabelItem: "mypool",
					},
					Point: types.Point{
						Value: 26.06,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "zfs_pool_health_status",
						types.LabelItem: "mypool",
					},
					Point: types.Point{
						Value: 0,
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "ZFS pool is ONLINE",
						},
					},
				},
			},
		},
		{
			name: "Ubuntu-twopool",
			zpoolOutput: `mypool	ONLINE	4831838208	102912	4831735296
pool_001	ONLINE	9663676416	107520	9663568896
`,
			want: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: "disk_used",
						types.LabelItem: "mypool",
					},
					Point: types.Point{
						Value: 102912,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "disk_total",
						types.LabelItem: "mypool",
					},
					Point: types.Point{
						Value: 4831838208,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "disk_free",
						types.LabelItem: "mypool",
					},
					Point: types.Point{
						Value: 4831735296,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "disk_used_perc",
						types.LabelItem: "mypool",
					},
					Point: types.Point{
						Value: 0.00213,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "zfs_pool_health_status",
						types.LabelItem: "mypool",
					},
					Point: types.Point{
						Value: 0,
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "ZFS pool is ONLINE",
						},
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "disk_used",
						types.LabelItem: "pool_001",
					},
					Point: types.Point{
						Value: 107520,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "disk_total",
						types.LabelItem: "pool_001",
					},
					Point: types.Point{
						Value: 9663676416,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "disk_free",
						types.LabelItem: "pool_001",
					},
					Point: types.Point{
						Value: 9663568896,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "disk_used_perc",
						types.LabelItem: "pool_001",
					},
					Point: types.Point{
						Value: 0.001112,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "zfs_pool_health_status",
						types.LabelItem: "pool_001",
					},
					Point: types.Point{
						Value: 0,
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "ZFS pool is ONLINE",
						},
					},
				},
			},
		},
		{
			// The single underlying iSCSI disk is removed
			name:        "Ubuntu-suspended",
			zpoolOutput: `mypool	SUSPENDED	4831838208	102912	4831735296`,
			want: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: "disk_used",
						types.LabelItem: "mypool",
					},
					Point: types.Point{
						Value: 102912,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "disk_total",
						types.LabelItem: "mypool",
					},
					Point: types.Point{
						Value: 4831838208,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "disk_free",
						types.LabelItem: "mypool",
					},
					Point: types.Point{
						Value: 4831735296,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "disk_used_perc",
						types.LabelItem: "mypool",
					},
					Point: types.Point{
						Value: 0.00213,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "zfs_pool_health_status",
						types.LabelItem: "mypool",
					},
					Point: types.Point{
						Value: 2,
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusCritical,
							StatusDescription: "ZFS pool is SUSPENDED",
						},
					},
				},
			},
		},
		{
			// One of the two underlying iSCSI disk removed
			name:        "Ubuntu-degraded",
			zpoolOutput: `pool_001	DEGRADED	4831838208	52667904	4779170304`,
			want: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: "disk_used",
						types.LabelItem: "pool_001",
					},
					Point: types.Point{
						Value: 52667904,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "disk_total",
						types.LabelItem: "pool_001",
					},
					Point: types.Point{
						Value: 4831838208,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "disk_free",
						types.LabelItem: "pool_001",
					},
					Point: types.Point{
						Value: 4779170304,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "disk_used_perc",
						types.LabelItem: "pool_001",
					},
					Point: types.Point{
						Value: 1.09,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "zfs_pool_health_status",
						types.LabelItem: "pool_001",
					},
					Point: types.Point{
						Value: 2,
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusCritical,
							StatusDescription: "ZFS pool is DEGRADED",
						},
					},
				},
			},
		},
		{
			name:        "unavailable",
			zpoolOutput: `pool_001	UNAVAIL	-	-	-`,
			want: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: "zfs_pool_health_status",
						types.LabelItem: "pool_001",
					},
					Point: types.Point{
						Value: 2,
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusCritical,
							StatusDescription: "ZFS pool is UNAVAIL",
						},
					},
				},
			},
		},
		{
			name:        "unavailable",
			zpoolOutput: `pool_001	FAULTED	-	-	-`,
			want: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: "zfs_pool_health_status",
						types.LabelItem: "pool_001",
					},
					Point: types.Point{
						Value: 2,
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusCritical,
							StatusDescription: "ZFS pool is FAULTED",
						},
					},
				},
			},
		},
		{
			name:        "unavailable",
			zpoolOutput: `pool_001	FAULTED	4831838208	52667904	4779170304`,
			want: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: "disk_used",
						types.LabelItem: "pool_001",
					},
					Point: types.Point{
						Value: 52667904,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "disk_total",
						types.LabelItem: "pool_001",
					},
					Point: types.Point{
						Value: 4831838208,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "disk_free",
						types.LabelItem: "pool_001",
					},
					Point: types.Point{
						Value: 4779170304,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "disk_used_perc",
						types.LabelItem: "pool_001",
					},
					Point: types.Point{
						Value: 1.09,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "zfs_pool_health_status",
						types.LabelItem: "pool_001",
					},
					Point: types.Point{
						Value: 2,
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusCritical,
							StatusDescription: "ZFS pool is FAULTED",
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := decodeZpool(tt.zpoolOutput)

			if diff := types.DiffMetricPoints(tt.want, got, true); diff != "" {
				t.Errorf("decodeZpool mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
