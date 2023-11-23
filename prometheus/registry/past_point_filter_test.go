package registry

import (
	"context"
	"testing"
	"time"

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

	ignoreUnexported := cmpopts.IgnoreUnexported([]any{io_prometheus_client.MetricFamily{}, io_prometheus_client.Metric{}, io_prometheus_client.LabelPair{}, io_prometheus_client.Untyped{}}...)

	tGatherer := testGatherer{firstSample}
	gatherer := WithPastPointFilter(&tGatherer, time.Hour)

	mfs, err := gatherer.GatherWithState(context.Background(), GatherState{})
	if err != nil {
		t.Fatal("Error while gathering:", err)
	}

	if diff := cmp.Diff(firstSample, mfs, ignoreUnexported); diff != "" {
		t.Fatalf("Nothing should have been filtered out (-want +got):\n%s", diff)
	}

	tGatherer.mfsToReturn = secondSample

	mfs, err = gatherer.GatherWithState(context.Background(), GatherState{})
	if err != nil {
		t.Fatal("Error while gathering:", err)
	}

	expectedSample := []*io_prometheus_client.MetricFamily{
		secondSample[0],
		{
			Name:   proto.String("disk_used_perc"),
			Help:   proto.String(""),
			Type:   ptr(io_prometheus_client.MetricType_UNTYPED),
			Metric: []*io_prometheus_client.Metric{}, // The sole metric of this family was dropped.
		},
	}

	if diff := cmp.Diff(expectedSample, mfs, ignoreUnexported); diff != "" {
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
					TimestampMs: proto.Int64(1700751600000), // 15:00:00 UTC
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
					},
					Untyped:     &io_prometheus_client.Untyped{Value: proto.Float64(56.7)},
					TimestampMs: proto.Int64(1700751600000), // 15:00:00 UTC
				},
			},
		},
	}

	ignoreUnexported := cmpopts.IgnoreUnexported([]any{io_prometheus_client.MetricFamily{}, io_prometheus_client.Metric{}, io_prometheus_client.LabelPair{}, io_prometheus_client.Untyped{}}...)
	allowUnexported := cmp.AllowUnexported(point{})

	t0 := time.UnixMilli(1700751600000) // 15:00:00 UTC

	tGatherer := testGatherer{sample}
	gatherer := WithPastPointFilter(&tGatherer, 5*time.Minute).(*pastPointFilter) //nolint: forcetypeassert
	gatherer.lastPurgeAt = t0

	gatherer.timeNow = func() time.Time {
		return t0 // 15:00:00 UTC
	}

	_, err := gatherer.GatherWithState(context.Background(), GatherState{})
	if err != nil {
		t.Fatal("Error while gathering:", err)
	}

	expectedCache := map[string]map[uint64]point{
		"cpu_used": {
			10203054987680334317: {timestampMs: 1700751600000, recordedAt: t0}, // 15:00:00 UTC,
		},
		"disk_used_perc": {
			10203054987680334317: {timestampMs: 1700751600000, recordedAt: t0}, // 15:00:00 UTC,
		},
	}
	if diff := cmp.Diff(expectedCache, gatherer.latestPointByLabelsByMetric, ignoreUnexported, allowUnexported); diff != "" {
		t.Fatalf("Nothing should have been purged (-want +got):\n%s", diff)
	}

	sample[1].Metric[0].TimestampMs = ptr[int64](1700751780000) // 15:03:00 UTC
	tGatherer.mfsToReturn = sample[:1]                          // No new points for the disk_used_perc metric
	gatherer.timeNow = func() time.Time {
		return t0.Add(4 * time.Minute) // 15:04:00 UTC
	}

	_, err = gatherer.GatherWithState(context.Background(), GatherState{})
	if err != nil {
		t.Fatal("Error while gathering:", err)
	}

	time.Sleep(10 * time.Millisecond)
	gatherer.l.Lock() // Prevent concurrent access by the purge goroutine

	expectedCache = map[string]map[uint64]point{
		"cpu_used": {
			10203054987680334317: {timestampMs: 1700751600000, recordedAt: t0.Add(4 * time.Minute)}, // 15:04:00 UTC,
		},
		"disk_used_perc": {
			10203054987680334317: {timestampMs: 1700751600000, recordedAt: t0}, // 15:00:00 UTC,
		},
	}
	if diff := cmp.Diff(expectedCache, gatherer.latestPointByLabelsByMetric, ignoreUnexported, allowUnexported); diff != "" {
		t.Fatalf("Still nothing should have been purged (-want +got):\n%s", diff)
	}

	gatherer.l.Unlock()

	tGatherer.mfsToReturn = []*io_prometheus_client.MetricFamily{} // No new points for both metrics
	gatherer.timeNow = func() time.Time {
		return t0.Add(8 * time.Minute) // 15:08:00 UTC
	}

	_, err = gatherer.GatherWithState(context.Background(), GatherState{})
	if err != nil {
		t.Fatal("Error while gathering:", err)
	}

	time.Sleep(10 * time.Millisecond)
	gatherer.l.Lock()

	expectedCache = map[string]map[uint64]point{
		"cpu_used": {
			10203054987680334317: {timestampMs: 1700751600000, recordedAt: t0.Add(4 * time.Minute)}, // 15:04:00 UTC,
		},
	}
	if diff := cmp.Diff(expectedCache, gatherer.latestPointByLabelsByMetric, ignoreUnexported, allowUnexported); diff != "" {
		t.Fatalf("Still nothing should have been purged (-want +got):\n%s", diff)
	}

	gatherer.l.Unlock()
}

func ptr[T any](e T) *T { return &e }
