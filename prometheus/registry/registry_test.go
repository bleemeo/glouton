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

// Package registry package implement a dynamic collection of metrics sources
//
// It support both pushed metrics (using AddMetricPointFunction) and pulled
// metrics thought Collector or Gatherer
//nolint:scopelint
package registry

import (
	"context"
	"glouton/types"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/gogo/protobuf/proto"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/prometheus/pkg/labels"
	"github.com/prometheus/prometheus/pkg/relabel"
)

const testAgentID = "fcdc81a8-5bce-4305-8108-8e1e75439329"

type fakeGatherer struct {
	l         sync.Mutex
	name      string
	callCount int
	response  []*dto.MetricFamily
}

type fakeFilter struct{}

func (f *fakeFilter) FilterPoints(points []types.MetricPoint) []types.MetricPoint {
	return points
}

func (f *fakeFilter) FilterFamilies(families []*dto.MetricFamily) []*dto.MetricFamily {
	return families
}

func (g *fakeGatherer) fillResponse() {
	helpStr := "fake metric"
	value := 1.0

	g.response = append(g.response, &dto.MetricFamily{
		Help: &helpStr,
		Name: &g.name,
		Type: dto.MetricType_GAUGE.Enum(),
		Metric: []*dto.Metric{
			{
				Label: []*dto.LabelPair{},
				Gauge: &dto.Gauge{Value: &value},
			},
		},
	})
}

func (g *fakeGatherer) Gather() ([]*dto.MetricFamily, error) {
	g.l.Lock()
	g.callCount++
	g.l.Unlock()

	result := make([]*dto.MetricFamily, len(g.response))

	for i, mf := range g.response {
		b, err := proto.Marshal(mf)
		if err != nil {
			panic(err)
		}

		var tmp dto.MetricFamily

		err = proto.Unmarshal(b, &tmp)
		if err != nil {
			panic(err)
		}

		result[i] = &tmp
	}

	return result, nil
}

