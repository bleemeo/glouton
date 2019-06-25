package internal

import (
	"math"
	"testing"
	"time"
)

func TestDefault(t *testing.T) {
	called := false
	finalFunc := func(measurement string, fields map[string]interface{}, tags map[string]string, t_ ...time.Time) {
		if len(fields) != 1 {
			t.Errorf("len(fields) = %v, want %v", len(fields), 1)
		}
		if len(tags) != 0 {
			t.Errorf("len(tags) = %v, want %v", len(tags), 0)
		}
		if fields["usage_user"] != 64.2 {
			t.Errorf("fields[usage_user] == %v, want %v", fields["usage_user"], 64.2)
		}
		called = true
	}
	acc := Accumulator{}
	acc.PrepareGather()
	acc.processMetrics(
		finalFunc,
		"cpu",
		map[string]interface{}{
			"usage_user": 64.2,
		},
		map[string]string{},
	)
	if !called {
		t.Errorf("finalFunc was not called")
	}
}

func TestRename(t *testing.T) {
	called := false
	transformMetrics := func(fields map[string]float64, tags map[string]string) map[string]float64 {
		if tags["newTag"] != "value" {
			t.Errorf("tags[newTag] == %#v, want %#v", tags["newTag"], "value")
		}
		for k, v := range fields {
			if k == "metricDouble" {
				fields[k] = v * 2
			}
			if k == "metricRename" {
				delete(fields, k)
				fields["metricNewName"] = v
			}
			if k == "metricDrop" {
				delete(fields, k)
			}
			if k == "metricDuplicate" {
				fields["metricDuplicated"] = v
			}
		}
		return fields
	}
	transformTags := func(tags map[string]string) (map[string]string, bool) {
		return map[string]string{
			"newTag": "value",
		}, false
	}
	finalFunc := func(measurement string, fields map[string]interface{}, tags map[string]string, t_ ...time.Time) {
		if measurement != "cpu2" {
			t.Errorf("measurement == %#v, want %#v", measurement, "cpu2")
		}
		if tags["newTag"] != "value" {
			t.Errorf("tags[newTag] = %#v, want %#v", tags["newTag"], "value")
		}
		cases := []struct {
			name  string
			value float64
		}{
			{"metricDouble", 40.0},
			{"metricNewName", 42.0},
			{"metricDuplicate", 5.0},
			{"metricDuplicated", 5.0},
		}
		if len(fields) != len(cases) {
			t.Errorf("len(fields) == %v, want %v", len(fields), len(cases))
		}
		for _, c := range cases {
			if fields[c.name] != c.value {
				t.Errorf("fields[%#v] == %v, want %v", c.name, fields[c.name], c.value)
			}
		}
		called = true
	}
	acc := Accumulator{
		TransformMetrics: transformMetrics,
		TransformTags:    transformTags,
		NewMeasurement:   "cpu2",
	}
	acc.PrepareGather()
	acc.processMetrics(
		finalFunc,
		"cpu",
		map[string]interface{}{
			"metricDouble":    20.0,
			"metricRename":    42,
			"metricDrop":      42.0,
			"metricDuplicate": uint64(5),
		},
		map[string]string{
			"oldTag": "value",
		},
	)
	if !called {
		t.Errorf("finalFunc was not called")
	}
}

func TestDerive(t *testing.T) {
	called1 := false
	called2 := false
	finalFunc1 := func(measurement string, fields map[string]interface{}, tags map[string]string, t_ ...time.Time) {
		cases := []struct {
			name  string
			value float64
		}{
			{"metricNoDerive", 42.0},
		}
		if len(fields) != len(cases) {
			t.Errorf("len(fields) == %v, want %v", len(fields), len(cases))
		}
		for _, c := range cases {
			if fields[c.name] != c.value {
				t.Errorf("fields[%#v] == %v, want %v", c.name, fields[c.name], c.value)
			}
		}
		called1 = true
	}
	finalFunc2 := func(measurement string, fields map[string]interface{}, tags map[string]string, t_ ...time.Time) {
		cases := []struct {
			name  string
			value float64
		}{
			{"metricNoDerive", 12.0},
			{"metricDeriveFloat", 1.3},
			{"metricDeriveInt", -1.0},
			{"metricDeriveUint", -2.0},
		}
		if len(fields) != len(cases) {
			t.Errorf("len(fields) == %v, want %v", len(fields), len(cases))
		}
		for _, c := range cases {
			got := fields[c.name].(float64)
			if math.Abs(got-c.value) > 0.001 {
				t.Errorf("fields[%#v] == %v, want %v", c.name, fields[c.name], c.value)
			}
		}
		called2 = true
	}
	t0 := time.Now()
	t1 := t0.Add(10 * time.Second)
	acc := Accumulator{
		DerivatedMetrics: []string{"metricDeriveFloat", "metricDeriveInt", "metricDeriveUint"},
	}
	acc.PrepareGather()
	acc.processMetrics(
		finalFunc1,
		"cpu",
		map[string]interface{}{
			"metricNoDerive":    42.0,
			"metricDeriveFloat": 10.0,
			"metricDeriveInt":   10,
			"metricDeriveUint":  uint64(1000020),
		},
		nil,
		t0,
	)
	acc.PrepareGather()
	acc.processMetrics(
		finalFunc2,
		"cpu",
		map[string]interface{}{
			"metricNoDerive":    12.0,
			"metricDeriveFloat": 23.0,
			"metricDeriveInt":   0,
			"metricDeriveUint":  uint64(1000000),
		},
		nil,
		t1,
	)
	if !called1 {
		t.Errorf("finalFunc1 was not called")
	}
	if !called2 {
		t.Errorf("finalFunc2 was not called")
	}
}

