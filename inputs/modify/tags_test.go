// Copyright 2015-2026 Bleemeo
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

package modify

import (
	"maps"
	"testing"
	"time"

	"github.com/bleemeo/glouton/inputs/internal"
	"github.com/bleemeo/glouton/types"
	"github.com/google/go-cmp/cmp"
	"github.com/influxdata/telegraf"
)

type fixedInput struct {
	measurementName string
	tags            map[string]string
	fields          map[string]any
	now             time.Time
}

func (i *fixedInput) Gather(acc telegraf.Accumulator) error {
	tagsCopy := maps.Clone(i.tags)
	fieldsCopy := maps.Clone(i.fields)
	acc.AddFields(i.measurementName, fieldsCopy, tagsCopy, i.now)

	return nil
}

func (*fixedInput) SampleConfig() string {
	return ""
}

// sharedTagsInput mimics inputs like Telegraf MySQL that reuse the same tags map
// across multiple AddFields calls within a single Gather.
type sharedTagsInput struct {
	measurementName string
	tags            map[string]string
	fieldSets       []map[string]any
	now             time.Time
}

func (i *sharedTagsInput) Gather(acc telegraf.Accumulator) error {
	sharedTags := maps.Clone(i.tags)

	for _, fields := range i.fieldSets {
		acc.AddFields(i.measurementName, maps.Clone(fields), sharedTags, i.now)
	}

	return nil
}

func (*sharedTagsInput) SampleConfig() string {
	return ""
}

func TestAddInstance(t *testing.T) {
	const (
		containerName   = "container_name"
		measurementName = "fixed_measure"
		tagValue        = "value"
	)

	fields := map[string]any{"cpu": 4.2}
	now := time.Now()

	tests := []struct {
		name     string
		instance string
		tags     map[string]string
		want     []internal.Measurement
	}{
		{
			name:     "instance-without-tags",
			instance: containerName,
			tags:     map[string]string{},
			want: []internal.Measurement{
				{
					Name:   measurementName,
					Fields: fields,
					Tags:   map[string]string{types.LabelItem: containerName},
					T:      []time.Time{now},
				},
			},
		},
		{
			name:     "instance-with-tags",
			instance: containerName,
			tags: map[string]string{
				"key": tagValue,
			},
			want: []internal.Measurement{
				{
					Name:   measurementName,
					Fields: fields,
					Tags:   map[string]string{types.LabelItem: containerName, "key": "value"},
					T:      []time.Time{now},
				},
			},
		},
		{
			name:     "instance-with-item",
			instance: containerName,
			tags: map[string]string{
				types.LabelItem: tagValue,
			},
			want: []internal.Measurement{
				{
					Name:   measurementName,
					Fields: fields,
					Tags:   map[string]string{types.LabelItem: containerName + "_value"},
					T:      []time.Time{now},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			acc := &internal.StoreAccumulator{}

			input := AddInstance(&fixedInput{tags: tt.tags, measurementName: measurementName, fields: fields, now: now}, tt.instance)

			if err := input.Gather(acc); err != nil {
				t.Error(err)
			}

			if diff := cmp.Diff(tt.want, acc.Measurement); diff != "" {
				t.Errorf("measurement mismatch (-want +got)\n%s", diff)
			}
		})
	}
}

// TestAddRenameCallbackInputWithSecrets verifies that AddRenameCallback on an InputWithSecrets
// adds the callback directly to the embedded *Input instead of wrapping it in a new *internal.Input.
func TestAddRenameCallbackInputWithSecrets(t *testing.T) {
	innerInput := &internal.Input{Input: &fixedInput{}}
	iws := internal.InputWithSecrets{Input: innerInput, Count: 1}

	result := AddRenameCallback(iws, func(labels map[string]string, annotations types.MetricAnnotations) (map[string]string, types.MetricAnnotations) {
		return labels, annotations
	})

	got, ok := result.(internal.InputWithSecrets)
	if !ok {
		t.Fatalf("expected InputWithSecrets, got %T", result)
	}

	if got.Input != innerInput {
		t.Error("expected the same *Input, got a different one (unnecessary wrapper was created)")
	}

	if len(innerInput.Accumulator.RenameCallbacks) != 1 {
		t.Errorf("expected 1 callback on the inner Input, got %d", len(innerInput.Accumulator.RenameCallbacks))
	}
}

// TestAddInstanceSharedTags verifies that AddInstance does not corrupt the item label when
// an input (like the Telegraf MySQL plugin) reuses the same tags map across multiple AddFields calls.
func TestAddInstanceSharedTags(t *testing.T) {
	const (
		containerName   = "my-mysql"
		measurementName = "mysql"
	)

	fieldSet1 := map[string]any{"queries": 100.0}
	fieldSet2 := map[string]any{"slow_queries": 5.0}
	fieldSet3 := map[string]any{"threads_connected": 10.0}
	now := time.Now()

	acc := &internal.StoreAccumulator{}

	input := AddInstance(&sharedTagsInput{
		measurementName: measurementName,
		tags:            map[string]string{"server": "127.0.0.1:3306"},
		fieldSets:       []map[string]any{fieldSet1, fieldSet2, fieldSet3},
		now:             now,
	}, containerName)

	if err := input.Gather(acc); err != nil {
		t.Error(err)
	}

	expectedTags := map[string]string{
		"server":        "127.0.0.1:3306",
		types.LabelItem: containerName,
	}

	for _, m := range acc.Measurement {
		if diff := cmp.Diff(expectedTags, m.Tags); diff != "" {
			t.Errorf("tags mismatch for measurement %q (-want +got)\n%s", m.Name, diff)
		}
	}

	if len(acc.Measurement) != 3 {
		t.Errorf("expected 3 measurements, got %d", len(acc.Measurement))
	}
}
