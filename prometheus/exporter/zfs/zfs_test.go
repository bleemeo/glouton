//nolint:dupl
package zfs

import (
	"glouton/types"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/prometheus/prometheus/model/labels"
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
						Value: 1,
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusWarning,
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
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := decodeZpool(tt.zpoolOutput)

			optMetricSort := cmpopts.SortSlices(func(x types.MetricPoint, y types.MetricPoint) bool {
				lblsX := labels.FromMap(x.Labels)
				lblsY := labels.FromMap(y.Labels)

				return labels.Compare(lblsX, lblsY) < 0
			})

			if diff := cmp.Diff(tt.want, got, optMetricSort, cmpopts.EquateEmpty(), cmpopts.EquateApprox(0.001, 0)); diff != "" {
				t.Errorf("decodeZpool mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
