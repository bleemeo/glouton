// Copyright 2015-2019 Bleemeo
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

package store

import (
	"glouton/types"
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
				types.LabelName: "cpu_used",
			},
			map[string]string{
				types.LabelName: "cpu_used",
			},
			true,
		},
		{
			map[string]string{
				types.LabelName: "disk_used",
				"mountpoint":    "/home",
			},
			map[string]string{
				types.LabelName: "disk_used",
			},
			true,
		},
		{
			map[string]string{
				types.LabelName: "disk_used",
				"mountpoint":    "/home",
			},
			map[string]string{
				types.LabelName: "cpu_used",
			},
			false,
		},
		{
			map[string]string{
				types.LabelName: "disk_used",
				"mountpoint":    "/home",
			},
			map[string]string{
				types.LabelName: "disk_used",
				"mountpoint":    "/",
			},
			false,
		},
		{
			map[string]string{
				types.LabelName: "disk_used",
				"mountpoint":    "/home",
			},
			map[string]string{
				types.LabelName: "disk_used",
				"extra":         "label",
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
				types.LabelName: "cpu_used",
			},
			map[string]string{
				types.LabelName: "cpu_used",
			},
			true,
		},
		{
			map[string]string{
				types.LabelName: "disk_used",
				"mountpoint":    "/home",
			},
			map[string]string{
				types.LabelName: "disk_used",
			},
			false,
		},
		{
			map[string]string{
				types.LabelName: "disk_used",
				"mountpoint":    "/home",
			},
			map[string]string{
				types.LabelName: "cpu_used",
			},
			false,
		},
		{
			map[string]string{
				types.LabelName: "disk_used",
				"mountpoint":    "/home",
			},
			map[string]string{
				types.LabelName: "disk_used",
				"mountpoint":    "/",
			},
			false,
		},
		{
			map[string]string{
				types.LabelName: "disk_used",
				"mountpoint":    "/home",
			},
			map[string]string{
				types.LabelName: "disk_used",
				"mountpoint":    "/home",
			},
			true,
		},
		{
			map[string]string{
				types.LabelName: "disk_used",
				"mountpoint":    "/home",
			},
			map[string]string{
				types.LabelName: "disk_used",
				"mountpoint":    "/home",
				"extra":         "label",
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
		types.LabelName: "measurement_fieldFloat",
	}
	db := New()
	m := db.metricGetOrCreate(labels, types.MetricAnnotations{})

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
		types.LabelName: "cpu_used",
	}
	labels2 := map[string]string{
		types.LabelName: "disk_used",
		"mountpoint":    "/home",
	}
	labels3 := map[string]string{
		types.LabelName: "disk_used",
		"mountpoint":    "/srv",
		"fstype":        "ext4",
	}
	db := New()
	db.metricGetOrCreate(labels1, types.MetricAnnotations{})
	db.metricGetOrCreate(labels2, types.MetricAnnotations{})
	db.metricGetOrCreate(labels3, types.MetricAnnotations{})

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

	metrics, err = db.Metrics(map[string]string{types.LabelName: "disk_used"})
	if err != nil {
		t.Error(err)
	}
	if len(metrics) != 2 {
		t.Errorf("len(metrics) == %v, want %v", len(metrics), 2)
	}
	for _, m := range metrics {
		if m.Labels()["mountpoint"] != "/home" && m.Labels()["mountpoint"] != "/srv" {
			t.Errorf("m.Labels()[mountpoint] == %v, want %v or %v", m.Labels()["mountpoint"], "/home", "/srv")
		}
	}

	metrics, err = db.Metrics(map[string]string{types.LabelName: "disk_used", "mountpoint": "/srv"})
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
		types.LabelName: "cpu_used",
	}
	db := New()
	m := db.metricGetOrCreate(labels, types.MetricAnnotations{})

	t0 := time.Now().Add(-60 * time.Second)
	t1 := t0.Add(10 * time.Second)
	t2 := t0.Add(20 * time.Second)
	p0 := types.Point{Time: t0, Value: 42.0}
	p1 := types.Point{Time: t1, Value: -88}
	p2 := types.Point{Time: t2, Value: 13.37}
	db.AddMetricPoints([]types.MetricPoint{
		{Point: p0, Labels: labels},
	})

	if len(db.points) != 1 {
		t.Errorf("len(db.points) == %v, want %v", len(db.points), 1)
	}
	if len(db.points[m.metricID]) != 1 {
		t.Errorf("len(db.points[%v]) == %v, want %v", m.metricID, len(db.points[m.metricID]), 1)
	}
	if !reflect.DeepEqual(db.points[m.metricID][0], p0) {
		t.Errorf("db.points[%v][0] == %v, want %v", m.metricID, db.points[m.metricID][0], p0)
	}

	db.AddMetricPoints([]types.MetricPoint{
		{Point: p1, Labels: labels},
	})
	db.AddMetricPoints([]types.MetricPoint{
		{Point: p2, Labels: labels},
	})

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
