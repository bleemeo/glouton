// Copyright 2015-2026 Bleemeo
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
	"maps"
	"testing"
	"time"

	"github.com/bleemeo/glouton/types"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/storage"
)

const (
	testCPUUsed       = "cpu_used"
	testDiskUsed      = "disk_used"
	testDescription   = "description"
	testALabel        = "alabel"
	testTest2         = "test2"
	testTest3         = "test3"
	testApache        = "apache"
	testCritical      = "critical"
	testZLabel        = "zlabel"
	testLabelTest     = "test"
	testPort7890      = "7890"
	testPort123456    = "123456"
	testMountHome     = "/home"
	testEmpty         = "empty"
	testMountSrv      = "/srv"
	testErrConvertFmt = "conversion mismatch: (-want +got)\n:%s"
)

func TestConversionLoop(t *testing.T) {
	now := time.Date(2022, 1, 25, 11, 21, 27, 0, time.UTC)

	cases := []struct {
		name      string
		defaultTS time.Time
		points    []types.MetricPoint
	}{
		{
			name:      testEmpty,
			defaultTS: now,
			points:    nil,
		},
		{
			name:      "one points",
			defaultTS: now,
			points: []types.MetricPoint{
				{
					Point:  types.Point{Time: now, Value: 42.1},
					Labels: map[string]string{types.LabelName: testCPUUsed},
				},
			},
		},
		{
			name:      "with annotations",
			defaultTS: now,
			points: []types.MetricPoint{
				{
					Point:  types.Point{Time: now, Value: 0.42},
					Labels: map[string]string{types.LabelName: testDiskUsed},
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
			name:      "newline in status description",
			defaultTS: now,
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
			name:      "multiple-points",
			defaultTS: now,
			points: []types.MetricPoint{
				{
					Point:  types.Point{Time: now, Value: 0},
					Labels: map[string]string{types.LabelName: testDiskUsed, types.LabelItem: testMountHome},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusUnknown,
							StatusDescription: "disk absent",
						},
					},
				},
				{
					Point:  types.Point{Time: now, Value: 12},
					Labels: map[string]string{types.LabelName: testDiskUsed, types.LabelItem: testMountSrv},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "LGTM",
						},
					},
				},
				{
					Point:  types.Point{Time: now, Value: 110},
					Labels: map[string]string{types.LabelName: testDiskUsed, types.LabelItem: "/"},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusCritical,
							StatusDescription: "110% is more than 100%",
						},
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
					Labels: map[string]string{types.LabelName: "unsorted_name", testDescription: "this one is between two another_name"},
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
			name:      "zero-time",
			defaultTS: now,
			points: []types.MetricPoint{
				{
					Point:  types.Point{Time: time.Time{}, Value: 1},
					Labels: map[string]string{types.LabelName: "name"},
				},
			},
		},
		{
			name:      "epoc-time",
			defaultTS: now,
			points: []types.MetricPoint{
				{
					Point:  types.Point{Time: time.UnixMilli(0), Value: 1},
					Labels: map[string]string{types.LabelName: "name"},
				},
			},
		},
	}

	for _, tt := range cases {
		for _, useAppendable := range []bool{false, true} {
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
					app2 = NewFromAppender(app).Appender(t.Context())
				} else {
					app2 = app
				}

				if err := SendPointsToAppender(copyPoints(tt.points), app2); err != nil {
					t.Fatal(err)
				}

				if err := app2.Commit(); err != nil {
					t.Fatal(err)
				}

				mfs, err := app.AsMF()
				if err != nil {
					t.Fatal(err)
				}

				// Expected points are the input points with the date changed if it was unset.
				// Unset means time.Time{} or Unix epoc. In both case they are replaced by defaultTS.
				expected := make([]types.MetricPoint, 0, len(tt.points))

				for _, point := range tt.points {
					if point.Time.IsZero() || point.Time.Equal(time.UnixMilli(0)) {
						point.Time = tt.defaultTS
					}

					expected = append(expected, point)
				}

				got := FamiliesToMetricPoints(tt.defaultTS, mfs, true)

				if diff := types.DiffMetricPoints(expected, got, false); diff != "" {
					t.Errorf(testErrConvertFmt, diff)
				}

				mfs = MetricPointsToFamilies(tt.points)
				got = FamiliesToMetricPoints(tt.defaultTS, mfs, true)

				if diff := types.DiffMetricPoints(expected, got, false); diff != "" {
					t.Errorf(testErrConvertFmt, diff)
				}
			})
		}
	}
}

