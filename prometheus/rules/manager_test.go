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
		Name        string
		Description string

		Points []types.MetricPoint
		Rules  []bleemeoTypes.Metric
		Want   []types.MetricPoint
	}{
		{
			Name:        "No points",
			Description: "No points in the store should not create any points.",
			Points:      []types.MetricPoint{},
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
			Want: []types.MetricPoint{},
		},
		{
			Name:        "No points above threshold",
			Description: "No points above threshold create Ok point starting 5 minutes after manager creation",
			Points: []types.MetricPoint{
				{
					Point: types.Point{
						Time:  now.Add(-10 * time.Minute),
						Value: 25,
					},
					Labels: map[string]string{
						types.LabelName: metricName,
					},
				},
				{
					Point: types.Point{
						Time:  now.Add(-5 * time.Minute),
						Value: 25,
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
			Want: []types.MetricPoint{
				{
					Point: types.Point{
						Time:  now.Add(-7 * time.Minute),
						Value: 0,
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Point: types.Point{
						Time:  now.Add(-6 * time.Minute),
						Value: 0,
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Point: types.Point{
						Time:  now.Add(-5 * time.Minute),
						Value: 0,
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Point: types.Point{
						Time:  now.Add(-4 * time.Minute),
						Value: 0,
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Point: types.Point{
						Time:  now.Add(-3 * time.Minute),
						Value: 0,
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Point: types.Point{
						Time:  now.Add(-2 * time.Minute),
						Value: 0,
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Point: types.Point{
						Time:  now.Add(-1 * time.Minute),
						Value: 0,
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
			Name:        "Warning threshold crossed",
			Description: "Warning threshold crossed should create Warning points",
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
			Want: []types.MetricPoint{
				{
					Point: types.Point{
						Time:  now.Add(-7 * time.Minute),
						Value: 0,
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Point: types.Point{
						Time:  now.Add(-6 * time.Minute),
						Value: 0,
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Point: types.Point{
						Time:  now.Add(-5 * time.Minute),
						Value: 0,
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Point: types.Point{
						Time:  now.Add(-4 * time.Minute),
						Value: 0,
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Point: types.Point{
						Time:  now.Add(-3 * time.Minute),
						Value: 0,
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Point: types.Point{
						Time:  now.Add(-2 * time.Minute),
						Value: 1,
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusWarning,
							StatusDescription: "",
						},
					},
				},
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
					},
				},
			},
		},
		{
			Name:        "Critical threshold crossed",
			Description: "Critical threshold crossed should create Critical points",
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
			Want: []types.MetricPoint{
				{
					Point: types.Point{
						Time:  now.Add(-7 * time.Minute),
						Value: 0,
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Point: types.Point{
						Time:  now.Add(-6 * time.Minute),
						Value: 0,
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Point: types.Point{
						Time:  now.Add(-5 * time.Minute),
						Value: 0,
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Point: types.Point{
						Time:  now.Add(-4 * time.Minute),
						Value: 0,
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Point: types.Point{
						Time:  now.Add(-3 * time.Minute),
						Value: 0,
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Point: types.Point{
						Time:  now.Add(-2 * time.Minute),
						Value: 2,
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusCritical,
							StatusDescription: "",
						},
					},
				},
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
					},
				},
			},
		},
		{
			Name:        "Critical threshold followed by warning",
			Description: "Critical threshold crossed for 5 minutes then warning should create a Critical point and Warning Points",
			Points: []types.MetricPoint{
				{
					Point: types.Point{
						Time:  now.Add(-9 * time.Minute),
						Value: 800,
					},
					Labels: map[string]string{
						types.LabelName: metricName,
					},
				},
				{
					Point: types.Point{
						Time:  now.Add(-5 * time.Minute),
						Value: 700,
					},
					Labels: map[string]string{
						types.LabelName: metricName,
					},
				},
				{
					Point: types.Point{
						Time:  now.Add(-1 * time.Minute),
						Value: 130,
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
			Want: []types.MetricPoint{
				{
					Point: types.Point{
						Time:  now.Add(-7 * time.Minute),
						Value: 0,
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Point: types.Point{
						Time:  now.Add(-6 * time.Minute),
						Value: 0,
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Point: types.Point{
						Time:  now.Add(-5 * time.Minute),
						Value: 0,
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Point: types.Point{
						Time:  now.Add(-4 * time.Minute),
						Value: 0,
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Point: types.Point{
						Time:  now.Add(-3 * time.Minute),
						Value: 0,
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Point: types.Point{
						Time:  now.Add(-2 * time.Minute),
						Value: 2,
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusCritical,
							StatusDescription: "",
						},
					},
				},
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
					},
				},
			},
		},
		{
			Name:        "Threshold crossed for < 4min should create an ok point",
			Description: "Threshold not crossed for a minute then crossed for < 4min should create an ok point",
			Points: []types.MetricPoint{
				{
					Point: types.Point{
						Time:  now.Add(-8 * time.Minute),
						Value: 20,
					},
					Labels: map[string]string{
						types.LabelName: metricName,
					},
				},
				{
					Point: types.Point{
						Time:  now.Add(-4 * time.Minute),
						Value: 120,
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
			Want: []types.MetricPoint{
				{
					Point: types.Point{
						Time:  now.Add(-7 * time.Minute),
						Value: 0,
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Point: types.Point{
						Time:  now.Add(-6 * time.Minute),
						Value: 0,
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Point: types.Point{
						Time:  now.Add(-5 * time.Minute),
						Value: 0,
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Point: types.Point{
						Time:  now.Add(-4 * time.Minute),
						Value: 0,
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Point: types.Point{
						Time:  now.Add(-3 * time.Minute),
						Value: 0,
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Point: types.Point{
						Time:  now.Add(-2 * time.Minute),
						Value: 0,
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
				{
					Point: types.Point{
						Time:  now.Add(-1 * time.Minute),
						Value: 0,
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
	}

	for _, test := range tests {
		test := test
		store := store.New()
		ruleManager := NewManager(ctx, store, now.Add(-13*time.Minute))
		resPoints := []types.MetricPoint{}

		store.PushPoints(test.Points)

		store.AddNotifiee(func(mp []types.MetricPoint) {
			resPoints = append(resPoints, mp...)
		})

		for _, r := range test.Rules {
			err := ruleManager.addAlertingRule(r)
			if err != nil {
				t.Error(err)

				return
			}
		}

		t.Run(test.Name, func(t *testing.T) {
			for i := -7; i < 0; i++ {
				ruleManager.Run(ctx, now.Add(time.Duration(i)*time.Minute))
			}

			eq := cmp.Diff(test.Want, resPoints)

			if eq != "" {
				t.Errorf("\nBase time for this test => %v\n%s", now, eq)
			}
		})
	}
}

func Test_NaN(t *testing.T) {
	store := store.New()
	ctx := context.Background()
	now := time.Now()
	ruleManager := NewManager(ctx, store, now)
	resPoints := []types.MetricPoint{}
	metricName := "node_cpu_seconds_global"
	thresholds := []float64{50, 500}

	store.AddNotifiee(func(mp []types.MetricPoint) {
		resPoints = append(resPoints, mp...)
	})

	err := ruleManager.addAlertingRule(bleemeoTypes.Metric{
		LabelsText: metricName,
		Threshold: bleemeoTypes.Threshold{
			HighWarning:  &thresholds[0],
			HighCritical: &thresholds[1],
		},
		PromQLQuery:       metricName,
		IsUserPromQLAlert: true,
	})
	if err != nil {
		t.Error(err)

		return
	}

	ruleManager.Run(ctx, now)

	if len(resPoints) != 1 {
		t.Errorf("Unexpected number of points; expected 1, got %d", len(resPoints))

		return
	}

	if !math.IsNaN(resPoints[0].Point.Value) {
		t.Errorf("Unexpected value in generated point: Expected NaN, got %f. Full res: %v", resPoints[0].Value, resPoints)
	}
}

// func Test_No_Points_On_Start(t *testing.T) {
// 	store := store.New()
// 	ctx := context.Background()
// 	now := time.Now().Truncate(time.Second)
// 	ruleManager := NewManager(ctx, store, now)
// 	resPoints := []types.MetricPoint{}
// 	metricName := "node_cpu_seconds_global"
// 	thresholds := []float64{50, 500}

// 	store.AddNotifiee(func(mp []types.MetricPoint) {
// 		resPoints = append(resPoints, mp...)
// 	})

// 	store.PushPoints([]types.MetricPoint{
// 		{
// 			Point: types.Point{
// 				Time:  now.Add(-10 * time.Minute),
// 				Value: 150,
// 			},
// 		},
// 		{
// 			Point: types.Point{
// 				Time:  now.Add(-5 * time.Minute),
// 				Value: 150,
// 			},
// 		},
// 		{
// 			Point: types.Point{
// 				Time:  now.Add(-2 * time.Minute),
// 				Value: 150,
// 			},
// 		},
// 	})

// 	err := ruleManager.addAlertingRule(bleemeoTypes.Metric{
// 		LabelsText: metricName,
// 		Threshold: bleemeoTypes.Threshold{
// 			HighWarning:  &thresholds[0],
// 			HighCritical: &thresholds[1],
// 		},
// 		PromQLQuery:       metricName,
// 		IsUserPromQLAlert: true,
// 	})
// 	if err != nil {
// 		t.Error(err)

// 		return
// 	}

// 	want := []types.MetricPoint{
// 		{
// 			Point: types.Point{
// 				Time:  now.Add(-2 * time.Minute),
// 				Value: 0,
// 			},
// 			Annotations: types.MetricAnnotations{
// 				Status: types.StatusDescription{
// 					CurrentStatus:     types.StatusOk,
// 					StatusDescription: "",
// 				},
// 			},
// 		},
// 		{
// 			Point: types.Point{
// 				Time:  now.Add(-1 * time.Minute),
// 				Value: 0,
// 			},
// 			Annotations: types.MetricAnnotations{
// 				Status: types.StatusDescription{
// 					CurrentStatus:     types.StatusOk,
// 					StatusDescription: "",
// 				},
// 			},
// 		},
// 	}

// 	ruleManager.Run(ctx, now)

// 	res := cmp.Diff(want, resPoints)

// 	if res != "" {
// 		t.Error(res)
// 	}
// }

func Test_Rebuild_Rules(t *testing.T) {
	store := store.New()
	ctx := context.Background()
	now := time.Now()
	ruleManager := NewManager(ctx, store, now)
	thresholds := []float64{50, 500}
	points := []bleemeoTypes.Metric{
		{
			LabelsText: "node_cpu_seconds_global",
			Threshold: bleemeoTypes.Threshold{
				HighWarning:  &thresholds[0],
				HighCritical: &thresholds[1],
			},
			PromQLQuery:       "node_cpu_seconds_global",
			IsUserPromQLAlert: false,
		},
		{
			LabelsText: "cpu_counter",
			Threshold: bleemeoTypes.Threshold{
				HighWarning:  &thresholds[0],
				HighCritical: &thresholds[1],
			},
			PromQLQuery:       "cpu_counter",
			IsUserPromQLAlert: false,
		},
	}

	err := ruleManager.RebuildAlertingRules(points)
	if err != nil {
		t.Error(err)
	}

	if len(ruleManager.alertingRules) != len(points) {
		t.Errorf("Unexpected number of points: expected %d, got %d\n", len(points), len(ruleManager.alertingRules))
	}
}
