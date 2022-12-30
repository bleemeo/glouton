// Copyright 2015-2022 Bleemeo
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
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	yamlParser "github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/file"
)

// Test that conversion hooks are correctly applied in the loader.
func TestHooksLoader(t *testing.T) {
	loader := configLoader{}

	err := loader.Load("", file.Provider("testdata/loader_hooks.conf"), yamlParser.Parser())
	if err != nil {
		t.Fatalf("Failed to load config: %s", err)
	}

	expected := []Item{
		{
			Key:      "bleemeo.enable",
			Value:    true,
			Source:   "file",
			Priority: 1,
		},
		{
			Key: "metric.softstatus_period",
			Value: map[string]any{
				"cpu_used": float64(60),
			},
			Source:   "file",
			Priority: 1,
		},
	}

	lessFunc := func(x Item, y Item) bool {
		return x.Key < y.Key
	}

	if diff := cmp.Diff(expected, loader.items, cmpopts.SortSlices(lessFunc)); diff != "" {
		t.Fatalf("diff:\n%s", diff)
	}
}