//nolint:cyclop
func TestRegistry_Register(t *testing.T) {
	reg := &Registry{}

	var (
		id1 int
		id2 int
		err error
	)

	gather1 := &fakeGatherer{
		name: "gather1",
	}
	gather1.fillResponse()

	gather2 := &fakeGatherer{
		name: "gather2",
	}
	gather2.fillResponse()

	if id1, err = reg.RegisterGatherer(RegistrationOption{}, gather1, false); err != nil {
		t.Errorf("reg.RegisterGatherer(gather1) failed: %v", err)
	}

	_, _ = reg.Gather()

	if gather1.callCount != 1 {
		t.Errorf("gather1.callCount = %v, want 1", gather1.callCount)
	}

	if !reg.Unregister(id1) {
		t.Errorf("reg.Unregister(%d) failed", id1)
	}

	_, _ = reg.Gather()

	if gather1.callCount != 1 {
		t.Errorf("gather1.callCount = %v, want 1", gather1.callCount)
	}

	if id1, err = reg.RegisterGatherer(RegistrationOption{ExtraLabels: map[string]string{"name": "value"}}, gather1, false); err != nil {
		t.Errorf("re-reg.RegisterGatherer(gather1) failed: %v", err)
	}

	if id2, err = reg.RegisterGatherer(RegistrationOption{}, gather2, false); err != nil {
		t.Errorf("re-reg.RegisterGatherer(gather2) failed: %v", err)
	}

	_, _ = reg.Gather()

	if gather1.callCount != 2 {
		t.Errorf("gather1.callCount = %v, want 2", gather1.callCount)
	}

	if gather2.callCount != 1 {
		t.Errorf("gather2.callCount = %v, want 1", gather2.callCount)
	}

	if !reg.Unregister(id1) {
		t.Errorf("reg.Unregister(%d) failed", id1)
	}

	if !reg.Unregister(id2) {
		t.Errorf("reg.Unregister(%d) failed", id2)
	}

	_, _ = reg.Gather()

	if gather1.callCount != 2 {
		t.Errorf("gather1.callCount = %v, want 2", gather1.callCount)
	}

	if gather2.callCount != 1 {
		t.Errorf("gather2.callCount = %v, want 1", gather2.callCount)
	}

	stopCallCount := 0

	if id1, err = reg.RegisterGatherer(RegistrationOption{StopCallback: func() { stopCallCount++ }, ExtraLabels: map[string]string{"dummy": "value", "empty-value-to-dropped": ""}}, gather1, false); err != nil {
		t.Errorf("reg.RegisterGatherer(gather1) failed: %v", err)
	}

	if _, err = reg.RegisterGatherer(RegistrationOption{}, gather2, false); err != nil {
		t.Errorf("re-reg.RegisterGatherer(gather2) failed: %v", err)
	}

	reg.UpdateRelabelHook(context.Background(), func(_ context.Context, labels map[string]string) (newLabel map[string]string, retryLater bool) {
		labels[types.LabelMetaBleemeoUUID] = testAgentID

		return labels, false
	})

	result, err := reg.Gather()
	if err != nil {
		t.Error(err)
	}

	helpText := "fake metric"
	dummyName := "dummy"
	dummyValue := "value"
	instanceIDName := types.LabelInstanceUUID
	instanceIDValue := testAgentID
	value := 1.0
	want := []*dto.MetricFamily{
		{
			Name: &gather1.name,
			Help: &helpText,
			Type: dto.MetricType_GAUGE.Enum(),
			Metric: []*dto.Metric{
				{
					Label: []*dto.LabelPair{
						{Name: &dummyName, Value: &dummyValue},
						{Name: &instanceIDName, Value: &instanceIDValue},
					},
					Gauge: &dto.Gauge{
						Value: &value,
					},
				},
			},
		},
		{
			Name: &gather2.name,
			Help: &helpText,
			Type: dto.MetricType_GAUGE.Enum(),
			Metric: []*dto.Metric{
				{
					Label: []*dto.LabelPair{
						{Name: &instanceIDName, Value: &instanceIDValue},
					},
					Gauge: &dto.Gauge{
						Value: &value,
					},
				},
			},
		},
	}

	if !reflect.DeepEqual(result, want) {
		t.Errorf("reg.Gather() = %v, want %v", result, want)
	}

	reg.Unregister(id1)

	if stopCallCount != 1 {
		t.Errorf("stopCallCount = %v, want 1", stopCallCount)
	}
}

