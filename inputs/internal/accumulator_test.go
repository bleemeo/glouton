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

package internal

import (
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bleemeo/glouton/types"
)

func TestDefault(t *testing.T) {
	called := false
	finalFunc := func(_ string, fields map[string]interface{}, tags map[string]string, _ types.MetricAnnotations, _ ...time.Time) {
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
	transformMetrics := func(currentContext GatherContext, fields map[string]float64, _ map[string]interface{}) map[string]float64 {
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

	renameGlobal := func(_ GatherContext) (newContext GatherContext, drop bool) {
		return GatherContext{
			Measurement: "cpu2",
			Tags: map[string]string{
				"newTag": "value",
			},
		}, false
	}
	finalFunc := func(measurement string, fields map[string]interface{}, tags map[string]string, _ types.MetricAnnotations, _ ...time.Time) {
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
	finalFunc1 := func(_ string, fields map[string]interface{}, _ map[string]string, _ types.MetricAnnotations, _ ...time.Time) {
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

	finalFunc2 := func(_ string, fields map[string]interface{}, _ map[string]string, _ types.MetricAnnotations, _ ...time.Time) {
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
			got, _ := fields[c.name].(float64)
			if math.Abs(got-c.value) > 0.001 {
				t.Errorf("fields[%#v] == %v, want %v", c.name, fields[c.name], c.value)
			}
		}

		called2 = true
	}

	t0 := time.Now()
	t1 := t0.Add(10 * time.Second)
	acc := Accumulator{
		DifferentiatedMetrics: []string{"metricDeriveFloat", "metricDeriveInt", "metricDeriveUint", "metricDeriveBack", "metricDeriveBack2"},
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
	finalFunc1 := func(_ string, fields map[string]interface{}, _ map[string]string, _ types.MetricAnnotations, _ ...time.Time) {
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
	finalFunc2 := func(_ string, fields map[string]interface{}, _ map[string]string, _ types.MetricAnnotations, _ ...time.Time) {
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
			got, _ := fields[c.name].(float64)
			if math.Abs(got-c.value) > 0.001 {
				t.Errorf("fields[%#v] == %v, want %v", c.name, fields[c.name], c.value)
			}
		}

		called2 = true
	}
	shouldDifferentiateMetrics := func(_ GatherContext, metricName string) bool {
		return strings.HasSuffix(metricName, "nt")
	}
	t0 := time.Now()
	t1 := t0.Add(10 * time.Second)
	acc := Accumulator{
		DifferentiatedMetrics:      []string{"metricDeriveFloat"},
		ShouldDifferentiateMetrics: shouldDifferentiateMetrics,
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
	finalFunc1 := func(_ string, fields map[string]interface{}, _ map[string]string, _ types.MetricAnnotations, _ ...time.Time) {
		called1++

		if len(fields) != 0 {
			t.Errorf("len(fields) == %v, want %v", len(fields), 0)
		}
	}

	finalFunc2 := func(_ string, fields map[string]interface{}, tags map[string]string, _ types.MetricAnnotations, _ ...time.Time) {
		var want float64

		switch tags["item"] {
		case "sda":
			want = 4.2
		case "nvme0n1":
			want = 1204.0
		}

		if len(fields) != 1 {
			t.Errorf("len(fields) == %v, want %v", len(fields), 1)
		}

		got, _ := fields["io_reads"].(float64)

		if math.Abs(got-want) > 0.001 {
			t.Errorf("fields[%#v] == %v, want %v", "io_reads", fields["io_reads"], want)
		}

		called2++
	}

	t0 := time.Now()
	t1 := t0.Add(20 * time.Second)
	acc := Accumulator{
		DifferentiatedMetrics: []string{"io_reads"},
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
	finalFunc := func(measurement string, fields map[string]interface{}, _ map[string]string, _ types.MetricAnnotations, _ ...time.Time) {
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
	renameMetrics := func(_ GatherContext, metricName string) (newMeasurement string, newMetricName string) {
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

func TestRenameCallback(t *testing.T) {
	called := 0
	want := map[string]string{
		"service":      "mysql",
		"service_name": "mysql_1",
	}
	finalFunc := func(_ string, _ map[string]interface{}, tags map[string]string, _ types.MetricAnnotations, _ ...time.Time) {
		if !reflect.DeepEqual(tags, want) {
			t.Errorf("tags == %v, want %v", tags, want)
		}

		called++
	}
	t0 := time.Now()
	t1 := t0.Add(10 * time.Second)
	acc := Accumulator{
		RenameCallbacks: []RenameCallback{
			func(labels map[string]string, annotations types.MetricAnnotations) (map[string]string, types.MetricAnnotations) {
				labels["service_name"] = "mysql_1"
				labels["service"] = "mysql"

				return labels, annotations
			},
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

func TestRenameCallback2(t *testing.T) {
	called := 0
	want := map[string]string{
		types.LabelContainerName: "postgres_1",
		"db":                     "dbname",
	}
	wantAnnotation := types.MetricAnnotations{
		ContainerID:     "1234",
		ServiceInstance: "changed",
	}
	finalFunc := func(_ string, _ map[string]interface{}, tags map[string]string, annotations types.MetricAnnotations, _ ...time.Time) {
		if !reflect.DeepEqual(tags, want) {
			t.Errorf("tags == %v, want %v", tags, want)
		}

		if !reflect.DeepEqual(annotations, wantAnnotation) {
			t.Errorf("annotations == %v, want %v", annotations, wantAnnotation)
		}

		called++
	}
	t0 := time.Now()
	t1 := t0.Add(10 * time.Second)
	acc := Accumulator{
		RenameGlobal: func(gatherContext GatherContext) (GatherContext, bool) {
			gatherContext.Annotations = types.MetricAnnotations{
				ServiceInstance: "changed",
			}

			return gatherContext, false
		},
		RenameCallbacks: []RenameCallback{
			func(labels map[string]string, annotations types.MetricAnnotations) (map[string]string, types.MetricAnnotations) {
				labels[types.LabelContainerName] = "postgres_1"
				annotations.ContainerID = "1234"

				return labels, annotations
			},
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
		"service":                "postgresql",
		types.LabelContainerName: "name",
		"db":                     "dbname",
	}
	finalFunc := func(_ string, _ map[string]interface{}, tags map[string]string, _ types.MetricAnnotations, _ ...time.Time) {
		if !reflect.DeepEqual(tags, want) {
			t.Errorf("tags == %v, want %v", tags, want)
		}

		called++
	}
	t0 := time.Now()
	acc := Accumulator{
		RenameGlobal: func(gatherContext GatherContext) (GatherContext, bool) {
			gatherContext.Tags = map[string]string{"db": "dbname"}

			return gatherContext, false
		},
		RenameCallbacks: []RenameCallback{
			func(labels map[string]string, annotations types.MetricAnnotations) (map[string]string, types.MetricAnnotations) {
				labels["service"] = "postgresql"
				labels[types.LabelContainerName] = "name"

				return labels, annotations
			},
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

func BenchmarkProcessMetrics(b *testing.B) {
	finalFunc := func(_ string, _ map[string]interface{}, _ map[string]string, _ types.MetricAnnotations, _ ...time.Time) {
	}
	renameGlobal := func(gatherContext GatherContext) (GatherContext, bool) {
		gatherContext.Tags["added"] = "new"
		gatherContext.Measurement += "2"

		return gatherContext, false
	}
	transformMetrics := func(_ GatherContext, fields map[string]float64, _ map[string]interface{}) map[string]float64 {
		return fields
	}

	for _, metricCount := range []int{3, 30, 300} {
		b.Run(fmt.Sprintf("metricCount-%d", metricCount), func(b *testing.B) {
			acc := Accumulator{
				RenameGlobal:     renameGlobal,
				TransformMetrics: transformMetrics,
			}
			t0 := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)

			b.ResetTimer()

			for b.Loop() {
				t0 = t0.Add(time.Second)

				acc.PrepareGather()

				for i := range metricCount / 3 {
					acc.processMetrics(
						finalFunc,
						"cpu",
						map[string]interface{}{
							"metricA": 20.0,
							"metricB": 42,
							"metricC": uint64(5),
						},
						map[string]string{
							"label1":     "value1",
							"labelCount": strconv.Itoa(i),
						},
						t0,
					)
				}
			}
		})
	}
}

func BenchmarkDeriveFunc(b *testing.B) {
	finalFunc := func(_ string, _ map[string]interface{}, _ map[string]string, _ types.MetricAnnotations, _ ...time.Time) {
	}
	shouldDerivativeMetrics := func(_ GatherContext, metricName string) bool {
		return strings.HasSuffix(metricName, "int")
	}

	for _, metricCount := range []int{3, 30, 300} {
		b.Run(fmt.Sprintf("metricCount-%d", metricCount), func(b *testing.B) {
			metrics := make(map[string]interface{}, metricCount)
			derivatedMetrics := make([]string, 0, metricCount/3)

			for i := range metricCount / 3 {
				name := fmt.Sprintf("metricDeriveFloat-%d", i)
				derivatedMetrics = append(derivatedMetrics, name)
				metrics[name] = 42.0 + i

				name = fmt.Sprintf("metricNoDerive-%d", i)
				metrics[name] = 10.0 + i

				name = fmt.Sprintf("metric-%d-int", i)
				metrics[name] = i
			}

			acc := Accumulator{
				DifferentiatedMetrics:      derivatedMetrics,
				ShouldDifferentiateMetrics: shouldDerivativeMetrics,
			}
			t0 := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)

			b.ResetTimer()

			for b.Loop() {
				t0 = t0.Add(time.Second)

				acc.PrepareGather()
				acc.processMetrics(
					finalFunc,
					"cpu",
					metrics,
					map[string]string{
						"label1": "value1",
					},
					t0,
				)
			}
		})
	}
}
