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
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/influxdata/telegraf"
	"github.com/prometheus/prometheus/model/value"
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
	c := New(nil)
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
	c := New(nil)
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

type mma map[string]map[string]any

type shallowAcc struct {
	fields map[time.Time]mma
	l      sync.Mutex
}

func (sa *shallowAcc) AddFields(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	_, _ = measurement, tags

	if t == nil {
		t = []time.Time{time.Now()}
	}

	sa.l.Lock()
	defer sa.l.Unlock()

	if _, ok := sa.fields[t[0]]; !ok {
		sa.fields[t[0]] = make(map[string]map[string]any)
	}

	sa.fields[t[0]][measurement] = fields
}

func (sa *shallowAcc) AddGauge(string, map[string]interface{}, map[string]string, ...time.Time) {
}

func (sa *shallowAcc) AddCounter(string, map[string]interface{}, map[string]string, ...time.Time) {
}

func (sa *shallowAcc) AddSummary(string, map[string]interface{}, map[string]string, ...time.Time) {
}

func (sa *shallowAcc) AddHistogram(string, map[string]interface{}, map[string]string, ...time.Time) {
}

func (sa *shallowAcc) AddMetric(telegraf.Metric) {}

func (sa *shallowAcc) SetPrecision(time.Duration) {}

func (sa *shallowAcc) AddError(error) {}

func (sa *shallowAcc) WithTracking(int) telegraf.TrackingAccumulator { return nil }

type shallowInput struct {
	measurement string
	fields      map[string]float64
}

func (s shallowInput) Gather(acc telegraf.Accumulator) error {
	if len(s.fields) == 0 {
		return nil // Nothing to gather
	}

	fields := make(map[string]any)

	for field, val := range s.fields {
		fields[field] = val
	}

	acc.AddFields(s.measurement, fields, nil)

	return nil
}

func (s shallowInput) SampleConfig() string {
	return "shallow"
}

func TestMarkInactive(t *testing.T) {
	acc := shallowAcc{fields: make(map[time.Time]mma)}
	input1 := shallowInput{measurement: "i1", fields: map[string]float64{"f1": 1, "f2": 0.2, "f3": 333}}
	input2 := shallowInput{measurement: "i2", fields: map[string]float64{"f": 2}}

	c := New(&acc)

	for name, input := range []*shallowInput{&input1, &input2} {
		_, err := c.AddInput(input, "shallow")
		if err != nil {
			t.Fatalf("Failed to register input %q: %v", name, err)
		}
	}

	t0 := time.Now()
	t1 := t0.Add(10 * time.Second)
	t2 := t1.Add(10 * time.Second)

	c.RunGather(context.Background(), t0)

	delete(input1.fields, "f1") // No more f1 metric
	// Keeping the same value for f2
	input1.fields["f3"] = 3.3 // The value of f3 has changed

	delete(input2.fields, "f")

	c.RunGather(context.Background(), t1)

	delete(input1.fields, "f2") // No more f2 metric
	input1.fields["f3"] = 3

	c.RunGather(context.Background(), t2)

	// Gathering a few times to trigger the fields cache purge
	c.RunGather(context.Background(), t2.Add(10*time.Second))
	c.RunGather(context.Background(), t2.Add(20*time.Second))
	c.RunGather(context.Background(), t2.Add(30*time.Second))

	t0Fields := acc.fields[t0]
	if diff := cmp.Diff(t0Fields, mma{"i1": {"f1": 1., "f2": 0.2, "f3": 333.}, "i2": {"f": 2.}}); diff != "" {
		t.Errorf("Unexpected fields at t0:\n%v", diff)
	}

	t1Fields := acc.fields[t1]
	if diff := cmp.Diff(t1Fields, mma{"i1": {"f1": value.StaleNaN, "f2": 0.2, "f3": 3.3}}); diff != "" {
		if t1Fields["i1"]["f1"] != value.StaleNaN {
			t.Error("The value of f1 field should be StaleNaN.")
		}

		t.Errorf("Unexpected fields at t1:\n%v", diff)
	}

	t2Fields := acc.fields[t2]
	if diff := cmp.Diff(t2Fields, mma{"i1": {"f2": value.StaleNaN, "f3": 3.}}); diff != "" {
		if t2Fields["i1"]["f2"] != value.StaleNaN {
			t.Error("The value of f2 field should be StaleNaN.")
		}
		t.Errorf("Unexpected fields at t2:\n%v", diff)
	}

	expectedCache := map[int]map[string]fieldCache{
		1: {
			"i1__": {
				Cache: map[string]struct{}{"f3": {}},
			},
		},
		2: {},
	}
	if diff := cmp.Diff(c.FieldsCaches, expectedCache, cmpopts.IgnoreUnexported(fieldCache{})); diff != "" {
		t.Errorf("Unexpected cache state (purge might have failed):\n%v", diff)
	}
}
