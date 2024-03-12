// Copyright 2015-2023 Bleemeo
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

package collector

import (
	"context"
	"errors"
	"glouton/inputs"
	"glouton/types"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/influxdata/telegraf"
	"github.com/prometheus/prometheus/model/value"
	"github.com/prometheus/prometheus/util/gate"
)

type mockInput struct {
	Name            string
	GatherCallCount int
}

func (m *mockInput) Gather(telegraf.Accumulator) error {
	m.GatherCallCount++

	return nil
}

func (m *mockInput) SampleConfig() string {
	return m.Name
}

func TestAddRemove(t *testing.T) {
	c := New(nil, gate.New(0))
	id1, _ := c.AddInput(&mockInput{Name: "input1"}, "input1")
	id2, _ := c.AddInput(&mockInput{Name: "input2"}, "input2")

	if len(c.inputs) != 2 {
		t.Errorf("len(c.inputs) == %v, want %v", len(c.inputs), 2)
	}

	c.RemoveInput(id1)

	if len(c.inputs) != 1 {
		t.Errorf("len(c.inputs) == %v, want %v", len(c.inputs), 1)
	}

	if input, ok := c.inputs[id2]; !ok {
		t.Errorf("c.inputs[id2=%v] == nil, want input2", id2)
	} else if input.SampleConfig() != "input2" {
		t.Errorf("c.inputs[id2=%v].Description() == %v, want %v", id2, input.SampleConfig(), "input2")
	}
}

func TestRun(t *testing.T) {
	c := New(nil, gate.New(0))
	c.runOnce(time.Now())

	input := &mockInput{Name: "input1"}

	_, err := c.AddInput(input, "input1")
	if err != nil {
		t.Error(err)
	}

	c.runOnce(time.Now())

	if input.GatherCallCount != 1 {
		t.Errorf("input.GatherCallCount == %v, want %v", input.GatherCallCount, 1)
	}

	c.runOnce(time.Now())

	if input.GatherCallCount != 2 {
		t.Errorf("input.GatherCallCount == %v, want %v", input.GatherCallCount, 2)
	}
}

type msmsa map[string]map[string]any

type shallowAcc struct {
	fields      map[time.Time]msmsa
	annotations map[time.Time]map[string]types.MetricAnnotations
	l           sync.Mutex
}

func (sa *shallowAcc) AddFields(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	if t == nil {
		t = []time.Time{time.Now()} // normally unused
	}

	sa.l.Lock()
	defer sa.l.Unlock()

	if _, ok := sa.fields[t[0]]; !ok {
		sa.fields[t[0]] = make(msmsa)
	}

	if _, ok := sa.fields[t[0]][measurement]; !ok {
		sa.fields[t[0]][measurement] = make(map[string]any)
	}

	for field, v := range fields {
		sa.fields[t[0]][measurement][field+"__"+types.LabelsToText(tags)] = v
	}
}

func (sa *shallowAcc) AddGauge(string, map[string]interface{}, map[string]string, ...time.Time) {
}

func (sa *shallowAcc) AddCounter(string, map[string]interface{}, map[string]string, ...time.Time) {
}

func (sa *shallowAcc) AddSummary(string, map[string]interface{}, map[string]string, ...time.Time) {
}

func (sa *shallowAcc) AddHistogram(string, map[string]interface{}, map[string]string, ...time.Time) {
}

func (sa *shallowAcc) AddFieldsWithAnnotations(measurement string, fields map[string]interface{}, tags map[string]string, annotations types.MetricAnnotations, t ...time.Time) {
	sa.l.Lock()

	func() {
		defer sa.l.Unlock()

		if _, ok := sa.annotations[t[0]]; !ok {
			sa.annotations[t[0]] = make(map[string]types.MetricAnnotations)
		}

		sa.annotations[t[0]][measurement+"__"+types.LabelsToText(tags)] = annotations
	}()

	sa.AddFields(measurement, fields, tags, t...)
}

func (sa *shallowAcc) AddMetric(telegraf.Metric) {}

func (sa *shallowAcc) SetPrecision(time.Duration) {}

func (sa *shallowAcc) AddError(error) {}

func (sa *shallowAcc) WithTracking(int) telegraf.TrackingAccumulator { return nil }

type shallowInput struct {
	measurement string
	tag         string
	fields      map[string]float64
	annotations types.MetricAnnotations
}

