package store

import (
	"agentgo/types"
	"reflect"
	"testing"
	"time"
)

func TestLabelsMatchNotExact(t *testing.T) {
	cases := []struct {
		labels, filter map[string]string
		want           bool
	}{
		{
			map[string]string{
				"__name__": "cpu_used",
			},
			map[string]string{
				"__name__": "cpu_used",
			},
			true,
		},
		{
			map[string]string{
				"__name__": "disk_used",
				"item":     "/home",
			},
			map[string]string{
				"__name__": "disk_used",
			},
			true,
		},
		{
			map[string]string{
				"__name__": "disk_used",
				"item":     "/home",
			},
			map[string]string{
				"__name__": "cpu_used",
			},
			false,
		},
		{
			map[string]string{
				"__name__": "disk_used",
				"item":     "/home",
			},
			map[string]string{
				"__name__": "disk_used",
				"item":     "/",
			},
			false,
		},
		{
			map[string]string{
				"__name__": "disk_used",
				"item":     "/home",
			},
			map[string]string{
				"__name__": "disk_used",
				"extra":    "label",
			},
			false,
		},
	}

	for _, c := range cases {
		got := labelsMatch(c.labels, c.filter, false)
		if got != c.want {
			t.Errorf("labelsMatch(%v, %v, false) == %v, want %v", c.labels, c.filter, got, c.want)
		}
	}
}

func TestLabelsMatchExact(t *testing.T) {
	cases := []struct {
		labels, filter map[string]string
		want           bool
	}{
		{
			map[string]string{
				"__name__": "cpu_used",
			},
			map[string]string{
				"__name__": "cpu_used",
			},
			true,
		},
		{
			map[string]string{
				"__name__": "disk_used",
				"item":     "/home",
			},
			map[string]string{
				"__name__": "disk_used",
			},
			false,
		},
		{
			map[string]string{
				"__name__": "disk_used",
				"item":     "/home",
			},
			map[string]string{
				"__name__": "cpu_used",
			},
			false,
		},
		{
			map[string]string{
				"__name__": "disk_used",
				"item":     "/home",
			},
			map[string]string{
				"__name__": "disk_used",
				"item":     "/",
			},
			false,
		},
		{
			map[string]string{
				"__name__": "disk_used",
				"item":     "/home",
			},
			map[string]string{
				"__name__": "disk_used",
				"item":     "/home",
			},
			true,
		},
		{
			map[string]string{
				"__name__": "disk_used",
				"item":     "/home",
			},
			map[string]string{
				"__name__": "disk_used",
				"item":     "/home",
				"extra":    "label",
			},
			false,
		},
	}

	for _, c := range cases {
		got := labelsMatch(c.labels, c.filter, true)
		if got != c.want {
			t.Errorf("labelsMatch(%v, %v, false) == %v, want %v", c.labels, c.filter, got, c.want)
		}
	}
}

func TestMetricsSimple(t *testing.T) {

	labels := map[string]string{
		"__name__": "measurement_fieldFloat",
	}
	db := New()
	m := db.metricGetOrCreate(labels)

	if _, ok := db.metrics[m.metricID]; !ok {
		t.Errorf("db.metrics[%v] == nil, want it to exists", m.metricID)
	}

	metrics, err := db.Metrics(labels)
	if err != nil {
		t.Error(err)
	}
	if len(metrics) != 1 {
		t.Errorf("len(metrics) == %v, want %v", len(metrics), 1)
	}
	if !reflect.DeepEqual(metrics[0].Labels(), labels) {
		t.Errorf("metrics[0].Labels() == %v, want %v", metrics[0].Labels(), labels)
	}
}

func TestMetricsMultiple(t *testing.T) {

	labels1 := map[string]string{
		"__name__": "cpu_used",
	}
	labels2 := map[string]string{
		"__name__": "disk_used",
		"item":     "/home",
	}
	labels3 := map[string]string{
		"__name__": "disk_used",
		"item":     "/srv",
		"fstype":   "ext4",
	}
	db := New()
	db.metricGetOrCreate(labels1)
	db.metricGetOrCreate(labels2)
	db.metricGetOrCreate(labels3)

	metrics, err := db.Metrics(labels1)
	if err != nil {
		t.Error(err)
	}
	if len(metrics) != 1 {
		t.Errorf("len(metrics) == %v, want %v", len(metrics), 1)
	}
	if !reflect.DeepEqual(metrics[0].Labels(), labels1) {
		t.Errorf("metrics[0].Labels() == %v, want %v", metrics[0].Labels(), labels1)
	}

	metrics, err = db.Metrics(map[string]string{"__name__": "disk_used"})
	if err != nil {
		t.Error(err)
	}
	if len(metrics) != 2 {
		t.Errorf("len(metrics) == %v, want %v", len(metrics), 2)
	}
	for _, m := range metrics {
		if m.Labels()["item"] != "/home" && m.Labels()["item"] != "/srv" {
			t.Errorf("m.Labels()[\"item\"] == %v, want %v or %v", m.Labels()["item"], "/home", "/srv")
		}
	}

	metrics, err = db.Metrics(map[string]string{"__name__": "disk_used", "item": "/srv"})
	if err != nil {
		t.Error(err)
	}
	if len(metrics) != 1 {
		t.Errorf("len(metrics) == %v, want %v", len(metrics), 1)
	}
	if !reflect.DeepEqual(metrics[0].Labels(), labels3) {
		t.Errorf("metrics[0].Labels() == %v, want %v", metrics[0].Labels(), labels3)
	}
}

func TestPoints(t *testing.T) {
	labels := map[string]string{
		"__name__": "cpu_used",
	}
	db := New()
	m := db.metricGetOrCreate(labels)

	t0 := time.Now().Add(-60 * time.Second)
	t1 := t0.Add(10 * time.Second)
	t2 := t0.Add(20 * time.Second)
	p0 := types.Point{Time: t0, Value: 42.0}
	p1 := types.Point{Time: t1, Value: -88}
	p2 := types.Point{Time: t2, Value: 13.37}
	db.addPoint(m.metricID, p0)

	if len(db.points) != 1 {
		t.Errorf("len(db.points) == %v, want %v", len(db.points), 1)
	}
	if len(db.points[m.metricID]) != 1 {
		t.Errorf("len(db.points[%v]) == %v, want %v", m.metricID, len(db.points[m.metricID]), 1)
	}
	if !reflect.DeepEqual(db.points[m.metricID][0], p0) {
		t.Errorf("db.points[%v][0] == %v, want %v", m.metricID, db.points[m.metricID][0], 1)
	}

	db.addPoint(m.metricID, p1)
	db.addPoint(m.metricID, p2)

	if len(db.points) != 1 {
		t.Errorf("len(db.points) == %v, want %v", len(db.points), 1)
	}
	if len(db.points[m.metricID]) != 3 {
		t.Errorf("len(db.points[%v]) == %v, want %v", m.metricID, len(db.points[m.metricID]), 3)
	}

	points, err := m.Points(t0, t2)
	if err != nil {
		t.Error(err)
	}
	if len(points) != 3 {
		t.Errorf("len(points) == %v, want %v", len(points), 3)
	}

	points, err = m.Points(t1, t1)
	if err != nil {
		t.Error(err)
	}
	if len(points) != 1 {
		t.Errorf("len(points) == %v, want %v", len(points), 1)
	}
	if !reflect.DeepEqual(points[0], p1) {
		t.Errorf("points[0] == %v, want %v", points[0], p1)
	}
}
