// Copyright 2015-2021 Bleemeo
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

package rules

import (
	"context"
	bleemeoTypes "glouton/bleemeo/types"
	"glouton/store"
	"glouton/types"
	"math"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func Test_manager(t *testing.T) {
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	thresholds := []float64{50, 500}
	metricName := "node_cpu_seconds_global"

	tests := []struct {
		Name   string
		Points []types.MetricPoint
		Rules  []bleemeoTypes.Metric
		want   []types.MetricPoint
	}{
		{
			Name: "basic warning",
			Points: []types.MetricPoint{
				{
					Point: types.Point{
						Time:  now.Add(-10 * time.Minute),
						Value: 120,
					},
					Labels: map[string]string{
						types.LabelName: metricName,
					},
				},
				{
					Point: types.Point{
						Time:  now.Add(-5 * time.Minute),
						Value: 110,
					},
					Labels: map[string]string{
						types.LabelName: metricName,
					},
				},
				{
					Point: types.Point{
						Time:  now.Add(-10 * time.Second),
						Value: 101,
					},
					Labels: map[string]string{
						types.LabelName: metricName,
					},
				},
			},
			Rules: []bleemeoTypes.Metric{
				{
					LabelsText: metricName,
					Threshold: bleemeoTypes.Threshold{
						HighWarning:  &thresholds[0],
						HighCritical: &thresholds[1],
					},
					PromQLQuery:       metricName,
					IsUserPromQLAlert: true,
				},
			},
			want: []types.MetricPoint{
				{
					Point: types.Point{
						Time:  now.Add(-1 * time.Minute),
						Value: 1,
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusWarning,
							StatusDescription: "",
						},
					}},
			},
		},
		{
			Name: "basic critical",
			Points: []types.MetricPoint{
				{
					Point: types.Point{
						Time:  now.Add(-10 * time.Minute),
						Value: 800,
					},
					Labels: map[string]string{
						types.LabelName: metricName,
					},
				},
				{
					Point: types.Point{
						Time:  now.Add(-5 * time.Minute),
						Value: 1100,
					},
					Labels: map[string]string{
						types.LabelName: metricName,
					},
				},
				{
					Point: types.Point{
						Time:  now.Add(-10 * time.Second),
						Value: 1001,
					},
					Labels: map[string]string{
						types.LabelName: metricName,
					},
				},
			},
			Rules: []bleemeoTypes.Metric{
				{
					LabelsText: metricName,
					Threshold: bleemeoTypes.Threshold{
						HighWarning:  &thresholds[0],
						HighCritical: &thresholds[1],
					},
					PromQLQuery:       metricName,
					IsUserPromQLAlert: true,
				},
			},
			want: []types.MetricPoint{
				{
					Point: types.Point{
						Time:  now.Add(-1 * time.Minute),
						Value: 2,
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusCritical,
							StatusDescription: "",
						},
					}},
			},
		},
		{
			Name: "Rule with no new points after 2 minutes runs every 10 minutes",
			Points: []types.MetricPoint{
				{
					Point: types.Point{
						Time:  now.Add(-2 * time.Minute),
						Value: 1000,
					},
					Labels: map[string]string{
						types.LabelName: metricName,
					},
				},
			},
			Rules: []bleemeoTypes.Metric{
				{
					LabelsText: metricName,
					Threshold: bleemeoTypes.Threshold{
						HighWarning:  &thresholds[0],
						HighCritical: &thresholds[1],
					},
					PromQLQuery:       metricName,
					IsUserPromQLAlert: false,
				},
			},
			want: []types.MetricPoint{},
		},
		{
			Name: "Critical < 5min after start does not send points",
			Points: []types.MetricPoint{
				{
					Point: types.Point{
						Time:  now.Add(-7 * time.Minute),
						Value: 1000,
					},
					Labels: map[string]string{
						types.LabelName: metricName,
					},
				},
			},
			Rules: []bleemeoTypes.Metric{
				{
					LabelsText: metricName,
					Threshold: bleemeoTypes.Threshold{
						HighWarning:  &thresholds[0],
						HighCritical: &thresholds[1],
					},
					PromQLQuery:       metricName,
					IsUserPromQLAlert: false,
				},
			},
			want: []types.MetricPoint{},
		},
	}

	for _, test := range tests {
		store := store.New()
		ruleManager := NewManager(ctx, store)
		store.PushPoints(test.Points)
		resPoints := []types.MetricPoint{}

		store.AddNotifiee(func(mp []types.MetricPoint) {
			resPoints = append(resPoints, mp...)
		})

		for _, r := range test.Rules {
			ruleManager.addAlertingRule(r)
		}

		t.Run(test.Name, func(t *testing.T) {
			for i := 0; i < 6; i++ {
				ruleManager.Run(ctx, now.Add(time.Duration(-1*(6-i))*time.Minute))
			}

			eq := cmp.Diff(test.want, resPoints)

			if eq != "" {
				t.Errorf("Base time => %v\n%s", now, eq)
			}
		})
	}
}

func Test_NaN(t *testing.T) {
	store := store.New()
	ctx := context.Background()
	ruleManager := NewManager(ctx, store)
	resPoints := []types.MetricPoint{}
	metricName := "node_cpu_seconds_global"
	thresholds := []float64{50, 500}
	now := time.Now()

	store.AddNotifiee(func(mp []types.MetricPoint) {
		resPoints = append(resPoints, mp...)
	})

	ruleManager.addAlertingRule(bleemeoTypes.Metric{
		LabelsText: metricName,
		Threshold: bleemeoTypes.Threshold{
			HighWarning:  &thresholds[0],
			HighCritical: &thresholds[1],
		},
		PromQLQuery:       metricName,
		IsUserPromQLAlert: true,
	})

	ruleManager.Run(ctx, now)

	if len(resPoints) != 1 {
		t.Errorf("Unexpected number of points; expected 1, got %d", len(resPoints))

		return
	}

	if !math.IsNaN(resPoints[0].Point.Value) {
		t.Errorf("Unexpected value in generated point: Expected NaN, got %f. Full res: %v", resPoints[0].Value, resPoints)
	}

}
