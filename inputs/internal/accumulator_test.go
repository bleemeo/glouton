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

package internal

import (
	"glouton/types"
	"math"
	"reflect"
	"strings"
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
	transformMetrics := func(originalContext GatherContext, currentContext GatherContext, fields map[string]float64, originalFields map[string]interface{}) map[string]float64 {
		if currentContext.Tags["newTag"] != "value" {
			t.Errorf("tags[newTag] == %#v, want %#v", currentContext.Tags["newTag"], "value")
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
	renameGlobal := func(originalContext GatherContext) (newContext GatherContext, drop bool) {
		return GatherContext{
			Measurement: "cpu2",
			Tags: map[string]string{
				"newTag": "value",
			},
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
		RenameGlobal:     renameGlobal,
		TransformMetrics: transformMetrics,
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
			{"metricDeriveInt", 1.0},
			{"metricDeriveUint", 2.0},
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
		DerivatedMetrics: []string{"metricDeriveFloat", "metricDeriveInt", "metricDeriveUint", "metricDeriveBack", "metricDeriveBack2"},
	}
	acc.PrepareGather()
	acc.processMetrics(
		finalFunc1,
		"cpu",
		map[string]interface{}{
			"metricNoDerive":    42.0,
			"metricDeriveFloat": 10.0,
			"metricDeriveInt":   0,
			"metricDeriveUint":  uint64(1000000),
			"metricDeriveBack":  100.0,
			"metricDeriveBack2": uint64(100),
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
			"metricDeriveInt":   10,
			"metricDeriveUint":  uint64(1000020),
			"metricDeriveBack":  42.0,
			"metricDeriveBack2": uint64(42),
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

func TestDeriveFunc(t *testing.T) {
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
			{"metricDeriveInt", 1.0},
			{"metricDeriveUint", 2.0},
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
	shouldDerivateMetrics := func(originalContext GatherContext, currentContext GatherContext, metricName string) bool {
		return strings.HasSuffix(metricName, "nt")
	}
	t0 := time.Now()
	t1 := t0.Add(10 * time.Second)
	acc := Accumulator{
		DerivatedMetrics:      []string{"metricDeriveFloat"},
		ShouldDerivateMetrics: shouldDerivateMetrics,
	}
	acc.PrepareGather()
	acc.processMetrics(
		finalFunc1,
		"cpu",
		map[string]interface{}{
			"metricNoDerive":    42.0,
			"metricDeriveFloat": 10.0,
			"metricDeriveInt":   0,
			"metricDeriveUint":  uint64(1000000),
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
			"metricDeriveInt":   10,
			"metricDeriveUint":  uint64(1000020),
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
		if tags[types.LabelBleemeoItem] == "sda" {
			want = 4.2
		} else if tags[types.LabelBleemeoItem] == "nvme0n1" {
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
			types.LabelBleemeoItem: "sda",
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
			types.LabelBleemeoItem: "nvme0n1",
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
			types.LabelBleemeoItem: "sda",
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
			types.LabelBleemeoItem: "nvme0n1",
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
		called++
		for _, c := range cases {
			if _, ok := fields[c.metricName]; ok {
				if measurement != c.measurementName {
					t.Errorf("measurement == %#v, want %#v", measurement, c.measurementName)
				}
				return
			}
		}
		t.Errorf("fields == %v, want load1, logged or no_match", fields)
	}
	renameMetrics := func(originalContext GatherContext, currentContext GatherContext, metricName string) (newMeasurement string, newMetricName string) {
		newMetricName = metricName
		switch metricName {
		case "load1":
			newMeasurement = "system"
		case "n_logged":
			newMeasurement = "users"
			newMetricName = "logged"
		default:
			newMeasurement = "newDefault"
		}
		return
	}
	acc := Accumulator{
		RenameMetrics: renameMetrics,
	}
	acc.PrepareGather()
	acc.processMetrics(
		finalFunc,
		"sys",
		map[string]interface{}{
			"load1":    20.0,
			"n_logged": 42,
			"no_match": 42.0,
		},
		nil,
	)
	if called != 3 {
		t.Errorf("called == %v, want %v", called, 3)
	}
}

func TestStaticLabels(t *testing.T) {
	called := 0
	want := map[string]string{
		types.LabelServiceName: "mysql",
		types.LabelBleemeoItem: "mysql_1",
	}
	finalFunc := func(measurement string, fields map[string]interface{}, tags map[string]string, t_ ...time.Time) {
		if !reflect.DeepEqual(tags, want) {
			t.Errorf("tags == %v, want %v", tags, want)
		}
		called++
	}
	t0 := time.Now()
	t1 := t0.Add(10 * time.Second)
	acc := Accumulator{
		StaticLabels: map[string]string{
			types.LabelServiceName: "mysql",
			types.LabelBleemeoItem: "mysql_1",
		},
	}
	acc.PrepareGather()
	acc.processMetrics(
		finalFunc,
		"mysql",
		map[string]interface{}{
			"requests": 42.0,
		},
		nil,
		t0,
	)
	acc.PrepareGather()
	acc.processMetrics(
		finalFunc,
		"mysql",
		map[string]interface{}{
			"requests": 1337.0,
		},
		nil,
		t1,
	)
	if called != 2 {
		t.Errorf("called == %v, want 2", called)
	}
}

func TestStaticLabels2(t *testing.T) {
	called := 0
	want := map[string]string{
		types.LabelServiceName: "postgresql",
		types.LabelContainerID: "1234",
		types.LabelBleemeoItem: "postgres_1_dbname",
	}
	finalFunc := func(measurement string, fields map[string]interface{}, tags map[string]string, t_ ...time.Time) {
		if !reflect.DeepEqual(tags, want) {
			t.Errorf("tags == %v, want %v", tags, want)
		}
		called++
	}
	t0 := time.Now()
	t1 := t0.Add(10 * time.Second)
	acc := Accumulator{
		StaticLabels: map[string]string{
			types.LabelServiceName: "postgresql",
			types.LabelContainerID: "1234",
			types.LabelBleemeoItem: "postgres_1",
		},
	}
	tags := map[string]string{types.LabelBleemeoItem: "dbname"}
	acc.PrepareGather()
	acc.processMetrics(
		finalFunc,
		"postgresql",
		map[string]interface{}{
			"requests": 42.0,
		},
		tags,
		t0,
	)
	acc.PrepareGather()
	acc.processMetrics(
		finalFunc,
		"postgresql",
		map[string]interface{}{
			"requests": 1337.0,
		},
		tags,
		t1,
	)
	if called != 2 {
		t.Errorf("called == %v, want 2", called)
	}
}

func TestLabelsMutation(t *testing.T) {
	called := 0
	want := map[string]string{
		types.LabelServiceName: "postgresql",
		types.LabelContainerID: "1234",
		types.LabelBleemeoItem: "postgres_1_dbname",
	}
	finalFunc := func(measurement string, fields map[string]interface{}, tags map[string]string, t_ ...time.Time) {
		if !reflect.DeepEqual(tags, want) {
			t.Errorf("tags == %v, want %v", tags, want)
		}
		called++
	}
	t0 := time.Now()
	acc := Accumulator{
		RenameGlobal: func(originalContext GatherContext) (GatherContext, bool) {
			newContext := GatherContext{
				Measurement: originalContext.Measurement,
				Tags:        make(map[string]string),
			}
			newContext.Tags[types.LabelBleemeoItem] = originalContext.Tags["db"]
			return newContext, false
		},
		StaticLabels: map[string]string{
			types.LabelServiceName: "postgresql",
			types.LabelContainerID: "1234",
			types.LabelBleemeoItem: "postgres_1",
		},
	}
	tags := map[string]string{"db": "dbname"}
	acc.PrepareGather()
	acc.processMetrics(
		finalFunc,
		"postgresql",
		map[string]interface{}{
			"requests": 42.0,
		},
		tags,
		t0,
	)
	if called != 1 {
		t.Errorf("called == %v, want 1", called)
	}
}
