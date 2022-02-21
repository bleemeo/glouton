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
	"errors"
	"fmt"
	"glouton/logger"
	"glouton/prometheus/registry"
	"glouton/store"
	"glouton/threshold"
	"glouton/types"
	"math"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/prometheus/prometheus/model/exemplar"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/storage"
)

const agentID = "60451941-91d9-40d2-8451-1b79250288d0"

var errNotImplemented = errors.New("not implemented")

type mockAppendable struct {
	points  []types.MetricPoint
	forceTS time.Time
	l       sync.Mutex
}

type mockAppender struct {
	parent *mockAppendable
	buffer []types.MetricPoint
}

func (app *mockAppendable) Appender(ctx context.Context) storage.Appender {
	return &mockAppender{
		parent: app,
	}
}

func (a *mockAppender) Append(ref storage.SeriesRef, l labels.Labels, t int64, v float64) (storage.SeriesRef, error) {
	labelsMap := make(map[string]string)

	for _, lblv := range l {
		labelsMap[lblv.Name] = lblv.Value
	}

	newPoint := types.MetricPoint{
		Point: types.Point{
			Time:  time.Unix(0, t*1e6),
			Value: v,
		},
		Labels:      labelsMap,
		Annotations: types.MetricAnnotations{},
	}

	if !a.parent.forceTS.IsZero() {
		newPoint.Point.Time = a.parent.forceTS
	}

	a.buffer = append(a.buffer, newPoint)

	return 0, nil
}

func (a *mockAppender) Commit() error {
	a.parent.l.Lock()
	defer a.parent.l.Unlock()

	a.parent.points = append(a.parent.points, a.buffer...)

	a.buffer = a.buffer[:0]

	return nil
}

func (a *mockAppender) Rollback() error {
	a.buffer = a.buffer[:0]

	return nil
}

func (a *mockAppender) AppendExemplar(ref storage.SeriesRef, l labels.Labels, e exemplar.Exemplar) (storage.SeriesRef, error) {
	return 0, errNotImplemented
}

func TestManager(t *testing.T) {
	t0 := time.Now().Add(-time.Minute).Round(time.Millisecond)
	t1 := t0.Add(time.Second)

	tests := []struct {
		name      string
		queryable storage.Queryable
		rules     map[string]string
		want      []types.MetricPoint
	}{
		{
			name: "LinuxCPU",
			queryable: storeFromPoints([]types.MetricPoint{
				{
					Point: types.Point{
						Time:  t0,
						Value: 123,
					},
					Labels: map[string]string{
						types.LabelName: "node_cpu_seconds_total",
						"cpu":           "0",
						"mode":          "irq",
					},
				},
				{
					Point: types.Point{
						Time:  t0,
						Value: 321,
					},
					Labels: map[string]string{
						types.LabelName: "node_cpu_seconds_total",
						"cpu":           "2",
						"mode":          "irq",
					},
				},
				{
					Point: types.Point{
						Time:  t0,
						Value: 666,
					},
					Labels: map[string]string{
						types.LabelName: "node_cpu_seconds_total",
						"cpu":           "0",
						"mode":          "user",
					},
				},
			}),
			rules: defaultLinuxRecordingRules,
			want: []types.MetricPoint{
				{
					Point: types.Point{
						Time:  t1,
						Value: 444,
					},
					Labels: map[string]string{
						types.LabelName: "node_cpu_seconds_global",
						"mode":          "irq",
					},
				},
				{
					Point: types.Point{
						Time:  t1,
						Value: 666,
					},
					Labels: map[string]string{
						types.LabelName: "node_cpu_seconds_global",
						"mode":          "user",
					},
				},
			},
		},
	}
	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			app := &mockAppendable{forceTS: t1}

			mgr := NewManager(context.Background(), tt.queryable, 10*time.Second)

			err := mgr.Collect(context.Background(), app.Appender(context.Background()))
			if err != nil {
				t.Error(err)
			}

			if diff := cmp.Diff(sortPoints(tt.want), sortPoints(app.points)); diff != "" {
				t.Errorf("points mismatch: (-want +got)\n%s", diff)
			}
		})
	}
}

func storeFromPoints(pts []types.MetricPoint) *store.Store {
	st := store.New(time.Hour)
	st.PushPoints(context.Background(), pts)

	return st
}

func sortPoints(metrics []types.MetricPoint) []types.MetricPoint {
	sort.Slice(metrics, func(i, j int) bool {
		lblsA := labels.FromMap(metrics[i].Labels)
		lblsB := labels.FromMap(metrics[j].Labels)

		switch {
		case metrics[i].Point.Time.Before(metrics[j].Point.Time):
			return true
		case metrics[i].Point.Time.After(metrics[j].Point.Time):
			return false
		default:
			return labels.Compare(lblsA, lblsB) < 0
		}
	})

	return metrics
}

func filterByDate(points []types.MetricPoint, cutoff time.Time) ([]types.MetricPoint, []types.MetricPoint) {
	var ready []types.MetricPoint

	i := 0

	for _, p := range points {
		if !p.Point.Time.After(cutoff) {
			ready = append(ready, p)

			continue
		}

		points[i] = p
		i++
	}

	points = points[:i]

	return ready, points
}

func makePoints(start time.Time, end time.Time, step time.Duration, template types.MetricPoint) []types.MetricPoint {
	result := make([]types.MetricPoint, 0, end.Sub(start)/step)

	for currentTime := start; currentTime.Before(end); currentTime = currentTime.Add(step) {
		p := template
		p.Point.Time = currentTime

		result = append(result, p)
	}

	return result
}

