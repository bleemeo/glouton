// Copyright 2015-2026 Bleemeo
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
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/bleemeo/glouton/logger"

	"github.com/go-viper/mapstructure/v2"
	goccyyaml "github.com/goccy/go-yaml"
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

var (
	errNullConfigValue = errors.New("config entry has a null value, ignoring it")
	errInvalidYAML     = errors.New("invalid YAML")
)

// Load config from a provider and add source information on config items.
func (c *configLoader) Load(path string, provider koanf.Provider, parser koanf.Parser) prometheus.MultiError {
	c.loadCount++

	var warnings prometheus.MultiError

	providerType := providerType(provider)

	k := koanf.New(delimiter)

	err := k.Load(provider, parser)
	if err != nil && path != "" {
		err = addYAMLSyntaxHint(err, path)
	}

	warnings.Append(err)

	// Migrate old configuration keys.
	k, moreWarnings := migrate(k, path, providerType)
	warnings = append(warnings, moreWarnings...)

	config, moreWarnings := convertTypes(k)
	warnings = append(warnings, moreWarnings...)

	// Computed once per Load() call, not once per key. dynamicKeys is only about merge priority, so that
	// an entry set by a dynamic per-listener/per-threshold environment variable merges into the file-defined
	// map instead of replacing it wholesale; priority()'s SourceFile branch never consults it.
	dynamicKeys := dynamicEnvVarConfigKeys()

	// Pruning is a separate concern: convertTypes' Config-struct round trip (Unmarshal into the typed
	// Config, then back out via structs.ProviderWithDelim) materializes every unset pointer field of a
	// struct-valued map key as an explicit nil, for every provider -- a file setting only one sibling
	// field (protocols.http, leaving protocols.grpc unset) round-trips with an explicit "grpc: null"
	// exactly like a dynamic env var does. Left in, merge() would read that invented nil as this file
	// deliberately overwriting a sibling an earlier-loaded file set, silently dropping it.
	prunedKeys := nilPrunedConfigKeys()

	for key, value := range config {
		if value == nil && !isNilAllowedFor(key) {
			warnings = append(warnings, fmt.Errorf("%q %w", key, errNullConfigValue))

			continue
		}

		if isNil(value) {
			continue
		}

		if prunedKeys[key] {
			value = pruneNilMapValues(value)
		}

		priority := priority(providerType, key, value, c.loadCount, dynamicKeys)

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

// addYAMLSyntaxHint improves a YAML syntax error by re-parsing the same file with github.com/goccy/go-yaml.
// Unlike yaml.v3, goccy/go-yaml's errors point at the exact line and column of the mistake.
func addYAMLSyntaxHint(err error, path string) error {
	data, readErr := os.ReadFile(path) //nolint:gosec
	if readErr != nil {
		return err
	}

	var out map[string]any

	goccyErr := goccyyaml.Unmarshal(data, &out)
	if goccyErr == nil {
		return err
	}

	var yamlErr goccyyaml.Error
	if errors.As(goccyErr, &yamlErr) {
		if tk := yamlErr.GetToken(); tk != nil && tk.Position != nil {
			return fmt.Errorf(
				"%w: line %d, column %d: %s",
				errInvalidYAML, tk.Position.Line, tk.Position.Column, yamlErr.GetMessage(),
			)
		}
	}

	return fmt.Errorf("%w: %s", errInvalidYAML, goccyyaml.FormatError(goccyErr, false, false))
}

// isNilAllowedFor returns whether the given key must escape the not-null-validation or not.
// Those exceptions are to avoid that some config fields, such as pointers,
// are warned as null just because they're absent from the config file.
func isNilAllowedFor(key string) bool {
	return map[string]bool{
		"blackbox.modules.http.http.http_client_config.http_headers": true,
		// Tri-state pointer: nil means "auto" (resolved at runtime
		// against bleemeo.enable).
		"agent.local_store.enable": true,
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
	case keyThresholds:
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
				networkProtocolsNullMeansDefaultHookFunc(),
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
// dynamicEnvKeys is dynamicEnvVarConfigKeys(), computed once by the caller (only
// meaningful for provider == SourceEnv; may be nil otherwise).
// It panics on unknown providers.
func priority(provider ItemSource, key string, value any, loadCount int, dynamicEnvKeys map[string]bool) int {
	const (
		priorityDefault         = -1
		priorityMapAndArrayFile = 1
		priorityEnv             = math.MaxInt32
	)

	switch provider {
	case SourceEnv:
		// Entries under a dynamicEnvVarList config key (e.g. opentelemetry.listeners, set by the dynamic
		// per-listener environment variables -- see resolveDynamicEnvKey) must merge into file-defined
		// entries instead of replacing the whole map.
		if dynamicEnvKeys[key] {
			return priorityMapAndArrayFile
		}

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
	warnings := make(prometheus.MultiError, 0, 4)

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
	warnings = append(warnings, synthesizeLegacyNetworkListener(config)...)
	dedupeFromListeners(config)

	k := koanf.New(delimiter)
	err := k.Load(confmap.Provider(config, delimiter), nil)
	warnings.Append(err)

	return k, warnings
}

// dedupeFromListeners drops repeated names from every receiver's from_listeners list. Merging appends
// leaf lists across files, which is what you want for entries that carry values, but from_listeners holds
// listener *names*: referencing one twice says nothing more than referencing it once, and deduplicating
// them is cheap precisely because they're plain strings rather than maps (see merge). Two files each
// naming the same listener on the same receiver produce exactly that repeat.
// Non-string entries are passed through untouched: they're invalid config, reported by validation later,
// and must not be silently dropped here (nor used as a map key, which would panic if unhashable).
func dedupeFromListeners(config map[string]any) {
	receivers, ok := config["log.opentelemetry.receivers"].(map[string]any)
	if !ok {
		return
	}

	for _, rawReceiver := range receivers {
		receiver, ok := rawReceiver.(map[string]any)
		if !ok {
			continue
		}

		listeners, ok := receiver["from_listeners"].([]any)
		if !ok {
			continue
		}

		seen := make(map[string]bool, len(listeners))

		deduped := make([]any, 0, len(listeners))

		for _, rawName := range listeners {
			name, isString := rawName.(string)
			if !isString {
				deduped = append(deduped, rawName)

				continue
			}

			if seen[name] {
				continue
			}

			seen[name] = true

			deduped = append(deduped, rawName)
		}

		receiver["from_listeners"] = deduped
	}
}

// Merge maps and append slices. A map merge recurses into any sub-key present as a map[string]any on both
// sides (e.g. a receiver's or a threshold's own fields), instead of letting src's value replace dst's
// wholesale -- otherwise splitting one named entry's fields across two config files/conf.d snippets (file
// A sets a receiver's include, file B sets its send_logs) silently drops the earlier file's fields.
// A sub-key that's a []any on both sides is appended too, the same way a top-level list key already
// merges across files: two conf.d snippets each contributing entries to one log.metrics_rules entry, or
// globs to one receiver's include, end up with both files' entries rather than only the last file's.
// Appending never deduplicates -- entries are often maps (services:), which are impractical to compare
// and to document -- so a key whose entries are plain names, where a repeat is meaningless rather than
// meaningful, gets its own normalization pass instead; see dedupeFromListeners.
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

		for key, srcVal := range srcMap {
			dstVal, exists := dstType[key]
			if !exists {
				dstType[key] = srcVal

				continue
			}

			dstValMap, dstIsMap := dstVal.(map[string]any)
			srcValMap, srcIsMap := srcVal.(map[string]any)

			if dstIsMap && srcIsMap {
				merged, err := merge(dstValMap, srcValMap)
				if err != nil {
					return nil, err
				}

				dstType[key] = merged

				continue
			}

			dstValSlice, dstIsSlice := dstVal.([]any)
			srcValSlice, srcIsSlice := srcVal.([]any)

			if dstIsSlice && srcIsSlice {
				dstType[key] = append(dstValSlice, srcValSlice...)

				continue
			}

			// Neither both maps nor both slices (a scalar, or a type mismatch): the later-loaded source
			// wins, matching the scalar behavior in priority().
			dstType[key] = srcVal
		}

		return dstType, nil
	default:
		return nil, fmt.Errorf("%w: unsupported type %T", errCannotMerge, dst)
	}
}

// nilPrunedConfigKeys is the set of mapKeys() entries whose config type is a map of structs, i.e. exactly
// the keys convertTypes' Config-struct round trip invents explicit nils inside. Derived from the Config
// types rather than listed by hand, and deliberately not from dynamicEnvVarConfigKeys(): the two sets
// happen to coincide today, but one is about environment variables while this one is about a struct's
// unset pointer fields materializing as nil. Adding a struct-valued map key to mapKeys() without a
// dynamic env var for it would otherwise quietly reintroduce the sibling-clobbering bug pruning exists to
// prevent. A map of scalars, of slices, or of raw map[string]any (a LogReceiver) has no struct fields to
// invent nils for, so it is left alone -- an explicit null there is the user's own and must survive.
//
// Called once per Load(), not once per key: mapKeys() is a handful of entries and the walk below is a
// shallow type traversal, so there is nothing worth caching across calls.
func nilPrunedConfigKeys() map[string]bool {
	keys := make(map[string]bool, len(mapKeys()))

	for _, key := range mapKeys() {
		field, found := configFieldTypeByPath(key)
		if !found {
			continue
		}

		if field.Kind() != reflect.Map {
			continue
		}

		elem := field.Elem()
		for elem.Kind() == reflect.Pointer {
			elem = elem.Elem()
		}

		if elem.Kind() == reflect.Struct {
			keys[key] = true
		}
	}

	return keys
}

// configFieldTypeByPath resolves a dotted config key (as written in mapKeys()) to the Go type of the
// Config field it names, walking yaml tags at each segment.
func configFieldTypeByPath(key string) (reflect.Type, bool) {
	current := reflect.TypeFor[Config]()

	for segment := range strings.SplitSeq(key, delimiter) {
		for current.Kind() == reflect.Pointer {
			current = current.Elem()
		}

		if current.Kind() != reflect.Struct {
			return nil, false
		}

		field, found := structFieldByYAMLName(current, segment)
		if !found {
			return nil, false
		}

		current = field
	}

	return current, true
}

func structFieldByYAMLName(structType reflect.Type, name string) (reflect.Type, bool) {
	for field := range structType.Fields() {
		yamlName, _, _ := strings.Cut(field.Tag.Get("yaml"), ",")
		if yamlName == name {
			return field.Type, true
		}
	}

	return nil, false
}

// pruneNilMapValues recursively removes nil-valued entries from a nested map[string]any. Applied to
// every dynamicEnvVarConfigKeys() item (see resolveDynamicEnvKey), from any provider (env, file,
// default): setting only one leaf/sibling field of one of these keys' struct types
// (NetworkListener/NetworkProtocols, Threshold) still round-trips through convertTypes' Config-struct
// Unmarshal-then-re-encode, which fills in every other sibling field as an explicit nil. Without
// pruning, merge() would treat those invented nils as this item intentionally overwriting a sibling
// field (e.g. an untouched HTTP endpoint) that a different item -- a dynamic env var, or another
// config file -- set.
func pruneNilMapValues(value any) any {
	m, ok := value.(map[string]any)
	if !ok {
		return value
	}

	for key, val := range m {
		if val == nil {
			delete(m, key)

			continue
		}

		m[key] = pruneNilMapValues(val)
	}

	return m
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
