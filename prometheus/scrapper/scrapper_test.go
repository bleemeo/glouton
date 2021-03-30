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

// nolint: scopelint
package scrapper

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/prometheus/pkg/labels"
)

func labelsToDTO(input []labels.Labels) []*dto.MetricFamily {
	resultIndex := make(map[string]int)
	result := make([]*dto.MetricFamily, 0, len(input))
	dummyStr := "dummy"

	for _, m := range input {
		name := m.Get("__name__")

		idx, ok := resultIndex[name]
		if !ok {
			idx = len(result)
			resultIndex[name] = idx

			result = append(result, &dto.MetricFamily{
				Name: &name,
				Help: &dummyStr,
				Type: dto.MetricType_COUNTER.Enum(),
			})
		}

		mf := result[idx]
		lbls := make([]*dto.LabelPair, 0, len(m)-1)

		for _, l := range m {
			l := l

			if l.Name == "__name__" {
				continue
			}

			lbls = append(lbls, &dto.LabelPair{
				Name:  &l.Name,
				Value: &l.Value,
			})
		}

		mf.Metric = append(mf.Metric, &dto.Metric{
			Label: lbls,
		})
	}

	return result
}

func DTOtoMetricLabels(input []*dto.MetricFamily) []labels.Labels {
	result := make([]labels.Labels, 0, len(input))

	for _, mf := range input {
		for _, m := range mf.Metric {
			result = append(result, dto2Labels(*mf.Name, m))
		}
	}

	return result
}