func (s shallowInput) Gather(acc telegraf.Accumulator) error {
	if len(s.fields) == 0 {
		return nil // Nothing to gather
	}

	fields := make(map[string]any, len(s.fields))

	for field, val := range s.fields {
		fields[field] = val
	}

	tags := map[string]string{"name": s.tag}

	if s.annotations.ServiceName != "" {
		annotAcc, ok := acc.(inputs.AnnotationAccumulator)
		if !ok {
			return errors.New("expected shallowAcc to accept annotations") //nolint:goerr113
		}

		annotAcc.AddFieldsWithAnnotations(s.measurement, fields, tags, s.annotations)
	} else {
		acc.AddFields(s.measurement, fields, tags)
	}

	return nil
}

func (s shallowInput) SampleConfig() string {
	return "shallow"
}

func TestMarkInactive(t *testing.T) {
	acc := shallowAcc{fields: make(map[time.Time]msmsa), annotations: make(map[time.Time]map[string]types.MetricAnnotations)}
	c := New(&acc, gate.New(0))

	input1 := shallowInput{measurement: "i1", tag: "i1", fields: map[string]float64{"f1": 1, "f2": 0.2, "f3": 333}}
	input2 := shallowInput{measurement: "i2", tag: "i2", fields: map[string]float64{"f": 2}}
	input2bis := shallowInput{measurement: "i2", tag: "i2b", fields: map[string]float64{"f": 2.2}}
	inputAnnot := shallowInput{
		measurement: "ia", tag: "ia", fields: map[string]float64{"fa": 7},
		annotations: types.MetricAnnotations{ServiceName: "A"},
	}

	for n, input := range []*shallowInput{&input1, &input2, &input2bis, &inputAnnot} {
		id, err := c.AddInput(input, "shallow")
		if err != nil {
			t.Fatalf("Failed to register input %d: %v", n, err)
		}

		if id != n+1 {
			t.Fatalf("Input id = %d, want %d", id, n+1)
		}
	}

	t0 := time.Now()
	t1 := t0.Add(10 * time.Second)
	t2 := t1.Add(10 * time.Second)

	c.RunGather(context.Background(), t0)

	expectedCache := map[int]map[string]map[string]map[string]fieldCache{
		1: {
			"i1": {
				"f1": {`name="i1"`: fieldCache{}},
				"f2": {`name="i1"`: fieldCache{}},
				"f3": {`name="i1"`: fieldCache{}},
			},
		},
		2: {
			"i2": {
				"f": {`name="i2"`: fieldCache{}},
			},
		},
		3: {
			"i2": {
				"f": {`name="i2b"`: fieldCache{}},
			},
		},
		4: {
			"ia": {
				"fa": {`name="ia"`: fieldCache{}},
			},
		},
	}
	if diff := cmp.Diff(expectedCache, c.fieldCaches, cmpopts.IgnoreUnexported(fieldCache{})); diff != "" {
		t.Errorf("Unexpected field cache state after t0:\n%v", diff)
	}

	delete(input1.fields, "f1") // No more f1 metric
	// Keeping the same value for f2
	input1.fields["f3"] = 3.3 // The value of f3 has changed

	input2.tag = "I2" // Tags have changed

	c.RunGather(context.Background(), t1)

	expectedCache = map[int]map[string]map[string]map[string]fieldCache{
		1: {
			"i1": {
				"f2": {`name="i1"`: fieldCache{}},
				"f3": {`name="i1"`: fieldCache{}},
			},
		},
		2: {
			"i2": {
				"f": {`name="I2"`: fieldCache{}},
			},
		},
		3: {
			"i2": {
				"f": {`name="i2b"`: fieldCache{}},
			},
		},
		4: {
			"ia": {
				"fa": {`name="ia"`: fieldCache{}},
			},
		},
	}
	if diff := cmp.Diff(expectedCache, c.fieldCaches, cmpopts.IgnoreUnexported(fieldCache{})); diff != "" {
		t.Errorf("Unexpected field cache state after t1:\n%v", diff)
	}

	delete(input1.fields, "f2") // No more f2 metric
	input1.fields["f3"] = 3

	input2.fields["f"] = 22

	input2bis.fields["f"] = 22.22

	c.RunGather(context.Background(), t2)

	// Since StaleNaN is directly given as a float to avoid conversions with a loss of precision,
	// expected values should also be represented as floats.
	floatStaleNaN := math.Float64frombits(value.StaleNaN)

	expectedCache = map[int]map[string]map[string]map[string]fieldCache{
		1: {
			"i1": {
				"f3": {`name="i1"`: fieldCache{}},
			},
		},
		2: {
			"i2": {
				"f": {`name="I2"`: fieldCache{}},
			},
		},
		3: {
			"i2": {
				"f": {`name="i2b"`: fieldCache{}},
			},
		},
		4: {
			"ia": {
				"fa": {`name="ia"`: fieldCache{}},
			},
		},
	}
	if diff := cmp.Diff(expectedCache, c.fieldCaches, cmpopts.IgnoreUnexported(fieldCache{})); diff != "" {
		t.Errorf("Unexpected field cache state after t2:\n%v", diff)
	}

	expectedT0 := msmsa{
		"i1": {`f1__name="i1"`: 1., `f2__name="i1"`: 0.2, `f3__name="i1"`: 333.},
		"i2": {`f__name="i2"`: 2., `f__name="i2b"`: 2.2},
		"ia": {`fa__name="ia"`: 7.},
	}
	if diff := cmp.Diff(expectedT0, acc.fields[t0], cmpStaleNaN()); diff != "" {
		t.Errorf("Unexpected fields at t0:\n%v", diff)
	}

	expectedAnnotT0 := map[string]types.MetricAnnotations{`ia__name="ia"`: {ServiceName: "A"}}
	if diff := cmp.Diff(expectedAnnotT0, acc.annotations[t0]); diff != "" {
		t.Errorf("Unexpected annotations at t0:\n%v", diff)
	}

	expectedT1 := msmsa{
		"i1": {`f1__name="i1"`: floatStaleNaN, `f2__name="i1"`: 0.2, `f3__name="i1"`: 3.3},
		"i2": {`f__name="i2"`: floatStaleNaN, `f__name="I2"`: 2., `f__name="i2b"`: 2.2},
		"ia": {`fa__name="ia"`: 7.},
	}
	if diff := cmp.Diff(expectedT1, acc.fields[t1], cmpStaleNaN()); diff != "" {
		t.Errorf("Unexpected fields at t1:\n%v", diff)

		if acc.fields[t1]["i1"]["f1"] != floatStaleNaN {
			t.Error("The value of f1 field should be StaleNaN.\n")
		}
	}

	expectedAnnotT1 := map[string]types.MetricAnnotations{`ia__name="ia"`: {ServiceName: "A"}}
	if diff := cmp.Diff(expectedAnnotT1, acc.annotations[t1]); diff != "" {
		t.Errorf("Unexpected annotations at t1:\n%v", diff)
	}

	expectedT2 := msmsa{
		"i1": {`f2__name="i1"`: floatStaleNaN, `f3__name="i1"`: 3.},
		"i2": {`f__name="I2"`: 22., `f__name="i2b"`: 22.22},
		"ia": {`fa__name="ia"`: 7.},
	}
	if diff := cmp.Diff(expectedT2, acc.fields[t2], cmpStaleNaN()); diff != "" {
		t.Errorf("Unexpected fields at t2:\n%v", diff)

		if acc.fields[t2]["i1"]["f2"] != floatStaleNaN {
			t.Error("The value of f2 field should be StaleNaN.\n")
		}
	}

	expectedAnnotT2 := map[string]types.MetricAnnotations{`ia__name="ia"`: {ServiceName: "A"}}
	if diff := cmp.Diff(expectedAnnotT2, acc.annotations[t2]); diff != "" {
		t.Errorf("Unexpected annotations at t2:\n%v", diff)
	}
}

