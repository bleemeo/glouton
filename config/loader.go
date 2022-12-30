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
	"encoding/json"
	"fmt"
	"glouton/logger"
	"math"
	"reflect"
	"strings"

	"github.com/knadh/koanf"
	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/structs"
	"github.com/mitchellh/mapstructure"
	"github.com/prometheus/client_golang/prometheus"
)

// configLoader loads the config from Koanf providers.
type configLoader struct {
	// Items loaded in the config.
	items []Item
	// Number of provider loaded, used to assign priority to items.
	loadCount int
}

// Item represents a single config key from a provider.
type Item struct {
	// The config Key (e.g. "bleemeo.enable").
	Key string
	// The Value for this config key.
	Value interface{}
	// Source of the config key (can be a default value, an environment variable or a file).
	Source Source
	// Path to the file the item comes (empty when it doesn't come from a file).
	Path string
	// Priority of the item.
	// When two items have the same key, the one with the highest Priority is kept.
	// When the value is a map or an array, the items may have the same Priority, in
	// this case the arrays are appended to each other, and the maps are merged.
	Priority int
}

// Source represents the Source of an item.
type Source string

const (
	SourceDefault Source = "default"
	SourceEnv     Source = "env"
	SourceFile    Source = "file"
)

// Load config from a provider and add source information on config items.
func (c *configLoader) Load(path string, provider koanf.Provider, parser koanf.Parser) prometheus.MultiError {
	c.loadCount++

	var warnings prometheus.MultiError

	providerType := providerType(provider)

	k := koanf.New(delimiter)

	err := k.Load(provider, parser)
	warnings.Append(err)

	// Migrate old configuration keys.
	k, moreWarnings := migrate(k)
	warnings = append(warnings, moreWarnings...)

	config, moreWarnings := convertTypes(k)
	warnings = append(warnings, moreWarnings...)

	for key, value := range config {
		priority := priority(providerType, key, value, c.loadCount)

		// Convert value to use JSON types.
		// This is needed because the values are stored on the Bleemeo API as JSON fields,
		// so to compare a local value with remote value we need to convert it here.
		// For instance without this conversion "bleemeo.mqtt.port" would be a int locally
		// but a float64 when read from the API, which makes them hard to compare.
		value, err := convertToJSONTypes(value)
		if err != nil {
			logger.V(1).Printf("Failed to convert value %v to JSON: %s", value, err)
		}

		c.items = append(c.items, Item{
			Key:      key,
			Value:    value,
			Source:   providerType,
			Path:     path,
			Priority: priority,
		})
	}

	return warnings
}

// convertToJSONTypes convert the value to only use JSON types.
// It converts int to float64, structs to map, []T to []any...
func convertToJSONTypes(value interface{}) (interface{}, error) {
	jsonBytes, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}

	var jsonValue interface{}

	err = json.Unmarshal(jsonBytes, &jsonValue)
	if err != nil {
		return nil, err
	}

	return jsonValue, nil
}

// convertTypes converts config keys to the right type and returns warnings.
func convertTypes(
	baseKoanf *koanf.Koanf,
) (map[string]interface{}, prometheus.MultiError) {
	var warnings prometheus.MultiError

	// Unmarshal the config to a struct, this does all needed type conversions
	// (int to string, string to bool, and many more).
	var config Config

	unmarshalConf := koanf.UnmarshalConf{
		DecoderConfig: &mapstructure.DecoderConfig{
			DecodeHook: mapstructure.ComposeDecodeHookFunc(
				mapstructure.StringToTimeDurationHookFunc(),
				mapstructure.StringToSliceHookFunc(","),
				mapstructure.TextUnmarshallerHookFunc(),
				blackboxModuleHookFunc(),
				stringToMapHookFunc(),
				stringToBoolHookFunc(),
			),
			Metadata:         nil,
			ErrorUnused:      true,
			Result:           &config,
			WeaklyTypedInput: true,
		},
		Tag: Tag,
	}

	err := baseKoanf.UnmarshalWithConf("", &config, unmarshalConf)
	warnings.Append(err)

	// Convert the structured configuration back to a koanf.
	typedKoanf := koanf.New(delimiter)

	err = typedKoanf.Load(structs.ProviderWithDelim(config, Tag, delimiter), nil)
	warnings.Append(err)

	// When the config is loaded from the struct, all possible config keys
	// are set. We want to only keep the keys that were present in the
	// base koanf, otherwise it would break merging config files because
	// we wouldn't be able to know if a key was set by a file.
	typedKeys := allKeys(typedKoanf)
	baseKeys := allKeys(baseKoanf)

	// Remove keys that were not set in the base config.
	for key := range typedKeys {
		if _, ok := baseKeys[key]; !ok {
			delete(typedKeys, key)
		}
	}

	// Some keys may be present in the base config but missing in the typed
	// config, so we need to add them here.
	// This happens because we embed the Blackbox config, which uses omitempty
	// on all its config keys. This means that if "ip_protocol_fallback" is set
	// to false in the config, it will be dropped when the structured config is
	// converted back to a koanf.
	for key, value := range baseKeys {
		if _, ok := typedKeys[key]; !ok {
			typedKeys[key] = value
		}
	}

	return typedKeys, warnings
}

