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

	expected := []item{
		{
			Key:      "bleemeo.enable",
			Value:    true,
			Source:   "file",
			Priority: 1,
		},
		{
			Key: "metric.softstatus_period",
			Value: map[string]int{
				"cpu_used": 60,
			},
			Source:   "file",
			Priority: 1,
		},
	}

	lessFunc := func(x item, y item) bool {
		return x.Key < y.Key
	}

	if diff := cmp.Diff(expected, loader.items, cmpopts.SortSlices(lessFunc)); diff != "" {
		t.Fatalf("diff:\n%s", diff)
	}
}
