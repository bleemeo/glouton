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
	"testing"
	"time"

	"github.com/bleemeo/glouton/prometheus/model"
	"github.com/bleemeo/glouton/types"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/prometheus/model/labels"
)

func Test_mergeLabels(t *testing.T) {
	var (
		strName   = "name"
		strValue  = "value"
		strDC     = "dc"
		strUSEast = "us-east"
		strItem   = "item"
		strHome   = "/home"
		strRoot   = "/"
	)

	type args struct {
		a []*dto.LabelPair
		b []*dto.LabelPair
	}

	tests := []struct {
		name string
		args args
		want []*dto.LabelPair
	}{
		{
			name: "second-empty",
			args: args{
				a: []*dto.LabelPair{
					{Name: &strName, Value: &strValue},
				},
			},
			want: []*dto.LabelPair{
				{Name: &strName, Value: &strValue},
			},
		},
		{
			name: "first-empty",
			args: args{
				b: []*dto.LabelPair{
					{Name: &strName, Value: &strValue},
				},
			},
			want: []*dto.LabelPair{
				{Name: &strName, Value: &strValue},
			},
		},
		{
			name: "no-conflict",
			args: args{
				a: []*dto.LabelPair{
					// Don't forget that labels must be sorted by name
					{Name: &strItem, Value: &strHome},
					{Name: &strName, Value: &strValue},
				},
				b: []*dto.LabelPair{
					{Name: &strDC, Value: &strUSEast},
				},
			},
			want: []*dto.LabelPair{
				{Name: &strDC, Value: &strUSEast},
				{Name: &strItem, Value: &strHome},
				{Name: &strName, Value: &strValue},
			},
		},
		{
			name: "conflict",
			args: args{
				a: []*dto.LabelPair{
					{Name: &strItem, Value: &strHome},
					{Name: &strName, Value: &strValue},
				},
				b: []*dto.LabelPair{
					{Name: &strDC, Value: &strUSEast},
					{Name: &strItem, Value: &strRoot},
				},
			},
			want: []*dto.LabelPair{
				{Name: &strDC, Value: &strUSEast},
				{Name: &strItem, Value: &strRoot},
				{Name: &strName, Value: &strValue},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			inputA := labels.FromMap(model.DTO2Labels("fake_name", tt.args.a))
			wantLabels := labels.FromMap(model.DTO2Labels("fake_name", tt.want))
			gotLabels := mergeLabels(inputA, tt.args.b)

			opts := []cmp.Option{
				cmp.Comparer(func(x, y labels.Labels) bool {
					return x.Hash() == y.Hash()
				}),
			}

			if diff := cmp.Diff(wantLabels, gotLabels, opts...); diff != "" {
				t.Errorf("mergeLabels() mismatch (-want +got)\n%s", diff)
			}

			got := mergeLabelsDTO(tt.args.a, tt.args.b)

			opts = []cmp.Option{
				cmpopts.IgnoreUnexported(dto.LabelPair{}),
				cmpopts.EquateEmpty(),
			}

			if diff := cmp.Diff(tt.want, got, opts...); diff != "" {
				t.Errorf("mergeLabelsDTO() mismatch (-want +got)\n%s", diff)
			}
		})
	}
}