// nolint: dupl
func TestTarget_filter(t *testing.T) {
	type fields struct {
		AllowList      []string
		DenyList       []string
		IncludeDefault bool
	}

	tests := []struct {
		name   string
		fields fields
		input  []labels.Labels
		want   []labels.Labels
	}{
		{
			name:   "zero-pass-none",
			fields: fields{},
			input: []labels.Labels{
				labels.FromMap(map[string]string{"__name__": "metric1"}),
				labels.FromMap(map[string]string{"__name__": "process_cpu_seconds_total"}),
				labels.FromMap(map[string]string{"__name__": "with_labels", "item": "value"}),
			},
			want: []labels.Labels{},
		},
		{
			name: "include-default",
			fields: fields{
				IncludeDefault: true,
			},
			input: []labels.Labels{
				labels.FromMap(map[string]string{"__name__": "metric1"}),
				labels.FromMap(map[string]string{"__name__": "process_cpu_seconds_total"}),
				labels.FromMap(map[string]string{"__name__": "process_resident_memory_bytes"}),
				labels.FromMap(map[string]string{"__name__": "process_start_time_seconds"}),
				labels.FromMap(map[string]string{"__name__": "with_labels", "item": "value"}),
			},
			want: []labels.Labels{
				labels.FromMap(map[string]string{"__name__": "process_cpu_seconds_total"}),
				labels.FromMap(map[string]string{"__name__": "process_resident_memory_bytes"}),
			},
		},
		{
			name: "allow-simple",
			fields: fields{
				AllowList: []string{"metric1", "metric2", "with_labels"},
			},
			input: []labels.Labels{
				labels.FromMap(map[string]string{"__name__": "metric1"}),
				labels.FromMap(map[string]string{"__name__": "metric2"}),
				labels.FromMap(map[string]string{"__name__": "metric3"}),
				labels.FromMap(map[string]string{"__name__": "process_cpu_seconds_total"}),
				labels.FromMap(map[string]string{"__name__": "process_resident_memory_bytes"}),
				labels.FromMap(map[string]string{"__name__": "process_start_time_seconds"}),
				labels.FromMap(map[string]string{"__name__": "with_labels", "item": "value"}),
				labels.FromMap(map[string]string{"__name__": "with_labels_more", "item": "value"}),
			},
			want: []labels.Labels{
				labels.FromMap(map[string]string{"__name__": "metric1"}),
				labels.FromMap(map[string]string{"__name__": "metric2"}),
				labels.FromMap(map[string]string{"__name__": "with_labels", "item": "value"}),
			},
		},
		{
			name: "allow-simple-with-default",
			fields: fields{
				AllowList:      []string{"metric1", "metric2", "with_labels"},
				DenyList:       []string{},
				IncludeDefault: true,
			},
			input: []labels.Labels{
				labels.FromMap(map[string]string{"__name__": "metric1"}),
				labels.FromMap(map[string]string{"__name__": "metric2"}),
				labels.FromMap(map[string]string{"__name__": "metric3"}),
				labels.FromMap(map[string]string{"__name__": "process_cpu_seconds_total"}),
				labels.FromMap(map[string]string{"__name__": "process_resident_memory_bytes"}),
				labels.FromMap(map[string]string{"__name__": "process_start_time_seconds"}),
				labels.FromMap(map[string]string{"__name__": "with_labels", "item": "value"}),
				labels.FromMap(map[string]string{"__name__": "with_labels_more", "item": "value"}),
			},
			want: []labels.Labels{
				labels.FromMap(map[string]string{"__name__": "metric1"}),
				labels.FromMap(map[string]string{"__name__": "metric2"}),
				labels.FromMap(map[string]string{"__name__": "process_cpu_seconds_total"}),
				labels.FromMap(map[string]string{"__name__": "process_resident_memory_bytes"}),
				labels.FromMap(map[string]string{"__name__": "with_labels", "item": "value"}),
			},
		},
		{
			name: "pass-all",
			fields: fields{
				AllowList:      []string{"*"},
				DenyList:       []string{},
				IncludeDefault: false,
			},
			input: []labels.Labels{
				labels.FromMap(map[string]string{"__name__": "metric1"}),
				labels.FromMap(map[string]string{"__name__": "process_cpu_seconds_total"}),
				labels.FromMap(map[string]string{"__name__": "process_resident_memory_bytes"}),
				labels.FromMap(map[string]string{"__name__": "process_start_time_seconds"}),
				labels.FromMap(map[string]string{"__name__": "with_labels", "item": "value"}),
			},
			want: []labels.Labels{
				labels.FromMap(map[string]string{"__name__": "metric1"}),
				labels.FromMap(map[string]string{"__name__": "process_cpu_seconds_total"}),
				labels.FromMap(map[string]string{"__name__": "process_resident_memory_bytes"}),
				labels.FromMap(map[string]string{"__name__": "process_start_time_seconds"}),
				labels.FromMap(map[string]string{"__name__": "with_labels", "item": "value"}),
			},
		},
		{
			name: "pass-all2",
			fields: fields{
				AllowList:      []string{"*"},
				DenyList:       []string{},
				IncludeDefault: true,
			},
			input: []labels.Labels{
				labels.FromMap(map[string]string{"__name__": "metric1"}),
				labels.FromMap(map[string]string{"__name__": "process_cpu_seconds_total"}),
				labels.FromMap(map[string]string{"__name__": "process_resident_memory_bytes"}),
				labels.FromMap(map[string]string{"__name__": "process_start_time_seconds"}),
				labels.FromMap(map[string]string{"__name__": "with_labels", "item": "value"}),
			},
			want: []labels.Labels{
				labels.FromMap(map[string]string{"__name__": "metric1"}),
				labels.FromMap(map[string]string{"__name__": "process_cpu_seconds_total"}),
				labels.FromMap(map[string]string{"__name__": "process_resident_memory_bytes"}),
				labels.FromMap(map[string]string{"__name__": "process_start_time_seconds"}),
				labels.FromMap(map[string]string{"__name__": "with_labels", "item": "value"}),
			},
		},
		{
			name: "deny",
			fields: fields{
				AllowList:      []string{"*"},
				DenyList:       []string{"metric1", "with_labels", "process_cpu_seconds_total"},
				IncludeDefault: true,
			},
			input: []labels.Labels{
				labels.FromMap(map[string]string{"__name__": "metric1"}),
				labels.FromMap(map[string]string{"__name__": "metric2"}),
				labels.FromMap(map[string]string{"__name__": "metric3"}),
				labels.FromMap(map[string]string{"__name__": "process_cpu_seconds_total"}),
				labels.FromMap(map[string]string{"__name__": "process_resident_memory_bytes"}),
				labels.FromMap(map[string]string{"__name__": "process_start_time_seconds"}),
				labels.FromMap(map[string]string{"__name__": "with_labels", "item": "value"}),
				labels.FromMap(map[string]string{"__name__": "with_labels", "item": "value2"}),
				labels.FromMap(map[string]string{"__name__": "with_labels_more", "item": "value"}),
				labels.FromMap(map[string]string{"__name__": "with_labels_more", "item": "value2"}),
			},
			want: []labels.Labels{
				labels.FromMap(map[string]string{"__name__": "metric2"}),
				labels.FromMap(map[string]string{"__name__": "metric3"}),
				labels.FromMap(map[string]string{"__name__": "process_resident_memory_bytes"}),
				labels.FromMap(map[string]string{"__name__": "process_start_time_seconds"}),
				labels.FromMap(map[string]string{"__name__": "with_labels_more", "item": "value"}),
				labels.FromMap(map[string]string{"__name__": "with_labels_more", "item": "value2"}),
			},
		},
		{
			name: "deny-all",
			fields: fields{
				AllowList:      []string{"*"},
				DenyList:       []string{"*"},
				IncludeDefault: true,
			},
			input: []labels.Labels{
				labels.FromMap(map[string]string{"__name__": "metric1"}),
				labels.FromMap(map[string]string{"__name__": "process_cpu_seconds_total"}),
				labels.FromMap(map[string]string{"__name__": "process_resident_memory_bytes"}),
				labels.FromMap(map[string]string{"__name__": "process_start_time_seconds"}),
				labels.FromMap(map[string]string{"__name__": "with_labels", "item": "value"}),
			},
			want: []labels.Labels{},
		},
		{
			name: "deny-glob",
			fields: fields{
				AllowList:      []string{"*"},
				DenyList:       []string{"metric*", "process*"},
				IncludeDefault: true,
			},
			input: []labels.Labels{
				labels.FromMap(map[string]string{"__name__": "metric1"}),
				labels.FromMap(map[string]string{"__name__": "metric2"}),
				labels.FromMap(map[string]string{"__name__": "metric3"}),
				labels.FromMap(map[string]string{"__name__": "process_cpu_seconds_total"}),
				labels.FromMap(map[string]string{"__name__": "process_resident_memory_bytes"}),
				labels.FromMap(map[string]string{"__name__": "process_start_time_seconds"}),
				labels.FromMap(map[string]string{"__name__": "with_labels", "item": "value"}),
				labels.FromMap(map[string]string{"__name__": "with_labels", "item": "value2"}),
				labels.FromMap(map[string]string{"__name__": "with_labels_more", "item": "value"}),
				labels.FromMap(map[string]string{"__name__": "with_labels_more", "item": "value2"}),
			},
			want: []labels.Labels{
				labels.FromMap(map[string]string{"__name__": "with_labels", "item": "value"}),
				labels.FromMap(map[string]string{"__name__": "with_labels", "item": "value2"}),
				labels.FromMap(map[string]string{"__name__": "with_labels_more", "item": "value"}),
				labels.FromMap(map[string]string{"__name__": "with_labels_more", "item": "value2"}),
			},
		},
		{
			name: "deny-matcher",
			fields: fields{
				AllowList:      []string{"*"},
				DenyList:       []string{"{item=\"value\"}"},
				IncludeDefault: true,
			},
			input: []labels.Labels{
				labels.FromMap(map[string]string{"__name__": "metric1"}),
				labels.FromMap(map[string]string{"__name__": "metric2"}),
				labels.FromMap(map[string]string{"__name__": "metric2", "item": "value"}),
				labels.FromMap(map[string]string{"__name__": "process_cpu_seconds_total"}),
				labels.FromMap(map[string]string{"__name__": "process_resident_memory_bytes"}),
				labels.FromMap(map[string]string{"__name__": "process_start_time_seconds"}),
				labels.FromMap(map[string]string{"__name__": "with_labels", "item": "value"}),
				labels.FromMap(map[string]string{"__name__": "with_labels", "item": "value2"}),
			},
			want: []labels.Labels{
				labels.FromMap(map[string]string{"__name__": "metric1"}),
				labels.FromMap(map[string]string{"__name__": "metric2"}),
				labels.FromMap(map[string]string{"__name__": "process_cpu_seconds_total"}),
				labels.FromMap(map[string]string{"__name__": "process_resident_memory_bytes"}),
				labels.FromMap(map[string]string{"__name__": "process_start_time_seconds"}),
				labels.FromMap(map[string]string{"__name__": "with_labels", "item": "value2"}),
			},
		},
		{
			name: "allow-matcher",
			fields: fields{
				AllowList:      []string{"with_labels{item=\"value\"}"},
				DenyList:       []string{},
				IncludeDefault: false,
			},
			input: []labels.Labels{
				labels.FromMap(map[string]string{"__name__": "metric1"}),
				labels.FromMap(map[string]string{"__name__": "metric2"}),
				labels.FromMap(map[string]string{"__name__": "metric2", "item": "value"}),
				labels.FromMap(map[string]string{"__name__": "process_cpu_seconds_total"}),
				labels.FromMap(map[string]string{"__name__": "process_resident_memory_bytes"}),
				labels.FromMap(map[string]string{"__name__": "process_start_time_seconds"}),
				labels.FromMap(map[string]string{"__name__": "with_labels", "item": "value"}),
				labels.FromMap(map[string]string{"__name__": "with_labels", "item": "value2"}),
			},
			want: []labels.Labels{
				labels.FromMap(map[string]string{"__name__": "with_labels", "item": "value"}),
			},
		},
		{
			name: "complex-allow",
			fields: fields{
				AllowList:      []string{"metric{item=~\"value[0-9]+\",mode=\"\",label=~\"a.*\",label=~\".*z\"}"},
				DenyList:       []string{},
				IncludeDefault: false,
			},
			input: []labels.Labels{
				labels.FromMap(map[string]string{"__name__": "process_cpu_seconds_total"}),
				labels.FromMap(map[string]string{"__name__": "metric"}),
				labels.FromMap(map[string]string{"__name__": "metric", "item": "value0"}),
				labels.FromMap(map[string]string{"__name__": "metric", "item": "value0", "label": "abc"}),
				labels.FromMap(map[string]string{"__name__": "metric", "item": "value0", "label": "xyz"}),
				labels.FromMap(map[string]string{"__name__": "metric", "item": "value0", "label": "abz"}),
				labels.FromMap(map[string]string{"__name__": "metric", "item": "value0", "label": "abz", "mode": "read"}),
				labels.FromMap(map[string]string{"__name__": "metric", "item": "value1", "label": "abz", "unused": "test"}),
				labels.FromMap(map[string]string{"__name__": "metric", "item": "value2", "label": "abz", "mode": ""}),
				labels.FromMap(map[string]string{"__name__": "metric", "item": "value", "label": "abz", "unused": "test"}),
				labels.FromMap(map[string]string{"__name__": "metric", "item": "valueB", "label": "abz", "unused": "test"}),
				labels.FromMap(map[string]string{"__name__": "metric1", "item": "value1", "label": "abz", "unused": "test"}),
			},
			want: []labels.Labels{
				labels.FromMap(map[string]string{"__name__": "metric", "item": "value0", "label": "abz"}),
				labels.FromMap(map[string]string{"__name__": "metric", "item": "value1", "label": "abz", "unused": "test"}),
				labels.FromMap(map[string]string{"__name__": "metric", "item": "value2", "label": "abz", "mode": ""}),
			},
		},
		{
			name: "allow-name-re",
			fields: fields{
				AllowList:      []string{"{__name__=~\"metric.*0\",__name__!=\"metric00\"}"},
				DenyList:       []string{},
				IncludeDefault: false,
			},
			input: []labels.Labels{
				labels.FromMap(map[string]string{"__name__": "process_cpu_seconds_total"}),
				labels.FromMap(map[string]string{"__name__": "metric"}),
				labels.FromMap(map[string]string{"__name__": "metric", "item": "value0"}),
				labels.FromMap(map[string]string{"__name__": "metric00", "item": "value0"}),
				labels.FromMap(map[string]string{"__name__": "metric01", "item": "value0"}),
				labels.FromMap(map[string]string{"__name__": "metric10", "item": "value0"}),
				labels.FromMap(map[string]string{"__name__": "metric11"}),
				labels.FromMap(map[string]string{"__name__": "metric20"}),
			},
			want: []labels.Labels{
				labels.FromMap(map[string]string{"__name__": "metric10", "item": "value0"}),
				labels.FromMap(map[string]string{"__name__": "metric20"}),
			},
		},
		{
			name: "all-without-deny",
			fields: fields{
				AllowList: []string{
					"full_name",
					"*suffix",
					"prefix*",
					"boundary*metric",
					"full_name2{}",
					"with_labels{item=\"test\"}",
					"multiple_name{__name__=\"multiple_name\",__name__!=\"not_allow\",__name__=~\".*name\"}",
					"{include_label!=\"\"}",
					"metric{include_label2=~\".*\"}",
				},
				DenyList:       []string{},
				IncludeDefault: false,
			},
			input: []labels.Labels{
				labels.FromMap(map[string]string{"__name__": "full_name", "row": "1"}),
				labels.FromMap(map[string]string{"__name__": "full_name", "row": "2", "exclude": "1"}),
				labels.FromMap(map[string]string{"__name__": "a_suffix", "row": "3"}),
				labels.FromMap(map[string]string{"__name__": "b_suffix", "row": "4"}),
				labels.FromMap(map[string]string{"__name__": "c_suffix", "row": "5", "exclude": "1"}),
				labels.FromMap(map[string]string{"__name__": "c_suffix_not", "row": "6"}),
				labels.FromMap(map[string]string{"__name__": "prefix_a", "row": "7"}),
				labels.FromMap(map[string]string{"__name__": "prefix", "row": "8"}),
				labels.FromMap(map[string]string{"__name__": "prefix", "row": "9"}),
				labels.FromMap(map[string]string{"__name__": "boundary*metric"}),
				labels.FromMap(map[string]string{"__name__": "boundarymetric"}),
				labels.FromMap(map[string]string{"__name__": "boundaryABCmetric"}),
				labels.FromMap(map[string]string{"__name__": "full_name2"}),
				labels.FromMap(map[string]string{"__name__": "with_labels", "item": "test"}),
				labels.FromMap(map[string]string{"__name__": "with_labels", "item": "test2"}),
				labels.FromMap(map[string]string{"__name__": "with_labels2", "item": "test"}),
				labels.FromMap(map[string]string{"__name__": "multiple_name"}),
				labels.FromMap(map[string]string{"__name__": "multiple_name", "item": "value"}),
				labels.FromMap(map[string]string{"__name__": "not_allow", "item": "value"}),
				labels.FromMap(map[string]string{"__name__": "my_name", "item": "value"}),
				labels.FromMap(map[string]string{"__name__": "whatever", "item": "value"}),
				labels.FromMap(map[string]string{"__name__": "whatever", "item": "value2", "include_label": "still-whatever"}),
			},
			want: []labels.Labels{
				labels.FromMap(map[string]string{"__name__": "full_name", "row": "1"}),
				labels.FromMap(map[string]string{"__name__": "full_name", "row": "2", "exclude": "1"}),
				labels.FromMap(map[string]string{"__name__": "a_suffix", "row": "3"}),
				labels.FromMap(map[string]string{"__name__": "b_suffix", "row": "4"}),
				labels.FromMap(map[string]string{"__name__": "c_suffix", "row": "5", "exclude": "1"}),
				labels.FromMap(map[string]string{"__name__": "prefix_a", "row": "7"}),
				labels.FromMap(map[string]string{"__name__": "prefix", "row": "8"}),
				labels.FromMap(map[string]string{"__name__": "prefix", "row": "9"}),
				labels.FromMap(map[string]string{"__name__": "boundary*metric"}),
				labels.FromMap(map[string]string{"__name__": "boundarymetric"}),
				labels.FromMap(map[string]string{"__name__": "boundaryABCmetric"}),
				labels.FromMap(map[string]string{"__name__": "full_name2"}),
				labels.FromMap(map[string]string{"__name__": "with_labels", "item": "test"}),
				labels.FromMap(map[string]string{"__name__": "multiple_name"}),
				labels.FromMap(map[string]string{"__name__": "multiple_name", "item": "value"}),
				labels.FromMap(map[string]string{"__name__": "whatever", "item": "value2", "include_label": "still-whatever"}),
			},
		},
		{
			name: "all-with-deny",
			fields: fields{
				AllowList: []string{
					"full_name",
					"*suffix",
					"prefix*",
					"boundary*metric",
					"full_name2{}",
					"with_labels{item=\"test\"}",
					"multiple_name{__name__=\"multiple_name\",__name__!=\"not_allow\",__name__=~\".*name\"}",
					"{include_label!=\"\"}",
					"metric{include_label2=~\".*\"}",
				},
				DenyList: []string{
					"whatever",
					"full_name{exclude=\"1\"}",
					"{row=~\"1|3|9\"}",
				},
				IncludeDefault: false,
			},
			input: []labels.Labels{
				labels.FromMap(map[string]string{"__name__": "full_name", "row": "1"}),
				labels.FromMap(map[string]string{"__name__": "full_name", "row": "2", "exclude": "1"}),
				labels.FromMap(map[string]string{"__name__": "a_suffix", "row": "3"}),
				labels.FromMap(map[string]string{"__name__": "b_suffix", "row": "4"}),
				labels.FromMap(map[string]string{"__name__": "c_suffix", "row": "5", "exclude": "1"}),
				labels.FromMap(map[string]string{"__name__": "c_suffix_not", "row": "6"}),
				labels.FromMap(map[string]string{"__name__": "prefix_a", "row": "7"}),
				labels.FromMap(map[string]string{"__name__": "prefix", "row": "8"}),
				labels.FromMap(map[string]string{"__name__": "prefix", "row": "9"}),
				labels.FromMap(map[string]string{"__name__": "boundary*metric"}),
				labels.FromMap(map[string]string{"__name__": "boundarymetric"}),
				labels.FromMap(map[string]string{"__name__": "boundaryABCmetric"}),
				labels.FromMap(map[string]string{"__name__": "full_name2"}),
				labels.FromMap(map[string]string{"__name__": "with_labels", "item": "test"}),
				labels.FromMap(map[string]string{"__name__": "with_labels", "item": "test2"}),
				labels.FromMap(map[string]string{"__name__": "with_labels2", "item": "test"}),
				labels.FromMap(map[string]string{"__name__": "multiple_name"}),
				labels.FromMap(map[string]string{"__name__": "multiple_name", "item": "value"}),
				labels.FromMap(map[string]string{"__name__": "not_allow", "item": "value"}),
				labels.FromMap(map[string]string{"__name__": "my_name", "item": "value"}),
				labels.FromMap(map[string]string{"__name__": "whatever", "item": "value"}),
				labels.FromMap(map[string]string{"__name__": "whatever", "item": "value2", "include_label": "still-whatever"}),
			},
			want: []labels.Labels{
				labels.FromMap(map[string]string{"__name__": "b_suffix", "row": "4"}),
				labels.FromMap(map[string]string{"__name__": "c_suffix", "row": "5", "exclude": "1"}),
				labels.FromMap(map[string]string{"__name__": "prefix_a", "row": "7"}),
				labels.FromMap(map[string]string{"__name__": "prefix", "row": "8"}),
				labels.FromMap(map[string]string{"__name__": "boundary*metric"}),
				labels.FromMap(map[string]string{"__name__": "boundarymetric"}),
				labels.FromMap(map[string]string{"__name__": "boundaryABCmetric"}),
				labels.FromMap(map[string]string{"__name__": "full_name2"}),
				labels.FromMap(map[string]string{"__name__": "with_labels", "item": "test"}),
				labels.FromMap(map[string]string{"__name__": "multiple_name"}),
				labels.FromMap(map[string]string{"__name__": "multiple_name", "item": "value"}),
			},
		},
	}

	for _, tt := range tests {
		tmp := DTOtoMetricLabels(labelsToDTO(tt.input))
		if diff := cmp.Diff(tt.input, tmp); diff != "" {
			t.Fatalf("DTOtoMetricLabels(labelsToDTO()) do not kept order: %v", diff)
		}

		t.Run(tt.name, func(t *testing.T) {
			target := &Target{
				AllowList:      tt.fields.AllowList,
				DenyList:       tt.fields.DenyList,
				IncludeDefault: tt.fields.IncludeDefault,
			}
			gotDTO := target.filter(labelsToDTO(tt.input))
			got := DTOtoMetricLabels(gotDTO)

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Target.filter() != want: %v", diff)
			}
		})
	}
}
