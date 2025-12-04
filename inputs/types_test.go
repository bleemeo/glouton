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

package inputs

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bleemeo/glouton/types"
)

type mockStore struct {
	points []types.MetricPoint
}

func (s *mockStore) PushPoints(_ context.Context, points []types.MetricPoint) {
	s.points = append(s.points, points...)
}

func (s *mockStore) getByName(name string) []types.MetricPoint {
	result := make([]types.MetricPoint, 0, 1)

	for _, p := range s.points {
		if p.Labels[types.LabelName] == name {
			result = append(result, p)
		}
	}

	return result
}

func TestAccumulator(t *testing.T) {
	t0 := time.Now()
	fields := map[string]any{
		"fieldFloat":  42.6,
		"fieldInt":    -42,
		"fieldUint64": uint64(42),
	}
	tags := map[string]string{
		"tag1": "value1",
		"item": "/home",
	}

	db := &mockStore{}
	acc := &Accumulator{Pusher: db, Context: t.Context()}

	if len(db.points) != 0 {
		t.Errorf("len(db.points) == %v, want %v", len(db.points), 0)
	}

	acc.AddFields(
		"measurement",
		fields,
		tags,
		t0,
	)

	if len(db.points) != len(fields) {
		t.Errorf("len(db.metrics) == %v, want %v", len(db.points), len(fields))
	}

	for _, points := range db.points {
		labels := points.Labels
		name := labels[types.LabelName]

		if !strings.HasPrefix(name, "measurement_") {
			t.Errorf("name == %v, want measurement_*", name)
		}

		if _, ok := fields[name[len("measurement_"):]]; !ok {
			t.Errorf("fields[%v] == nil, want it to exists", name)
		}

		tags[types.LabelName] = name
		if !reflect.DeepEqual(labels, tags) {
			t.Errorf("m.Labels() = %v, want %v", labels, tags)
		}
	}

	for k, v := range fields {
		name := "measurement_" + k

		metrics := db.getByName(name)
		if len(metrics) != 1 {
			t.Errorf("len(db.Metrics(__name__=%v)) == %v, want %v", name, len(metrics), 1)
		}

		m := metrics[0]

		labels := m.Labels
		if labels[types.LabelName] != name {
			t.Errorf("labels[__name__] == %v, want %v", labels[types.LabelName], name)
		}

		tags[types.LabelName] = name
		if !reflect.DeepEqual(labels, tags) {
			t.Errorf("db.Metrics(__name__=%v).Labels() = %v, want %v", name, labels, tags)
		}

		point := m.Point
		vFloat, _ := ConvertToFloat(v)
		want := types.Point{Time: t0, Value: vFloat}

		if !reflect.DeepEqual(point, want) {
			t.Errorf("db.Metrics(__name__=%v).Points(...)[0] == %v, want %v", name, point, want)
		}
	}
}

func TestStoreAccumulatorWithStatus(t *testing.T) {
	t0 := time.Now()
	fields1 := map[string]any{
		"system": 16.0,
		"idle":   3.0,
	}
	fields2 := map[string]any{
		"used": 97.0,
	}
	fields3 := map[string]any{
		"user": 81.0,
	}
	fields4 := map[string]any{
		"user_status": 1.0,
	}
	statusUsed := types.StatusDescription{CurrentStatus: types.StatusCritical, StatusDescription: "CPU 97%"}
	statusUser := types.StatusDescription{CurrentStatus: types.StatusWarning, StatusDescription: "CPU 81%"}
	annotations1 := types.MetricAnnotations{}
	annotations2 := types.MetricAnnotations{
		Status: statusUsed,
	}
	annotations3 := types.MetricAnnotations{
		Status: statusUser,
	}
	annotations4 := types.MetricAnnotations{
		Status:   statusUser,
		StatusOf: "cpu_user",
	}

	tags := map[string]string{
		"tag1": "value1",
		"item": "/home",
	}
	want := map[string]float64{
		"cpu_used":        97.0,
		"cpu_user":        81.0,
		"cpu_system":      16.0,
		"cpu_idle":        3.0,
		"cpu_user_status": 1.0,
	}
	wantStatus := map[string]types.StatusDescription{
		"cpu_used":        statusUsed,
		"cpu_user":        statusUser,
		"cpu_user_status": statusUser,
	}

	db := &mockStore{}
	acc := &Accumulator{Pusher: db, Context: t.Context()}

	if len(db.points) != 0 {
		t.Errorf("len(db.metrics) == %v, want %v", len(db.points), 0)
	}

	acc.AddFieldsWithAnnotations(
		"cpu",
		fields1,
		tags,
		annotations1,
		t0,
	)
	acc.AddFieldsWithAnnotations(
		"cpu",
		fields2,
		tags,
		annotations2,
		t0,
	)
	acc.AddFieldsWithAnnotations(
		"cpu",
		fields3,
		tags,
		annotations3,
		t0,
	)
	acc.AddFieldsWithAnnotations(
		"cpu",
		fields4,
		tags,
		annotations4,
		t0,
	)

	if len(db.points) != len(want) {
		t.Errorf("len(db.points) == %v, want %v", len(db.points), len(want))
	}

	for name, v := range want {
		metrics := db.getByName(name)
		if len(metrics) != 1 {
			t.Errorf("len(db.Metrics(__name__=%v)) == %v, want %v", name, len(metrics), 1)
		}

		m := metrics[0]
		labels := m.Labels
		annotations := m.Annotations

		if labels[types.LabelName] != name {
			t.Errorf("labels[__name__] == %v, want %v", labels[types.LabelName], name)
		}

		if strippedName, found := strings.CutSuffix(name, "_status"); found {
			if annotations.StatusOf != strippedName {
				t.Errorf("annotations.StatusOf == %v, want %v", annotations.StatusOf, strippedName)
			}
		}

		delete(labels, types.LabelName)

		if !reflect.DeepEqual(labels, tags) {
			t.Errorf("db.Metrics(__name__=%v).Labels() = %v, want %v", name, labels, tags)
		}

		point := m.Point
		vFloat, _ := ConvertToFloat(v)
		want := types.Point{Time: t0, Value: vFloat}

		if !reflect.DeepEqual(point, want) {
			t.Errorf("db.Metrics(__name__=%v).Points(...)[0] == %v, want %v", name, point, want)
		}

		if st, ok := wantStatus[name]; ok {
			if !reflect.DeepEqual(annotations.Status, st) {
				t.Errorf("db.Metrics(__name__=%v).Status == %v, want %v", name, annotations.Status, st)
			}
		} else if annotations.Status.CurrentStatus.IsSet() {
			t.Errorf("db.Metrics(__name__=%v).StatusDescription == %v, want none", name, annotations.Status)
		}
	}
}