func TestRegistry_pushPoint(t *testing.T) {
	reg := &Registry{
		option: Option{
			Filter: &fakeFilter{},
		},
	}

	t0 := time.Date(2020, 3, 2, 10, 30, 0, 0, time.UTC)
	t0MS := t0.UnixNano() / 1e6

	pusher := reg.WithTTL(24 * time.Hour)
	pusher.PushPoints(
		context.Background(),
		[]types.MetricPoint{
			{
				Point: types.Point{Value: 1.0, Time: t0},
				Labels: map[string]string{
					"__name__": "point1",
					"dummy":    "value",
				},
			},
			{
				Point: types.Point{Value: 2.0, Time: t0},
				Labels: map[string]string{
					"__name__": "unfixable-name#~",
					"item":     "something",
				},
			},
			{
				Point: types.Point{Value: 3.0, Time: t0},
				Labels: map[string]string{
					"__name__": "fixable-name.0",
					"extra":    "label",
				},
			},
		},
	)

	got, err := reg.Gather()
	if err != nil {
		t.Error(err)
	}

	metricName1 := "point1"
	metricName2 := "fixable_name_0"
	helpText := ""
	dummyName := "dummy"
	dummyValue := "value"
	extraName := "extra"
	extraValue := "label"
	instanceIDName := types.LabelInstanceUUID
	instanceIDValue := testAgentID
	value1 := 1.0
	value2 := 3.0
	want := []*dto.MetricFamily{
		{
			Name: &metricName2,
			Help: &helpText,
			Type: dto.MetricType_UNTYPED.Enum(),
			Metric: []*dto.Metric{
				{
					Label: []*dto.LabelPair{
						{Name: &extraName, Value: &extraValue},
					},
					Untyped: &dto.Untyped{
						Value: &value2,
					},
					TimestampMs: &t0MS,
				},
			},
		},
		{
			Name: &metricName1,
			Help: &helpText,
			Type: dto.MetricType_UNTYPED.Enum(),
			Metric: []*dto.Metric{
				{
					Label: []*dto.LabelPair{
						{Name: &dummyName, Value: &dummyValue},
					},
					Untyped: &dto.Untyped{
						Value: &value1,
					},
					TimestampMs: &t0MS,
				},
			},
		},
	}

	sort.Slice(got, func(i, j int) bool {
		return *got[i].Name < *got[j].Name
	})

	if diff := cmp.Diff(got, want); diff != "" {
		t.Error(diff)
	}

	reg.UpdateRelabelHook(context.Background(), func(_ context.Context, labels map[string]string) (newLabel map[string]string, retryLater bool) {
		labels[types.LabelMetaBleemeoUUID] = testAgentID

		return labels, false
	})

	got, err = reg.Gather()
	if err != nil {
		t.Error(err)
	}

	if len(got) > 0 {
		t.Errorf("reg.Gather() len=%v, want 0", len(got))
	}

	pusher.PushPoints(
		context.Background(),
		[]types.MetricPoint{
			{
				Point: types.Point{Value: 1.0, Time: t0},
				Labels: map[string]string{
					"__name__": "point1",
					"dummy":    "value",
				},
			},
		},
	)

	got, err = reg.Gather()
	if err != nil {
		t.Error(err)
	}

	want = []*dto.MetricFamily{
		{
			Name: &metricName1,
			Help: &helpText,
			Type: dto.MetricType_UNTYPED.Enum(),
			Metric: []*dto.Metric{
				{
					Label: []*dto.LabelPair{
						{Name: &dummyName, Value: &dummyValue},
						{Name: &instanceIDName, Value: &instanceIDValue},
					},
					Untyped: &dto.Untyped{
						Value: &value1,
					},
					TimestampMs: &t0MS,
				},
			},
		},
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("reg.Gather() = %v, want %v", got, want)
	}
}

