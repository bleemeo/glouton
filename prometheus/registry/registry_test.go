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
//nolint: scopelint
package registry

import (
	"context"
	"glouton/types"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/gogo/protobuf/proto"
	"github.com/google/go-cmp/cmp"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/prometheus/pkg/labels"
	"github.com/prometheus/prometheus/pkg/relabel"
)

type fakeGatherer struct {
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
	g.callCount++

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

//nolint: gocyclo
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

	if id1, err = reg.RegisterGatherer(gather1, nil, nil, false); err != nil {
		t.Errorf("reg.RegisterGatherer(gather1) failed: %v", err)
	}

	_, _ = reg.Gather()

	if gather1.callCount != 1 {
		t.Errorf("gather1.callCount = %v, want 1", gather1.callCount)
	}

	if !reg.UnregisterGatherer(id1) {
		t.Errorf("reg.UnregisterGatherer(%d) failed", id1)
	}

	_, _ = reg.Gather()

	if gather1.callCount != 1 {
		t.Errorf("gather1.callCount = %v, want 1", gather1.callCount)
	}

	if id1, err = reg.RegisterGatherer(gather1, nil, map[string]string{"name": "value"}, false); err != nil {
		t.Errorf("re-reg.RegisterGatherer(gather1) failed: %v", err)
	}

	if id2, err = reg.RegisterGatherer(gather2, nil, nil, false); err != nil {
		t.Errorf("re-reg.RegisterGatherer(gather2) failed: %v", err)
	}

	_, _ = reg.Gather()

	if gather1.callCount != 2 {
		t.Errorf("gather1.callCount = %v, want 2", gather1.callCount)
	}

	if gather2.callCount != 1 {
		t.Errorf("gather2.callCount = %v, want 1", gather2.callCount)
	}

	if !reg.UnregisterGatherer(id1) {
		t.Errorf("reg.UnregisterGatherer(%d) failed", id1)
	}

	if !reg.UnregisterGatherer(id2) {
		t.Errorf("reg.UnregisterGatherer(%d) failed", id2)
	}

	_, _ = reg.Gather()

	if gather1.callCount != 2 {
		t.Errorf("gather1.callCount = %v, want 2", gather1.callCount)
	}

	if gather2.callCount != 1 {
		t.Errorf("gather2.callCount = %v, want 1", gather2.callCount)
	}

	stopCallCount := 0

	if id1, err = reg.RegisterGatherer(gather1, func() { stopCallCount++ }, map[string]string{"dummy": "value", "empty-value-to-dropped": ""}, false); err != nil {
		t.Errorf("reg.RegisterGatherer(gather1) failed: %v", err)
	}

	if _, err = reg.RegisterGatherer(gather2, nil, nil, false); err != nil {
		t.Errorf("re-reg.RegisterGatherer(gather2) failed: %v", err)
	}

	reg.UpdateBleemeoAgentID(context.Background(), "fake-uuid")

	result, err := reg.Gather()
	if err != nil {
		t.Error(err)
	}

	helpText := "fake metric"
	dummyName := "dummy"
	dummyValue := "value"
	instanceIDName := types.LabelInstanceUUID
	instanceIDValue := "fake-uuid"
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

	reg.UnregisterGatherer(id1)

	if stopCallCount != 1 {
		t.Errorf("stopCallCount = %v, want 1", stopCallCount)
	}
}

