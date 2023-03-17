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

package zfs

import (
	"glouton/prometheus/model"
	"glouton/types"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/prometheus/prometheus/model/labels"
)

//nolint:makezero
func Test_addZPoolStatus(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)

	metrics := []types.MetricPoint{
		{
			Labels: map[string]string{
				types.LabelName: "zfs_pool_allocated",
				"health":        "DEGRADED",
				"pool":          "with space",
			},
			Point: types.Point{Time: now, Value: 4756},
		},
		{
			Labels: map[string]string{
				types.LabelName: "zfs_pool_size",
				"health":        "DEGRADED",
				"pool":          "with space",
			},
			Point: types.Point{Time: now, Value: 42},
		},
		{
			Labels: map[string]string{
				types.LabelName: "zfs_pool_size",
				"health":        "FAULTED",
				"pool":          "tank",
			},
			Point: types.Point{Time: now, Value: 42},
		},
		{
			Labels: map[string]string{
				types.LabelName: "zfs_pool_size",
				"health":        "OFFLINE",
				"pool":          "offline",
			},
			Point: types.Point{Time: now, Value: 42},
		},
		{
			Labels: map[string]string{
				types.LabelName: "zfs_pool_size",
				"health":        "ONLINE",
				"pool":          "boot-pool",
			},
			Point: types.Point{Time: now, Value: 42},
		},
		{
			Labels: map[string]string{
				types.LabelName: "disk_used_perc",
				"item":          "boot-pool",
			},
			Point: types.Point{Time: now, Value: 42},
			Annotations: types.MetricAnnotations{
				BleemeoItem: "boot-pool",
			},
		},
	}
	want := make([]types.MetricPoint, len(metrics))
	copy(want, metrics)

	want = append(want, types.MetricPoint{
		Labels: map[string]string{
			types.LabelName: "zfs_pool_health_status",
			"item":          "boot-pool",
		},
		Point: types.Point{Time: now, Value: float64(types.StatusOk.NagiosCode())},
		Annotations: types.MetricAnnotations{
			Status: types.StatusDescription{
				CurrentStatus:     types.StatusOk,
				StatusDescription: "ZFS pool is ONLINE",
			},
			BleemeoItem: "boot-pool",
		},
	})
	want = append(want, types.MetricPoint{
		Labels: map[string]string{
			types.LabelName: "zfs_pool_health_status",
			"item":          "with space",
		},
		Point: types.Point{Time: now, Value: float64(types.StatusWarning.NagiosCode())},
		Annotations: types.MetricAnnotations{
			Status: types.StatusDescription{
				CurrentStatus:     types.StatusWarning,
				StatusDescription: "ZFS pool is DEGRADED",
			},
			BleemeoItem: "with space",
		},
	})
	want = append(want, types.MetricPoint{
		Labels: map[string]string{
			types.LabelName: "zfs_pool_health_status",
			"item":          "offline",
		},
		Point: types.Point{Time: now, Value: float64(types.StatusWarning.NagiosCode())},
		Annotations: types.MetricAnnotations{
			Status: types.StatusDescription{
				CurrentStatus:     types.StatusWarning,
				StatusDescription: "ZFS pool is OFFLINE",
			},
			BleemeoItem: "offline",
		},
	})
	want = append(want, types.MetricPoint{
		Labels: map[string]string{
			types.LabelName: "zfs_pool_health_status",
			"item":          "tank",
		},
		Point: types.Point{Time: now, Value: float64(types.StatusCritical.NagiosCode())},
		Annotations: types.MetricAnnotations{
			Status: types.StatusDescription{
				CurrentStatus:     types.StatusCritical,
				StatusDescription: "ZFS pool is FAULTED",
			},
			BleemeoItem: "tank",
		},
	})

	mfs := model.MetricPointsToFamilies(metrics)
	mfs = addZPoolStatus(mfs)

	got := model.FamiliesToMetricPoints(now, mfs, true)
	optMetricSort := cmpopts.SortSlices(func(x types.MetricPoint, y types.MetricPoint) bool {
		lblsX := labels.FromMap(x.Labels)
		lblsY := labels.FromMap(y.Labels)

		return labels.Compare(lblsX, lblsY) < 0
	})

	if diff := cmp.Diff(want, got, optMetricSort, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("addZPoolStatus mismatch (-want +got):\n%s", diff)
	}
}