func TestRegistry_applyRelabel(t *testing.T) {
	type fields struct {
		relabelConfigs []*relabel.Config
	}

	type args struct {
		input map[string]string
	}

	tests := []struct {
		name            string
		fields          fields
		args            args
		want            labels.Labels
		wantAnnotations types.MetricAnnotations
	}{
		{
			name:   "node_exporter",
			fields: fields{relabelConfigs: getDefaultRelabelConfig()},
			args: args{map[string]string{
				types.LabelMetaGloutonFQDN: "hostname",
				types.LabelMetaGloutonPort: "8015",
				types.LabelMetaPort:        "8015",
			}},
			want: labels.FromMap(map[string]string{
				types.LabelInstance: "hostname:8015",
			}),
			wantAnnotations: types.MetricAnnotations{},
		},
		{
			name:   "node_exporter, with LabelMetaSendScraperUUID",
			fields: fields{relabelConfigs: getDefaultRelabelConfig()},
			args: args{map[string]string{
				types.LabelMetaGloutonFQDN:     "hostname",
				types.LabelMetaGloutonPort:     "8015",
				types.LabelMetaPort:            "8015",
				types.LabelMetaSendScraperUUID: "yes",
			}},
			want: labels.FromMap(map[string]string{
				types.LabelInstance: "hostname:8015",
			}),
			wantAnnotations: types.MetricAnnotations{},
		},
		{
			name:   "mysql container",
			fields: fields{relabelConfigs: getDefaultRelabelConfig()},
			args: args{map[string]string{
				types.LabelMetaServiceName:   "mysql",
				types.LabelMetaContainerName: "mysql_1",
				types.LabelMetaContainerID:   "1234",
				types.LabelMetaGloutonFQDN:   "hostname",
				types.LabelMetaGloutonPort:   "8015",
				types.LabelMetaServicePort:   "3306",
				types.LabelMetaPort:          "3306",
			}},
			want: labels.FromMap(map[string]string{
				types.LabelContainerName: "mysql_1",
				types.LabelInstance:      "hostname-mysql_1:3306",
			}),
			wantAnnotations: types.MetricAnnotations{
				ServiceName: "mysql",
				ContainerID: "1234",
			},
		},
		{
			name:   "blackbox_probe_icmp",
			fields: fields{relabelConfigs: getDefaultRelabelConfig()},
			args: args{map[string]string{
				types.LabelMetaProbeTarget:            "icmp://8.8.8.8",
				types.LabelMetaProbeScraperName:       "test",
				types.LabelMetaBleemeoTargetAgentUUID: "c571f9cf-6f07-492a-9e86-b8d5f5027557",
				types.LabelMetaBleemeoUUID:            "a39e5a8e-34cf-4b15-87bd-4b9cdaa59c42",
				// necessary for some labels to be applied
				types.LabelMetaProbeServiceUUID: "dcb8e864-0a1f-4a67-b470-327ceb461b4e",
				types.LabelMetaSendScraperUUID:  "yes",
			}},
			want: labels.FromMap(map[string]string{
				types.LabelInstance:     "icmp://8.8.8.8",
				types.LabelScraper:      "test",
				types.LabelInstanceUUID: "c571f9cf-6f07-492a-9e86-b8d5f5027557",
				types.LabelScraperUUID:  "a39e5a8e-34cf-4b15-87bd-4b9cdaa59c42",
			}),
			wantAnnotations: types.MetricAnnotations{
				BleemeoAgentID: "c571f9cf-6f07-492a-9e86-b8d5f5027557",
			},
		},
		// when LabelMetaProbeScraperName is not provided, the 'scraper' label is the traditional 'instance' label
		{
			name:   "blackbox_probe_icmp_no_scraper_name",
			fields: fields{relabelConfigs: getDefaultRelabelConfig()},
			args: args{map[string]string{
				types.LabelMetaProbeTarget:            "icmp://8.8.8.8",
				types.LabelInstance:                   "super-instance:1111",
				types.LabelMetaBleemeoTargetAgentUUID: "c571f9cf-6f07-492a-9e86-b8d5f5027557",
				types.LabelMetaBleemeoUUID:            "a39e5a8e-34cf-4b15-87bd-4b9cdaa59c42",
				// necessary for some labels to be applied
				types.LabelMetaProbeServiceUUID: "dcb8e864-0a1f-4a67-b470-327ceb461b4e",
				types.LabelMetaSendScraperUUID:  "yes",
			}},
			want: labels.FromMap(map[string]string{
				types.LabelInstance:     "icmp://8.8.8.8",
				types.LabelScraper:      "super-instance:1111",
				types.LabelInstanceUUID: "c571f9cf-6f07-492a-9e86-b8d5f5027557",
				types.LabelScraperUUID:  "a39e5a8e-34cf-4b15-87bd-4b9cdaa59c42",
			}),
			wantAnnotations: types.MetricAnnotations{
				BleemeoAgentID: "c571f9cf-6f07-492a-9e86-b8d5f5027557",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &Registry{}
			r.relabelConfigs = tt.fields.relabelConfigs
			promLabels, annotations := r.applyRelabel(tt.args.input)
			if !reflect.DeepEqual(promLabels, tt.want) {
				t.Errorf("Registry.applyRelabel() promLabels = %+v, want %+v", promLabels, tt.want)
			}
			if !reflect.DeepEqual(annotations, tt.wantAnnotations) {
				t.Errorf("Registry.applyRelabel() annotations = %+v, want %+v", annotations, tt.wantAnnotations)
			}
		})
	}
}