func Test_manager(t *testing.T) {
	const (
		resultName   = "copy_of_node_cpu_seconds_global"
		resultName2  = "copy_of_node_cpu_seconds_global2"
		sourceMetric = "node_cpu_seconds_global"
		promqlQuery  = "node_cpu_seconds_global"
	)

	ctx := context.Background()
	t0 := time.Now().Truncate(time.Second)
	okPoints := []types.MetricPoint{
		{
			Point: types.Point{
				Time:  t0,
				Value: 0,
			},
			Labels: map[string]string{
				types.LabelName: resultName,
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
				Time:  t0.Add(1 * time.Minute),
				Value: 0,
			},
			Labels: map[string]string{
				types.LabelName: resultName,
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
				Time:  t0.Add(2 * time.Minute),
				Value: 0,
			},
			Labels: map[string]string{
				types.LabelName: resultName,
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
				Time:  t0.Add(3 * time.Minute),
				Value: 0,
			},
			Labels: map[string]string{
				types.LabelName: resultName,
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
				Time:  t0.Add(4 * time.Minute),
				Value: 0,
			},
			Labels: map[string]string{
				types.LabelName: resultName,
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
				Time:  t0.Add(5 * time.Minute),
				Value: 0,
			},
			Labels: map[string]string{
				types.LabelName: resultName,
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
				Time:  t0.Add(6 * time.Minute),
				Value: 0,
			},
			Labels: map[string]string{
				types.LabelName: resultName,
			},
			Annotations: types.MetricAnnotations{
				Status: types.StatusDescription{
					CurrentStatus:     types.StatusOk,
					StatusDescription: "",
				},
			},
		},
	}
	warningPoints := []types.MetricPoint{
		{
			Point: types.Point{
				Time:  t0.Add(5 * time.Minute),
				Value: 1,
			},
			Labels: map[string]string{
				types.LabelName: resultName,
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
				Time:  t0.Add(6 * time.Minute),
				Value: 1,
			},
			Labels: map[string]string{
				types.LabelName: resultName,
			},
			Annotations: types.MetricAnnotations{
				Status: types.StatusDescription{
					CurrentStatus:     types.StatusWarning,
					StatusDescription: "",
				},
			},
		},
	}
	criticalPoints := []types.MetricPoint{
		{
			Point: types.Point{
				Time:  t0.Add(5 * time.Minute),
				Value: 2,
			},
			Labels: map[string]string{
				types.LabelName: resultName,
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
				Time:  t0.Add(6 * time.Minute),
				Value: 2,
			},
			Labels: map[string]string{
				types.LabelName: resultName,
			},
			Annotations: types.MetricAnnotations{
				Status: types.StatusDescription{
					CurrentStatus:     types.StatusCritical,
					StatusDescription: "",
				},
			},
		},
	}

	tests := []struct {
		Name        string
		Description string

		ScrapResolution time.Duration
		RunDuration     time.Duration
		Points          []types.MetricPoint
		Rules           []MetricAlertRule
		Want            []types.MetricPoint
	}{
		{
			Name:        "No points",
			Description: "No points in the store should not create any points.",
			Points:      []types.MetricPoint{},
			Rules: []MetricAlertRule{
				{
					Labels: labels.FromMap(map[string]string{
						types.LabelName: resultName,
					}),
					Threshold: threshold.Threshold{
						HighWarning:  50,
						HighCritical: 500,
						LowCritical:  math.NaN(),
						LowWarning:   math.NaN(),
					},
					PromQLQuery:       promqlQuery,
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
						Time:  t0.Add(-1 * time.Minute),
						Value: 25,
					},
					Labels: map[string]string{
						types.LabelName: sourceMetric,
					},
				},
				{
					Point: types.Point{
						Time:  t0.Add(3 * time.Minute),
						Value: 25,
					},
					Labels: map[string]string{
						types.LabelName: sourceMetric,
					},
				},
			},
			Rules: []MetricAlertRule{
				{
					Labels: labels.FromMap(map[string]string{
						types.LabelName: resultName,
					}),
					Threshold: threshold.Threshold{
						HighWarning:  50,
						HighCritical: 500,
						LowCritical:  math.NaN(),
						LowWarning:   math.NaN(),
					},
					PromQLQuery:       promqlQuery,
					IsUserPromQLAlert: false,
				},
			},
			Want: okPoints,
		},
		{ //nolint: dupl
			Name:        "Warning threshold crossed",
			Description: "Warning threshold crossed should create Warning points",
			Points: []types.MetricPoint{
				{
					Point: types.Point{
						Time:  t0.Add(-1 * time.Minute),
						Value: 120,
					},
					Labels: map[string]string{
						types.LabelName: sourceMetric,
					},
				},
				{
					Point: types.Point{
						Time:  t0.Add(4 * time.Minute),
						Value: 110,
					},
					Labels: map[string]string{
						types.LabelName: sourceMetric,
					},
				},
			},
			Rules: []MetricAlertRule{
				{
					Labels: labels.FromMap(map[string]string{
						types.LabelName: resultName,
					}),
					Threshold: threshold.Threshold{
						HighWarning:  50,
						HighCritical: 500,
						LowCritical:  math.NaN(),
						LowWarning:   math.NaN(),
					},
					PromQLQuery:       promqlQuery,
					IsUserPromQLAlert: true,
				},
			},
			Want: func() []types.MetricPoint {
				res := []types.MetricPoint{}

				res = append(res, okPoints[0:5]...)

				res = append(res, warningPoints...)

				return res
			}(),
		},
		{ //nolint: dupl
			Name:        "Critical threshold crossed",
			Description: "Critical threshold crossed should create Critical points",
			Points: []types.MetricPoint{
				{
					Point: types.Point{
						Time:  t0.Add(-1 * time.Minute),
						Value: 800,
					},
					Labels: map[string]string{
						types.LabelName: sourceMetric,
					},
				},
				{
					Point: types.Point{
						Time:  t0.Add(4 * time.Minute),
						Value: 1100,
					},
					Labels: map[string]string{
						types.LabelName: sourceMetric,
					},
				},
			},
			Rules: []MetricAlertRule{
				{
					Labels: labels.FromMap(map[string]string{
						types.LabelName: resultName,
					}),
					Threshold: threshold.Threshold{
						HighWarning:  50,
						HighCritical: 500,
						LowCritical:  math.NaN(),
						LowWarning:   math.NaN(),
					},
					PromQLQuery:       promqlQuery,
					IsUserPromQLAlert: true,
				},
			},
			Want: func() []types.MetricPoint {
				res := []types.MetricPoint{}

				res = append(res, okPoints[0:5]...)

				res = append(res, criticalPoints...)

				return res
			}(),
		},
		{
			Name:        "Critical threshold followed by warning",
			Description: "Critical threshold crossed for 5 minutes then warning should create a Critical point and Warning Points",
			Points: []types.MetricPoint{
				{
					Point: types.Point{
						Time:  t0.Add(-1 * time.Minute),
						Value: 800,
					},
					Labels: map[string]string{
						types.LabelName: sourceMetric,
					},
				},
				{
					Point: types.Point{
						Time:  t0.Add(4 * time.Minute),
						Value: 700,
					},
					Labels: map[string]string{
						types.LabelName: sourceMetric,
					},
				},
				{
					Point: types.Point{
						Time:  t0.Add(6 * time.Minute),
						Value: 130,
					},
					Labels: map[string]string{
						types.LabelName: sourceMetric,
					},
				},
			},
			Rules: []MetricAlertRule{
				{
					Labels: labels.FromMap(map[string]string{
						types.LabelName: resultName,
					}),
					Threshold: threshold.Threshold{
						HighWarning:  50,
						HighCritical: 500,
						LowCritical:  math.NaN(),
						LowWarning:   math.NaN(),
					},
					PromQLQuery:       promqlQuery,
					IsUserPromQLAlert: true,
				},
			},
			Want: func() []types.MetricPoint {
				res := []types.MetricPoint{}

				res = append(res, okPoints[0:5]...)
				res = append(res, criticalPoints[0])
				res = append(res, warningPoints[1])

				return res
			}(),
		},
		{
			Name:        "Threshold crossed for < 4min should create an ok point",
			Description: "Threshold not crossed for a minute then crossed for < 4min should create an ok point",
			Points: []types.MetricPoint{
				{
					Point: types.Point{
						Time:  t0.Add(-1 * time.Minute),
						Value: 20,
					},
					Labels: map[string]string{
						types.LabelName: sourceMetric,
					},
				},
				{
					Point: types.Point{
						Time:  t0.Add(4 * time.Minute),
						Value: 120,
					},
					Labels: map[string]string{
						types.LabelName: sourceMetric,
					},
				},
			},
			Rules: []MetricAlertRule{
				{
					Labels: labels.FromMap(map[string]string{
						types.LabelName: resultName,
					}),
					Threshold: threshold.Threshold{
						HighWarning:  50,
						HighCritical: 500,
						LowCritical:  math.NaN(),
						LowWarning:   math.NaN(),
					},
					PromQLQuery:       promqlQuery,
					IsUserPromQLAlert: false,
				},
			},
			Want: okPoints,
		},
		{
			Name:        "unnamed",
			Description: "",
			Points:      []types.MetricPoint{},
			Rules: []MetricAlertRule{
				{
					Labels: labels.FromMap(map[string]string{
						types.LabelName: resultName,
					}),
					Threshold: threshold.Threshold{
						HighWarning:  50,
						HighCritical: 500,
						LowCritical:  math.NaN(),
						LowWarning:   math.NaN(),
					},
					PromQLQuery:       promqlQuery,
					IsUserPromQLAlert: true,
				},
			},
			Want: func() []types.MetricPoint {
				res := make([]types.MetricPoint, 7)

				copy(res, okPoints)

				for i := range res {
					res[i].Point.Value = 3
					res[i].Annotations.Status.CurrentStatus = types.StatusUnknown
				}

				return res
			}(),
		},
		{
			Name:            "resolution-10s",
			Description:     "rule using 10s resolution",
			ScrapResolution: 10 * time.Second,
			RunDuration:     10 * time.Minute,
			Points: makePoints(
				t0.Add(-10*time.Minute),
				t0.Add(10*time.Minute),
				10*time.Second,
				types.MetricPoint{
					Point: types.Point{
						Time:  t0,
						Value: 20,
					},
					Labels: map[string]string{
						types.LabelName: sourceMetric,
					},
				},
			),
			Rules: []MetricAlertRule{
				{
					Labels: labels.FromMap(map[string]string{
						types.LabelName: resultName,
					}),
					Threshold: threshold.Threshold{
						HighWarning:  50,
						HighCritical: 500,
						LowCritical:  math.NaN(),
						LowWarning:   math.NaN(),
					},
					PromQLQuery:       promqlQuery,
					IsUserPromQLAlert: false,
					Resolution:        10 * time.Second,
				},
			},
			Want: makePoints(
				t0,
				t0.Add(10*time.Minute),
				10*time.Second,
				types.MetricPoint{
					Point: types.Point{
						Time:  t0,
						Value: 0,
					},
					Labels: map[string]string{
						types.LabelName: resultName,
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
			),
		},
		{
			Name:            "resolution-1m",
			Description:     "rule using 1 minute resolution",
			ScrapResolution: time.Minute,
			RunDuration:     10 * time.Minute,
			Points: makePoints(
				t0.Add(-10*time.Minute),
				t0.Add(10*time.Minute),
				time.Minute,
				types.MetricPoint{
					Point: types.Point{
						Time:  t0,
						Value: 20,
					},
					Labels: map[string]string{
						types.LabelName: sourceMetric,
					},
				},
			),
			Rules: []MetricAlertRule{
				{
					Labels: labels.FromMap(map[string]string{
						types.LabelName: resultName,
					}),
					Threshold: threshold.Threshold{
						HighWarning:  50,
						HighCritical: 500,
						LowCritical:  math.NaN(),
						LowWarning:   math.NaN(),
					},
					PromQLQuery:       promqlQuery,
					IsUserPromQLAlert: false,
					Resolution:        time.Minute,
				},
			},
			Want: makePoints(
				t0,
				t0.Add(10*time.Minute),
				time.Minute,
				types.MetricPoint{
					Point: types.Point{
						Time:  t0,
						Value: 0,
					},
					Labels: map[string]string{
						types.LabelName: resultName,
					},
					Annotations: types.MetricAnnotations{
						Status: types.StatusDescription{
							CurrentStatus:     types.StatusOk,
							StatusDescription: "",
						},
					},
				},
			),
		},
		{
			Name:            "resolution-both-resolution",
			Description:     "rule using 10s and 1 minute resolution",
			ScrapResolution: 10 * time.Second,
			RunDuration:     10 * time.Minute,
			Points: makePoints(
				t0.Add(-10*time.Minute),
				t0.Add(10*time.Minute),
				10*time.Second,
				types.MetricPoint{
					Point: types.Point{
						Time:  t0,
						Value: 20,
					},
					Labels: map[string]string{
						types.LabelName: sourceMetric,
					},
				},
			),
			Rules: []MetricAlertRule{
				{
					Labels: labels.FromMap(map[string]string{
						types.LabelName: resultName,
					}),
					Threshold: threshold.Threshold{
						HighWarning:  50,
						HighCritical: 500,
						LowCritical:  math.NaN(),
						LowWarning:   math.NaN(),
					},
					PromQLQuery:       promqlQuery,
					IsUserPromQLAlert: false,
					Resolution:        10 * time.Second,
				},
				{
					Labels: labels.FromMap(map[string]string{
						types.LabelName: resultName2,
					}),
					Threshold: threshold.Threshold{
						HighWarning:  50,
						HighCritical: 500,
						LowCritical:  math.NaN(),
						LowWarning:   math.NaN(),
					},
					PromQLQuery:       promqlQuery,
					IsUserPromQLAlert: false,
					Resolution:        time.Minute,
				},
			},
			Want: sortPoints(append(
				makePoints(
					t0,
					t0.Add(10*time.Minute),
					10*time.Second,
					types.MetricPoint{
						Point: types.Point{
							Time:  t0,
							Value: 0,
						},
						Labels: map[string]string{
							types.LabelName: resultName,
						},
						Annotations: types.MetricAnnotations{
							Status: types.StatusDescription{
								CurrentStatus:     types.StatusOk,
								StatusDescription: "",
							},
						},
					},
				),
				makePoints(
					t0,
					t0.Add(10*time.Minute),
					time.Minute,
					types.MetricPoint{
						Point: types.Point{
							Time:  t0,
							Value: 0,
						},
						Labels: map[string]string{
							types.LabelName: resultName2,
						},
						Annotations: types.MetricAnnotations{
							Status: types.StatusDescription{
								CurrentStatus:     types.StatusOk,
								StatusDescription: "",
							},
						},
					},
				)...,
			)),
		},
	}

	for _, test := range tests {
		test := test

		t.Run(test.Name, func(t *testing.T) {
			t.Parallel()

			var (
				resPoints []types.MetricPoint
				l         sync.Mutex
			)

			store := store.New(time.Hour)
			reg, err := registry.New(registry.Option{
				FQDN:        "example.com",
				GloutonPort: "8015",
				PushPoint: pushFunction(func(ctx context.Context, points []types.MetricPoint) {
					l.Lock()
					defer l.Unlock()

					resPoints = append(resPoints, points...)
				}),
			})
			if err != nil {
				t.Fatal(err)
			}

			reg.UpdateRelabelHook(ctx, func(ctx context.Context, labels map[string]string) (newLabel map[string]string, retryLater bool) {
				labels[types.LabelMetaBleemeoUUID] = agentID

				return labels, false
			})

			ruleManager := newManager(ctx, store, defaultLinuxRecordingRules, t0.Add(-7*time.Minute), 15*time.Second)

			err = ruleManager.RebuildAlertingRules(test.Rules)
			if err != nil {
				t.Error(err)
			}

			ruleManager.UpdateMetricResolution(10 * time.Second)

			id, err := reg.RegisterAppenderCallback(
				ctx,
				registry.RegistrationOption{
					NoLabelsAlteration:    true,
					DisablePeriodicGather: true,
				},
				registry.AppenderRegistrationOption{},
				ruleManager,
			)
			if err != nil {
				t.Fatal(err)
			}

			endAt := t0.Add(test.RunDuration)
			step := test.ScrapResolution

			if test.RunDuration == 0 {
				endAt = t0.Add(7 * time.Minute)
			}

			if test.ScrapResolution == 0 {
				step = time.Minute
			}

			pointsToPush := make([]types.MetricPoint, len(test.Points))
			copy(pointsToPush, test.Points)

			if test.Name != "resolution-both-resolution" {
				t.Skip()
			}

			for currentTime := t0; currentTime.Before(endAt); currentTime = currentTime.Add(step) {
				var ready []types.MetricPoint
				currentTime := currentTime
				ruleManager.now = func() time.Time { return currentTime }

				ready, pointsToPush = filterByDate(pointsToPush, currentTime)
				store.PushPoints(ctx, ready)

				reg.InternalRunScape(ctx, currentTime, id)
			}

			// Description are not fully tested, only the common prefix.
			// Completely testing them would require too much copy/paste in test.
			for i := range resPoints {
				if !strings.HasPrefix(resPoints[i].Annotations.Status.StatusDescription, "Current value:") &&
					resPoints[i].Annotations.Status.StatusDescription != "Current value is within the thresholds." &&
					!strings.HasPrefix(resPoints[i].Annotations.Status.StatusDescription, "PromQL read zero point") {
					t.Errorf("Got point was not formatted correctly: got %s, expected start with \"Current value:\"", resPoints[i].Annotations.Status.StatusDescription)
				}
				resPoints[i].Annotations.Status.StatusDescription = ""
			}

			if diff := cmp.Diff(test.Want, resPoints, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("result mismatch: (-want +got)\n%s", diff)
			}
		})
	}
}

func Test_Rebuild_Rules(t *testing.T) {
	const (
		resultName   = "my_rule_metric"
		sourceMetric = "node_cpu_seconds_global"
		promqlQuery  = "node_cpu_seconds_global"
	)

	var (
		resPoints []types.MetricPoint
		l         sync.Mutex
	)

	reg, err := registry.New(registry.Option{
		PushPoint: pushFunction(func(ctx context.Context, points []types.MetricPoint) {
			l.Lock()
			defer l.Unlock()

			resPoints = append(resPoints, points...)
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	store := store.New(time.Hour)
	ctx := context.Background()
	t1 := time.Now().Truncate(time.Second)
	t0 := t1.Add(-7 * time.Minute)
	ruleManager := newManager(ctx, store, defaultLinuxRecordingRules, t0, 15*time.Second)

	store.PushPoints(context.Background(), []types.MetricPoint{
		{
			Point: types.Point{
				Time:  t0.Add(-1 * time.Second),
				Value: 700,
			},
			Labels: map[string]string{
				types.LabelName: sourceMetric,
			},
		},
		{
			Point: types.Point{
				Time:  t0.Add(4 * time.Minute),
				Value: 700,
			},
			Labels: map[string]string{
				types.LabelName: sourceMetric,
			},
		},
		{
			Point: types.Point{
				Time:  t0.Add(8 * time.Minute),
				Value: 700,
			},
			Labels: map[string]string{
				types.LabelName: sourceMetric,
			},
		},
		{
			Point: types.Point{
				Time:  t0.Add(11 * time.Minute),
				Value: 700,
			},
			Labels: map[string]string{
				types.LabelName: sourceMetric,
			},
		},
	})

	want := []types.MetricPoint{
		{
			Point: types.Point{
				Time:  t1,
				Value: 0,
			},
			Labels: map[string]string{
				types.LabelName: resultName,
			},
			Annotations: types.MetricAnnotations{
				Status: types.StatusDescription{
					CurrentStatus:     types.StatusOk,
					StatusDescription: "Current value: 700.00. Threshold (50.00) not exceeded for the last " + promAlertTime.String(),
				},
			},
		},
		{
			Point: types.Point{
				Time:  t1.Add(time.Minute),
				Value: 0,
			},
			Labels: map[string]string{
				types.LabelName: resultName,
			},
			Annotations: types.MetricAnnotations{
				Status: types.StatusDescription{
					CurrentStatus:     types.StatusOk,
					StatusDescription: "Current value: 700.00. Threshold (50.00) not exceeded for the last " + promAlertTime.String(),
				},
			},
		},
		{
			Point: types.Point{
				Time:  t1.Add(2 * time.Minute),
				Value: 0,
			},
			Labels: map[string]string{
				types.LabelName: resultName,
			},
			Annotations: types.MetricAnnotations{
				Status: types.StatusDescription{
					CurrentStatus:     types.StatusOk,
					StatusDescription: "Current value: 700.00. Threshold (50.00) not exceeded for the last " + promAlertTime.String(),
				},
			},
		},

		{
			Point: types.Point{
				Time:  t1.Add(3 * time.Minute),
				Value: 0,
			},
			Labels: map[string]string{
				types.LabelName: resultName,
			},
			Annotations: types.MetricAnnotations{
				Status: types.StatusDescription{
					CurrentStatus:     types.StatusOk,
					StatusDescription: "Current value: 700.00. Threshold (50.00) not exceeded for the last " + promAlertTime.String(),
				},
			},
		},
		{
			Point: types.Point{
				Time:  t1.Add(4 * time.Minute),
				Value: 0,
			},
			Labels: map[string]string{
				types.LabelName: resultName,
			},
			Annotations: types.MetricAnnotations{
				Status: types.StatusDescription{
					CurrentStatus:     types.StatusOk,
					StatusDescription: "Current value: 700.00. Threshold (50.00) not exceeded for the last " + promAlertTime.String(),
				},
			},
		},
		// {
		// 	Point: types.Point{
		// 		Time:  t1.Add(5 * time.Minute),
		// 		Value: 0,
		// 	},
		// 	Annotations: types.MetricAnnotations{
		// 		Status: types.StatusDescription{
		// 			CurrentStatus:     types.StatusOk,
		// 			StatusDescription: "Current value: 700.00. Threshold (50.00) not exceeded for the last " + promAlertTime.String(),
		// 		},
		// 	},
		// },
		{
			Point: types.Point{
				Time:  t1.Add(5 * time.Minute),
				Value: 2,
			},
			Labels: map[string]string{
				types.LabelName: resultName,
			},
			Annotations: types.MetricAnnotations{
				Status: types.StatusDescription{
					CurrentStatus:     types.StatusCritical,
					StatusDescription: "Current value: 700.00. Threshold (50.00) exceeded for the last " + promAlertTime.String(),
				},
			},
		},
	}

	alertsRules := []MetricAlertRule{
		{
			Labels: labels.FromMap(map[string]string{
				types.LabelName: resultName,
			}),
			Threshold: threshold.Threshold{
				HighWarning:  50,
				HighCritical: 500,
				LowCritical:  math.NaN(),
				LowWarning:   math.NaN(),
			},
			PromQLQuery:       promqlQuery,
			IsUserPromQLAlert: true,
		},
		// {
		// 	ID:         "CPU-ID",
		// 	LabelsText: "cpu_counter",
		// 	Threshold: bleemeoTypes.Threshold{
		// 		HighWarning:  &thresholds[0],
		// 		HighCritical: &thresholds[1],
		// 	},
		// 	PromQLQuery:       "cpu_counter",
		// 	IsUserPromQLAlert: false,
		// },
	}

	id, err := reg.RegisterAppenderCallback(
		context.Background(),
		registry.RegistrationOption{
			NoLabelsAlteration:    true,
			DisablePeriodicGather: true,
		},
		registry.AppenderRegistrationOption{},
		ruleManager,
	)
	if err != nil {
		t.Fatal(err)
	}

	err = ruleManager.RebuildAlertingRules(alertsRules)
	if err != nil {
		t.Error(err)

		return
	}

	logger.V(0).Printf("BASE TIME: %v", t1)

	for i := 0; i < 5; i++ {
		ruleManager.now = func() time.Time { return t1.Add(time.Duration(i) * time.Minute) }

		reg.InternalRunScape(ctx, t1.Add(time.Duration(i)*time.Minute), id)
	}

	err = ruleManager.RebuildAlertingRules(alertsRules)
	if err != nil {
		t.Error(err)

		return
	}

	// this call to run should create another point.
	// By doing so we can verify multiple calls to RebuildAlertingRules won't reset rules state.
	ruleManager.now = func() time.Time { return t1.Add(5 * time.Minute) }

	reg.InternalRunScape(ctx, t1.Add(5*time.Minute), id)

	if len(ruleManager.alertingRules) != len(alertsRules) {
		t.Errorf("Unexpected number of points: expected %d, got %d\n", len(alertsRules), len(ruleManager.alertingRules))
	}

	if diff := cmp.Diff(want, resPoints); diff != "" {
		t.Errorf("RebuildRules mismatch (-want +got)\n%s", diff)
	}
}

// This test is handling cases where on glouton start we have alert rules
// already in the pending state (value already exceeded threshold).
// We should NOT send Ok points for the first 5 minutes, as to make sure Prometheus
// can properly evaluate rules and their actual state.
func Test_GloutonStart(t *testing.T) {
	const (
		resultName   = "my_rule_metric"
		sourceMetric = "cpu_used"
		promqlQuery  = "cpu_used"
	)

	store := store.New(time.Hour)
	ctx := context.Background()
	t0 := time.Now().Truncate(time.Second)
	ruleManager := newManager(ctx, store, defaultLinuxRecordingRules, t0, 15*time.Second)
	resPoints := []types.MetricPoint{}

	store.PushPoints(context.Background(), []types.MetricPoint{
		{
			Point: types.Point{
				Time:  t0,
				Value: 700,
			},
			Labels: map[string]string{
				types.LabelName: sourceMetric,
			},
		},
		{
			Point: types.Point{
				Time:  t0.Add(2 * time.Minute),
				Value: 800,
			},
			Labels: map[string]string{
				types.LabelName: sourceMetric,
			},
		},
		{
			Point: types.Point{
				Time:  t0.Add(3 * time.Minute),
				Value: 800,
			},
			Labels: map[string]string{
				types.LabelName: sourceMetric,
			},
		},
		{
			Point: types.Point{
				Time:  t0.Add(4 * time.Minute),
				Value: 800,
			},
			Labels: map[string]string{
				types.LabelName: sourceMetric,
			},
		},
	})

	metricList := []MetricAlertRule{
		{
			Labels: labels.FromMap(map[string]string{
				types.LabelName: resultName,
			}),
			Threshold: threshold.Threshold{
				HighWarning:  50,
				HighCritical: 500,
				LowCritical:  math.NaN(),
				LowWarning:   math.NaN(),
			},
			PromQLQuery:       promqlQuery,
			IsUserPromQLAlert: true,
		},
	}

	store.AddNotifiee(func(mp []types.MetricPoint) {
		resPoints = append(resPoints, mp...)
	})

	err := ruleManager.RebuildAlertingRules(metricList)
	if err != nil {
		t.Error(err)
	}

	for i := 0; i < 6; i++ {
		ruleManager.now = func() time.Time { return t0.Add(time.Duration(i) * time.Minute) }

		if err := ruleManager.Collect(ctx, store.Appender(ctx)); err != nil {
			t.Error(err)
		}
	}

	// Manager should not create ok points for the next 5 minutes after start,
	// as we do not provide a way for prometheus to know previous values before start.
	// This test should be changed in the future if we implement a persistent store,
	// as critical and warning points would be allowed.
	if len(resPoints) != 0 {
		t.Errorf("Unexpected number of points generated: expected 0, got %d:\n%v", len(resPoints), resPoints)
	}

	ruleManager.now = func() time.Time { return t0.Add(time.Duration(7) * time.Minute) }

	err = ruleManager.Collect(ctx, store.Appender(ctx))
	if err != nil {
		t.Error(err)
	}

	if len(resPoints) == 0 {
		t.Errorf("Unexpected number of points generated: expected >0, got 0:\n%v", resPoints)
	}
}

// Test that metrics won't temporary change status on Glouton restart.
// This test verify that a alert on metric like "cpu_used > 1" (assuming cpu_used is always more than 1%)
// start in warning/critical and never send any Ok because the Prometheus rule is in pending 5 minutes
// after startup.
// This test mostly do the same as Test_GloutonStart, but with more realistic scenario.
func Test_NoStatutsChangeOnStart(t *testing.T) {
	const (
		resultName   = "copy_of_node_cpu_seconds_global"
		sourceMetric = "node_cpu_seconds_global"
		promqlQuery  = "node_cpu_seconds_global"
	)

	for _, resolutionSecond := range []int{10, 30, 60} {
		t.Run(fmt.Sprintf("resolution=%d", resolutionSecond), func(t *testing.T) {
			var (
				resPoints []types.MetricPoint
				l         sync.Mutex
			)

			store := store.New(time.Hour)
			reg, err := registry.New(registry.Option{
				PushPoint: pushFunction(func(ctx context.Context, points []types.MetricPoint) {
					l.Lock()
					defer l.Unlock()

					resPoints = append(resPoints, points...)
				}),
			})
			if err != nil {
				t.Fatal(err)
			}

			ctx := context.Background()
			t0 := time.Now().Truncate(time.Second)

			// we always boot the manager with 10 seconds resolution
			ruleManager := newManager(ctx, store, defaultLinuxRecordingRules, t0, 10*time.Second)

			metricList := []MetricAlertRule{
				{
					Labels: labels.FromMap(map[string]string{
						types.LabelName: resultName,
					}),
					Threshold: threshold.Threshold{
						HighWarning:  0,
						HighCritical: 100,
						LowCritical:  math.NaN(),
						LowWarning:   math.NaN(),
					},
					PromQLQuery:       promqlQuery,
					IsUserPromQLAlert: true,
					InstanceUUID:      agentID,
					Resolution:        time.Duration(resolutionSecond) * time.Second,
				},
			}

			err = ruleManager.RebuildAlertingRules(metricList)
			if err != nil {
				t.Error(err)
			}

			ruleManager.UpdateMetricResolution(time.Duration(resolutionSecond) * time.Second)

			id, err := reg.RegisterAppenderCallback(
				context.Background(),
				registry.RegistrationOption{
					NoLabelsAlteration:    true,
					DisablePeriodicGather: true,
				},
				registry.AppenderRegistrationOption{},
				ruleManager,
			)
			if err != nil {
				t.Fatal(err)
			}

			for currentTime := t0; currentTime.Before(t0.Add(7 * time.Minute)); currentTime = currentTime.Add(time.Second * time.Duration(resolutionSecond)) {
				if !currentTime.Equal(t0) {
					// cpu_used need two gather to be calculated, skip first point.
					store.PushPoints(context.Background(), []types.MetricPoint{
						{
							Point: types.Point{
								Time:  currentTime,
								Value: 30,
							},
							Labels: map[string]string{
								types.LabelName:         sourceMetric,
								types.LabelInstanceUUID: agentID,
							},
						},
					})
				}

				if currentTime.Sub(t0) > 6*time.Minute {
					logger.V(0).Printf("Number of points: %d", len(resPoints))
				}

				ruleManager.now = func() time.Time { return currentTime }
				reg.InternalRunScape(ctx, currentTime, id)
			}

			var hadResult bool

			// Manager should not create ok points since the metric is always in critical.
			// This test might be changed in the future if we implement a persistent store,
			// as it would allow to known the exact hold state of the Prometheus rule.
			for _, p := range resPoints {
				if p.Labels[types.LabelName] != resultName {
					t.Errorf("unexpected point with labels: %v", p.Labels)

					continue
				}

				if p.Annotations.Status.CurrentStatus == types.StatusWarning {
					hadResult = true

					continue
				}

				t.Errorf("point status = %v want %v", p.Annotations.Status.CurrentStatus, types.StatusWarning)
			}

			if !hadResult {
				t.Errorf("rule never returned any points")
			}
		})
	}
}

// Test_NoCrossRead checks that we don't read metric from another instance_uuid.
// Glouton could measure metrics for multiple agent, for example for the normal/"main" agent and for SNMP agents.
// We want alert to run only on one set of metrics. Since instance_uuid should always be set in such case, we use
// this label for filtering.
func Test_NoCrossRead(t *testing.T) {
	const (
		resultName   = "copy_of_node_cpu_seconds_global"
		sourceMetric = "node_cpu_seconds_global"
		promqlQuery  = "node_cpu_seconds_global"
	)

	var (
		resPoints []types.MetricPoint
		l         sync.Mutex
	)

	store := store.New(time.Hour)

	reg, err := registry.New(registry.Option{
		PushPoint: pushFunction(func(ctx context.Context, points []types.MetricPoint) {
			l.Lock()
			defer l.Unlock()

			resPoints = append(resPoints, points...)
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	t0 := time.Now().Truncate(time.Second)

	// we always boot the manager with 10 seconds resolution
	ruleManager := newManager(ctx, store, defaultLinuxRecordingRules, t0, 10*time.Second)

	metricList := []MetricAlertRule{
		{
			Labels: labels.FromMap(map[string]string{
				types.LabelName: resultName,
			}),
			Threshold: threshold.Threshold{
				HighWarning:  0,
				HighCritical: 100,
			},
			PromQLQuery:       promqlQuery,
			InstanceUUID:      agentID,
			Resolution:        10 * time.Second,
			IsUserPromQLAlert: true,
		},
	}

	err = ruleManager.RebuildAlertingRules(metricList)
	if err != nil {
		t.Error(err)
	}

	ruleManager.UpdateMetricResolution(10 * time.Second)

	id, err := reg.RegisterAppenderCallback(
		context.Background(),
		registry.RegistrationOption{
			NoLabelsAlteration:    true,
			DisablePeriodicGather: true,
		},
		registry.AppenderRegistrationOption{},
		ruleManager,
	)
	if err != nil {
		t.Fatal(err)
	}

	for currentTime := t0; currentTime.Before(t0.Add(10 * time.Minute)); currentTime = currentTime.Add(10 * time.Second) {
		if !currentTime.Equal(t0) {
			// cpu_used need two gather to be calculated, skip first point.
			store.PushPoints(context.Background(), []types.MetricPoint{
				{
					Point: types.Point{
						Time:  currentTime,
						Value: 30,
					},
					Labels: map[string]string{
						types.LabelName: sourceMetric,
					},
				},
				{
					Point: types.Point{
						Time:  currentTime,
						Value: 30,
					},
					Labels: map[string]string{
						types.LabelName:         sourceMetric,
						types.LabelInstanceUUID: "not-agentID",
					},
				},
			})
		}

		ruleManager.now = func() time.Time { return currentTime }

		reg.InternalRunScape(ctx, currentTime, id)
	}

	var hadResult bool

	for _, p := range resPoints {
		if p.Labels[types.LabelName] != resultName {
			t.Errorf("unexpected point with labels: %v", p.Labels)

			continue
		}

		if p.Annotations.Status.CurrentStatus == types.StatusUnknown {
			hadResult = true

			continue
		}

		if p.Annotations.Status.CurrentStatus != types.StatusUnknown {
			t.Errorf("point status = %v want %v", p.Annotations.Status.CurrentStatus, types.StatusUnknown)
		}
	}

	if !hadResult {
		t.Errorf("rule never returned any points")
	}
}

// Test_NoUnknownOnStart checks that we don't emit wrong unknown status on Glouton start.
// On start, if the source of the metrics is not yet gatherer (e.g. it need multiple gather, its period is 60 seconds...)
// the rule may read zero points and then yield a unknown status with "PromQL reads zero points" message.
// We don't want this on startup and give an extra-grace time for such error on startup.
func Test_NoUnknownOnStart(t *testing.T) {
	const (
		resultName   = "copy_of_node_cpu_seconds_global"
		sourceMetric = "node_cpu_seconds_global"
		promqlQuery  = "node_cpu_seconds_global"
	)

	var (
		resPoints []types.MetricPoint
		l         sync.Mutex
	)

	store := store.New(time.Hour)

	reg, err := registry.New(registry.Option{
		PushPoint: pushFunction(func(ctx context.Context, points []types.MetricPoint) {
			l.Lock()
			defer l.Unlock()

			resPoints = append(resPoints, points...)
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	t0 := time.Now().Truncate(time.Second)

	// we always boot the manager with 10 seconds resolution
	ruleManager := newManager(ctx, store, defaultLinuxRecordingRules, t0, 10*time.Second)

	metricList := []MetricAlertRule{
		{
			Labels: labels.FromMap(map[string]string{
				types.LabelName: resultName,
			}),
			Threshold: threshold.Threshold{
				HighWarning:  0,
				HighCritical: 100,
			},
			PromQLQuery:       promqlQuery,
			IsUserPromQLAlert: true,
		},
	}

	err = ruleManager.RebuildAlertingRules(metricList)
	if err != nil {
		t.Error(err)
	}

	ruleManager.UpdateMetricResolution(10 * time.Second)

	id, err := reg.RegisterAppenderCallback(
		context.Background(),
		registry.RegistrationOption{
			NoLabelsAlteration:    true,
			DisablePeriodicGather: true,
		},
		registry.AppenderRegistrationOption{},
		ruleManager,
	)
	if err != nil {
		t.Fatal(err)
	}

	for currentTime := t0; currentTime.Before(t0.Add(9 * time.Minute)); currentTime = currentTime.Add(10 * time.Second) {
		if currentTime.After(t0.Add(1 * time.Minute)) {
			// Took one full minute before first points.
			store.PushPoints(context.Background(), []types.MetricPoint{
				{
					Point: types.Point{
						Time:  currentTime,
						Value: 30,
					},
					Labels: map[string]string{
						types.LabelName: sourceMetric,
					},
				},
			})
		}

		ruleManager.now = func() time.Time { return currentTime }

		reg.InternalRunScape(ctx, currentTime, id)
	}

	var hadResult bool

	for _, p := range resPoints {
		if p.Labels[types.LabelName] != resultName {
			t.Errorf("unexpected point with labels: %v", p.Labels)

			continue
		}

		if p.Annotations.Status.CurrentStatus == types.StatusWarning {
			hadResult = true

			continue
		}

		if p.Annotations.Status.CurrentStatus == types.StatusUnknown {
			t.Errorf("point status = %v want %v", p.Annotations.Status.CurrentStatus, types.StatusWarning)
		}
	}

	if !hadResult {
		t.Errorf("rule never returned any points")
	}
}

type pushFunction func(ctx context.Context, points []types.MetricPoint)

func (f pushFunction) PushPoints(ctx context.Context, points []types.MetricPoint) {
	f(ctx, points)
}
