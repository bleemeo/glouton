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

package synchronizer

import (
	"net/url"
	"strconv"
	"syscall"
	"testing"

	"github.com/bleemeo/bleemeo-go"
	"github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/config"

	"github.com/google/go-cmp/cmp"
)

func TestCensorSecretItem(t *testing.T) {
	configItems := []config.Item{
		{
			Key: "item 1",
			Value: map[string]any{
				"secret": "hello",
				"b":      map[string]any{"key": "123"},
			},
		},
	}
	items := make(map[comparableConfigItem]any, len(configItems))

	backedUpConfigItems := deepCopy(configItems)

	for _, item := range configItems {
		item.Value = config.CensorSecretItem(item.Key, item.Value)

		key := comparableConfigItem{
			Key:      item.Key,
			Priority: item.Priority,
			Source:   bleemeoItemSourceFromConfigSource(item.Source),
			Path:     item.Path,
			Type:     bleemeoItemTypeFromConfigType(item.Type),
		}

		items[key] = item.Value
	}

	expectedCensoring := map[comparableConfigItem]any{
		{"item 1", 0, 1, "", 0}: map[string]any{
			"b":      map[string]any{"key": "*****"},
			"secret": "*****",
		},
	}

	if !cmp.Equal(items, expectedCensoring) {
		t.Log(cmp.Diff(items, expectedCensoring))
		t.Fatal("Unexpected censoring result")
	}

	if !cmp.Equal(configItems, backedUpConfigItems) {
		t.Fatal("Initial list have been modified.")
	}
}

func deepCopy(items []config.Item) []config.Item {
	cpy := make([]config.Item, len(items))

	for i, item := range items {
		cpy[i] = config.Item{Key: item.Key, Value: deepCopyValue(item.Value)}
	}

	return cpy
}

func deepCopyValue(value any) any {
	if valueAsMap, isMap := value.(map[string]any); isMap {
		m := make(map[string]any, len(valueAsMap))

		for k, v := range valueAsMap {
			m[k] = deepCopyValue(v)
		}

		return m
	}

	if valueAsSlice, isSlice := value.([]any); isSlice {
		s := make([]any, len(valueAsSlice))

		for i, e := range valueAsSlice {
			s[i] = deepCopyValue(e)
		}

		return s
	}

	return value
}

func TestTryImproveRegisterError(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		err              error
		expectedErrorStr string
	}{
		{
			err:              &url.Error{Op: "GET", URL: "url", Err: syscall.ECONNREFUSED},
			expectedErrorStr: "GET \"url\": connection refused",
		},
		{
			err: &bleemeo.APIError{
				Response: []byte("[{\"value\":[\"This field may not be null.\"]}, {}]"),
			},
			expectedErrorStr: "can't register the following item:\n- a.b: This field may not be null.",
		},
		{
			err: &bleemeo.APIError{
				Response: []byte("[{\"value\":[\"This field may not something.\"]},{\"value\":[\"This field may not be null.\", \"Something else.\"]}]"),
			},
			expectedErrorStr: "can't register the following items:\n- a.b: This field may not something.\n- 1.2.3: This field may not be null. / Something else.",
		},
	}

	configItems := []types.GloutonConfigItem{
		{Key: "a.b"},
		{Key: "1.2.3"},
	}

	for i, tc := range testCases {
		t.Run(strconv.Itoa(i+1), func(t *testing.T) {
			t.Parallel()

			outputErr := tryImproveRegisterError(tc.err, configItems)
			if diff := cmp.Diff(outputErr.Error(), tc.expectedErrorStr); diff != "" {
				t.Fatal("Unexpected error (-want +got):\n", diff)
			}
		})
	}
}
