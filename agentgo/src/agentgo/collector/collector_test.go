package collector

import (
	"testing"

	"github.com/influxdata/telegraf"
)

type mockInput struct {
	Name            string
	GatherCallCount int
}

func (m *mockInput) Description() string {
	return m.Name
}

func (m *mockInput) Gather(acc telegraf.Accumulator) error {
	m.GatherCallCount++
	return nil
}

func (m *mockInput) SampleConfig() string {
	return ""
}

func TestAddRemove(t *testing.T) {
	c := New(nil)
	id1 := c.AddInput(&mockInput{Name: "input1"})
	id2 := c.AddInput(&mockInput{Name: "input2"})

	if len(c.inputs) != 2 {
		t.Errorf("len(c.inputs) == %v, want %v", len(c.inputs), 2)
	}

	c.RemoveInput(id1)
	if len(c.inputs) != 1 {
		t.Errorf("len(c.inputs) == %v, want %v", len(c.inputs), 1)
	}
	if input, ok := c.inputs[id2]; !ok {
		t.Errorf("c.inputs[id2=%v] == nil, want input2", id2)
	} else if input.Description() != "input2" {
		t.Errorf("c.inputs[id2=%v].Description() == %v, want %v", id2, input.Description(), "input2")
	}
}

func TestRun(t *testing.T) {
	c := New(nil)
	c.run()

	input := &mockInput{Name: "input1"}
	c.AddInput(input)
	c.run()
	if input.GatherCallCount != 1 {
		t.Errorf("input.GatherCallCount == %v, want %v", input.GatherCallCount, 1)
	}
	c.run()
	if input.GatherCallCount != 2 {
		t.Errorf("input.GatherCallCount == %v, want %v", input.GatherCallCount, 2)
	}
}
