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
	"glouton/types"
	"reflect"
	"testing"

	"github.com/gogo/protobuf/proto"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/prometheus/pkg/labels"
	"github.com/prometheus/prometheus/pkg/relabel"
)

type fakeCollector struct {
	name      string
	callCount int
}

func (c *fakeCollector) Collect(chan<- prometheus.Metric) {
	c.callCount++
}
func (c *fakeCollector) Describe(chan<- *prometheus.Desc) {

}

type fakeGatherer struct {
	name      string
	callCount int
	response  []*dto.MetricFamily
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

func TestRegistry_Register(t *testing.T) {
	reg := &Registry{}

	coll1 := &fakeCollector{
		name: "coll1",
	}
	coll2 := &fakeCollector{
		name: "coll2",
	}
	gather1 := &fakeGatherer{
		name: "gather1",
	}

	if err := reg.Register(coll1); err != nil {
		t.Errorf("reg.Register(coll1) failed: %v", err)
	}
	if err := reg.Register(coll1); err == nil {
		t.Errorf("Second reg.Register(coll1) succeeded, want fail")
	}

	_, _ = reg.Gather()
	if coll1.callCount != 1 {
		t.Errorf("coll1.callCount = %v, want 1", coll1.callCount)
	}

	if !reg.Unregister(coll1) {
		t.Errorf("reg.Unregister(coll1) failed")
	}

	_, _ = reg.Gather()
	if coll1.callCount != 1 {
		t.Errorf("coll1.callCount = %v, want 1", coll1.callCount)
	}

	if err := reg.RegisterWithLabels(coll1, map[string]string{"name": "value"}); err != nil {
		t.Errorf("re-reg.Register(coll1) failed: %v", err)
	}
	if err := reg.Register(coll2); err != nil {
		t.Errorf("re-reg.Register(coll2) failed: %v", err)
	}

	_, _ = reg.Gather()
	if coll1.callCount != 2 {
		t.Errorf("coll1.callCount = %v, want 2", coll1.callCount)
	}
	if coll2.callCount != 1 {
		t.Errorf("coll2.callCount = %v, want 1", coll2.callCount)
	}

	if !reg.Unregister(coll1) {
		t.Errorf("reg.Unregister(coll1) failed")
	}
	if !reg.Unregister(coll2) {
		t.Errorf("reg.Unregister(coll2) failed")
	}

	_, _ = reg.Gather()
	if coll1.callCount != 2 {
		t.Errorf("coll1.callCount = %v, want 2", coll1.callCount)
	}
	if coll2.callCount != 1 {
		t.Errorf("coll2.callCount = %v, want 1", coll2.callCount)
	}

	if err := reg.RegisterGatherer(gather1, map[string]string{"name": "value"}); err != nil {
		t.Errorf("reg.RegisterGatherer(gather1) failed: %v", err)
	}

	_, _ = reg.Gather()
	if coll1.callCount != 2 {
		t.Errorf("coll1.callCount = %v, want 2", coll1.callCount)
	}
	if coll2.callCount != 1 {
		t.Errorf("coll2.callCount = %v, want 1", coll2.callCount)
	}
	if gather1.callCount != 1 {
		t.Errorf("gather1.callCount = %v, want 1", gather1.callCount)
	}

	if !reg.UnregisterGatherer(gather1) {
		t.Errorf("reg.Unregister(coll1) failed")
	}

	_, _ = reg.Gather()
	if coll1.callCount != 2 {
		t.Errorf("coll1.callCount = %v, want 2", coll1.callCount)
	}
	if coll2.callCount != 1 {
		t.Errorf("coll2.callCount = %v, want 1", coll2.callCount)
	}
	if gather1.callCount != 1 {
		t.Errorf("gather1.callCount = %v, want 1", gather1.callCount)
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
				types.LabelGloutonFQDN: "hostname",
				types.LabelGloutonPort: "8015",
				types.LabelPort:        "8015",
			}},
			want: labels.FromMap(map[string]string{
				"instance": "hostname:8015",
				"job":      "glouton",
			}),
			wantAnnotations: types.MetricAnnotations{},
		},
		{
			name:   "mysql container",
			fields: fields{relabelConfigs: getDefaultRelabelConfig()},
			args: args{map[string]string{
				types.LabelServiceName:   "mysql",
				types.LabelContainerName: "mysql_1",
				types.LabelContainerID:   "1234",
				types.LabelGloutonFQDN:   "hostname",
				types.LabelGloutonPort:   "8015",
				types.LabelServicePort:   "3306",
				types.LabelPort:          "3306",
			}},
			want: labels.FromMap(map[string]string{
				"container_name": "mysql_1",
				"instance":       "hostname-mysql_1:3306",
				"job":            "glouton",
			}),
			wantAnnotations: types.MetricAnnotations{
				ServiceName: "mysql",
				ContainerID: "1234",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &Registry{}
			r.relabelConfigs = tt.fields.relabelConfigs
			promLabels, annotations := r.applyRelabel(tt.args.input)
			if !reflect.DeepEqual(promLabels, tt.want) {
				t.Errorf("Registry.applyRelabel() promLabels = %v, want %v", promLabels, tt.want)
			}
			if !reflect.DeepEqual(annotations, tt.wantAnnotations) {
				t.Errorf("Registry.applyRelabel() annotations = %v, want %v", annotations, tt.wantAnnotations)
			}
		})
	}
}