func TestRegistry_run(t *testing.T) {
	for _, format := range []types.MetricFormat{types.MetricFormatBleemeo, types.MetricFormatPrometheus} {
		format := format

		t.Run(format.String(), func(t *testing.T) {
			var (
				l      sync.Mutex
				t0     time.Time
				points []types.MetricPoint
			)

			reg := &Registry{
				option: Option{
					MetricFormat: format,
					PushPoint: pushFunction(func(_ context.Context, pts []types.MetricPoint) {
						l.Lock()
						points = append(points, pts...)
						l.Unlock()
					}),
					FQDN:        "example.com",
					GloutonPort: "1234",
					Filter:      &fakeFilter{},
				},
			}
			reg.UpdateRelabelHook(context.Background(), func(ctx context.Context, labels map[string]string) (newLabel map[string]string, retryLater bool) {
				labels[types.LabelMetaBleemeoUUID] = testAgentID

				return labels, false
			})
			reg.init()
			reg.UpdateDelay(250 * time.Millisecond)

			gather1 := &fakeGatherer{name: "name1"}
			gather1.fillResponse()

			gather2 := &fakeGatherer{name: "name2"}
			gather2.fillResponse()

			// Sleep until the next rounded 250 millisecond.
			// Then sleep another millisecond.
			// We do this because the 3 gatherer added below should start at the same time, so we
			// must ensure the first isn't registered just before a rounded 250ms and other are resgistered after.
			// If this occur, the first will run while the other aren't yet registered.
			time.Sleep(time.Until(time.Now().Truncate(250 * time.Millisecond).Add(250 * time.Millisecond).Add(time.Millisecond)))

			id1, err := reg.RegisterGatherer(RegistrationOption{}, gather1, false)
			if err != nil {
				t.Error(err)
			}

			id2, err := reg.RegisterGatherer(RegistrationOption{}, gather2, true)
			if err != nil {
				t.Error(err)
			}

			id3, err := reg.RegisterPushPointsCallback(RegistrationOption{}, func(_ context.Context, t time.Time) {
				l.Lock()
				t0 = t
				l.Unlock()

				reg.WithTTL(5*time.Minute).PushPoints(context.Background(), []types.MetricPoint{ //nolint: contextcheck
					{Point: types.Point{Time: t, Value: 42.0}, Labels: map[string]string{"__name__": "push", "something": "value"}, Annotations: types.MetricAnnotations{BleemeoItem: "/home"}},
				})
			})
			if err != nil {
				t.Error(err)
			}

			// We don't know the schedule of scraper... wait until we have the expected number of points
			deadline := time.Now().Add(time.Second)

			l.Lock()

			for time.Now().Before(deadline) {
				l.Unlock()
				time.Sleep(50 * time.Millisecond)
				l.Lock()

				if len(points) >= 2 {
					break
				}
			}

			var want []types.MetricPoint

			if format == types.MetricFormatBleemeo {
				want = []types.MetricPoint{
					{Point: types.Point{Time: t0, Value: 42.0}, Labels: map[string]string{"__name__": "push", "item": "/home"}, Annotations: types.MetricAnnotations{BleemeoItem: "/home"}},
					{Point: types.Point{Time: t0, Value: 1.0}, Labels: map[string]string{"__name__": "name2", "instance": "example.com:1234", "instance_uuid": testAgentID}},
				}
			} else if format == types.MetricFormatPrometheus {
				want = []types.MetricPoint{
					{Point: types.Point{Time: t0, Value: 42.0}, Labels: map[string]string{"__name__": "push", "instance": "example.com:1234", "instance_uuid": testAgentID, "something": "value"}, Annotations: types.MetricAnnotations{BleemeoItem: "/home"}},
					{Point: types.Point{Time: t0, Value: 1.0}, Labels: map[string]string{"__name__": "name2", "instance": "example.com:1234", "instance_uuid": testAgentID}},
				}
			}

			pointLess := func(x, y types.MetricPoint) bool {
				return x.Labels[types.LabelName] < y.Labels[types.LabelName]
			}

			if diff := cmp.Diff(want, points, cmpopts.SortSlices(pointLess), cmpopts.EquateApproxTime(50*time.Millisecond)); diff != "" {
				t.Errorf("points mismatch (-want +got)\n%s", diff)
			}

			l.Unlock()

			reg.Unregister(id1)
			reg.Unregister(id2)
			reg.Unregister(id3)
		})
	}
}