func TestConversion(t *testing.T) { //nolint: maintidx
	now := time.UnixMilli(time.Now().UnixMilli())

	cases := []struct {
		name           string
		defaultTS      time.Time
		input          []types.MetricPoint
		wantMFS        []*dto.MetricFamily
		wantPromLabels []labels.Labels
		wantPoints     []types.MetricPoint
	}{
		{
			name:           testEmpty,
			defaultTS:      now,
			input:          nil,
			wantMFS:        nil,
			wantPromLabels: nil,
			wantPoints:     nil,
		},
		{
			name:      "one-points",
			defaultTS: now,
			input: []types.MetricPoint{
				{
					Point:  types.Point{Time: now, Value: 42.1},
					Labels: map[string]string{types.LabelName: testCPUUsed},
				},
			},
			wantMFS: []*dto.MetricFamily{
				{
					Name: new(testCPUUsed),
					Help: new(""),
					Type: dto.MetricType_UNTYPED.Enum(),
					Metric: []*dto.Metric{
						{
							TimestampMs: new(now.UnixMilli()),
							Untyped: &dto.Untyped{
								Value: new(42.1),
							},
						},
					},
				},
			},
			wantPromLabels: []labels.Labels{
				labels.FromMap(map[string]string{
					types.LabelName: testCPUUsed,
				}),
			},
			wantPoints: []types.MetricPoint{
				{
					Point:  types.Point{Time: now, Value: 42.1},
					Labels: map[string]string{types.LabelName: testCPUUsed},
				},
			},
		},
		{
			name:      "zero-time",
			defaultTS: now,
			input: []types.MetricPoint{
				{
					Point:  types.Point{Time: time.Time{}, Value: 42.1},
					Labels: map[string]string{types.LabelName: testCPUUsed},
				},
			},
			wantMFS: []*dto.MetricFamily{
				{
					Name: new(testCPUUsed),
					Help: new(""),
					Type: dto.MetricType_UNTYPED.Enum(),
					Metric: []*dto.Metric{
						{
							Untyped: &dto.Untyped{
								Value: new(42.1),
							},
						},
					},
				},
			},
			wantPromLabels: []labels.Labels{
				labels.FromMap(map[string]string{
					types.LabelName: testCPUUsed,
				}),
			},
			wantPoints: []types.MetricPoint{
				{
					Point:  types.Point{Time: now, Value: 42.1},
					Labels: map[string]string{types.LabelName: testCPUUsed},
				},
			},
		},
		{
			name:      "epoc-time",
			defaultTS: now,
			input: []types.MetricPoint{
				{
					Point:  types.Point{Time: time.UnixMilli(0), Value: 42.1},
					Labels: map[string]string{types.LabelName: testCPUUsed},
				},
			},
			wantMFS: []*dto.MetricFamily{
				{
					Name: new(testCPUUsed),
					Help: new(""),
					Type: dto.MetricType_UNTYPED.Enum(),
					Metric: []*dto.Metric{
						{
							Untyped: &dto.Untyped{
								Value: new(42.1),
							},
						},
					},
				},
			},
			wantPromLabels: []labels.Labels{
				labels.FromMap(map[string]string{
					types.LabelName: testCPUUsed,
				}),
			},
			wantPoints: []types.MetricPoint{
				{
					Point:  types.Point{Time: now, Value: 42.1},
					Labels: map[string]string{types.LabelName: testCPUUsed},
				},
			},
		},
		{
			name:      "zero-time-to-0",
			defaultTS: time.Time{},
			input: []types.MetricPoint{
				{
					Point:  types.Point{Time: time.Time{}, Value: 42.1},
					Labels: map[string]string{types.LabelName: testCPUUsed},
				},
			},
			wantMFS: []*dto.MetricFamily{
				{
					Name: new(testCPUUsed),
					Help: new(""),
					Type: dto.MetricType_UNTYPED.Enum(),
					Metric: []*dto.Metric{
						{
							Untyped: &dto.Untyped{
								Value: new(42.1),
							},
						},
					},
				},
			},
			wantPromLabels: []labels.Labels{
				labels.FromMap(map[string]string{
					types.LabelName: testCPUUsed,
				}),
			},
			wantPoints: []types.MetricPoint{
				{
					Point:  types.Point{Time: time.Time{}, Value: 42.1},
					Labels: map[string]string{types.LabelName: testCPUUsed},
				},
			},
		},
		{
			name:      "epoc-time-to-0",
			defaultTS: time.Time{},
			input: []types.MetricPoint{
				{
					Point:  types.Point{Time: time.UnixMilli(0), Value: 42.1},
					Labels: map[string]string{types.LabelName: testCPUUsed},
				},
			},
			wantMFS: []*dto.MetricFamily{
				{
					Name: new(testCPUUsed),
					Help: new(""),
					Type: dto.MetricType_UNTYPED.Enum(),
					Metric: []*dto.Metric{
						{
							Untyped: &dto.Untyped{
								Value: new(42.1),
							},
						},
					},
				},
			},
			wantPromLabels: []labels.Labels{
				labels.FromMap(map[string]string{
					types.LabelName: testCPUUsed,
				}),
			},
			wantPoints: []types.MetricPoint{
				{
					Point:  types.Point{Time: time.Time{}, Value: 42.1},
					Labels: map[string]string{types.LabelName: testCPUUsed},
				},
			},
		},
		{
			name:      "zero-time-to-epoc",
			defaultTS: time.UnixMilli(0),
			input: []types.MetricPoint{
				{
					Point:  types.Point{Time: time.Time{}, Value: 42.1},
					Labels: map[string]string{types.LabelName: testCPUUsed},
				},
			},
			wantMFS: []*dto.MetricFamily{
				{
					Name: new(testCPUUsed),
					Help: new(""),
					Type: dto.MetricType_UNTYPED.Enum(),
					Metric: []*dto.Metric{
						{
							Untyped: &dto.Untyped{
								Value: new(42.1),
							},
						},
					},
				},
			},
			wantPromLabels: []labels.Labels{
				labels.FromMap(map[string]string{
					types.LabelName: testCPUUsed,
				}),
			},
			wantPoints: []types.MetricPoint{
				{
					Point:  types.Point{Time: time.UnixMilli(0), Value: 42.1},
					Labels: map[string]string{types.LabelName: testCPUUsed},
				},
			},
		},
		{
			name:      "epoc-time-to-epoc",
			defaultTS: time.UnixMilli(0),
			input: []types.MetricPoint{
				{
					Point:  types.Point{Time: time.UnixMilli(0), Value: 42.1},
					Labels: map[string]string{types.LabelName: testCPUUsed},
				},
			},
			wantMFS: []*dto.MetricFamily{
				{
					Name: new(testCPUUsed),
					Help: new(""),
					Type: dto.MetricType_UNTYPED.Enum(),
					Metric: []*dto.Metric{
						{
							Untyped: &dto.Untyped{
								Value: new(42.1),
							},
						},
					},
				},
			},
			wantPromLabels: []labels.Labels{
				labels.FromMap(map[string]string{
					types.LabelName: testCPUUsed,
				}),
			},
			wantPoints: []types.MetricPoint{
				{
					Point:  types.Point{Time: time.UnixMilli(0), Value: 42.1},
					Labels: map[string]string{types.LabelName: testCPUUsed},
				},
			},
		},
		{
			name:      "annotations-in-annotations",
			defaultTS: now,
			input: []types.MetricPoint{
				{
					Point: types.Point{Time: now, Value: 42.1},
					Labels: map[string]string{
						types.LabelName: testCPUUsed,
						testALabel:      testLabelTest,
						testZLabel:      testTest2,
					},
					Annotations: types.MetricAnnotations{
						ContainerID: testPort123456,
						ServiceName: testApache,
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusCritical,
							StatusDescription: testDescription,
						},
					},
				},
				{
					Point: types.Point{Time: now, Value: 42.1},
					Labels: map[string]string{
						types.LabelName: testCPUUsed,
						testALabel:      testTest3,
					},
					Annotations: types.MetricAnnotations{
						ContainerID: testPort7890,
						ServiceName: testApache,
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusCritical,
							StatusDescription: testDescription,
						},
					},
				},
			},
			wantMFS: []*dto.MetricFamily{ //nolint: dupl
				{
					Name: new(testCPUUsed),
					Help: new(""),
					Type: dto.MetricType_UNTYPED.Enum(),
					Metric: []*dto.Metric{
						{
							TimestampMs: new(now.UnixMilli()),
							Label: []*dto.LabelPair{
								{Name: new(types.LabelMetaContainerID), Value: new(testPort123456)},
								{Name: new(types.LabelMetaCurrentDescription), Value: new(testDescription)},
								{Name: new(types.LabelMetaCurrentStatus), Value: new(testCritical)},
								{Name: new(types.LabelMetaServiceName), Value: new(testApache)},
								{Name: new(testALabel), Value: new(testLabelTest)},
								{Name: new(testZLabel), Value: new(testTest2)},
							},
							Untyped: &dto.Untyped{
								Value: new(42.1),
							},
						},
						{
							TimestampMs: new(now.UnixMilli()),
							Label: []*dto.LabelPair{
								{Name: new(types.LabelMetaContainerID), Value: new(testPort7890)},
								{Name: new(types.LabelMetaCurrentDescription), Value: new(testDescription)},
								{Name: new(types.LabelMetaCurrentStatus), Value: new(testCritical)},
								{Name: new(types.LabelMetaServiceName), Value: new(testApache)},
								{Name: new(testALabel), Value: new(testTest3)},
							},
							Untyped: &dto.Untyped{
								Value: new(42.1),
							},
						},
					},
				},
			},
			wantPromLabels: []labels.Labels{
				labels.FromMap(map[string]string{
					types.LabelName:                   testCPUUsed,
					testALabel:                        testLabelTest,
					testZLabel:                        testTest2,
					types.LabelMetaContainerID:        testPort123456,
					types.LabelMetaCurrentDescription: testDescription,
					types.LabelMetaCurrentStatus:      testCritical,
					types.LabelMetaServiceName:        testApache,
				}),
				labels.FromMap(map[string]string{
					types.LabelName:                   testCPUUsed,
					testALabel:                        testTest3,
					types.LabelMetaContainerID:        testPort7890,
					types.LabelMetaCurrentDescription: testDescription,
					types.LabelMetaCurrentStatus:      testCritical,
					types.LabelMetaServiceName:        testApache,
				}),
			},
			wantPoints: []types.MetricPoint{
				{
					Point: types.Point{Time: now, Value: 42.1},
					Labels: map[string]string{
						types.LabelName: testCPUUsed,
						testALabel:      testLabelTest,
						testZLabel:      testTest2,
					},
					Annotations: types.MetricAnnotations{
						ContainerID: testPort123456,
						ServiceName: testApache,
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusCritical,
							StatusDescription: testDescription,
						},
					},
				},
				{
					Point: types.Point{Time: now, Value: 42.1},
					Labels: map[string]string{
						types.LabelName: testCPUUsed,
						testALabel:      testTest3,
					},
					Annotations: types.MetricAnnotations{
						ContainerID: testPort7890,
						ServiceName: testApache,
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusCritical,
							StatusDescription: testDescription,
						},
					},
				},
			},
		},
		{
			name:      "annotations-in-labels",
			defaultTS: now,
			input: []types.MetricPoint{
				{
					Point: types.Point{Time: now, Value: 42.1},
					Labels: map[string]string{
						types.LabelName:                   testCPUUsed,
						testALabel:                        testLabelTest,
						testZLabel:                        testTest2,
						types.LabelMetaContainerID:        testPort123456,
						types.LabelMetaCurrentDescription: testDescription,
						types.LabelMetaCurrentStatus:      testCritical,
						types.LabelMetaServiceName:        testApache,
					},
				},
				{
					Point: types.Point{Time: now, Value: 42.1},
					Labels: map[string]string{
						types.LabelName:                   testCPUUsed,
						testALabel:                        testTest3,
						types.LabelMetaContainerID:        testPort7890,
						types.LabelMetaCurrentDescription: testDescription,
						types.LabelMetaCurrentStatus:      testCritical,
						types.LabelMetaServiceName:        testApache,
					},
				},
			},
			wantMFS: []*dto.MetricFamily{ //nolint: dupl
				{
					Name: new(testCPUUsed),
					Help: new(""),
					Type: dto.MetricType_UNTYPED.Enum(),
					Metric: []*dto.Metric{
						{
							TimestampMs: new(now.UnixMilli()),
							Label: []*dto.LabelPair{
								{Name: new(types.LabelMetaContainerID), Value: new(testPort123456)},
								{Name: new(types.LabelMetaCurrentDescription), Value: new(testDescription)},
								{Name: new(types.LabelMetaCurrentStatus), Value: new(testCritical)},
								{Name: new(types.LabelMetaServiceName), Value: new(testApache)},
								{Name: new(testALabel), Value: new(testLabelTest)},
								{Name: new(testZLabel), Value: new(testTest2)},
							},
							Untyped: &dto.Untyped{
								Value: new(42.1),
							},
						},
						{
							TimestampMs: new(now.UnixMilli()),
							Label: []*dto.LabelPair{
								{Name: new(types.LabelMetaContainerID), Value: new(testPort7890)},
								{Name: new(types.LabelMetaCurrentDescription), Value: new(testDescription)},
								{Name: new(types.LabelMetaCurrentStatus), Value: new(testCritical)},
								{Name: new(types.LabelMetaServiceName), Value: new(testApache)},
								{Name: new(testALabel), Value: new(testTest3)},
							},
							Untyped: &dto.Untyped{
								Value: new(42.1),
							},
						},
					},
				},
			},
			wantPromLabels: []labels.Labels{
				labels.FromMap(map[string]string{
					types.LabelName:                   testCPUUsed,
					testALabel:                        testLabelTest,
					testZLabel:                        testTest2,
					types.LabelMetaContainerID:        testPort123456,
					types.LabelMetaCurrentDescription: testDescription,
					types.LabelMetaCurrentStatus:      testCritical,
					types.LabelMetaServiceName:        testApache,
				}),
				labels.FromMap(map[string]string{
					types.LabelName:                   testCPUUsed,
					testALabel:                        testTest3,
					types.LabelMetaContainerID:        testPort7890,
					types.LabelMetaCurrentDescription: testDescription,
					types.LabelMetaCurrentStatus:      testCritical,
					types.LabelMetaServiceName:        testApache,
				}),
			},
			wantPoints: []types.MetricPoint{
				{
					Point: types.Point{Time: now, Value: 42.1},
					Labels: map[string]string{
						types.LabelName: testCPUUsed,
						testALabel:      testLabelTest,
						testZLabel:      testTest2,
					},
					Annotations: types.MetricAnnotations{
						ContainerID: testPort123456,
						ServiceName: testApache,
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusCritical,
							StatusDescription: testDescription,
						},
					},
				},
				{
					Point: types.Point{Time: now, Value: 42.1},
					Labels: map[string]string{
						types.LabelName: testCPUUsed,
						testALabel:      testTest3,
					},
					Annotations: types.MetricAnnotations{
						ContainerID: testPort7890,
						ServiceName: testApache,
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusCritical,
							StatusDescription: testDescription,
						},
					},
				},
			},
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotMFS := MetricPointsToFamilies(tt.input)

			if diff := types.DiffMetricFamilies(tt.wantMFS, gotMFS, false, false); diff != "" {
				t.Errorf("MetricPointsToFamilies mismatch (-want +got)\n%s", diff)
			}

			got := FamiliesToMetricPoints(tt.defaultTS, gotMFS, true)
			if diff := types.DiffMetricPoints(tt.wantPoints, got, false); diff != "" {
				t.Errorf("FamiliesToMetricPoints mismatch (-want +got)\n%s", diff)
			}

			gotPromLabels := make([]labels.Labels, 0, len(tt.input))

			for _, pts := range tt.input {
				promLabels := AnnotationToMetaLabels(labels.FromMap(pts.Labels), pts.Annotations)
				gotPromLabels = append(gotPromLabels, promLabels)
			}

			opts := []cmp.Option{
				cmpopts.EquateEmpty(),
				cmp.Comparer(func(x, y labels.Labels) bool {
					return x.Hash() == y.Hash()
				}),
			}

			if diff := cmp.Diff(tt.wantPromLabels, gotPromLabels, opts...); diff != "" {
				t.Errorf("AnnotationToMetaLabels mismatch (-want +got)\n%s", diff)
			}
		})
	}
}