func TestDeriveMultipleTag(t *testing.T) {
	called1 := 0
	called2 := 0
	finalFunc1 := func(measurement string, fields map[string]interface{}, tags map[string]string, t_ ...time.Time) {
		called1++
		if len(fields) != 0 {
			t.Errorf("len(fields) == %v, want %v", len(fields), 0)
		}
	}
	finalFunc2 := func(measurement string, fields map[string]interface{}, tags map[string]string, t_ ...time.Time) {
		var want float64
		if tags["item"] == "sda" {
			want = 4.2
		} else if tags["item"] == "nvme0n1" {
			want = 1204.0
		}
		if len(fields) != 1 {
			t.Errorf("len(fields) == %v, want %v", len(fields), 1)
		}
		got := fields["io_reads"].(float64)
		if math.Abs(got-want) > 0.001 {
			t.Errorf("fields[%#v] == %v, want %v", "io_reads", fields["io_reads"], want)
		}
		called2++
	}
	t0 := time.Now()
	t1 := t0.Add(20 * time.Second)
	acc := Accumulator{
		DerivatedMetrics: []string{"io_reads"},
	}
	acc.PrepareGather()
	acc.processMetrics(
		finalFunc1,
		"cpu",
		map[string]interface{}{
			"io_reads": 100,
		},
		map[string]string{
			"item": "sda",
		},
		t0,
	)
	acc.processMetrics(
		finalFunc1,
		"cpu",
		map[string]interface{}{
			"io_reads": 5748,
		},
		map[string]string{
			"item": "nvme0n1",
		},
		t0,
	)
	acc.PrepareGather()
	acc.processMetrics(
		finalFunc2,
		"cpu",
		map[string]interface{}{
			"io_reads": 100 + int(4.2*20),
		},
		map[string]string{
			"item": "sda",
		},
		t1,
	)
	acc.processMetrics(
		finalFunc2,
		"cpu",
		map[string]interface{}{
			"io_reads": 5748 + int(1204*20),
		},
		map[string]string{
			"item": "nvme0n1",
		},
		t1,
	)
	if called1 != 2 {
		t.Errorf("finalFunc1 was not called twice")
	}
	if called2 != 2 {
		t.Errorf("finalFunc2 was not called twice")
	}
}

func TestMeasurementMap(t *testing.T) {
	called := 0
	finalFunc := func(measurement string, fields map[string]interface{}, tags map[string]string, t_ ...time.Time) {
		if len(fields) != 1 {
			t.Errorf("len(fields) == %v, want 1", len(fields))
		}
		cases := []struct {
			metricName      string
			measurementName string
		}{
			{"load1", "system"},
			{"logged", "users"},
			{"no_match", "newDefault"},
		}
		for _, c := range cases {
			if _, ok := fields[c.metricName]; ok {
				if measurement != c.measurementName {
					t.Errorf("measurement == %#v, want %#v", measurement, c.measurementName)
				}
			}
		}
		called++
	}
	acc := Accumulator{
		NewMeasurement: "newDefault",
		NewMeasurementMap: map[string]string{
			"load1":  "system",
			"logged": "users",
		},
	}
	acc.PrepareGather()
	acc.processMetrics(
		finalFunc,
		"sys",
		map[string]interface{}{
			"load1":    20.0,
			"logged":   42,
			"no_match": 42.0,
		},
		nil,
	)
	if called != 3 {
		t.Errorf("finalFunc was not three time")
	}
}
