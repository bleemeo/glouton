package synchronizer

import (
	"glouton/config"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestCensorSecretItem(t *testing.T) {
	configItems := []config.Item{
		{
			Key: "item 1",
			Value: map[string]interface{}{
				"secret": "hello",
				"b":      map[string]interface{}{"key": "123"},
			},
		},
	}
	items := make(map[comparableConfigItem]interface{}, len(configItems))

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

func deepCopyValue(value interface{}) interface{} {
	if valueAsMap, isMap := value.(map[string]interface{}); isMap {
		m := make(map[string]interface{}, len(valueAsMap))

		for k, v := range valueAsMap {
			m[k] = deepCopyValue(v)
		}

		return m
	}

	if valueAsSlice, isSlice := value.([]interface{}); isSlice {
		s := make([]interface{}, len(valueAsSlice))

		for i, e := range valueAsSlice {
			s[i] = deepCopyValue(e)
		}

		return s
	}

	return value
}