// allKeys returns all keys from the koanf.
// Map keys are fixed: instead of returning map keys separately
// ("metric.softstatus_period.cpu_used",  "metric.softstatus_period.disk_used"),
// return a single key per map ("metric.softstatus_period").
func allKeys(k *koanf.Koanf) map[string]interface{} {
	all := k.All()

	for key := range all {
		if isMap, mapKey := isMapKey(key); isMap {
			delete(all, key)

			all[mapKey] = k.Get(mapKey)
		}
	}

	return all
}

// priority returns the priority for a provider and a config key value.
// When two items have the same key, the one with the highest priority is kept.
// When the value is a map or an array, the items may have the same priority, in
// this case the arrays are appended to each other, and the maps are merged.
// It panics on unknown providers.
func priority(provider Source, key string, value interface{}, loadCount int) int {
	const (
		priorityDefault         = -1
		priorityMapAndArrayFile = 1
		priorityEnv             = math.MaxInt32
	)

	switch provider {
	case SourceEnv:
		return priorityEnv
	case SourceFile:
		// Slices in files all have the same priority because they are appended.
		if reflect.TypeOf(value).Kind() == reflect.Slice {
			return priorityMapAndArrayFile
		}

		// Map in files all have the same priority because they are merged.
		if isMap, _ := isMapKey(key); isMap {
			return priorityMapAndArrayFile
		}

		// For basic types (string, int, bool, float), the config from the
		// last loaded file has a greater priority than the previous files.
		return loadCount
	case SourceDefault:
		return priorityDefault
	default:
		panic(fmt.Errorf("%w: %T", errUnsupportedProvider, provider))
	}
}

// providerTypes return the provider type from a Koanf provider.
func providerType(provider koanf.Provider) Source {
	switch provider.(type) {
	case *env.Env:
		return SourceEnv
	case *file.File:
		return SourceFile
	case *structs.Structs:
		return SourceDefault
	default:
		panic(fmt.Errorf("%w: %T", errUnsupportedProvider, provider))
	}
}

// isMapKey returns true if the config key represents a map value, and the map key.
// For instance: isMapKey("thresholds.cpu_used.low_warning") -> (true, "thresholds").
func isMapKey(key string) (bool, string) {
	for _, mapKey := range mapKeys() {
		// For the map key "thresholds", the key corresponds to this map if the keys
		// are equal or if it begins by the map key and a dot ("thresholds.cpu_used").
		if key == mapKey || strings.HasPrefix(key, fmt.Sprintf("%s.", mapKey)) {
			return true, mapKey
		}
	}

	return false, ""
}

// Build the configuration from the loaded items.
func (c *configLoader) Build() (*koanf.Koanf, prometheus.MultiError) {
	var warnings prometheus.MultiError

	config := make(map[string]interface{})
	priorities := make(map[string]int)

	for _, item := range c.items {
		_, configExists := config[item.Key]
		previousPriority := priorities[item.Key]

		switch {
		// Higher priority items overwrite previous values.
		case !configExists || previousPriority < item.Priority:
			config[item.Key] = item.Value
			priorities[item.Key] = item.Priority
		// Same priority items are merged (slices are appended and maps are merged).
		case previousPriority == item.Priority:
			var err error

			config[item.Key], err = merge(config[item.Key], item.Value)
			warnings.Append(err)
		// Previous item has higher priority, nothing to do.
		case previousPriority > item.Priority:
		}
	}

	k := koanf.New(delimiter)
	err := k.Load(confmap.Provider(config, delimiter), nil)
	warnings.Append(err)

	return k, warnings
}

// Merge maps and append slices.
func merge(dst interface{}, src interface{}) (interface{}, error) {
	switch dstType := dst.(type) {
	case []interface{}:
		return mergeSlices(dstType, src)
	case []string:
		return mergeSlices(dstType, src)
	case map[string]interface{}:
		return mergeMaps(dstType, src)
	case map[string]int:
		return mergeMaps(dstType, src)
	default:
		return nil, fmt.Errorf("%w: unsupported type %T", errCannotMerge, dst)
	}
}

// mergeSlices merges two slices by appending them.
// It returns an error if src doesn't have the same type as dst.
func mergeSlices[T any](dst []T, src interface{}) ([]T, error) {
	srcSlice, ok := src.([]T)
	if !ok {
		return nil, fmt.Errorf("%w: %T and %T are not compatible", errCannotMerge, src, dst)
	}

	return append(dst, srcSlice...), nil
}

// mergeMaps merges two maps.
// It retursn an error if src doesn't have the same type as dst.
func mergeMaps[T any](dst map[string]T, src interface{}) (map[string]T, error) {
	srcMap, ok := src.(map[string]T)
	if !ok {
		return nil, fmt.Errorf("%w: %T and %T are not compatible", errCannotMerge, src, dst)
	}

	for key, value := range srcMap {
		dst[key] = value
	}

	return dst, nil
}
