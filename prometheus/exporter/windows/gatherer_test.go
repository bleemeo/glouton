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

package windows

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/bleemeo/glouton/types"
	"github.com/google/go-cmp/cmp"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	prometheusModel "github.com/prometheus/common/model"
)

func fileToMFS(filename string) ([]*dto.MetricFamily, error) {
	fd, err := os.Open(filename)
	if err != nil {
		return nil, err
	}

	parser := expfmt.NewTextParser(prometheusModel.LegacyValidation)

	tmpMap, err := parser.TextToMetricFamilies(fd)
	if err != nil {
		return nil, err
	}

	tmp := make([]*dto.MetricFamily, 0, len(tmpMap))

	for _, v := range tmpMap {
		tmp = append(tmp, v)
	}

	sort.Slice(tmp, func(i, j int) bool {
		return tmp[i].GetName() < tmp[j].GetName()
	})

	return tmp, nil
}

func Test_getSmartStatus(t *testing.T) { //nolint:maintidx
	t.Parallel()

	tests := []struct {
		name       string
		metricFile string
		want       []types.MetricPoint
	}{
		{
			name:       "disk-ok",
			metricFile: "disk-ok.metrics",
			want: []types.MetricPoint{
				{
					Point: types.Point{
						Value: float64(types.StatusOk.NagiosCode()),
					},
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "PHYSICALDRIVE0",
						types.LabelModel:  "ST1000LM035",
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "SMART tests passed",
						},
					},
				},
			},
		},
		{
			name:       "disk-error",
			metricFile: "disk-error.metrics",
			want: []types.MetricPoint{
				{
					Point: types.Point{
						Value: float64(types.StatusCritical.NagiosCode()),
					},
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "PHYSICALDRIVE0",
						types.LabelModel:  "ST1000LM035",
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusCritical,
							StatusDescription: "Disk had error",
						},
					},
				},
			},
		},
		{
			// Ok, Unknown, Pred fail
			name:       "disk-multiple-status1",
			metricFile: "disk-multiple-status1.metrics",
			want: []types.MetricPoint{
				{
					Point: types.Point{
						Value: float64(types.StatusCritical.NagiosCode()),
					},
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "PHYSICALDRIVE0",
						types.LabelModel:  "ST1000LM035",
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusCritical,
							StatusDescription: "Disk is offline for unknown reason",
						},
					},
				},
			},
		},
		{
			// Degraded, Ok, Stressed
			name:       "disk-multiple-status2",
			metricFile: "disk-multiple-status2.metrics",
			want: []types.MetricPoint{
				{
					Point: types.Point{
						Value: float64(types.StatusWarning.NagiosCode()),
					},
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "PHYSICALDRIVE0",
						types.LabelModel:  "ST1000LM035",
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusWarning,
							StatusDescription: "Disk is degraded",
						},
					},
				},
			},
		},
		{
			name:       "disks-all-status",
			metricFile: "disks-all-status.metrics",
			want: []types.MetricPoint{
				{
					Point: types.Point{
						Value: float64(types.StatusWarning.NagiosCode()),
					},
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "PHYSICALDRIVE0",
						types.LabelModel:  "DD1",
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusWarning,
							StatusDescription: "Disk is degraded",
						},
					},
				},
				{
					Point: types.Point{
						Value: float64(types.StatusCritical.NagiosCode()),
					},
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "PHYSICALDRIVE1",
						types.LabelModel:  "DD2",
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusCritical,
							StatusDescription: "Disk had error",
						},
					},
				},
				{
					Point: types.Point{
						Value: float64(types.StatusOk.NagiosCode()),
					},
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "PHYSICALDRIVE10",
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "SMART tests passed",
						},
					},
				},
				{
					Point: types.Point{
						Value: float64(types.StatusCritical.NagiosCode()),
					},
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "PHYSICALDRIVE11",
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusCritical,
							StatusDescription: "Disk is offline for unknown reason",
						},
					},
				},
				{
					Point: types.Point{
						Value: float64(types.StatusCritical.NagiosCode()),
					},
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "PHYSICALDRIVE2",
						types.LabelModel:  "DD3",
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusCritical,
							StatusDescription: "Disk is offline due to lost communication with it",
						},
					},
				},
				{
					Point: types.Point{
						Value: float64(types.StatusCritical.NagiosCode()),
					},
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "PHYSICALDRIVE3",
						types.LabelModel:  "DD4",
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusCritical,
							StatusDescription: "Disk is offline due to unable to contact it",
						},
					},
				},
				{
					Point: types.Point{
						Value: float64(types.StatusCritical.NagiosCode()),
					},
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "PHYSICALDRIVE4",
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusCritical,
							StatusDescription: "Disk is offline due to non-recoverable error",
						},
					},
				},
				{
					Point: types.Point{
						Value: float64(types.StatusOk.NagiosCode()),
					},
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "PHYSICALDRIVE5",
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "SMART tests passed",
						},
					},
				},
				{
					Point: types.Point{
						Value: float64(types.StatusWarning.NagiosCode()),
					},
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "PHYSICALDRIVE6",
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusWarning,
							StatusDescription: "SMART report predicted failure in the (near) future",
						},
					},
				},
				{
					Point: types.Point{
						Value: float64(types.StatusCritical.NagiosCode()),
					},
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "PHYSICALDRIVE7",
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusCritical,
							StatusDescription: "Disk is offline for servicing",
						},
					},
				},
				{
					Point: types.Point{
						Value: float64(types.StatusCritical.NagiosCode()),
					},
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "PHYSICALDRIVE8",
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusCritical,
							StatusDescription: "Disk is starting",
						},
					},
				},
				{
					Point: types.Point{
						Value: float64(types.StatusCritical.NagiosCode()),
					},
					Labels: map[string]string{
						types.LabelName:   "smart_device_health_status",
						types.LabelDevice: "PHYSICALDRIVE9",
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusCritical,
							StatusDescription: "Disk is stopping",
						},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mfs, err := fileToMFS(filepath.Join("testdata", tt.metricFile))
			if err != nil {
				t.Fatal(err)
			}

			got := getSmartStatus(mfs)

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("getSmartStatus() mismatch (-want +got)\n%s", diff)
			}
		})
	}
}
