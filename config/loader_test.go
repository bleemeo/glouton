package config

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	yamlParser "github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/file"
)

// Test that conversion hooks are correctly applied in the loader.
func TestLoaderHooks(t *testing.T) {
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
			Priority: 0,
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

	if diff := cmp.Diff(expected, loader.items); diff != "" {
		t.Fatalf("diff:\n%s", diff)
	}
}
