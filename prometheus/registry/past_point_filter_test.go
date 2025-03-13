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

package registry

import (
	"context"
	"testing"
	"time"

	"github.com/bleemeo/glouton/types"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	io_prometheus_client "github.com/prometheus/client_model/go"
	"google.golang.org/protobuf/proto"
)

type testGatherer struct {
	mfsToReturn []*io_prometheus_client.MetricFamily
}

func (g *testGatherer) Gather() ([]*io_prometheus_client.MetricFamily, error) {
	return g.mfsToReturn, nil
}

func (g *testGatherer) GatherWithState(_ context.Context, _ GatherState) ([]*io_prometheus_client.MetricFamily, error) {
	return g.mfsToReturn, nil
}

func TestFilterPastPoints(t *testing.T) {
	firstSample := []*io_prometheus_client.MetricFamily{ //nolint: dupl
		{
			Name: proto.String("cpu_used"),
			Help: proto.String(""),
			Type: ptr(io_prometheus_client.MetricType_UNTYPED),
			Metric: []*io_prometheus_client.Metric{
				{
					Label: []*io_prometheus_client.LabelPair{
						{Name: proto.String("__meta_vsphere"), Value: proto.String("127.0.0.1:xxxxx")},
						{Name: proto.String("__meta_vsphere_moid"), Value: proto.String("10")},
						{Name: proto.String("dcname"), Value: proto.String("ha-datacenter")},
						{Name: proto.String("esxhostname"), Value: proto.String("esxi.test")},
						{Name: proto.String("vmname"), Value: proto.String("alp1")},
					},
					Untyped:     &io_prometheus_client.Untyped{Value: proto.Float64(5.22334)},
					TimestampMs: proto.Int64(1700749777777),
				},
			},
		},
		{
			Name: proto.String("disk_used_perc"),
			Help: proto.String(""),
			Type: ptr(io_prometheus_client.MetricType_UNTYPED),
			Metric: []*io_prometheus_client.Metric{
				{
					Label: []*io_prometheus_client.LabelPair{
						{Name: proto.String("__meta_vsphere"), Value: proto.String("127.0.0.1:xxxxx")},
						{Name: proto.String("__meta_vsphere_moid"), Value: proto.String("10")},
						{Name: proto.String("clustername"), Value: proto.String("esxi.test")},
						{Name: proto.String("dcname"), Value: proto.String("ha-datacenter")},
						{Name: proto.String("esxhostname"), Value: proto.String("esxi.test")},
						{Name: proto.String("item"), Value: proto.String("/")},
						{Name: proto.String("vmname"), Value: proto.String("alp1")},
					},
					Untyped:     &io_prometheus_client.Untyped{Value: proto.Float64(9.26)},
					TimestampMs: proto.Int64(1700749777777),
				},
			},
		},
	}
	secondSample := []*io_prometheus_client.MetricFamily{ //nolint: dupl
		{
			Name: proto.String("cpu_used"),
			Help: proto.String(""),
			Type: ptr(io_prometheus_client.MetricType_UNTYPED),
			Metric: []*io_prometheus_client.Metric{
				{
					Label: []*io_prometheus_client.LabelPair{
						{Name: proto.String("__meta_vsphere"), Value: proto.String("127.0.0.1:xxxxx")},
						{Name: proto.String("__meta_vsphere_moid"), Value: proto.String("10")},
						{Name: proto.String("dcname"), Value: proto.String("ha-datacenter")},
						{Name: proto.String("esxhostname"), Value: proto.String("esxi.test")},
						{Name: proto.String("vmname"), Value: proto.String("alp1")},
					},
					Untyped:     &io_prometheus_client.Untyped{Value: proto.Float64(6.90137124)},
					TimestampMs: proto.Int64(1700749977777), // after the last point
				},
			},
		},
		{
			Name: proto.String("disk_used_perc"),
			Help: proto.String(""),
			Type: ptr(io_prometheus_client.MetricType_UNTYPED),
			Metric: []*io_prometheus_client.Metric{
				{
					Label: []*io_prometheus_client.LabelPair{
						{Name: proto.String("__meta_vsphere"), Value: proto.String("127.0.0.1:xxxxx")},
						{Name: proto.String("__meta_vsphere_moid"), Value: proto.String("10")},
						{Name: proto.String("clustername"), Value: proto.String("esxi.test")},
						{Name: proto.String("dcname"), Value: proto.String("ha-datacenter")},
						{Name: proto.String("esxhostname"), Value: proto.String("esxi.test")},
						{Name: proto.String("item"), Value: proto.String("/")},
						{Name: proto.String("vmname"), Value: proto.String("alp1")},
					},
					Untyped:     &io_prometheus_client.Untyped{Value: proto.Float64(8.794201)},
					TimestampMs: proto.Int64(1700749577777), // before the last point
				},
			},
		},
	}

	tGatherer := testGatherer{firstSample}
	gatherer := WithPastPointFilter(&tGatherer, time.Hour)

	mfs, err := gatherer.GatherWithState(t.Context(), GatherState{T0: time.Now()})
	if err != nil {
		t.Fatal("Error while gathering:", err)
	}

	if diff := types.DiffMetricFamilies(firstSample, mfs, false, false); diff != "" {
		t.Fatalf("Nothing should have been filtered out (-want +got):\n%s", diff)
	}

	tGatherer.mfsToReturn = secondSample

	mfs, err = gatherer.GatherWithState(t.Context(), GatherState{T0: time.Now()})
	if err != nil {
		t.Fatal("Error while gathering:", err)
	}

	expectedSample := []*io_prometheus_client.MetricFamily{
		secondSample[0],
		// The whole disk_used_perc metric family was dropped, because its sole metric was removed.
	}

	if diff := types.DiffMetricFamilies(expectedSample, mfs, false, false); diff != "" {
		t.Fatalf("The second metric should have been filtered out (-want +got):\n%s", diff)
	}
}

