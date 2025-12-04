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
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"reflect"
	"strings"
	"time"

	"github.com/bleemeo/glouton/logger"

	"github.com/go-viper/mapstructure/v2"
	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/structs"
	"github.com/knadh/koanf/v2"
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
	Value any
	// Type of the value.
	Type ItemType
	// Source of the config key (can be a default value, an environment variable or a file).
	Source ItemSource
	// Path to the file the item comes (empty when it doesn't come from a file).
	Path string
	// Priority of the item.
	// When two items have the same key, the one with the highest Priority is kept.
	// When the value is a map or an array, the items may have the same Priority, in
	// this case the arrays are appended to each other, and the maps are merged.
	Priority int
}

// ItemSource represents the ItemSource of an item.
type ItemSource int

const (
	SourceDefault ItemSource = iota
	SourceEnv
	SourceFile
)

// ItemType represents the type of an item value.
type ItemType int

const (
	TypeAny ItemType = iota
	TypeInt
	TypeFloat
	TypeBool
	TypeString
	TypeListString
	TypeListInt
	TypeMapStrStr
	TypeMapStrInt
	TypeThresholds
	TypeServices
	TypeNameInstances
	TypeBlackboxTargets
	TypePrometheusTargets
	TypeSNMPTargets
	TypeLogInputs
)

var errNullConfigValue = errors.New("config entry has a null value, ignoring it")

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
		if value == nil && !isNilAllowedFor(key) {
			warnings = append(warnings, fmt.Errorf("%q %w", key, errNullConfigValue))

			continue
		}

		if isNil(value) {
			continue
		}

		priority := priority(providerType, key, value, c.loadCount)

		// Keep the real type of the value before it's converted to JSON.
		valueType := itemTypeFromValue(key, value)

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
			Type:     valueType,
			Source:   providerType,
			Path:     path,
			Priority: priority,
		})
	}

	return warnings
}

// isNilAllowedFor returns whether the given key must escape the not-null-validation or not.
// Those exceptions are to avoid that some config fields, such as pointers,
// are warned as null just because they're absent from the config file.
func isNilAllowedFor(key string) bool {
	return map[string]bool{
		"blackbox.modules.http.http.http_client_config.http_headers": true,
	}[key]
}

// isNil returns whether v or its underlying value is nil.
func isNil(v any) bool {
	if v == nil { // fast-path
		return true
	}

	refV := reflect.ValueOf(v)

	if !map[reflect.Kind]bool{
		reflect.Map:       true,
		reflect.Pointer:   true,
		reflect.Interface: true,
		reflect.Slice:     true,
	}[refV.Kind()] {
		// Since v doesn't belong to any of the above types, it isn't nillable.
		return false
	}

	return refV.IsNil()
}

func itemTypeFromValue(key string, value any) ItemType {
	// Detect base types.
	switch value.(type) {
	case int, time.Duration:
		return TypeInt
	case float64:
		return TypeFloat
	case bool:
		return TypeBool
	case string:
		return TypeString
	case map[string]int:
		return TypeMapStrInt
	case map[string]string:
		return TypeMapStrStr
	case []string:
		return TypeListString
	case []int:
		return TypeListInt
	}

	// For more complex types (map or slices of structs), we use the key.
	switch key {
	case "thresholds":
		return TypeThresholds
	case "service":
		return TypeServices
	case "service_ignore_metrics", "service_ignore_check":
		return TypeNameInstances
	case "blackbox.targets":
		return TypeBlackboxTargets
	case "metric.prometheus.targets":
		return TypePrometheusTargets
	case "metric.snmp.targets":
		return TypeSNMPTargets
	case "log.inputs":
		return TypeLogInputs
	}

	logger.V(1).Printf("Unsupported item type %T (key %q)", value, key)

	return TypeAny
}

// convertToJSONTypes convert the value to only use JSON types.
// It converts int to float64, structs to map, []T to []any...
func convertToJSONTypes(value any) (any, error) {
	jsonBytes, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}

	var jsonValue any

	err = json.Unmarshal(jsonBytes, &jsonValue)
	if err != nil {
		return nil, err
	}

	return jsonValue, nil
}

// convertTypes converts config keys to the right type and returns warnings.
func convertTypes(
	baseKoanf *koanf.Koanf,
) (map[string]any, prometheus.MultiError) {
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
				StringToIntSliceHookFunc(","),
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
func allKeys(k *koanf.Koanf) map[string]any {
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
func priority(provider ItemSource, key string, value any, loadCount int) int {
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
		if value != nil && reflect.TypeOf(value).Kind() == reflect.Slice {
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
func providerType(provider koanf.Provider) ItemSource {
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
		if key == mapKey || strings.HasPrefix(key, mapKey+".") {
			return true, mapKey
		}
	}

	return false, ""
}

// Build the configuration from the loaded items.
func (c *configLoader) Build() (*koanf.Koanf, prometheus.MultiError) {
	var warnings prometheus.MultiError

	config := make(map[string]any)
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

	warnings.Append(mergeKnownLogFormats(config))

	k := koanf.New(delimiter)
	err := k.Load(confmap.Provider(config, delimiter), nil)
	warnings.Append(err)

	return k, warnings
}

// Merge maps and append slices.
func merge(dst any, src any) (any, error) {
	switch dstType := dst.(type) {
	case []any:
		srcSlice, ok := src.([]any)
		if !ok {
			return nil, fmt.Errorf("%w: []interface{} with %T", errCannotMerge, src)
		}

		return append(dstType, srcSlice...), nil
	case map[string]any:
		srcMap, ok := src.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%w: map[string]interface{} with %T", errCannotMerge, src)
		}

		maps.Copy(dstType, srcMap)

		return dstType, nil
	default:
		return nil, fmt.Errorf("%w: unsupported type %T", errCannotMerge, dst)
	}
}

// mergeKnownLogFormats seeks for all items like log.opentelemetry.known_log_formats.*,
// and merge them into the log.opentelemetry.known_log_formats map.
func mergeKnownLogFormats(config map[string]any) error {
	var (
		topMap        map[string][]OTELOperator
		detachedItems = make(map[string][]OTELOperator)
	)

	for key, item := range config {
		if key == "log.opentelemetry.known_log_formats" {
			err := mapstructure.Decode(item, &topMap)
			if err != nil {
				return fmt.Errorf("merging known log formats: failed to decode %T into %T: %w", item, topMap, err)
			}
		} else if after, ok := strings.CutPrefix(key, "log.opentelemetry.known_log_formats."); ok {
			formatName := after
			if strings.Contains(formatName, delimiter) {
				logger.V(1).Printf("Unexpected config item %q", key)

				continue
			}

			var operators []OTELOperator

			err := mapstructure.Decode(item, &operators)
			if err != nil {
				return fmt.Errorf("merging known log formats: failed to decode %T into %T: %w", item, operators, err)
			}

			detachedItems[formatName] = operators

			delete(config, key)
		}
	}

	if len(detachedItems) == 0 {
		return nil
	}

	if topMap == nil {
		topMap = detachedItems
	} else {
		maps.Insert(topMap, maps.All(detachedItems))
	}

	config["log.opentelemetry.known_log_formats"] = topMap

	return nil
}
