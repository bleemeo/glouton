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

func TestAddInstance(t *testing.T) {
	const (
		containerName   = "container_name"
		measurementName = "fixed_measure"
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
				"key": "value",
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
				types.LabelItem: "value",
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