func TestFilterPurge(t *testing.T) {
	sample := []*io_prometheus_client.MetricFamily{
		{
			Name: proto.String("cpu_used"),
			Help: proto.String(""),
			Type: ptr(io_prometheus_client.MetricType_UNTYPED),
			Metric: []*io_prometheus_client.Metric{
				{
					Label: []*io_prometheus_client.LabelPair{
						{Name: proto.String("__meta_vsphere"), Value: proto.String("127.0.0.1:xxxxx")},
						{Name: proto.String("__meta_vsphere_moid"), Value: proto.String("10")},
					},
					Untyped:     &io_prometheus_client.Untyped{Value: proto.Float64(1.2)},
					TimestampMs: proto.Int64(time.Date(2023, 11, 23, 15, 0, 0, 0, time.UTC).UnixMilli()),
				},
			},
		},
		{
			Name: proto.String("disk_used_perc"),
			Help: proto.String(""),
			Type: ptr(io_prometheus_client.MetricType_UNTYPED),
			Metric: []*io_prometheus_client.Metric{
				{
					Label: []*io_prometheus_client.LabelPair{
						{Name: proto.String("__meta_vsphere"), Value: proto.String("127.0.0.1:xxxxx")},
						{Name: proto.String("__meta_vsphere_moid"), Value: proto.String("5")},
					},
					Untyped:     &io_prometheus_client.Untyped{Value: proto.Float64(23.4)},
					TimestampMs: proto.Int64(time.Date(2023, 11, 23, 15, 0, 0, 0, time.UTC).UnixMilli()),
				},
				{
					Label: []*io_prometheus_client.LabelPair{
						{Name: proto.String("__meta_vsphere"), Value: proto.String("127.0.0.1:xxxxx")},
						{Name: proto.String("__meta_vsphere_moid"), Value: proto.String("10")},
					},
					Untyped:     &io_prometheus_client.Untyped{Value: proto.Float64(56.7)},
					TimestampMs: proto.Int64(time.Date(2023, 11, 23, 15, 0, 0, 0, time.UTC).UnixMilli()),
				},
			},
		},
	}

	ignoreUnexported := cmpopts.IgnoreUnexported([]any{io_prometheus_client.MetricFamily{}, io_prometheus_client.Metric{}, io_prometheus_client.LabelPair{}, io_prometheus_client.Untyped{}}...)
	allowUnexported := cmp.AllowUnexported(point{})

	t0 := time.Date(2023, 11, 23, 15, 0, 0, 0, time.UTC)

	tGatherer := testGatherer{sample}
	gatherer := WithPastPointFilter(&tGatherer, 5*time.Minute).(*pastPointFilter) //nolint: forcetypeassert
	gatherer.lastPurgeAt = t0

	gatherer.timeNow = func() time.Time {
		return t0 // 15:00:00 UTC
	}

	_, err := gatherer.GatherWithState(t.Context(), GatherState{T0: gatherer.timeNow()})
	if err != nil {
		t.Fatal("Error while gathering:", err)
	}

	expectedCache := map[string]map[uint64]point{
		"cpu_used": {
			10203054987680334317: {timestampMs: 1700751600000, recordedAt: t0}, // 15:00:00 UTC,
		},
		"disk_used_perc": {
			3900352098746294457:  {timestampMs: 1700751600000, recordedAt: t0}, // 15:00:00 UTC,
			10203054987680334317: {timestampMs: 1700751600000, recordedAt: t0}, // 15:00:00 UTC,
		},
	}
	if diff := cmp.Diff(expectedCache, gatherer.latestPointByLabelsByMetric, ignoreUnexported, allowUnexported); diff != "" {
		t.Fatalf("Nothing should have been purged (-want +got):\n%s", diff)
	}

	sample[1].Metric[0].TimestampMs = ptr[int64](t0.Add(3 * time.Minute).UnixMilli())
	tGatherer.mfsToReturn = sample[:1] // No new points for disk_used_perc metrics
	gatherer.timeNow = func() time.Time {
		return t0.Add(4 * time.Minute) // 15:04:00 UTC
	}

	_, err = gatherer.GatherWithState(t.Context(), GatherState{T0: gatherer.timeNow()})
	if err != nil {
		t.Fatal("Error while gathering:", err)
	}

	expectedCache = map[string]map[uint64]point{
		"cpu_used": {
			10203054987680334317: {timestampMs: t0.UnixMilli(), recordedAt: t0.Add(4 * time.Minute)},
		},
		"disk_used_perc": {
			3900352098746294457:  {timestampMs: t0.UnixMilli(), recordedAt: t0},
			10203054987680334317: {timestampMs: t0.UnixMilli(), recordedAt: t0},
		},
	}
	if diff := cmp.Diff(expectedCache, gatherer.latestPointByLabelsByMetric, ignoreUnexported, allowUnexported); diff != "" {
		t.Fatalf("Still nothing should have been purged (-want +got):\n%s", diff)
	}

	tGatherer.mfsToReturn = []*io_prometheus_client.MetricFamily{
		{
			Name: proto.String("disk_used_perc"),
			Help: proto.String(""),
			Type: ptr(io_prometheus_client.MetricType_UNTYPED),
			Metric: []*io_prometheus_client.Metric{
				{
					Label: []*io_prometheus_client.LabelPair{
						{Name: proto.String("__meta_vsphere"), Value: proto.String("127.0.0.1:xxxxx")},
						{Name: proto.String("__meta_vsphere_moid"), Value: proto.String("5")},
					},
					Untyped:     &io_prometheus_client.Untyped{Value: proto.Float64(25.7)},
					TimestampMs: proto.Int64(t0.Add(8 * time.Minute).UnixMilli()),
				},
			},
		},
	}
	gatherer.timeNow = func() time.Time {
		return t0.Add(8 * time.Minute) // 15:08:00 UTC
	}

	_, err = gatherer.GatherWithState(t.Context(), GatherState{T0: gatherer.timeNow()})
	if err != nil {
		t.Fatal("Error while gathering:", err)
	}

	expectedCache = map[string]map[uint64]point{
		"cpu_used": {
			10203054987680334317: {timestampMs: t0.UnixMilli(), recordedAt: t0.Add(4 * time.Minute)},
		},
		"disk_used_perc": {
			3900352098746294457: {timestampMs: t0.Add(8 * time.Minute).UnixMilli(), recordedAt: t0.Add(8 * time.Minute)},
		},
	}
	if diff := cmp.Diff(expectedCache, gatherer.latestPointByLabelsByMetric, ignoreUnexported, allowUnexported); diff != "" {
		t.Fatalf("Still nothing should have been purged (-want +got):\n%s", diff)
	}
}

func ptr[T any](e T) *T { return &e }
