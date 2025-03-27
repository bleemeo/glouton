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

package collector

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/types"

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
	ctx := t.Context()
	c := New(nil, gate.New(0))
	c.runOnce(ctx, time.Now())

	input := &mockInput{Name: "input1"}

	_, err := c.AddInput(input, "input1")
	if err != nil {
		t.Error(err)
	}

	c.runOnce(ctx, time.Now())

	if input.GatherCallCount != 1 {
		t.Errorf("input.GatherCallCount == %v, want %v", input.GatherCallCount, 1)
	}

	c.runOnce(ctx, time.Now())

	if input.GatherCallCount != 2 {
		t.Errorf("input.GatherCallCount == %v, want %v", input.GatherCallCount, 2)
	}
}

type msmsa map[string]map[string]any

type shallowAcc struct {
	fields      map[time.Time]msmsa
	annotations map[time.Time]map[string]types.MetricAnnotations
	errs        []error
	l           sync.Mutex
}

func (sa *shallowAcc) AddFields(measurement string, fields map[string]any, tags map[string]string, t ...time.Time) {
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

func (sa *shallowAcc) AddGauge(string, map[string]any, map[string]string, ...time.Time) {
}

func (sa *shallowAcc) AddCounter(string, map[string]any, map[string]string, ...time.Time) {
}

func (sa *shallowAcc) AddSummary(string, map[string]any, map[string]string, ...time.Time) {
}

func (sa *shallowAcc) AddHistogram(string, map[string]any, map[string]string, ...time.Time) {
}

func (sa *shallowAcc) AddFieldsWithAnnotations(measurement string, fields map[string]any, tags map[string]string, annotations types.MetricAnnotations, t ...time.Time) {
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

func (sa *shallowAcc) AddError(err error) {
	sa.l.Lock()
	defer sa.l.Unlock()

	sa.errs = append(sa.errs, err)
}

func (sa *shallowAcc) Errors() []error {
	sa.l.Lock()
	defer sa.l.Unlock()

	return sa.errs
}

func (sa *shallowAcc) WithTracking(int) telegraf.TrackingAccumulator { return nil }

// assertValue ensures the given value exists for the given timestamp, measurement, field and tag.
// If not, it fails the test.
func (sa *shallowAcc) assertValue(t *testing.T, ts time.Time, measurement, field, tag string, val any) { //nolint:unparam
	t.Helper()

	atT, ok := sa.fields[ts]
	if !ok {
		t.Fatalf("No measurement found at %s", ts)
	}

	fields, ok := atT[measurement]
	if !ok {
		t.Fatalf("No measurement %q found", measurement)
	}

	v, ok := fields[field+"__"+tag]
	if !ok {
		t.Fatalf("No field %q with tag %q found", field, tag)
	}

	// Special case for NaN comparison
	vFloat, okV := v.(float64)
	valFloat, okVal := val.(float64)

	if okV && okVal && value.IsStaleNaN(vFloat) && value.IsStaleNaN(valFloat) {
		return
	}

	if v != val {
		t.Fatalf("Expected value %#v, got %#v", val, v)
	}
}

// assertNoValue ensures no measurement has been done for the given TS.
// If it happens to be the case, it fails the test.
func (sa *shallowAcc) assertNoValue(t *testing.T, ts time.Time, measurement, field, tag string) {
	t.Helper()

	atT, ok := sa.fields[ts]
	if !ok {
		return
	}

	fields, ok := atT[measurement]
	if !ok {
		return
	}

	_, ok = fields[field+"__"+tag]
	if ok {
		t.Fatalf("Found unexpected value at %s, for measurement %q / field %q / tag %q", ts, measurement, field, tag)
	}
}

type shallowInput struct {
	measurement string
	tag         string
	fields      map[string]float64
	annotations types.MetricAnnotations
	retErr      error
}

func (s shallowInput) Gather(acc telegraf.Accumulator) error {
	if len(s.fields) == 0 { // Nothing to gather
		return s.retErr
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

	return s.retErr
}

func (s shallowInput) SampleConfig() string {
	return "shallow"
}

type inputFunc func(telegraf.Accumulator) error

func (iFn inputFunc) SampleConfig() string {
	return "func"
}

func (iFn inputFunc) Gather(acc telegraf.Accumulator) error {
	return iFn(acc)
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

	err := c.RunGather(t.Context(), t0)
	if err != nil {
		t.Fatalf("Unexpected c.RunGather() error: %v", err)
	}

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

	err = c.RunGather(t.Context(), t1)
	if err != nil {
		t.Fatalf("Unexpected c.RunGather() error: %v", err)
	}

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

	err = c.RunGather(t.Context(), t2)
	if err != nil {
		t.Fatalf("Unexpected c.RunGather() error: %v", err)
	}

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

	var wg sync.WaitGroup

	wg.Add(1)

	go func() {
		// Simulate another goroutine removing the input during the gathering setup
		c.RemoveInput(id)
		wg.Done()
	}()

	err = c.RunGather(t.Context(), time.Now())
	if err != nil {
		t.Fatalf("Unexpected c.RunGather() error: %v", err)
	}

	wg.Wait() // Wait for the goroutine removing the input to complete

	c.l.Lock()
	defer c.l.Unlock()

	if _, ok := c.fieldCaches[id]; ok {
		t.Fatal("The input's fieldCaches should have been removed from the collector.")
	}

	t.Log("The goroutine did not panic on a nil map assignment - success !")
}

func TestMarkInactiveWithErrors(t *testing.T) {
	acc := shallowAcc{fields: make(map[time.Time]msmsa), annotations: make(map[time.Time]map[string]types.MetricAnnotations)}
	c := New(&acc, gate.New(0))

	input := shallowInput{
		measurement: "i1",
		tag:         "i1",
		fields:      map[string]float64{"f1": 1, "f2": 0.2, "f3": 333},
	}

	id, err := c.AddInput(&input, "input")
	if err != nil {
		t.Fatal("Failed to add input to collector:", err)
	}

	t0 := time.Now().Round(time.Second)
	t1 := t0.Add(10 * time.Second)
	t2 := t1.Add(keepMetricBecauseOfGatherErrorGraceDelay)

	err = c.RunGather(t.Context(), t0)
	if err != nil {
		t.Fatal("Gathering failed:", err)
	}

	acc.assertValue(t, t0, "i1", "f1", `name="i1"`, 1.)

	expectedCache := map[int]map[string]map[string]map[string]fieldCache{
		id: {
			"i1": {
				"f1": {`name="i1"`: fieldCache{}},
				"f2": {`name="i1"`: fieldCache{}},
				"f3": {`name="i1"`: fieldCache{}},
			},
		},
	}
	if diff := cmp.Diff(expectedCache, c.fieldCaches, cmpopts.IgnoreTypes(fieldCache{})); diff != "" {
		t.Fatalf("Unexpected field cache state at t0:\n%v", diff)
	}

	input.fields = map[string]float64{"f2": 0.22, "f3": 33} // no value for f1
	input.retErr = io.ErrUnexpectedEOF                      // perhaps because of this error

	err = c.RunGather(t.Context(), t1)
	if err != nil {
		t.Fatal("Gathering failed:", err)
	}

	// We expected no value for f1, because the input has returned an error (and thus no value),
	// but no NaN neither since we're within the grace period.
	acc.assertNoValue(t, t1, "i1", "f1", `name="i1"`)
	acc.assertValue(t, t1, "i1", "f2", `name="i1"`, 0.22)

	// We expected the cache to still be the same, since t1 is within the grace period.
	if diff := cmp.Diff(expectedCache, c.fieldCaches, cmpopts.IgnoreTypes(fieldCache{})); diff != "" {
		t.Fatalf("Unexpected field cache state at t1:\n%v", diff)
	}

	input.fields = map[string]float64{"f3": 3, "f4": 4.4} // no more value for f2 neither; a new metric appears;
	// the error is still present

	err = c.RunGather(t.Context(), t2)
	if err != nil {
		t.Fatal("Gathering failed:", err)
	}

	// The grace period has ended, the metric is now marked as inactive -> StaleNaN.
	acc.assertValue(t, t2, "i1", "f1", `name="i1"`, math.Float64frombits(value.StaleNaN))
	acc.assertValue(t, t2, "i1", "f2", `name="i1"`, math.Float64frombits(value.StaleNaN))
	acc.assertValue(t, t2, "i1", "f3", `name="i1"`, 3.)
	acc.assertValue(t, t2, "i1", "f4", `name="i1"`, 4.4)

	expectedCache = map[int]map[string]map[string]map[string]fieldCache{
		id: {
			"i1": {
				"f3": {`name="i1"`: fieldCache{}},
				"f4": {`name="i1"`: fieldCache{}},
			},
		},
	}
	if diff := cmp.Diff(expectedCache, c.fieldCaches, cmpopts.IgnoreTypes(fieldCache{})); diff != "" {
		t.Fatalf("Unexpected field cache state at t2:\n%v", diff)
	}
}

func TestErrorHandling(t *testing.T) {
	t.Parallel()

	acc := shallowAcc{fields: make(map[time.Time]msmsa), annotations: make(map[time.Time]map[string]types.MetricAnnotations)}
	c := New(&acc, gate.New(0))

	inpt1 := inputFunc(func(acc telegraf.Accumulator) error {
		acc.AddError(os.ErrNotExist)

		return nil
	})
	inpt2 := inputFunc(func(telegraf.Accumulator) error {
		return nil
	})
	inpt3 := inputFunc(func(acc telegraf.Accumulator) error {
		acc.AddError(os.ErrPermission)
		acc.AddError(io.ErrUnexpectedEOF)

		return nil
	})

	for i, input := range []telegraf.Input{inpt1, inpt2, inpt3} {
		_, err := c.AddInput(input, fmt.Sprint("input-", i+1))
		if err != nil {
			t.Fatalf("Failed to add input n°%d to collector: %v", i+1, err)
		}
	}

	err := c.RunGather(t.Context(), time.Now())
	expectedErrors := []error{os.ErrPermission, os.ErrPermission, io.ErrUnexpectedEOF}

	for _, expectedErr := range expectedErrors {
		if !errors.Is(err, expectedErr) {
			t.Errorf("Expected gather error to contain error %q", expectedErr)
		}
	}
}
