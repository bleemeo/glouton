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

package config

import (
	"regexp"
	"strconv"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestFlattenOps(t *testing.T) {
	t.Parallel()

	cases := []struct {
		input          []any
		expectedPanic  bool
		expectedOutput []OTELOperator
	}{
		{
			input: []any{
				OTELOperator{"a": "b"},
				OTELOperator{"c": "d"},
			},
			expectedPanic: false,
			expectedOutput: []OTELOperator{
				{"a": "b"},
				{"c": "d"},
			},
		},
		{
			input: []any{
				[]OTELOperator{
					{"a": "b"},
					{"c": "d"},
				},
			},
			expectedPanic: false,
			expectedOutput: []OTELOperator{
				{"a": "b"},
				{"c": "d"},
			},
		},
		{
			input: []any{
				OTELOperator{"a": "b"},
				[]OTELOperator{
					{"c": "d"},
					{"e": "f"},
				},
				OTELOperator{"g": "h"},
				[]OTELOperator{
					{"i": "j"},
				},
			},
			expectedPanic: false,
			expectedOutput: []OTELOperator{
				{"a": "b"},
				{"c": "d"},
				{"e": "f"},
				{"g": "h"},
				{"i": "j"},
			},
		},
		{
			input: []any{
				[]OTELOperator{
					{"a": "b"},
				},
				[][]OTELOperator{
					{
						{"c": "d"},
					},
				},
			},
			expectedPanic: true,
		},
	}

	for i, tc := range cases {
		t.Run(strconv.Itoa(i+1), func(t *testing.T) {
			t.Parallel()

			defer func() {
				r := recover()
				if r == nil && tc.expectedPanic {
					t.Fatal("Expected the function to panic, but it didn't")
				} else if r != nil && !tc.expectedPanic {
					t.Fatal("Did not expect the function to panic, but it did:", r)
				}
			}()

			output := flattenOps(tc.input...)
			if diff := cmp.Diff(tc.expectedOutput, output); diff != "" {
				t.Fatalf("Unexpected output (-want, +got):\n%s", diff)
			}
		})
	}
}

func TestLogFormatsRegexps(t *testing.T) {
	t.Parallel()

	var compiled int

	for format, ops := range DefaultKnownLogFormats() {
		for i, op := range ops {
			re, found := op["regex"]
			if found {
				reStr, ok := re.(string)
				if !ok {
					t.Logf("[WARN] regex field in operator n°%d of log format %q is not a string but a %T", i+1, format, re)

					continue
				}

				_, err := regexp.Compile(reStr)
				if err != nil {
					t.Errorf("Failed to compile regex in operator n°%d of log format %q: %v", i+1, format, err)
				} else {
					compiled++
				}
			}
		}
	}

	t.Logf("Compiled %d regexps successfully", compiled)
}