// Test_labeledGatherer_GatherPoints should be converted to call of registry.scrape.
func Test_labeledGatherer_GatherPoints(t *testing.T) {
	var (
		strMetric   = "up"
		strItem     = "item"
		strHome     = "/home"
		strJob      = "job"
		strValue    = "value"
		floatValue1 = 42.0
		floatValue2 = 1337.0
		timestampMS = time.Date(2020, 2, 28, 13, 54, 48, 0, time.UTC).UnixNano() / 1e6
	)

	g := &fakeGatherer{
		name: "fake 1",
		response: []*dto.MetricFamily{
			{
				Name: &strMetric,
				Type: dto.MetricType_COUNTER.Enum(),
				Metric: []*dto.Metric{
					{
						Label: []*dto.LabelPair{
							{Name: &strItem, Value: &strHome},
						},
						TimestampMs: &timestampMS,
						Counter:     &dto.Counter{Value: &floatValue1},
					},
					{
						Label:       []*dto.LabelPair{},
						TimestampMs: &timestampMS,
						Counter:     &dto.Counter{Value: &floatValue2},
					},
				},
			},
		},
	}

	type fields struct {
		source      prometheus.Gatherer
		labels      labels.Labels
		annotations types.MetricAnnotations
	}

	tests := []struct {
		name    string
		fields  fields
		want    []types.MetricPoint
		wantErr bool
	}{
		{
			name: "no-change",
			fields: fields{
				source:      g,
				annotations: types.MetricAnnotations{},
				labels:      labels.EmptyLabels(),
			},
			want: []types.MetricPoint{
				{
					Point: types.Point{Time: time.Unix(0, timestampMS*1e6), Value: floatValue1},
					Labels: map[string]string{
						types.LabelName: "up",
						strItem:         strHome,
					},
				},
				{
					Point:       types.Point{Time: time.Unix(0, timestampMS*1e6), Value: floatValue2},
					Annotations: types.MetricAnnotations{},
					Labels: map[string]string{
						types.LabelName: "up",
					},
				},
			},
		},
		{
			name: "change",
			fields: fields{
				source: g,
				annotations: types.MetricAnnotations{
					ServiceName: "service-name",
				},
				labels: labels.FromStrings(strJob, strValue),
			},
			want: []types.MetricPoint{
				{
					Point: types.Point{Time: time.Unix(0, timestampMS*1e6), Value: floatValue1},
					Annotations: types.MetricAnnotations{
						ServiceName: "service-name",
					},
					Labels: map[string]string{
						types.LabelName: "up",
						strJob:          strValue,
						strItem:         strHome,
					},
				},
				{
					Point: types.Point{Time: time.Unix(0, timestampMS*1e6), Value: floatValue2},
					Annotations: types.MetricAnnotations{
						ServiceName: "service-name",
					},
					Labels: map[string]string{
						types.LabelName: "up",
						strJob:          strValue,
					},
				},
			},
		},
		{
			name: "change-conflict",
			fields: fields{
				source: g,
				annotations: types.MetricAnnotations{
					ServiceName: "service-name",
				},
				labels: labels.FromStrings(strItem, strJob),
			},
			want: []types.MetricPoint{
				{
					Point: types.Point{Time: time.Unix(0, timestampMS*1e6), Value: floatValue1},
					Annotations: types.MetricAnnotations{
						ServiceName: "service-name",
					},
					Labels: map[string]string{
						types.LabelName: "up",
						strItem:         strJob,
					},
				},
				{
					Point: types.Point{Time: time.Unix(0, timestampMS*1e6), Value: floatValue2},
					Annotations: types.MetricAnnotations{
						ServiceName: "service-name",
					},
					Labels: map[string]string{
						types.LabelName: "up",
						strItem:         strJob,
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := newWrappedGatherer(tt.fields.source, tt.fields.labels, RegistrationOption{HonorTimestamp: true})

			mfs, err := g.GatherWithState(t.Context(), GatherState{T0: time.Now(), NoFilter: true})
			if (err != nil) != tt.wantErr {
				t.Errorf("labeledGatherer.GatherPoints() error = %v, wantErr %v", err, tt.wantErr)

				return
			}

			got := model.FamiliesToMetricPoints(time.Now(), mfs, true)
			if (tt.fields.annotations != types.MetricAnnotations{}) {
				for i := range got {
					got[i].Annotations = got[i].Annotations.Merge(tt.fields.annotations)
				}
			}

			if diff := types.DiffMetricPoints(tt.want, got, false); diff != "" {
				t.Errorf("labeledGatherer.GatherPoints() mismatch (-want +got)\n%s", diff)
			}
		})
	}
}