func cmpStaleNaN() cmp.Option {
	f := func(x, y float64) bool {
		return math.IsNaN(x) && math.IsNaN(y)
	}
	comparer := func(x, y float64) bool {
		return value.IsStaleNaN(x) == value.IsStaleNaN(y)
	}

	return cmp.FilterValues(f, cmp.Comparer(comparer))
}

func TestMarkInactiveWhileDroppingInput(t *testing.T) {
	var id int

	acc := shallowAcc{fields: make(map[time.Time]msmsa), annotations: make(map[time.Time]map[string]types.MetricAnnotations)}
	c := New(&acc, gate.New(0))

	input := shallowInput{
		measurement: "i1",
		tag:         "i1",
		fields:      map[string]float64{"f1": 1, "f2": 0.2, "f3": 333},
	}

	var err error

	id, err = c.AddInput(input, "input")
	if err != nil {
		t.Fatal("Failed to add input to collector:", err)
	}

	// Simulate another goroutine removing the input during the gathering setup
	c.RemoveInput(id)

	c.RunGather(context.Background(), time.Now())

	if _, ok := c.fieldCaches[id]; ok {
		t.Fatal("The input's fieldCaches should have been removed from the collector.")
	}

	time.Sleep(10 * time.Millisecond) // Let the other goroutine panic, if necessary

	t.Log("The goroutine did not panic on a nil map assignment - success !")
}
