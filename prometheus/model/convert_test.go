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

package model

import (
	"context"
	"glouton/types"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/storage"
	"google.golang.org/protobuf/proto"
)

func TestConvertionLoop(t *testing.T) {
	now := time.Date(2022, 1, 25, 11, 21, 27, 0, time.UTC)

	cases := []struct {
		name   string
		points []types.MetricPoint
	}{
		{
			name:   "empty",
			points: nil,
		},
		{
			name: "one points",
			points: []types.MetricPoint{
				{
					Point:  types.Point{Time: now, Value: 42.1},
					Labels: map[string]string{types.LabelName: "cpu_used"},
				},
			},
		},
		{
			name: "with annotations",
			points: []types.MetricPoint{
				{
					Point:  types.Point{Time: now, Value: 0.42},
					Labels: map[string]string{types.LabelName: "disk_used"},
					Annotations: types.MetricAnnotations{
						ContainerID:     "a container id",
						ServiceName:     "some service name",
						ServiceInstance: "some instance",
						SNMPTarget:      "a SNMP target",
						BleemeoAgentID:  "some id of agent",
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusWarning,
							StatusDescription: "some description for the status",
						},
					},
				},
			},
		},
		{
			name: "newline in status description",
			points: []types.MetricPoint{
				{
					Point:  types.Point{Time: now, Value: 1.24},
					Labels: map[string]string{types.LabelName: "used_used"},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusUnknown,
							StatusDescription: "some description\nwith newline for the status",
						},
					},
				},
			},
		},
		{
			name: "multiple-points",
			points: []types.MetricPoint{
				{
					Point:  types.Point{Time: now, Value: 0},
					Labels: map[string]string{types.LabelName: "disk_used", types.LabelItem: "/home"},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusUnknown,
							StatusDescription: "disk absent",
						},
						BleemeoItem: "/home",
					},
				},
				{
					Point:  types.Point{Time: now, Value: 12},
					Labels: map[string]string{types.LabelName: "disk_used", types.LabelItem: "/srv"},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "LGTM",
						},
						BleemeoItem: "/srv",
					},
				},
				{
					Point:  types.Point{Time: now, Value: 110},
					Labels: map[string]string{types.LabelName: "disk_used", types.LabelItem: "/"},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusCritical,
							StatusDescription: "110% is more than 100%",
						},
						BleemeoItem: "/",
					},
				},
				{
					Point:  types.Point{Time: now, Value: 110},
					Labels: map[string]string{types.LabelName: "another_name", "custom": "label"},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusWarning,
							StatusDescription: "",
						},
					},
				},
				{
					Point:  types.Point{Time: now, Value: 110},
					Labels: map[string]string{types.LabelName: "unsorted_name", "description": "this one is between two another_name"},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusUnknown,
							StatusDescription: "",
						},
					},
				},
				{
					Point:  types.Point{Time: now, Value: 110},
					Labels: map[string]string{types.LabelName: "another_name", "other": "label"},
					Annotations: types.MetricAnnotations{
						ServiceName:     "ok",
						ServiceInstance: "",
					},
				},
			},
		},
		{
			name: "zero-time",
			points: []types.MetricPoint{
				{
					Point:  types.Point{Time: time.Time{}, Value: 1},
					Labels: map[string]string{types.LabelName: "name"},
				},
			},
		},
	}

	for _, tt := range cases {
		tt := tt

		for _, useAppenable := range []bool{false, true} {
			useAppendable := useAppenable

			fullName := tt.name + "WithoutAppendable"
			if useAppendable {
				fullName = tt.name + "WithAppendable"
			}

			t.Run(fullName, func(t *testing.T) {
				t.Parallel()

				var (
					app2 storage.Appender
					mfs  []*dto.MetricFamily
				)

				app := NewBufferAppender()

				if useAppendable {
					app2 = NewFromAppender(app).Appender(context.Background())
				} else {
					app2 = app
				}

				if err := SendPointsToAppender(copyPoints(tt.points), app2); err != nil {
					t.Fatal(err)
				}

				if err := app2.Commit(); err != nil {
					t.Fatal(err)
				}

				for _, samples := range app.Committed {
					mf, err := SamplesToMetricFamily(samples, nil)
					if err != nil {
						t.Fatal(err)
					}

					mfs = append(mfs, mf)
				}

				// Expected points are the input points with the date changed if it was empty.
				// The default behavior is to set the date to now if it was empty.
				expected := make([]types.MetricPoint, 0, len(tt.points))

				for _, point := range tt.points {
					if point.Time.IsZero() {
						point.Time = now
					}

					expected = append(expected, point)
				}

				got := FamiliesToMetricPoints(now, mfs, true)

				if diff := types.DiffMetricPoints(expected, got, false); diff != "" {
					t.Errorf("conversion mismatch: (-want +got)\n:%s", diff)
				}

				mfs = MetricPointsToFamilies(tt.points)
				got = FamiliesToMetricPoints(now, mfs, true)

				if diff := types.DiffMetricPoints(expected, got, false); diff != "" {
					t.Errorf("conversion mismatch: (-want +got)\n:%s", diff)
				}
			})
		}
	}
}

