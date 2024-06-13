package synchronizer

import (
	"testing"

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
