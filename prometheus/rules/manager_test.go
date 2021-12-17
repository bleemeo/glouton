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
	"glouton/store"
	"glouton/types"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/prometheus/prometheus/pkg/exemplar"
	"github.com/prometheus/prometheus/pkg/labels"
	"github.com/prometheus/prometheus/storage"
)

var errNotImplemented = errors.New("not implemented")

type mockAppendable struct {
	points []types.MetricPoint
	l      sync.Mutex
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

func (a *mockAppender) Append(ref uint64, l labels.Labels, t int64, v float64) (uint64, error) {
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

func (a *mockAppender) AppendExemplar(ref uint64, l labels.Labels, e exemplar.Exemplar) (uint64, error) {
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

			app := &mockAppendable{}

			mgr := NewManager(context.Background(), tt.queryable, app)
			mgr.Run(t1)

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

		return labels.Compare(lblsA, lblsB) < 0
	})

	return metrics
}