func TestConvertion(t *testing.T) { //nolint: maintidx
	now := time.UnixMilli(time.Now().UnixMilli())

	cases := []struct {
		name           string
		input          []types.MetricPoint
		wantMFS        []*dto.MetricFamily
		wantPromLabels []labels.Labels
		wantPoints     []types.MetricPoint
	}{
		{
			name:           "empty",
			input:          nil,
			wantMFS:        nil,
			wantPromLabels: nil,
			wantPoints:     nil,
		},
		{
			name: "one-points",
			input: []types.MetricPoint{
				{
					Point:  types.Point{Time: now, Value: 42.1},
					Labels: map[string]string{types.LabelName: "cpu_used"},
				},
			},
			wantMFS: []*dto.MetricFamily{
				{
					Name: proto.String("cpu_used"),
					Help: proto.String(""),
					Type: dto.MetricType_UNTYPED.Enum(),
					Metric: []*dto.Metric{
						{
							TimestampMs: proto.Int64(now.UnixMilli()),
							Untyped: &dto.Untyped{
								Value: proto.Float64(42.1),
							},
						},
					},
				},
			},
			wantPromLabels: []labels.Labels{
				labels.FromMap(map[string]string{
					types.LabelName: "cpu_used",
				}),
			},
			wantPoints: []types.MetricPoint{
				{
					Point:  types.Point{Time: now, Value: 42.1},
					Labels: map[string]string{types.LabelName: "cpu_used"},
				},
			},
		},
		{
			name: "zero-time",
			input: []types.MetricPoint{
				{
					Point:  types.Point{Time: time.Time{}, Value: 42.1},
					Labels: map[string]string{types.LabelName: "cpu_used"},
				},
			},
			wantMFS: []*dto.MetricFamily{
				{
					Name: proto.String("cpu_used"),
					Help: proto.String(""),
					Type: dto.MetricType_UNTYPED.Enum(),
					Metric: []*dto.Metric{
						{
							Untyped: &dto.Untyped{
								Value: proto.Float64(42.1),
							},
						},
					},
				},
			},
			wantPromLabels: []labels.Labels{
				labels.FromMap(map[string]string{
					types.LabelName: "cpu_used",
				}),
			},
			wantPoints: []types.MetricPoint{
				{
					Point:  types.Point{Time: now, Value: 42.1},
					Labels: map[string]string{types.LabelName: "cpu_used"},
				},
			},
		},
		{
			name: "annotations-in-annotations",
			input: []types.MetricPoint{
				{
					Point: types.Point{Time: now, Value: 42.1},
					Labels: map[string]string{
						types.LabelName: "cpu_used",
						"alabel":        "test",
						"zlabel":        "test2",
					},
					Annotations: types.MetricAnnotations{
						BleemeoItem: "value1",
						ContainerID: "123456",
						ServiceName: "apache",
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusCritical,
							StatusDescription: "decription",
						},
					},
				},
				{
					Point: types.Point{Time: now, Value: 42.1},
					Labels: map[string]string{
						types.LabelName: "cpu_used",
						"alabel":        "test3",
					},
					Annotations: types.MetricAnnotations{
						BleemeoItem: "value2",
						ContainerID: "7890",
						ServiceName: "apache",
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusCritical,
							StatusDescription: "decription",
						},
					},
				},
			},
			wantMFS: []*dto.MetricFamily{ //nolint: dupl
				{
					Name: proto.String("cpu_used"),
					Help: proto.String(""),
					Type: dto.MetricType_UNTYPED.Enum(),
					Metric: []*dto.Metric{
						{
							TimestampMs: proto.Int64(now.UnixMilli()),
							Label: []*dto.LabelPair{
								{Name: proto.String(types.LabelMetaBleemeoItem), Value: proto.String("value1")},
								{Name: proto.String(types.LabelMetaContainerID), Value: proto.String("123456")},
								{Name: proto.String(types.LabelMetaCurrentDescription), Value: proto.String("decription")},
								{Name: proto.String(types.LabelMetaCurrentStatus), Value: proto.String("critical")},
								{Name: proto.String(types.LabelMetaServiceName), Value: proto.String("apache")},
								{Name: proto.String("alabel"), Value: proto.String("test")},
								{Name: proto.String("zlabel"), Value: proto.String("test2")},
							},
							Untyped: &dto.Untyped{
								Value: proto.Float64(42.1),
							},
						},
						{
							TimestampMs: proto.Int64(now.UnixMilli()),
							Label: []*dto.LabelPair{
								{Name: proto.String(types.LabelMetaBleemeoItem), Value: proto.String("value2")},
								{Name: proto.String(types.LabelMetaContainerID), Value: proto.String("7890")},
								{Name: proto.String(types.LabelMetaCurrentDescription), Value: proto.String("decription")},
								{Name: proto.String(types.LabelMetaCurrentStatus), Value: proto.String("critical")},
								{Name: proto.String(types.LabelMetaServiceName), Value: proto.String("apache")},
								{Name: proto.String("alabel"), Value: proto.String("test3")},
							},
							Untyped: &dto.Untyped{
								Value: proto.Float64(42.1),
							},
						},
					},
				},
			},
			wantPromLabels: []labels.Labels{
				labels.FromMap(map[string]string{
					types.LabelName:                   "cpu_used",
					"alabel":                          "test",
					"zlabel":                          "test2",
					types.LabelMetaBleemeoItem:        "value1",
					types.LabelMetaContainerID:        "123456",
					types.LabelMetaCurrentDescription: "decription",
					types.LabelMetaCurrentStatus:      "critical",
					types.LabelMetaServiceName:        "apache",
				}),
				labels.FromMap(map[string]string{
					types.LabelName:                   "cpu_used",
					"alabel":                          "test3",
					types.LabelMetaBleemeoItem:        "value2",
					types.LabelMetaContainerID:        "7890",
					types.LabelMetaCurrentDescription: "decription",
					types.LabelMetaCurrentStatus:      "critical",
					types.LabelMetaServiceName:        "apache",
				}),
			},
			wantPoints: []types.MetricPoint{
				{
					Point: types.Point{Time: now, Value: 42.1},
					Labels: map[string]string{
						types.LabelName: "cpu_used",
						"alabel":        "test",
						"zlabel":        "test2",
					},
					Annotations: types.MetricAnnotations{
						BleemeoItem: "value1",
						ContainerID: "123456",
						ServiceName: "apache",
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusCritical,
							StatusDescription: "decription",
						},
					},
				},
				{
					Point: types.Point{Time: now, Value: 42.1},
					Labels: map[string]string{
						types.LabelName: "cpu_used",
						"alabel":        "test3",
					},
					Annotations: types.MetricAnnotations{
						BleemeoItem: "value2",
						ContainerID: "7890",
						ServiceName: "apache",
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusCritical,
							StatusDescription: "decription",
						},
					},
				},
			},
		},
		{
			name: "annotations-in-labels",
			input: []types.MetricPoint{
				{
					Point: types.Point{Time: now, Value: 42.1},
					Labels: map[string]string{
						types.LabelName:                   "cpu_used",
						"alabel":                          "test",
						"zlabel":                          "test2",
						types.LabelMetaBleemeoItem:        "value1",
						types.LabelMetaContainerID:        "123456",
						types.LabelMetaCurrentDescription: "decription",
						types.LabelMetaCurrentStatus:      "critical",
						types.LabelMetaServiceName:        "apache",
					},
				},
				{
					Point: types.Point{Time: now, Value: 42.1},
					Labels: map[string]string{
						types.LabelName:                   "cpu_used",
						"alabel":                          "test3",
						types.LabelMetaBleemeoItem:        "value2",
						types.LabelMetaContainerID:        "7890",
						types.LabelMetaCurrentDescription: "decription",
						types.LabelMetaCurrentStatus:      "critical",
						types.LabelMetaServiceName:        "apache",
					},
				},
			},
			wantMFS: []*dto.MetricFamily{ //nolint: dupl
				{
					Name: proto.String("cpu_used"),
					Help: proto.String(""),
					Type: dto.MetricType_UNTYPED.Enum(),
					Metric: []*dto.Metric{
						{
							TimestampMs: proto.Int64(now.UnixMilli()),
							Label: []*dto.LabelPair{
								{Name: proto.String(types.LabelMetaBleemeoItem), Value: proto.String("value1")},
								{Name: proto.String(types.LabelMetaContainerID), Value: proto.String("123456")},
								{Name: proto.String(types.LabelMetaCurrentDescription), Value: proto.String("decription")},
								{Name: proto.String(types.LabelMetaCurrentStatus), Value: proto.String("critical")},
								{Name: proto.String(types.LabelMetaServiceName), Value: proto.String("apache")},
								{Name: proto.String("alabel"), Value: proto.String("test")},
								{Name: proto.String("zlabel"), Value: proto.String("test2")},
							},
							Untyped: &dto.Untyped{
								Value: proto.Float64(42.1),
							},
						},
						{
							TimestampMs: proto.Int64(now.UnixMilli()),
							Label: []*dto.LabelPair{
								{Name: proto.String(types.LabelMetaBleemeoItem), Value: proto.String("value2")},
								{Name: proto.String(types.LabelMetaContainerID), Value: proto.String("7890")},
								{Name: proto.String(types.LabelMetaCurrentDescription), Value: proto.String("decription")},
								{Name: proto.String(types.LabelMetaCurrentStatus), Value: proto.String("critical")},
								{Name: proto.String(types.LabelMetaServiceName), Value: proto.String("apache")},
								{Name: proto.String("alabel"), Value: proto.String("test3")},
							},
							Untyped: &dto.Untyped{
								Value: proto.Float64(42.1),
							},
						},
					},
				},
			},
			wantPromLabels: []labels.Labels{
				labels.FromMap(map[string]string{
					types.LabelName:                   "cpu_used",
					"alabel":                          "test",
					"zlabel":                          "test2",
					types.LabelMetaBleemeoItem:        "value1",
					types.LabelMetaContainerID:        "123456",
					types.LabelMetaCurrentDescription: "decription",
					types.LabelMetaCurrentStatus:      "critical",
					types.LabelMetaServiceName:        "apache",
				}),
				labels.FromMap(map[string]string{
					types.LabelName:                   "cpu_used",
					"alabel":                          "test3",
					types.LabelMetaBleemeoItem:        "value2",
					types.LabelMetaContainerID:        "7890",
					types.LabelMetaCurrentDescription: "decription",
					types.LabelMetaCurrentStatus:      "critical",
					types.LabelMetaServiceName:        "apache",
				}),
			},
			wantPoints: []types.MetricPoint{
				{
					Point: types.Point{Time: now, Value: 42.1},
					Labels: map[string]string{
						types.LabelName: "cpu_used",
						"alabel":        "test",
						"zlabel":        "test2",
					},
					Annotations: types.MetricAnnotations{
						BleemeoItem: "value1",
						ContainerID: "123456",
						ServiceName: "apache",
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusCritical,
							StatusDescription: "decription",
						},
					},
				},
				{
					Point: types.Point{Time: now, Value: 42.1},
					Labels: map[string]string{
						types.LabelName: "cpu_used",
						"alabel":        "test3",
					},
					Annotations: types.MetricAnnotations{
						BleemeoItem: "value2",
						ContainerID: "7890",
						ServiceName: "apache",
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusCritical,
							StatusDescription: "decription",
						},
					},
				},
			},
		},
	}

	for _, tt := range cases {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotMFS := MetricPointsToFamilies(tt.input)

			if diff := types.DiffMetricFamilies(tt.wantMFS, gotMFS, false, false); diff != "" {
				t.Errorf("MetricPointsToFamilies mismatch (-want +got)\n%s", diff)
			}

			got := FamiliesToMetricPoints(now, gotMFS, true)
			if diff := types.DiffMetricPoints(tt.wantPoints, got, false); diff != "" {
				t.Errorf("FamiliesToMetricPoints mismatch (-want +got)\n%s", diff)
			}

			gotPromLabels := make([]labels.Labels, 0, len(tt.input))

			for _, pts := range tt.input {
				promLabels := AnnotationToMetaLabels(labels.FromMap(pts.Labels), pts.Annotations)
				gotPromLabels = append(gotPromLabels, promLabels)
			}

			if diff := cmp.Diff(tt.wantPromLabels, gotPromLabels, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("AnnotationToMetaLabels mismatch (-want +got)\n%s", diff)
			}
		})
	}
}

