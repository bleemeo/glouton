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

//nolint:dupl
package ssacli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/prometheus/model"
	"github.com/bleemeo/glouton/prometheus/registry"
	"github.com/bleemeo/glouton/types"
	"github.com/google/go-cmp/cmp"
)

// Test_GatherWithState is the principal test and indirectly test other method.
// Other test ensentially helps to diagnostic issue on sub-function / test some corner case.
// For each new server that we can test, we should write a test case for Test_GatherWithState before other tests.
// To generate testdata file, run the following command:
//
//	TZ=UTC LANG=C ssacli controller all show
//
// Then for each controller:
//
//	TZ=UTC LANG=C ssacli controller slot=0 physicaldrive all show
//
// Then copy the output of each command in files inside testdata folder.
func TestGatherer_GatherWithState(t *testing.T) { //nolint:maintidx
	now := time.Date(2023, 7, 24, 8, 23, 53, 0, time.UTC)

	tests := []struct {
		name           string
		config         config.SSACLI
		want           []types.MetricPoint
		wantController []int
		wantMethod     ssaMethod
	}{
		{
			name:           "gen8-two-controllers",
			wantMethod:     methodSSACli,
			wantController: []int{3, 0},
			want: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "controller 0 array A box 2 bay 1",
					},
					Point: types.Point{
						Time:  now,
						Value: float64(types.StatusOk.NagiosCode()),
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "controller 0 array A box 2 bay 2",
					},
					Point: types.Point{
						Time:  now,
						Value: float64(types.StatusOk.NagiosCode()),
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "controller 0 array A box 2 bay 3",
					},
					Point: types.Point{
						Time:  now,
						Value: float64(types.StatusOk.NagiosCode()),
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "controller 0 array A box 2 bay 4",
					},
					Point: types.Point{
						Time:  now,
						Value: float64(types.StatusOk.NagiosCode()),
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "controller 0 array A box 2 bay 5",
					},
					Point: types.Point{
						Time:  now,
						Value: float64(types.StatusOk.NagiosCode()),
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "controller 0 array A box 2 bay 6",
					},
					Point: types.Point{
						Time:  now,
						Value: float64(types.StatusCritical.NagiosCode()),
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusCritical,
							StatusDescription: "ssacli return a status \"Failed\"",
						},
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "controller 0 array B box 2 bay 7",
					},
					Point: types.Point{
						Time:  now,
						Value: float64(types.StatusOk.NagiosCode()),
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "controller 0 array B box 2 bay 8",
					},
					Point: types.Point{
						Time:  now,
						Value: float64(types.StatusOk.NagiosCode()),
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "controller 3 box 0 bay 1",
					},
					Point: types.Point{
						Time:  now,
						Value: float64(types.StatusOk.NagiosCode()),
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "controller 3 box 0 bay 2",
					},
					Point: types.Point{
						Time:  now,
						Value: float64(types.StatusOk.NagiosCode()),
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "controller 3 box 0 bay 3",
					},
					Point: types.Point{
						Time:  now,
						Value: float64(types.StatusOk.NagiosCode()),
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "controller 3 box 0 bay 4",
					},
					Point: types.Point{
						Time:  now,
						Value: float64(types.StatusOk.NagiosCode()),
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
			},
		},
		{
			name:           "gen10",
			wantMethod:     methodSSACli,
			wantController: []int{0},
			want: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "bay 1",
					},
					Point: types.Point{
						Time:  now,
						Value: float64(types.StatusOk.NagiosCode()),
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "bay 2",
					},
					Point: types.Point{
						Time:  now,
						Value: float64(types.StatusOk.NagiosCode()),
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
			},
		},
		{
			name:           "gen7",
			wantMethod:     methodSSACli,
			wantController: []int{0},
			want: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "bay 1",
					},
					Point: types.Point{
						Time:  now,
						Value: float64(types.StatusOk.NagiosCode()),
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "bay 2",
					},
					Point: types.Point{
						Time:  now,
						Value: float64(types.StatusOk.NagiosCode()),
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "bay 3",
					},
					Point: types.Point{
						Time:  now,
						Value: float64(types.StatusOk.NagiosCode()),
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "bay 4",
					},
					Point: types.Point{
						Time:  now,
						Value: float64(types.StatusOk.NagiosCode()),
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "bay 5",
					},
					Point: types.Point{
						Time:  now,
						Value: float64(types.StatusOk.NagiosCode()),
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "bay 6",
					},
					Point: types.Point{
						Time:  now,
						Value: float64(types.StatusOk.NagiosCode()),
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "bay 7",
					},
					Point: types.Point{
						Time:  now,
						Value: float64(types.StatusOk.NagiosCode()),
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "bay 8",
					},
					Point: types.Point{
						Time:  now,
						Value: float64(types.StatusOk.NagiosCode()),
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
			},
		},
		{
			// This testdata is actually generated with "hpacucli" instead of "ssacli".
			// Glouton don't support hpacucli, but having test that use hpacucli output helps
			// having a parser that is compatible with all version of ssacli.
			name:           "gen7-hpacucli",
			wantMethod:     methodSSACli,
			wantController: []int{0},
			want: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "bay 1",
					},
					Point: types.Point{
						Time:  now,
						Value: float64(types.StatusOk.NagiosCode()),
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "bay 2",
					},
					Point: types.Point{
						Time:  now,
						Value: float64(types.StatusOk.NagiosCode()),
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "bay 3",
					},
					Point: types.Point{
						Time:  now,
						Value: float64(types.StatusOk.NagiosCode()),
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "bay 4",
					},
					Point: types.Point{
						Time:  now,
						Value: float64(types.StatusOk.NagiosCode()),
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "bay 5",
					},
					Point: types.Point{
						Time:  now,
						Value: float64(types.StatusOk.NagiosCode()),
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "bay 6",
					},
					Point: types.Point{
						Time:  now,
						Value: float64(types.StatusOk.NagiosCode()),
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "bay 7",
					},
					Point: types.Point{
						Time:  now,
						Value: float64(types.StatusOk.NagiosCode()),
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "bay 8",
					},
					Point: types.Point{
						Time:  now,
						Value: float64(types.StatusOk.NagiosCode()),
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
			},
		},
		{
			name:           "gen8-p222",
			wantMethod:     methodSSACli,
			wantController: []int{1},
			want: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "bay 1",
					},
					Point: types.Point{
						Time:  now,
						Value: float64(types.StatusOk.NagiosCode()),
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "bay 2",
					},
					Point: types.Point{
						Time:  now,
						Value: float64(types.StatusOk.NagiosCode()),
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
			},
		},
		{
			name:           "gen9-nocontroller",
			wantMethod:     methodNoCmdAvailable,
			wantController: nil,
			want:           nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				cmdListController = func(ctx context.Context) ([]byte, error) {
					return os.ReadFile(filepath.Join("testdata", tt.name, "controller-list.txt"))
				}
				cmdListDrive = func(ctx context.Context, arg string) ([]byte, error) {
					return os.ReadFile(filepath.Join("testdata", tt.name, fmt.Sprintf("controller-slot-%s.txt", arg)))
				}
			)

			g := newGatherer(tt.config, cmdListController, cmdListDrive)

			mfs, err := g.GatherWithState(t.Context(), registry.GatherState{T0: now})
			if err != nil {
				t.Fatal(err)
			}

			if g.usedMethod != tt.wantMethod {
				t.Errorf("usedMethod = %s, want %s", methodName(g.usedMethod), methodName(tt.wantMethod))
			}

			if diff := cmp.Diff(tt.wantController, g.controllerSlots); diff != "" {
				t.Errorf("g.controllerSlots mismatch (-want +got):\n%s", diff)
			}

			got := model.FamiliesToMetricPoints(now, mfs, true)

			if diff := types.DiffMetricPoints(tt.want, got, false); diff != "" {
				t.Errorf("Gather points mismatch (-want +got):\n%s", diff)
			}

			for _, extraCall := range []int{2, 3} {
				mfs, err := g.GatherWithState(t.Context(), registry.GatherState{T0: now})
				if err != nil {
					t.Fatal(err)
				}

				got = model.FamiliesToMetricPoints(now, mfs, true)

				if diff := types.DiffMetricPoints(tt.want, got, false); diff != "" {
					t.Errorf("call #%d Gather points mismatch (-want +got):\n%s", extraCall, diff)
				}
			}
		})
	}
}
