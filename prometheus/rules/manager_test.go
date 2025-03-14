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

package rules

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/bleemeo/glouton/prometheus/registry"
	"github.com/bleemeo/glouton/store"
	"github.com/bleemeo/glouton/types"

	"github.com/google/go-cmp/cmp"
	"github.com/prometheus/prometheus/model/exemplar"
	"github.com/prometheus/prometheus/model/histogram"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/model/metadata"
	"github.com/prometheus/prometheus/storage"
)

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

func (app *mockAppendable) Appender(context.Context) storage.Appender {
	return &mockAppender{
		parent: app,
	}
}

func (a *mockAppender) Append(_ storage.SeriesRef, l labels.Labels, t int64, v float64) (storage.SeriesRef, error) {
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

func (a *mockAppender) AppendExemplar(storage.SeriesRef, labels.Labels, exemplar.Exemplar) (storage.SeriesRef, error) {
	return 0, errNotImplemented
}

func (a *mockAppender) AppendHistogram(storage.SeriesRef, labels.Labels, int64, *histogram.Histogram, *histogram.FloatHistogram) (storage.SeriesRef, error) {
	return 0, errNotImplemented
}

func (a *mockAppender) UpdateMetadata(storage.SeriesRef, labels.Labels, metadata.Metadata) (storage.SeriesRef, error) {
	return 0, errNotImplemented
}

func (a *mockAppender) AppendCTZeroSample(_ storage.SeriesRef, _ labels.Labels, _, _ int64) (storage.SeriesRef, error) {
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
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			app := &mockAppendable{forceTS: t1}

			mgr := NewManager(t.Context(), tt.queryable, nil)

			err := mgr.CollectWithState(t.Context(), registry.GatherState{T0: time.Now()}, app.Appender(t.Context()))
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
	st := store.New("test store", time.Hour, time.Hour)
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