func TestRegistry_pushPoint(t *testing.T) {
	reg := &Registry{}

	t0 := time.Date(2020, 3, 2, 10, 30, 0, 0, time.UTC)
	t0MS := t0.UnixNano() / 1e6

	pusher := reg.WithTTL(24 * time.Hour)
	pusher.PushPoints(
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

	got, err := reg.Gather()
	if err != nil {
		t.Error(err)
	}

	metricName := "point1"
	helpText := ""
	dummyName := "dummy"
	dummyValue := "value"
	instanceIDName := types.LabelInstanceUUID
	instanceIDValue := "fake-uuid"
	value := 1.0
	want := []*dto.MetricFamily{
		{
			Name: &metricName,
			Help: &helpText,
			Type: dto.MetricType_UNTYPED.Enum(),
			Metric: []*dto.Metric{
				{
					Label: []*dto.LabelPair{
						{Name: &dummyName, Value: &dummyValue},
					},
					Untyped: &dto.Untyped{
						Value: &value,
					},
					TimestampMs: &t0MS,
				},
			},
		},
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("reg.Gather() = %v, want %v", got, want)
	}

	reg.UpdateBleemeoAgentID(context.Background(), "fake-uuid")

	got, err = reg.Gather()
	if err != nil {
		t.Error(err)
	}

	if len(got) > 0 {
		t.Errorf("reg.Gather() len=%v, want 0", len(got))
	}

	pusher.PushPoints(
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
			Name: &metricName,
			Help: &helpText,
			Type: dto.MetricType_UNTYPED.Enum(),
			Metric: []*dto.Metric{
				{
					Label: []*dto.LabelPair{
						{Name: &dummyName, Value: &dummyValue},
						{Name: &instanceIDName, Value: &instanceIDValue},
					},
					Untyped: &dto.Untyped{
						Value: &value,
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
				types.LabelMetaProbeTarget:      "icmp://8.8.8.8",
				types.LabelMetaProbeScraperName: "test",
				types.LabelMetaProbeAgentUUID:   "c571f9cf-6f07-492a-9e86-b8d5f5027557",
				types.LabelMetaBleemeoUUID:      "a39e5a8e-34cf-4b15-87bd-4b9cdaa59c42",
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
				types.LabelMetaProbeTarget:    "icmp://8.8.8.8",
				types.LabelInstance:           "super-instance:1111",
				types.LabelMetaProbeAgentUUID: "c571f9cf-6f07-492a-9e86-b8d5f5027557",
				types.LabelMetaBleemeoUUID:    "a39e5a8e-34cf-4b15-87bd-4b9cdaa59c42",
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

func TestRegistry_runOnce(t *testing.T) {
	var (
		l                sync.Mutex
		bleemeoT0        time.Time
		bleemeoPoints    []types.MetricPoint
		prometheusT0     time.Time
		prometheusPoints []types.MetricPoint
	)

	regBleemeo := &Registry{
		Option: Option{
			MetricFormat: types.MetricFormatBleemeo,
			PushPoint: pushFunction(func(points []types.MetricPoint) {
				l.Lock()
				bleemeoPoints = append(bleemeoPoints, points...)
				l.Unlock()
			}),
			BleemeoAgentID: "fake-uuid",
			FQDN:           "example.com",
			GloutonPort:    "1234",
			Filter:         &fakeFilter{},
		},
	}
	regBleemeo.init()

	regPrometheus := &Registry{
		Option: Option{
			MetricFormat: types.MetricFormatPrometheus,
			PushPoint: pushFunction(func(points []types.MetricPoint) {
				l.Lock()
				prometheusPoints = append(prometheusPoints, points...)
				l.Unlock()
			}),
			BleemeoAgentID: "fake-uuid",
			FQDN:           "example.com",
			GloutonPort:    "1234",
			Filter:         &fakeFilter{},
		},
	}
	regPrometheus.init()

	gather1 := &fakeGatherer{name: "name1"}
	gather1.fillResponse()

	gather2 := &fakeGatherer{name: "name2"}
	gather2.fillResponse()

	_, err := regBleemeo.RegisterGatherer(gather1, nil, nil, false)
	if err != nil {
		t.Error(err)
	}

	_, err = regBleemeo.RegisterGatherer(gather2, nil, nil, true)
	if err != nil {
		t.Error(err)
	}

	_, err = regPrometheus.RegisterGatherer(gather1, nil, nil, false)
	if err != nil {
		t.Error(err)
	}

	_, err = regPrometheus.RegisterGatherer(gather2, nil, nil, true)
	if err != nil {
		t.Error(err)
	}

	regBleemeo.AddPushPointsCallback(func(t time.Time) {
		l.Lock()
		bleemeoT0 = t
		l.Unlock()

		regBleemeo.WithTTL(5 * time.Minute).PushPoints([]types.MetricPoint{
			{Point: types.Point{Time: t, Value: 42.0}, Labels: map[string]string{"__name__": "push", "something": "value"}, Annotations: types.MetricAnnotations{BleemeoItem: "/home"}},
		})
	})

	regPrometheus.AddPushPointsCallback(func(t time.Time) {
		l.Lock()
		prometheusT0 = t
		l.Unlock()

		regPrometheus.WithTTL(5 * time.Minute).PushPoints([]types.MetricPoint{
			{Point: types.Point{Time: t, Value: 42.0}, Labels: map[string]string{"__name__": "push", "something": "value"}, Annotations: types.MetricAnnotations{BleemeoItem: "/home"}},
		})
	})

	if bleemeoPoints != nil {
		t.Errorf("bleemeoPoints = %v, want nil", bleemeoPoints)
	}

	gatherTime := regBleemeo.runOnce()

	want := []types.MetricPoint{
		{Point: types.Point{Time: bleemeoT0, Value: 42.0}, Labels: map[string]string{"__name__": "push", "item": "/home"}, Annotations: types.MetricAnnotations{BleemeoItem: "/home"}},
		{Point: types.Point{Time: bleemeoT0, Value: 1.0}, Labels: map[string]string{"__name__": "name2", "instance": "example.com:1234", "instance_uuid": "fake-uuid"}},
		{Point: types.Point{Time: bleemeoT0, Value: gatherTime.Seconds()}, Labels: map[string]string{"__name__": "agent_gather_time"}},
	}

	if diff := cmp.Diff(want, bleemeoPoints); diff != "" {
		t.Errorf("bleemeoPoints != want: %v", diff)
	}

	if prometheusPoints != nil {
		t.Errorf("prometheusPoints = %v, want nil", prometheusPoints)
	}

	regPrometheus.runOnce()

	want = []types.MetricPoint{
		{Point: types.Point{Time: prometheusT0, Value: 42.0}, Labels: map[string]string{"__name__": "push", "instance": "example.com:1234", "instance_uuid": "fake-uuid", "something": "value"}, Annotations: types.MetricAnnotations{BleemeoItem: "/home"}},
		{Point: types.Point{Time: prometheusT0, Value: 1.0}, Labels: map[string]string{"__name__": "name2", "instance": "example.com:1234", "instance_uuid": "fake-uuid"}},
	}

	if diff := cmp.Diff(want, prometheusPoints); diff != "" {
		t.Errorf("prometheusPoints != want: %v", diff)
	}
}
