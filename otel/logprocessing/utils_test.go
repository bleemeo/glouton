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

package logprocessing

import (
	"strconv"
	"testing"

	"github.com/bleemeo/glouton/config"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/entry"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/helper"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/parser/regex"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/parser/time"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/transformer/add"
)

func TestValidateContainerOperators(t *testing.T) {
	t.Parallel()

	globalOpsConfig := map[string][]config.OTELOperator{
		"op-1": {},
		"op-2": {},
	}

	testCases := []struct {
		ctrOps         map[string]string
		expectedCtrOps map[string]string
	}{
		{
			ctrOps: map[string]string{
				"ctr-1": "op-1",
				"ctr-2": "op-2",
			},
			expectedCtrOps: map[string]string{
				"ctr-1": "op-1",
				"ctr-2": "op-2",
			},
		},
		{
			ctrOps: map[string]string{
				"ctr-1": "",
				"ctr-2": "op-3",
			},
			expectedCtrOps: map[string]string{},
		},
	}

	for i, tc := range testCases {
		t.Run(strconv.Itoa(i+1), func(t *testing.T) {
			t.Parallel()

			res := validateContainerOperators(tc.ctrOps, globalOpsConfig)
			if diff := cmp.Diff(tc.expectedCtrOps, res); diff != "" {
				t.Fatalf("Unexpected result (-want +got):\n%s", diff)
			}
		})
	}
}

func TestBuildOperators(t *testing.T) {
	t.Parallel()

	rawOperators := []map[string]any{
		{
			"type":  "add",
			"field": "resource['service.name']",
			"value": "apache_server",
		},
		{
			"type":  "regex_parser",
			"regex": `^(?P<time>\[.*?\])$`,
		},
		{
			"type":       "time_parser",
			"parse_from": "attributes.time",
			"layout":     "[%d/%b/%Y:%H:%M:%S %z]",
		},
	}

	expectedOperators := []operator.Config{
		{
			Builder: &add.Config{
				TransformerConfig: helper.TransformerConfig{
					WriterConfig: helper.WriterConfig{
						BasicConfig: helper.BasicConfig{
							OperatorID:   "add",
							OperatorType: "add",
						},
					},
					OnError: "send",
				},
				Field: entry.Field{
					FieldInterface: entry.ResourceField{
						Keys: []string{"service.name"},
					},
				},
				Value: "apache_server",
			},
		},
		{
			Builder: &regex.Config{
				ParserConfig: helper.ParserConfig{
					TransformerConfig: helper.TransformerConfig{
						WriterConfig: helper.WriterConfig{
							BasicConfig: helper.BasicConfig{
								OperatorID:   "regex_parser",
								OperatorType: "regex_parser",
							},
						},
						OnError: "send",
					},
					ParseFrom: entry.Field{
						FieldInterface: entry.BodyField{
							Keys: []string{},
						},
					},
					ParseTo: entry.RootableField{
						Field: entry.Field{
							FieldInterface: entry.AttributeField{
								Keys: []string{},
							},
						},
					},
				},
				Regex: `^(?P<time>\[.*?\])$`,
			},
		},
		{
			Builder: &time.Config{
				TransformerConfig: helper.TransformerConfig{
					WriterConfig: helper.WriterConfig{
						BasicConfig: helper.BasicConfig{
							OperatorID:   "time_parser",
							OperatorType: "time_parser",
						},
					},
					OnError: "send",
				},
				TimeParser: helper.TimeParser{
					ParseFrom: &entry.Field{
						FieldInterface: entry.AttributeField{
							Keys: []string{"time"},
						},
					},
					Layout:     "[%d/%b/%Y:%H:%M:%S %z]",
					LayoutType: "strptime",
				},
			},
		},
	}

	operators, err := buildOperators(rawOperators)
	if err != nil {
		t.Fatal("Failed to build operators:", err)
	}

	if diff := cmp.Diff(expectedOperators, operators, cmpopts.IgnoreUnexported(helper.TimeParser{})); diff != "" {
		t.Fatalf("Unexpected operators (-want +got):\n%s", diff)
	}
}