func copyPoints(input []types.MetricPoint) []types.MetricPoint {
	result := make([]types.MetricPoint, 0, len(input))

	for _, p := range input {
		p.Labels = maps.Clone(p.Labels)
		result = append(result, p)
	}

	return result
}

func TestFamiliesToCollector(t *testing.T) {
	tests := []struct {
		name   string
		points []types.MetricPoint
	}{
		{
			name:   testEmpty,
			points: nil,
		},
		{
			name: "one points",
			points: []types.MetricPoint{
				{
					Point:  types.Point{Time: time.Time{}, Value: 42.1},
					Labels: map[string]string{types.LabelName: testCPUUsed},
				},
			},
		},
		{
			name: "more points",
			points: []types.MetricPoint{
				{
					Point:  types.Point{Time: time.Time{}, Value: 42.1},
					Labels: map[string]string{types.LabelName: testCPUUsed},
				},
				{
					Point: types.Point{Time: time.Time{}, Value: 42.1},
					Labels: map[string]string{
						types.LabelName: testDiskUsed,
						"not_item":      testMountHome,
					},
				},
			},
		},
	}
	for _, tt := range tests {
		for _, targetType := range []dto.MetricType{dto.MetricType_GAUGE, dto.MetricType_COUNTER} {
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
					t.Errorf(testErrConvertFmt, diff)
				}
			})
		}
	}
}

