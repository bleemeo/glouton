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
	"testing"
	"time"

	"github.com/influxdata/telegraf"
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
