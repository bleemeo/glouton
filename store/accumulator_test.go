package store

import (
	"agentgo/types"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestStoreAccumulator(t *testing.T) {
	t0 := time.Now()
	fields := map[string]interface{}{
		"fieldFloat":  42.6,
		"fieldInt":    -42,
		"fieldUint64": uint64(42),
	}
	tags := map[string]string{
		"tag1": "value1",
		"item": "/home",
	}

	db := New()
	acc := db.Accumulator()

	if len(db.metrics) != 0 {
		t.Errorf("len(db.metrics) == %v, want %v", len(db.metrics), 0)
	}
	acc.AddFields(
		"measurement",
		fields,
		tags,
		t0,
	)

	if len(db.metrics) != len(fields) {
		t.Errorf("len(db.metrics) == %v, want %v", len(db.metrics), len(fields))
	}
	allMetrics, err := db.Metrics(nil)
	if err != nil {
		t.Errorf("db.Metrics(nil) raise err == %v", err)
	}
	if len(allMetrics) != len(fields) {
		t.Errorf("len(allMetrics) == %v, want %v", len(allMetrics), len(fields))
	}

	for _, m := range allMetrics {
		labels := m.Labels()
		name := labels["__name__"]
		if !strings.HasPrefix(name, "measurement_") {
			t.Errorf("name == %v, want measurement_*", name)
		}
		if _, ok := fields[name[len("measurement_"):]]; !ok {
			t.Errorf("fields[%v] == nil, want it to exists", name)
		}
		delete(labels, "__name__")
		if !reflect.DeepEqual(labels, tags) {
			t.Errorf("m.Labels() = %v, want %v", labels, tags)
		}
	}

	for k, v := range fields {
		name := "measurement_" + k
		metrics, err := db.Metrics(map[string]string{"__name__": name})
		if err != nil {
			t.Errorf("db.Metrics(__name__=%v) raise err == %v", name, err)
		}
		if len(metrics) != 1 {
			t.Errorf("len(db.Metrics(__name__=%v)) == %v, want %v", name, len(metrics), 1)
		}
		m := metrics[0]
		labels := m.Labels()
		if labels["__name__"] != name {
			t.Errorf("labels[__name__] == %v, want %v", labels["__name__"], name)
		}
		delete(labels, "__name__")
		if !reflect.DeepEqual(labels, tags) {
			t.Errorf("db.Metrics(__name__=%v).Labels() = %v, want %v", name, labels, tags)
		}
		points, err := m.Points(t0, t0)
		if err != nil {
			t.Errorf("db.Metrics(__name__=%v).Points(...) raise err == %v", name, err)
		}
		if len(points) != 1 {
			t.Errorf("len(db.Metrics(__name__=%v).Points(...)) == %v, want %v", name, len(points), 1)
		}
		vFloat, _ := convertInterface(v)
		want := types.Point{Time: t0, Value: vFloat}
		if !reflect.DeepEqual(points[0].Point, want) {
			t.Errorf("db.Metrics(__name__=%v).Points(...)[0] == %v, want %v", name, points[0].Point, want)
		}
	}
}

func TestStoreAccumulatorWithStatus(t *testing.T) {
	t0 := time.Now()
	fields := map[string]interface{}{
		"used":   97.0,
		"user":   81.0,
		"system": 16.0,
		"idle":   3.0,
	}
	statuses := map[string]types.StatusDescription{
		"used": {CurrentStatus: types.StatusCritical, StatusDescription: "CPU 97%"},
		"user": {CurrentStatus: types.StatusWarning, StatusDescription: "CPU 81%"},
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
		"cpu_used_status": 2.0,
		"cpu_user_status": 1.0,
	}
	wantStatus := map[string]types.StatusDescription{
		"cpu_used":        statuses["used"],
		"cpu_user":        statuses["user"],
		"cpu_used_status": statuses["used"],
		"cpu_user_status": statuses["user"],
	}

	db := New()
	acc := db.Accumulator()

	if len(db.metrics) != 0 {
		t.Errorf("len(db.metrics) == %v, want %v", len(db.metrics), 0)
	}
	acc.AddFieldsWithStatus(
		"cpu",
		fields,
		tags,
		statuses,
		true,
		t0,
	)

	allMetrics, err := db.Metrics(nil)
	if err != nil {
		t.Errorf("db.Metrics(nil) raise err == %v", err)
	}
	if len(allMetrics) != len(want) {
		t.Errorf("len(allMetrics) == %v, want %v", len(allMetrics), len(want))
	}

	for name, v := range want {
		metrics, err := db.Metrics(map[string]string{"__name__": name})
		if err != nil {
			t.Errorf("db.Metrics(__name__=%v) raise err == %v", name, err)
		}
		if len(metrics) != 1 {
			t.Errorf("len(db.Metrics(__name__=%v)) == %v, want %v", name, len(metrics), 1)
		}
		m := metrics[0]
		labels := m.Labels()
		if labels["__name__"] != name {
			t.Errorf("labels[__name__] == %v, want %v", labels["__name__"], name)
		}
		delete(labels, "__name__")
		if !reflect.DeepEqual(labels, tags) {
			t.Errorf("db.Metrics(__name__=%v).Labels() = %v, want %v", name, labels, tags)
		}
		points, err := m.Points(t0, t0)
		if err != nil {
			t.Errorf("db.Metrics(__name__=%v).Points(...) raise err == %v", name, err)
		}
		if len(points) != 1 {
			t.Errorf("len(db.Metrics(__name__=%v).Points(...)) == %v, want %v", name, len(points), 1)
		}
		vFloat, _ := convertInterface(v)
		want := types.Point{Time: t0, Value: vFloat}
		if !reflect.DeepEqual(points[0].Point, want) {
			t.Errorf("db.Metrics(__name__=%v).Points(...)[0] == %v, want %v", name, points[0].Point, want)
		}
		if st, ok := wantStatus[name]; ok {
			if !reflect.DeepEqual(points[0].StatusDescription, st) {
				t.Errorf("db.Metrics(__name__=%v).Points(...)[0].StatusDescription == %v, want %v", name, points[0].StatusDescription, st)
			}
		} else if points[0].CurrentStatus.IsSet() {
			t.Errorf("db.Metrics(__name__=%v).Points(...)[0].StatusDescription == %v, want none", name, points[0].StatusDescription)
		}
	}
}