func TestFamiliesToNameAndItem(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input []*dto.MetricFamily
		want  []*dto.MetricFamily
	}{
		{
			name: "just-name",
			input: []*dto.MetricFamily{
				{
					Name: new(testCPUUsed),
					Help: new(""),
					Type: dto.MetricType_UNTYPED.Enum(),
					Metric: []*dto.Metric{
						{
							Untyped: &dto.Untyped{
								Value: new(42.1),
							},
						},
					},
				},
			},
			want: []*dto.MetricFamily{
				{
					Name: new(testCPUUsed),
					Help: new(""),
					Type: dto.MetricType_UNTYPED.Enum(),
					Metric: []*dto.Metric{
						{
							Untyped: &dto.Untyped{
								Value: new(42.1),
							},
						},
					},
				},
			},
		},
		{
			name: "just-one-item",
			input: []*dto.MetricFamily{
				{
					Name: new(testDiskUsed),
					Help: new(""),
					Type: dto.MetricType_UNTYPED.Enum(),
					Metric: []*dto.Metric{
						{
							Label: []*dto.LabelPair{
								{Name: new(types.LabelItem), Value: new(testMountHome)},
							},
							Untyped: &dto.Untyped{
								Value: new(42.1),
							},
						},
					},
				},
			},
			want: []*dto.MetricFamily{
				{
					Name: new(testDiskUsed),
					Help: new(""),
					Type: dto.MetricType_UNTYPED.Enum(),
					Metric: []*dto.Metric{
						{
							Label: []*dto.LabelPair{
								{Name: new(types.LabelItem), Value: new(testMountHome)},
							},
							Untyped: &dto.Untyped{
								Value: new(42.1),
							},
						},
					},
				},
			},
		},
		{
			name: "only_meta_labels_are_kept",
			input: []*dto.MetricFamily{
				{
					Name: new(testDiskUsed),
					Help: new(""),
					Type: dto.MetricType_UNTYPED.Enum(),
					Metric: []*dto.Metric{
						{
							Label: []*dto.LabelPair{
								{Name: new(types.LabelMetaBleemeoUUID), Value: new("kept")},
								{Name: new(types.LabelMetaProbeScraperName), Value: new("kept2")},
								{Name: new(types.LabelDevice), Value: new("remove")},
								{Name: new(types.LabelInstance), Value: new("remove2")},
								{Name: new(types.LabelInstanceUUID), Value: new("remove3")},
								{Name: new(types.LabelItem), Value: new(testMountSrv)},
							},
							Untyped: &dto.Untyped{
								Value: new(42.1),
							},
						},
					},
				},
			},
			want: []*dto.MetricFamily{
				{
					Name: new(testDiskUsed),
					Help: new(""),
					Type: dto.MetricType_UNTYPED.Enum(),
					Metric: []*dto.Metric{
						{
							Label: []*dto.LabelPair{
								{Name: new(types.LabelMetaBleemeoUUID), Value: new("kept")},
								{Name: new(types.LabelMetaProbeScraperName), Value: new("kept2")},
								{Name: new(types.LabelItem), Value: new(testMountSrv)},
							},
							Untyped: &dto.Untyped{
								Value: new(42.1),
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := FamiliesDeepCopy(tt.input)
			FamiliesToNameAndItem(got)

			if diff := types.DiffMetricFamilies(tt.want, got, false, false); diff != "" {
				t.Errorf("FamiliesToNameAndItem() mismatch (-want +got)\n%s", diff)
			}
		})
	}
}