func copyPoints(input []types.MetricPoint) []types.MetricPoint {
	result := make([]types.MetricPoint, 0, len(input))

	for _, p := range input {
		work := p
		work.Labels = make(map[string]string, len(p.Labels))

		for k, v := range p.Labels {
			work.Labels[k] = v
		}

		result = append(result, work)
	}

	return result
}

func TestFamiliesToCollector(t *testing.T) {
	tests := []struct {
		name   string
		points []types.MetricPoint
	}{
		{
			name:   "empty",
			points: nil,
		},
		{
			name: "one points",
			points: []types.MetricPoint{
				{
					Point:  types.Point{Time: time.Time{}, Value: 42.1},
					Labels: map[string]string{types.LabelName: "cpu_used"},
				},
			},
		},
		{
			name: "more points",
			points: []types.MetricPoint{
				{
					Point:  types.Point{Time: time.Time{}, Value: 42.1},
					Labels: map[string]string{types.LabelName: "cpu_used"},
				},
				{
					Point: types.Point{Time: time.Time{}, Value: 42.1},
					Labels: map[string]string{
						types.LabelName: "disk_used",
						"not_item":      "/home",
					},
				},
			},
		},
	}
	for _, tt := range tests {
		tt := tt

		for _, targetType := range []dto.MetricType{dto.MetricType_GAUGE, dto.MetricType_COUNTER} {
			targetType := targetType

			name := tt.name + "-" + targetType.String()

			t.Run(name, func(t *testing.T) {
				mfs := MetricPointsToFamilies(tt.points)
				for _, mf := range mfs {
					FamilyConvertType(mf, targetType)
				}

				metrics, err := FamiliesToCollector(mfs)
				if err != nil {
					t.Fatal(err)
				}

				mfs, err = CollectorToFamilies(metrics)
				if err != nil {
					t.Fatal(err)
				}

				got := FamiliesToMetricPoints(time.Time{}, mfs, true)

				if diff := types.DiffMetricPoints(tt.points, got, false); diff != "" {
					t.Errorf("conversion mismatch: (-want +got)\n:%s", diff)
				}
			})
		}
	}
}
