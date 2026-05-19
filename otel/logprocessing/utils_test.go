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
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/parser/timeparser"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/transformer/add"
	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/filterprocessor"
)

func TestValidateContainerOperators(t *testing.T) {
	t.Parallel()

	globalOpsConfig := map[string][]config.OTELOperator{
		testOpID1: {},
		testOpID2: {},
	}

	testCases := []struct {
		ctrOps         map[string]string
		expectedCtrOps map[string]string
	}{
		{
			ctrOps: map[string]string{
				testContainerCtr1: "op-1",
				testContainerCtr2: "op-2",
			},
			expectedCtrOps: map[string]string{
				testContainerCtr1: "op-1",
				testContainerCtr2: "op-2",
			},
		},
		{
			ctrOps: map[string]string{
				testContainerCtr1: "",
				testContainerCtr2: "op-3",
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

func TestBuildLogFilterConfig(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		input           config.OTELFilters
		expectedOutput  filterprocessor.LogFilters
		expectedWarning string
		expectedError   string
	}{
		{
			input: config.OTELFilters{
				"exclude": map[string]any{
					"match_type": testRegexp,
					"bodies": []string{
						"GET",
					},
				},
			},
			expectedOutput: filterprocessor.LogFilters{
				Exclude: &filterprocessor.LogMatchProperties{
					LogMatchType: filterprocessor.LogMatchType(testRegexp),
					LogBodies: []string{
						"GET",
					},
				},
			},
		},
		{
			input: config.OTELFilters{
				testFilterLogRecord: []string{
					`IsMatch(body, "/nginx_status")`,
				},
			},
			expectedOutput: filterprocessor.LogFilters{
				LogConditions: []string{
					`IsMatch(body, "/nginx_status")`,
				},
			},
		},
		{
			input: config.OTELFilters{
				testFilterLogRecord: []string{
					"UnknownFunc(body)",
				},
			},
			expectedError: `unable to parse OTTL condition "UnknownFunc(body)": undefined function "UnknownFunc"`,
		},
		{
			input: config.OTELFilters{
				testFieldInclude: map[string]any{
					"match_type": "strict",
					"severity_texts": []string{
						"error",
					},
				},
				"excluded": map[string]any{}, // bad property name
			},
			expectedOutput: filterprocessor.LogFilters{
				Include: &filterprocessor.LogMatchProperties{
					LogMatchType: filterprocessor.LogMatchType("strict"),
					SeverityTexts: []string{
						"error",
					},
				},
			},
			expectedWarning: "some unknown field(s) were found: excluded",
		},
		{
			input: config.OTELFilters{
				testFieldInclude:    map[string]any{},
				"exclude":           map[string]any{},
				testFilterLogRecord: []string{},
			},
			expectedError: "cannot use \"logs.resource\", \"logs.log\" and the settings \"logs.include\", \"logs.exclude\" at the same time",
		},
	}

	for i, tc := range testCases {
		t.Run(strconv.Itoa(i+1), func(t *testing.T) {
			t.Parallel()

			output, warn, err := buildLogFilterConfig(tc.input)
			if err != nil {
				if tc.expectedError == "" {
					t.Fatal("Unexpected error:", err)
				}

				if err.Error() != tc.expectedError {
					t.Fatalf("Unexpected error: want %q, got %q", tc.expectedError, err.Error())
				}

				return
			}

			if warn != nil {
				if tc.expectedWarning == "" {
					t.Fatal("Unexpected warning:", warn)
				}

				if warn.Error() != tc.expectedWarning {
					t.Fatalf("Unexpected warning: want %q, got %q", tc.expectedWarning, warn.Error())
				}
			}

			if diff := cmp.Diff(tc.expectedOutput, output.Logs); diff != "" { //nolint: staticcheck
				t.Fatalf("Unexpected result (-want +got):\n%s", diff)
			}
		})
	}
}

func TestExpandOperators(t *testing.T) {
	t.Parallel()

	knownIncludes := map[string][]config.OTELOperator{
		"regex_time": {
			{
				testFieldType:  testRegexParser,
				testFieldRegex: testRegexTimePattern,
			},
			{
				testFieldType:      testTimeParser,
				testFieldParseFrom: testParseFromTime,
				testFieldLayout:    testTimeFmtLayout,
			},
		},
	}

	opsConfig := []config.OTELOperator{
		{
			testFieldType:  testFieldAdd,
			testFieldName:  testRouteServiceName,
			testFieldValue: testServiceApache,
		},
		{
			testFieldInclude: "regex_time",
		},
	}

	ops, err := expandOperators(opsConfig, knownIncludes, false)
	if err != nil {
		t.Fatal("Failed to expand operators:", err)
	}

	expectedOperators := []config.OTELOperator{
		{
			testFieldType:  testFieldAdd,
			testFieldName:  testRouteServiceName,
			testFieldValue: testServiceApache,
		},
		{
			testFieldType:  testRegexParser,
			testFieldRegex: testRegexTimePattern,
		},
		{
			testFieldType:      testTimeParser,
			testFieldParseFrom: testParseFromTime,
			testFieldLayout:    testTimeFmtLayout,
		},
	}

	if diff := cmp.Diff(expectedOperators, ops); diff != "" {
		t.Fatalf("Unexpected operators (-want +got):\n%s", diff)
	}
}

func TestExpandLogFormats(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		sourceLogFormats   map[string][]config.OTELOperator
		expectedLogFormats map[string][]config.OTELOperator
		expectedErrMsg     string
	}{
		{
			sourceLogFormats: map[string][]config.OTELOperator{
				testFmt1: {
					{
						testFieldType:  testFieldAdd,
						testFieldName:  testResServiceName,
						testFieldValue: testServiceApache,
					},
				},
				testFmt2: {
					{
						testFieldInclude: testFmt1,
					},
					{
						testFieldType: "move",
						"from":        testResServiceName,
						"to":          testRouteServiceName,
					},
				},
			},
			expectedLogFormats: map[string][]config.OTELOperator{
				testFmt1: {
					{
						testFieldType:  testFieldAdd,
						testFieldName:  testResServiceName,
						testFieldValue: testServiceApache,
					},
				},
				testFmt2: {
					{
						testFieldType:  testFieldAdd,
						testFieldName:  testResServiceName,
						testFieldValue: testServiceApache,
					},
					{
						testFieldType: "move",
						"from":        testResServiceName,
						"to":          testRouteServiceName,
					},
				},
			},
		},
		{
			sourceLogFormats: map[string][]config.OTELOperator{
				testFmt1: {
					{
						testFieldType:  testFieldAdd,
						testFieldName:  "resource.env",
						testFieldValue: `EXPR(env("KEY")`,
					},
					{
						testFieldInclude: testFmt2,
					},
				},
				testFmt2: {
					{
						testFieldType:  testFieldAdd,
						testFieldName:  testResServiceName,
						testFieldValue: testServiceApache,
					},
				},
			},
			expectedLogFormats: map[string][]config.OTELOperator{
				testFmt1: {
					{
						testFieldType:  testFieldAdd,
						testFieldName:  "resource.env",
						testFieldValue: `EXPR(env("KEY")`,
					},
					{
						testFieldType:  testFieldAdd,
						testFieldName:  testResServiceName,
						testFieldValue: testServiceApache,
					},
				},
				testFmt2: {
					{
						testFieldType:  testFieldAdd,
						testFieldName:  testResServiceName,
						testFieldValue: testServiceApache,
					},
				},
			},
		},
		{
			sourceLogFormats: map[string][]config.OTELOperator{
				testFmt1: {
					{
						testFieldInclude: testFmt2,
					},
				},
				testFmt2: {
					{
						testFieldInclude: "fmt-3",
					},
				},
				"fmt-3": {
					{
						testFieldType:  testFieldAdd,
						testFieldName:  testRouteServiceName,
						testFieldValue: testServiceApache,
					},
				},
			},
			expectedErrMsg: "\"fmt-1\": include reference \"fmt-2\" is recursive",
		},
	}

	for i, tc := range testCases {
		t.Run(strconv.Itoa(i+1), func(t *testing.T) {
			t.Parallel()

			result, err := expandLogFormats(tc.sourceLogFormats)
			if err != nil {
				if tc.expectedErrMsg == "" {
					t.Fatalf("Unexpected error: %v", err)
				} else if err.Error() != tc.expectedErrMsg {
					t.Fatalf("Unexpected error message: want %q, got %q", tc.expectedErrMsg, err.Error())
				}

				return
			} else if tc.expectedErrMsg != "" {
				t.Fatalf("Expected error %q, but got none", tc.expectedErrMsg)
			}

			if diff := cmp.Diff(tc.expectedLogFormats, result); diff != "" {
				t.Fatalf("Unexpected result (-want +got):\n%s", diff)
			}
		})
	}
}

func TestBuildOperators(t *testing.T) {
	t.Parallel()

	rawOperators := []config.OTELOperator{
		{
			testFieldType:  testFieldAdd,
			testFieldName:  testRouteServiceName,
			testFieldValue: testServiceApache,
		},
		{
			testFieldType:  testRegexParser,
			testFieldRegex: testRegexTimePattern,
		},
		{
			testFieldType:      testTimeParser,
			testFieldParseFrom: testParseFromTime,
			testFieldLayout:    testTimeFmtLayout,
		},
	}

	expectedOperators := []operator.Config{
		{
			Builder: &add.Config{
				TransformerConfig: helper.TransformerConfig{
					WriterConfig: helper.WriterConfig{
						BasicConfig: helper.BasicConfig{
							OperatorID:   testFieldAdd,
							OperatorType: testFieldAdd,
						},
					},
					OnError: testOnErrorSend,
				},
				Field: entry.Field{
					FieldInterface: entry.ResourceField{
						Keys: []string{testAttrServiceName},
					},
				},
				Value: testServiceApache,
			},
		},
		{
			Builder: &regex.Config{
				ParserConfig: helper.ParserConfig{
					TransformerConfig: helper.TransformerConfig{
						WriterConfig: helper.WriterConfig{
							BasicConfig: helper.BasicConfig{
								OperatorID:   testRegexParser,
								OperatorType: testRegexParser,
							},
						},
						OnError: testOnErrorSend,
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
				Regex: testRegexTimePattern,
			},
		},
		{
			Builder: &timeparser.Config{
				TransformerConfig: helper.TransformerConfig{
					WriterConfig: helper.WriterConfig{
						BasicConfig: helper.BasicConfig{
							OperatorID:   testTimeParser,
							OperatorType: testTimeParser,
						},
					},
					OnError: testOnErrorSend,
				},
				TimeParser: helper.TimeParser{
					ParseFrom: &entry.Field{
						FieldInterface: entry.AttributeField{
							Keys: []string{"time"},
						},
					},
					Layout:     testTimeFmtLayout,
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
