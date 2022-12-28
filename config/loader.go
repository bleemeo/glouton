package config

import (
	"fmt"
	"math"
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
	items []item
}

type item struct {
	// The config Key (e.g. "bleemeo.enable").
	Key string
	// The Value for this config key.
	Value interface{}
	// Source of the config key (can be a default value, an environment variable or a file).
	Source source
	// Path to the file the item comes (empty when it doesn't come from a file).
	Path string
	// Priority of the item.
	// When two items have the same key, the one with the highest Priority is kept.
	// When the value is a map or an array, the items may have the same Priority, in
	// this case the arrays are appended to each other, and the maps are merged.
	Priority int
}

// TODO: int enum?
type source string

const (
	sourceDefault source = "default"
	sourceEnv     source = "env"
	sourceFile    source = "file"
)

// Load config from a provider and add source information on config items.
func (c *configLoader) Load(path string, kProvider koanf.Provider, parser koanf.Parser) error {
	provider := providerType(kProvider)

	config, err := typedConfig(kProvider, parser)
	if err != nil {
		return err
	}

	for key, value := range config {
		priority := c.priority(provider, key, value)

		c.items = append(c.items, item{
			Key:      key,
			Value:    value,
			Source:   provider,
			Path:     path,
			Priority: priority,
		})
	}

	return nil
}

// typedConfig loads config from the given provider and does type conversions when needed.
// For details about the type conversions, see the DecodeHook below and mapstructure WeaklyTypedInput.
func typedConfig(kProvider koanf.Provider, parser koanf.Parser) (map[string]interface{}, error) {
	// Unmarshal base config to do all needed type conversions.
	baseKoanf := koanf.New(delimiter)

	err := baseKoanf.Load(kProvider, parser)
	if err != nil {
		return nil, err
	}

	var config Config

	// We need to use the "yaml" tag instead of the default "koanf" tag because
	// the config embeds the blackbox module config which uses YAML.
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

	err = baseKoanf.UnmarshalWithConf("", &config, unmarshalConf)
	if err != nil {
		return nil, err
	}

	// Convert the structured configuration back to a koanf.
	typedKoanf := koanf.New(delimiter)

	err = typedKoanf.Load(structs.ProviderWithDelim(config, Tag, delimiter), nil)
	if err != nil {
		return nil, err
	}

	// Use another koanf to remove keys that were not set in the given config.
	cleanKeys := allKeys(typedKoanf)
	baseKeys := allKeys(baseKoanf)

	for key := range cleanKeys {
		if _, ok := baseKeys[key]; !ok {
			delete(cleanKeys, key)
		}
	}

	return cleanKeys, nil
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
func (c *configLoader) priority(provider source, key string, value interface{}) int {
	const (
		priorityDefault         = -1
		priorityMapAndArrayFile = 1
		priorityEnv             = math.MaxInt
	)

	switch provider {
	case sourceEnv:
		return priorityEnv
	case sourceFile:
		// Slices in files all have the same priority because they are appended.
		if _, ok := value.([]interface{}); ok {
			return priorityMapAndArrayFile
		}

		// Map in files all have the same priority because they are merged.
		if isMap, _ := isMapKey(key); isMap {
			return priorityMapAndArrayFile
		}

		// For basic types (string, int, bool, float), the config from the
		// last loaded file has a greater priority than the previous files.
		return len(c.items)
	case sourceDefault:
		return priorityDefault
	default:
		panic(fmt.Errorf("%w: %T", errUnsupportedProvider, provider))
	}
}

// providerTypes return the provider type from a Koanf provider.
func providerType(provider koanf.Provider) source {
	switch provider.(type) {
	case *env.Env:
		return sourceEnv
	case *file.File:
		return sourceFile
	case *structsProvider:
		return sourceDefault
	default:
		panic(fmt.Errorf("%w: %T", errUnsupportedProvider, provider))
	}
}

// mapKeys returns the config keys that hold map values.
func mapKeys() []string {
	// TODO: this could be generated from the default config.
	return []string{
		"thresholds",
		"metric.softstatus_period",
		"influxdb.tags",
	}
}

// isMapKey returns true if the config key represents a map value, and the map key.
// For instance: isMapKey("thresholds.cpu_used.low_warning") -> (true, "thresholds")
func isMapKey(key string) (bool, string) {
	// TODO: don't hardcode this, use the default config to know the type.
	// Special case for "softstatus_period_default" which
	// has the same prefix as "softstatus_period".
	if key == "metric.softstatus_period_default" {
		return false, ""
	}

	for _, mapKey := range mapKeys() {
		if strings.HasPrefix(key, mapKey) {
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
		previousPriority, _ := priorities[item.Key]

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

	// Migrate old configuration keys.
	k, moreWarnings := migrate(k)
	warnings = append(warnings, moreWarnings...)

	return k, warnings
}

// Merge append slices and merges maps.
func merge(dstInt interface{}, srcInt interface{}) (interface{}, error) {
	switch dst := dstInt.(type) {
	case []interface{}:
		src, ok := srcInt.([]interface{})
		if !ok {
			return nil, fmt.Errorf("%w: %T and %T are not compatible", errCannotMerge, srcInt, dstInt)
		}

		return append(dst, src...), nil
	case map[string]interface{}:
		src, ok := srcInt.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("%w: %T and %T are not compatible", errCannotMerge, srcInt, dstInt)
		}

		for key, value := range src {
			dst[key] = value
		}

		return dst, nil
	default:
		// This should never happen, only map and strings can be merged.
		return nil, fmt.Errorf("%w: unsupported type %T", errCannotMerge, dstInt)
	}
}
