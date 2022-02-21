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
//nolint:scopelint,dupl
package registry

import (
	"context"
	"glouton/prometheus/model"
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
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/model/relabel"
	"github.com/prometheus/prometheus/storage"
)

const testAgentID = "fcdc81a8-5bce-4305-8108-8e1e75439329"

type fakeGatherer struct {
	l         sync.Mutex
	name      string
	callCount int
	response  []*dto.MetricFamily
}

type fakeFilter struct{}

type fakeAppenderCallback struct {
	input []types.MetricPoint
}

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

func (cb fakeAppenderCallback) Collect(ctx context.Context, app storage.Appender) error {
	if err := model.SendPointsToAppender(cb.input, app); err != nil {
		return err
	}

	return app.Commit()
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

	if id1, err = reg.RegisterGatherer(context.Background(), RegistrationOption{DisablePeriodicGather: true}, gather1); err != nil {
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

	if id1, err = reg.RegisterGatherer(context.Background(), RegistrationOption{ExtraLabels: map[string]string{"name": "value"}, DisablePeriodicGather: true}, gather1); err != nil {
		t.Errorf("re-reg.RegisterGatherer(gather1) failed: %v", err)
	}

	if id2, err = reg.RegisterGatherer(context.Background(), RegistrationOption{DisablePeriodicGather: true}, gather2); err != nil {
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

	if id1, err = reg.RegisterGatherer(context.Background(), RegistrationOption{StopCallback: func() { stopCallCount++ }, ExtraLabels: map[string]string{"dummy": "value", "empty-value-to-dropped": ""}, DisablePeriodicGather: true}, gather1); err != nil {
		t.Errorf("reg.RegisterGatherer(gather1) failed: %v", err)
	}

	if _, err = reg.RegisterGatherer(context.Background(), RegistrationOption{DisablePeriodicGather: true}, gather2); err != nil {
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

func BenchmarkRegistry_applyRelabel(b *testing.B) {
	cases := []struct {
		name   string
		labels map[string]string
	}{
		{
			name: "cpu_used",
			labels: map[string]string{
				types.LabelName: "cpu_used",
			},
		},
		{
			name: "disk_used_1",
			labels: map[string]string{
				types.LabelName: "disk_used",
				types.LabelItem: "/",
			},
		},
		{
			name: "disk_used_2",
			labels: map[string]string{
				types.LabelName:     "disk_used",
				types.LabelItem:     "/",
				types.LabelInstance: "localhost:8015",
			},
		},
		{
			name: "mysql",
			labels: map[string]string{
				types.LabelName:              "mysql_command_select",
				types.LabelMetaServiceName:   "mysql",
				types.LabelMetaContainerName: "mysql_1",
				types.LabelMetaContainerID:   "1234",
				types.LabelMetaGloutonFQDN:   "hostname",
				types.LabelMetaGloutonPort:   "8015",
				types.LabelMetaServicePort:   "3306",
				types.LabelMetaPort:          "3306",
			},
		},
	}

	for _, tt := range cases {
		tt := tt

		b.Run(tt.name, func(b *testing.B) {
			r := &Registry{}
			r.relabelConfigs = getDefaultRelabelConfig()

			b.ResetTimer()

			for n := 0; n < b.N; n++ {
				r.applyRelabel(tt.labels)
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

			ctx := context.Background()
			reg.UpdateRelabelHook(ctx, func(ctx context.Context, labels map[string]string) (newLabel map[string]string, retryLater bool) {
				labels[types.LabelMetaBleemeoUUID] = testAgentID

				return labels, false
			})
			reg.init()
			reg.UpdateDelay(ctx, 250*time.Millisecond)

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

			id1, err := reg.RegisterGatherer(ctx, RegistrationOption{DisablePeriodicGather: true}, gather1)
			if err != nil {
				t.Error(err)
			}

			id2, err := reg.RegisterGatherer(ctx, RegistrationOption{DisablePeriodicGather: false}, gather2)
			if err != nil {
				t.Error(err)
			}

			id3, err := reg.RegisterPushPointsCallback(ctx, RegistrationOption{}, func(_ context.Context, t time.Time) {
				l.Lock()
				t0 = t
				l.Unlock()

				reg.WithTTL(5*time.Minute).PushPoints(ctx, []types.MetricPoint{
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
					{Point: types.Point{Time: t0, Value: 42.0}, Labels: map[string]string{"__name__": "push", "item": "/home", "instance_uuid": testAgentID}, Annotations: types.MetricAnnotations{BleemeoItem: "/home"}},
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

func TestRegistry_pointsAlteration(t *testing.T) {
	type sourceKind string

	const (
		kindPushPoint         sourceKind = "pushpoint"
		kindPushPointCallback sourceKind = "pushpointCallback"
		kindAppenderCallback  sourceKind = "appenderCallback"
		kindGatherer          sourceKind = "gatherer"
	)

	tests := []struct {
		name                  string
		input                 []types.MetricPoint
		opt                   RegistrationOption
		kindToTest            sourceKind
		metricFormat          types.MetricFormat
		metricFamiliesUseTime bool
		wantOverrideMFType    map[string]*dto.MetricType
		wantOverrideMFHelp    map[string]string
		want                  []types.MetricPoint
	}{
		{
			name:         "pushpoint-bleemeo",
			kindToTest:   kindPushPoint,
			metricFormat: types.MetricFormatBleemeo,
			input: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: "cpu_used",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "disk_used",
						types.LabelItem: "/home",
					},
					Annotations: types.MetricAnnotations{
						BleemeoItem: "/home",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "disk_used_perc",
					},
					Annotations: types.MetricAnnotations{
						BleemeoItem: "/srv",
					},
				},
			},
			opt: RegistrationOption{
				DisablePeriodicGather: true,
			},
			want: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: "cpu_used",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "disk_used",
						types.LabelItem: "/home",
					},
					Annotations: types.MetricAnnotations{
						BleemeoItem: "/home",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "disk_used_perc",
						types.LabelItem: "/srv",
					},
					Annotations: types.MetricAnnotations{
						BleemeoItem: "/srv",
					},
				},
			},
			metricFamiliesUseTime: true,
			wantOverrideMFType: map[string]*dto.MetricType{
				"cpu_used":       dto.MetricType_UNTYPED.Enum(),
				"disk_used":      dto.MetricType_UNTYPED.Enum(),
				"disk_used_perc": dto.MetricType_UNTYPED.Enum(),
			},
			wantOverrideMFHelp: map[string]string{
				"cpu_used":       "",
				"disk_used":      "",
				"disk_used_perc": "",
			},
		},
		{
			name:         "pushpointCallback-bleemeo",
			kindToTest:   kindPushPointCallback,
			metricFormat: types.MetricFormatBleemeo,
			input: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: "cpu_used",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "disk_used",
						types.LabelItem: "/home",
					},
					Annotations: types.MetricAnnotations{
						BleemeoItem: "/home",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "disk_used_perc",
					},
					Annotations: types.MetricAnnotations{
						BleemeoItem: "/srv",
					},
				},
			},
			opt: RegistrationOption{
				DisablePeriodicGather: true,
			},
			want: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: "cpu_used",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "disk_used",
						types.LabelItem: "/home",
					},
					Annotations: types.MetricAnnotations{
						BleemeoItem: "/home",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "disk_used_perc",
						types.LabelItem: "/srv",
					},
					Annotations: types.MetricAnnotations{
						BleemeoItem: "/srv",
					},
				},
			},
			metricFamiliesUseTime: true,
			wantOverrideMFType: map[string]*dto.MetricType{
				"cpu_used":       dto.MetricType_UNTYPED.Enum(),
				"disk_used":      dto.MetricType_UNTYPED.Enum(),
				"disk_used_perc": dto.MetricType_UNTYPED.Enum(),
			},
			wantOverrideMFHelp: map[string]string{
				"cpu_used":       "",
				"disk_used":      "",
				"disk_used_perc": "",
			},
		},
		{
			name:         "pushpoint-prometheus",
			kindToTest:   kindPushPoint,
			metricFormat: types.MetricFormatPrometheus,
			input: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: "cpu_used",
						"anyOther":      "label",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "disk_used",
						types.LabelItem: "/home",
					},
					Annotations: types.MetricAnnotations{
						BleemeoItem: "/home",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "disk_used_perc",
						types.LabelItem: "/srv",
					},
					Annotations: types.MetricAnnotations{
						BleemeoItem: "/srv",
					},
				},
			},
			opt: RegistrationOption{
				DisablePeriodicGather: true,
			},
			want: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName:     "cpu_used",
						"anyOther":          "label",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:     "disk_used",
						types.LabelItem:     "/home",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Annotations: types.MetricAnnotations{
						BleemeoItem: "/home",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:     "disk_used_perc",
						types.LabelItem:     "/srv",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Annotations: types.MetricAnnotations{
						BleemeoItem: "/srv",
					},
				},
			},
			metricFamiliesUseTime: true,
			wantOverrideMFType: map[string]*dto.MetricType{
				"cpu_used":       dto.MetricType_UNTYPED.Enum(),
				"disk_used":      dto.MetricType_UNTYPED.Enum(),
				"disk_used_perc": dto.MetricType_UNTYPED.Enum(),
			},
			wantOverrideMFHelp: map[string]string{
				"cpu_used":       "",
				"disk_used":      "",
				"disk_used_perc": "",
			},
		},
		{
			name:         "appender",
			kindToTest:   kindAppenderCallback,
			metricFormat: types.MetricFormatBleemeo,
			input: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: "cpu_used",
						"anyOther":      "label",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "disk_used",
						types.LabelItem: "/home",
					},
					Annotations: types.MetricAnnotations{
						BleemeoItem: "/home",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "disk_used_perc",
						types.LabelItem: "/srv",
					},
					Annotations: types.MetricAnnotations{
						BleemeoItem: "annotation are not used in appender mode",
					},
				},
			},
			opt: RegistrationOption{
				DisablePeriodicGather: true,
			},
			want: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName:     "cpu_used",
						"anyOther":          "label",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:     "disk_used",
						types.LabelItem:     "/home",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Annotations: types.MetricAnnotations{},
				},
				{
					Labels: map[string]string{
						types.LabelName:     "disk_used_perc",
						types.LabelItem:     "/srv",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Annotations: types.MetricAnnotations{},
				},
			},
			metricFamiliesUseTime: true,
			wantOverrideMFType: map[string]*dto.MetricType{
				"cpu_used":       dto.MetricType_UNTYPED.Enum(),
				"disk_used":      dto.MetricType_UNTYPED.Enum(),
				"disk_used_perc": dto.MetricType_UNTYPED.Enum(),
			},
			wantOverrideMFHelp: map[string]string{
				"cpu_used":       "",
				"disk_used":      "",
				"disk_used_perc": "",
			},
		},
		{
			name:         "gatherer-bleemeo",
			kindToTest:   kindGatherer,
			metricFormat: types.MetricFormatBleemeo,
			input: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: "cpu_used",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "disk_used",
						types.LabelItem: "/home",
					},
					Annotations: types.MetricAnnotations{
						BleemeoItem: "/home... but annotation are NOT used with Gatherer",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "disk_used_perc",
						types.LabelItem: "/srv",
					},
					Annotations: types.MetricAnnotations{
						BleemeoItem: "/srv",
					},
				},
			},
			opt: RegistrationOption{
				DisablePeriodicGather: true,
			},
			want: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName:     "cpu_used",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:     "disk_used",
						types.LabelItem:     "/home",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:     "disk_used_perc",
						types.LabelItem:     "/srv",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
				},
			},
		},
		{
			name:         "gatherer-extralabels",
			kindToTest:   kindGatherer,
			metricFormat: types.MetricFormatBleemeo,
			input: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: "ifOutBytes",
						"ifDesc":        "Some value",
					},
				},
			},
			opt: RegistrationOption{
				ExtraLabels: map[string]string{
					types.LabelMetaSNMPTarget: "1.2.3.4:8080",
					"another":                 "value",
				},
				Rules:                 DefaultSNMPRules(),
				DisablePeriodicGather: true,
			},
			want: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName:       "ifOutBytes",
						"ifDesc":              "Some value",
						types.LabelInstance:   "server.bleemeo.com:8016",
						"another":             "value",
						types.LabelSNMPTarget: "1.2.3.4:8080",
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "1.2.3.4:8080",
					},
				},
			},
		},
		{
			name:         "metric-rename-simple-gathere",
			kindToTest:   kindGatherer,
			metricFormat: types.MetricFormatBleemeo,
			input: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: "hrProcessorLoad",
						"hrDeviceDescr": "CPU Pkg/ID/Node: 0/0/0 Intel Xeon E3-12xx v2 (Ivy Bridge, IBRS)",
						"hrDeviceIndex": "1",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:  "hrStorageUsed",
						"hrStorageDescr": "Real Memory",
					},
					Point: types.Point{Value: 8},
				},
				{
					Labels: map[string]string{
						types.LabelName:  "hrStorageUsed",
						"hrStorageDescr": "Unreal Memory",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:  "hrStorageAllocationUnits",
						"hrStorageDescr": "Real Memory",
					},
					Point: types.Point{Value: 1024},
				},
				{
					Labels: map[string]string{
						types.LabelName:  "hrStorageAllocationUnits",
						"hrStorageDescr": "Unreal Memory",
					},
					Point: types.Point{Value: 1},
				},
			},
			opt: RegistrationOption{
				ExtraLabels: map[string]string{
					types.LabelMetaSNMPTarget: "192.168.1.2",
				},
				Rules:                 DefaultSNMPRules(),
				DisablePeriodicGather: true,
			},
			want: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName:       "cpu_used",
						"core":                "1",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:       "hrStorageAllocationUnits",
						"hrStorageDescr":      "Real Memory",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
					},
					Point: types.Point{Value: 1024},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:       "hrStorageAllocationUnits",
						"hrStorageDescr":      "Unreal Memory",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
					},
					Point: types.Point{Value: 1},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:       "hrStorageUsed",
						"hrStorageDescr":      "Real Memory",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
					},
					Point: types.Point{Value: 8},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:       "hrStorageUsed",
						"hrStorageDescr":      "Unreal Memory",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:       "mem_used",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
					},
					Point: types.Point{Value: 8192},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
				},
			},
			wantOverrideMFType: map[string]*dto.MetricType{
				"mem_used": dto.MetricType_GAUGE.Enum(),
			},
			wantOverrideMFHelp: map[string]string{
				"mem_used": "",
			},
		},
		{
			name:         "metric-rename-simple-pushpoint",
			kindToTest:   kindPushPointCallback,
			metricFormat: types.MetricFormatPrometheus,
			input: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: "hrProcessorLoad",
						"hrDeviceDescr": "CPU Pkg/ID/Node: 0/0/0 Intel Xeon E3-12xx v2 (Ivy Bridge, IBRS)",
						"hrDeviceIndex": "1",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:  "hrStorageUsed",
						"hrStorageDescr": "Real Memory",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:  "hrStorageUsed",
						"hrStorageDescr": "Unreal Memory",
					},
				},
			},
			opt: RegistrationOption{
				ExtraLabels: map[string]string{
					types.LabelMetaSNMPTarget: "192.168.1.2",
					"extranLabels":            "are ignored by pushpoints. So snmp target will be ignored, like rules",
				},
				Rules:                 DefaultSNMPRules(),
				DisablePeriodicGather: true,
			},
			want: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName:     "cpu_used",
						"core":              "1",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Annotations: types.MetricAnnotations{},
				},
				{
					Labels: map[string]string{
						types.LabelName:     "hrStorageUsed",
						"hrStorageDescr":    "Real Memory",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Annotations: types.MetricAnnotations{},
				},
				{
					Labels: map[string]string{
						types.LabelName:     "hrStorageUsed",
						"hrStorageDescr":    "Unreal Memory",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Annotations: types.MetricAnnotations{},
				},
			},
			metricFamiliesUseTime: true,
			wantOverrideMFType: map[string]*dto.MetricType{
				"cpu_used":      dto.MetricType_UNTYPED.Enum(),
				"hrStorageUsed": dto.MetricType_UNTYPED.Enum(),
			},
			wantOverrideMFHelp: map[string]string{
				"cpu_used":      "",
				"hrStorageUsed": "",
			},
		},
		{
			name:         "metric-rename-simple-2",
			kindToTest:   kindGatherer,
			metricFormat: types.MetricFormatBleemeo,
			input: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName:    "cpmCPUTotal1minRev",
						"cpmCPUTotalIndex": "1",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:    "cpmCPUMemoryUsed",
						"cpmCPUTotalIndex": "42",
					},
					Point: types.Point{Value: 145},
				},
				{
					Labels: map[string]string{
						types.LabelName:    "cpmCPUMemoryFree",
						"cpmCPUTotalIndex": "42",
					},
					Point: types.Point{Value: 7},
				},
			},
			opt: RegistrationOption{
				ExtraLabels: map[string]string{
					types.LabelMetaSNMPTarget: "192.168.1.2",
				},
				Rules:                 DefaultSNMPRules(),
				DisablePeriodicGather: true,
			},
			want: sortMetricPoints([]types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName:       "cpu_used",
						"core":                "1",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:       "cpmCPUMemoryUsed",
						"cpmCPUTotalIndex":    "42",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
					},
					Point: types.Point{Value: 145},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:       "cpmCPUMemoryFree",
						"cpmCPUTotalIndex":    "42",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
					},
					Point: types.Point{Value: 7},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:       "mem_free",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
					},
					Point: types.Point{Value: 7168},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:       "mem_used",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
					},
					Point: types.Point{Value: 148480},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:       "mem_used_perc",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
					},
					Point: types.Point{Value: 95.39},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
				},
			}),
			wantOverrideMFType: map[string]*dto.MetricType{
				"mem_used":      dto.MetricType_GAUGE.Enum().Enum(),
				"mem_free":      dto.MetricType_GAUGE.Enum().Enum(),
				"mem_used_perc": dto.MetricType_GAUGE.Enum().Enum(),
			},
			wantOverrideMFHelp: map[string]string{
				"mem_used":      "",
				"mem_free":      "",
				"mem_used_perc": "",
			},
		},
		{
			name:         "metric-rename-multiple-1",
			kindToTest:   kindGatherer,
			metricFormat: types.MetricFormatBleemeo,
			input: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName:       "ciscoMemoryPoolUsed",
						"ciscoMemoryPoolName": "Processor",
						"uniqueValue":         "1",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:       "ciscoMemoryPoolUsed",
						"ciscoMemoryPoolName": "Processor",
						"uniqueValue":         "2",
					},
				},
			},
			opt: RegistrationOption{
				ExtraLabels: map[string]string{
					types.LabelMetaSNMPTarget: "192.168.1.2",
				},
				Rules:                 DefaultSNMPRules(),
				DisablePeriodicGather: true,
			},
			want: sortMetricPoints([]types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName:       "mem_used",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
						"uniqueValue":         "1",
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:       "mem_used",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
						"uniqueValue":         "2",
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
				},
			}),
		},
		{
			name:         "metric-rename-multiple-2",
			kindToTest:   kindGatherer,
			metricFormat: types.MetricFormatBleemeo,
			input: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName:       "ciscoMemoryPoolUsed",
						"ciscoMemoryPoolName": "System memory",
						"uniqueValue":         "1",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:       "ciscoMemoryPoolFree",
						"ciscoMemoryPoolName": "System memory",
						"uniqueValue":         "1",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:       "ciscoMemoryPoolUsed",
						"ciscoMemoryPoolName": "Anything Else",
						"uniqueValue":         "2",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:       "ciscoMemoryPoolFree",
						"ciscoMemoryPoolName": "Processor",
						"uniqueValue":         "3",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:       "ciscoMemoryPoolFree",
						"ciscoMemoryPoolName": "anything else",
						"uniqueValue":         "5",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:    "cpmCPUMemoryFree",
						"cpmCPUTotalIndex": "2021",
						"uniqueValue":      "6",
					},
					Point: types.Point{Value: 789},
				},
				{
					Labels: map[string]string{
						types.LabelName:                     "ciscoEnvMonTemperatureStatusValue",
						"ciscoEnvMonTemperatureStatusDescr": "CPU",
						"uniqueValue":                       "7",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "rlCpuUtilDuringLastMinute",
						"uniqueValue":   "8",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:              "rlPhdUnitEnvParamTempSensorValue",
						"rlPhdUnitEnvParamStackUnit": "1",
						"uniqueValue":                "9",
					},
				},
			},
			opt: RegistrationOption{
				ExtraLabels: map[string]string{
					types.LabelMetaSNMPTarget: "192.168.1.2",
				},
				Rules:                 DefaultSNMPRules(),
				DisablePeriodicGather: true,
			},
			want: sortMetricPoints([]types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName:       "mem_used",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
						"uniqueValue":         "1",
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:       "mem_free",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
						"uniqueValue":         "1",
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:       "mem_used_perc",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
						"uniqueValue":         "1",
					},
					Point: types.Point{Value: 50},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:       "ciscoMemoryPoolUsed",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
						"ciscoMemoryPoolName": "Anything Else",
						"uniqueValue":         "2",
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:       "mem_free",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
						"uniqueValue":         "3",
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:       "ciscoMemoryPoolFree",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
						"ciscoMemoryPoolName": "anything else",
						"uniqueValue":         "5",
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:       "cpmCPUMemoryFree",
						"cpmCPUTotalIndex":    "2021",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
						"uniqueValue":         "6",
					},
					Point: types.Point{Value: 789},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:       "mem_free",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
						"uniqueValue":         "6",
					},
					Point: types.Point{Value: 807936},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:       "temperature",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
						"sensor":              "CPU",
						"uniqueValue":         "7",
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:       "cpu_used",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
						"uniqueValue":         "8",
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:       "temperature",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
						"sensor":              "CPU",
						"uniqueValue":         "9",
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
				},
			}),
			wantOverrideMFType: map[string]*dto.MetricType{
				"mem_free":      dto.MetricType_GAUGE.Enum().Enum(),
				"mem_used_perc": dto.MetricType_GAUGE.Enum(),
			},
			wantOverrideMFHelp: map[string]string{
				"mem_free":      "",
				"mem_used_perc": "",
			},
		},
		{
			name:         "metric-rule-and-rename",
			kindToTest:   kindGatherer,
			metricFormat: types.MetricFormatBleemeo,
			input: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName:  "hrStorageUsed",
						"hrStorageDescr": "Real Memory",
						"hrStorageIndex": "6",
					},
					Point: types.Point{
						Value: 1.49028e+06,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:  "hrStorageAllocationUnits",
						"hrStorageDescr": "Real Memory",
						"hrStorageIndex": "6",
					},
					Point: types.Point{
						Value: 1024,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:  "hrStorageSize",
						"hrStorageDescr": "Real Memory",
						"hrStorageIndex": "6",
					},
					Point: types.Point{
						Value: 8.385008e+06,
					},
				},
			},
			opt: RegistrationOption{
				ExtraLabels: map[string]string{
					types.LabelMetaSNMPTarget: "192.168.1.2",
				},
				Rules:                 DefaultSNMPRules(),
				DisablePeriodicGather: true,
			},
			want: sortMetricPoints([]types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName:       "hrStorageAllocationUnits",
						"hrStorageDescr":      "Real Memory",
						"hrStorageIndex":      "6",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
					},
					Point: types.Point{
						Value: 1024.0,
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:       "hrStorageSize",
						"hrStorageDescr":      "Real Memory",
						"hrStorageIndex":      "6",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
					},
					Point: types.Point{
						Value: 8.385008e+06,
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:       "hrStorageUsed",
						"hrStorageDescr":      "Real Memory",
						"hrStorageIndex":      "6",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
					},
					Point: types.Point{
						Value: 1.49028e+06,
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:       "mem_used",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
					},
					Point: types.Point{
						Value: 1526046720.0,
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:       "mem_used_perc",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
					},
					Point: types.Point{
						Value: 17.77,
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:       "mem_free",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
					},
					Point: types.Point{
						Value: 7060201472.0,
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
				},
			}),
			wantOverrideMFType: map[string]*dto.MetricType{
				"mem_used":      dto.MetricType_GAUGE.Enum(),
				"mem_used_perc": dto.MetricType_GAUGE.Enum(),
				"mem_free":      dto.MetricType_GAUGE.Enum(),
			},
			wantOverrideMFHelp: map[string]string{
				"mem_used":      "",
				"mem_used_perc": "",
				"mem_free":      "",
			},
		},
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				l         sync.Mutex
				gotPoints []types.MetricPoint
			)

			reg, err := New(
				Option{
					PushPoint: pushFunction(func(_ context.Context, pts []types.MetricPoint) {
						l.Lock()
						gotPoints = append(gotPoints, pts...)
						l.Unlock()
					}),
					FQDN:         "server.bleemeo.com",
					GloutonPort:  "8016",
					MetricFormat: tt.metricFormat,
				},
			)
			if err != nil {
				t.Fatal(err)
			}

			now := time.Date(2021, 12, 7, 10, 11, 13, 0, time.UTC)
			fillDateAndValue(tt.input, now)
			fillDateAndValue(tt.want, now)

			switch tt.kindToTest {
			case kindPushPoint:
				reg.WithTTL(5*time.Minute).PushPoints(context.Background(), tt.input)
			case kindPushPointCallback:
				id, err := reg.registerPushPointsCallback(
					context.Background(),
					tt.opt,
					func(c context.Context, t time.Time) {
						reg.WithTTL(5*time.Minute).PushPoints(c, tt.input)
					},
				)
				if err != nil {
					t.Fatal(err)
				}

				reg.InternalRunScape(context.Background(), now, id)
			case kindAppenderCallback:
				id, err := reg.RegisterAppenderCallback(
					context.Background(),
					tt.opt,
					AppenderRegistrationOption{},
					fakeAppenderCallback{
						input: tt.input,
					},
				)
				if err != nil {
					t.Fatal(err)
				}

				reg.InternalRunScape(context.Background(), now, id)
			case kindGatherer:
				id, err := reg.RegisterGatherer(
					context.Background(),
					tt.opt,
					&fakeGatherer{
						response: metricPointsToFamilies(tt.input, time.Time{}, nil, nil),
					},
				)
				if err != nil {
					t.Fatal(err)
				}

				reg.InternalRunScape(context.Background(), now, id)
			}

			gotPoints = sortMetricPoints(gotPoints)

			if diff := cmp.Diff(tt.want, gotPoints, cmpopts.EquateApprox(0.001, 0)); diff != "" {
				t.Errorf("gotPoints mismatch (-want +got):\n%s", diff)
			}

			got, err := reg.Gather()
			if err != nil {
				t.Fatal(err)
			}

			var mfsTime time.Time

			if tt.metricFamiliesUseTime {
				mfsTime = now
			}

			wantMFs := metricPointsToFamilies(tt.want, mfsTime, tt.wantOverrideMFType, tt.wantOverrideMFHelp)

			if diff := cmp.Diff(wantMFs, got, cmpopts.EquateApprox(0.001, 0), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("Gather mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func fillDateAndValue(in []types.MetricPoint, now time.Time) {
	for i := range in {
		in[i].Point.Time = now
		if in[i].Point.Value == 0 {
			in[i].Point.Value = 4.2
		}
	}
}

func metricPointsToFamilies(points []types.MetricPoint, now time.Time, typeOverload map[string]*dto.MetricType, helpOverload map[string]string) []*dto.MetricFamily {
	resultMap := make(map[string]*dto.MetricFamily)

	for _, pts := range points {
		name := pts.Labels[types.LabelName]
		mf := resultMap[name]

		if mf == nil {
			typ := typeOverload[name]
			if typ == nil {
				typ = dto.MetricType_COUNTER.Enum()
			}

			help, ok := helpOverload[name]
			if !ok {
				help = "fake metrics"
			}

			mf = &dto.MetricFamily{
				Name: proto.String(pts.Labels[types.LabelName]),
				Type: typ,
				Help: proto.String(help),
			}
		}

		var ts *int64

		if !now.IsZero() {
			ts = proto.Int64(now.UnixMilli())
		}

		m := &dto.Metric{
			TimestampMs: ts,
		}

		switch *mf.Type {
		case dto.MetricType_COUNTER:
			m.Counter = &dto.Counter{Value: proto.Float64(pts.Value)}
		case dto.MetricType_GAUGE:
			m.Gauge = &dto.Gauge{Value: proto.Float64(pts.Value)}
		case dto.MetricType_UNTYPED:
			m.Untyped = &dto.Untyped{Value: proto.Float64(pts.Value)}
		case dto.MetricType_HISTOGRAM:
			m.Histogram = &dto.Histogram{SampleCount: proto.Uint64(1), SampleSum: proto.Float64(pts.Value)}
		case dto.MetricType_SUMMARY:
			m.Summary = &dto.Summary{SampleCount: proto.Uint64(1), SampleSum: proto.Float64(pts.Value)}
		}

		for k, v := range pts.Labels {
			if k == types.LabelName {
				continue
			}

			m.Label = append(m.Label, &dto.LabelPair{
				Name:  proto.String(k),
				Value: proto.String(v),
			})
		}

		sort.Slice(m.Label, func(i, j int) bool {
			return m.Label[i].GetName() < m.Label[j].GetName()
		})

		mf.Metric = append(mf.Metric, m)
		resultMap[pts.Labels[types.LabelName]] = mf
	}

	result := make([]*dto.MetricFamily, 0, len(resultMap))
	for _, mf := range resultMap {
		result = append(result, mf)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].GetName() < result[j].GetName()
	})

	return result
}

func sortMetricPoints(points []types.MetricPoint) []types.MetricPoint {
	sort.SliceStable(points, func(i, j int) bool {
		nameA := points[i].Labels[types.LabelName]
		nameB := points[j].Labels[types.LabelName]

		return nameA < nameB
	})

	return points
}
