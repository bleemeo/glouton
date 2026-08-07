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
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/go-viper/mapstructure/v2"
	bbConf "github.com/prometheus/blackbox_exporter/config"
	"gopkg.in/yaml.v3"
)

// blackboxModuleHookFunc unmarshals Blackbox module config.
// We embed the external module config from Blackbox in our own config.
// Blackbox implements its own yaml marshaller that sets default values,
// so we need to unmarshal it to set the default values.
func blackboxModuleHookFunc() mapstructure.DecodeHookFuncType {
	return func(_ reflect.Type, target reflect.Type, data any) (any, error) {
		module, ok := reflect.New(target).Interface().(*bbConf.Module)
		if !ok {
			return data, nil
		}

		srcModule, ok := data.(map[string]any)
		if !ok {
			return data, nil
		}

		// Durations are converted to float64 values in the config loader,
		// but unmarshalling float64 to a duration fails, so we convert it.
		if timeout, ok := srcModule["timeout"].(float64); ok {
			srcModule["timeout"] = time.Duration(timeout)
		}

		marshalled, err := yaml.Marshal(srcModule)
		if err != nil {
			return nil, fmt.Errorf("%w: cannot marshal blackbox_exporter module configuration: %s", ErrInvalidValue, err)
		}

		if err := yaml.Unmarshal(marshalled, &module); err != nil {
			return nil, fmt.Errorf("%w: cannot unmarshal blackbox_exporter module configuration: %s", ErrInvalidValue, err)
		}

		return module, nil
	}
}

// stringToMapHookFunc converts a string to map.
// It assumes the following format: "k1=v1,k2=v2".
// This is used to override map settings from environment variables.
func stringToMapHookFunc() mapstructure.DecodeHookFuncType {
	return func(source reflect.Type, target reflect.Type, data any) (any, error) {
		if source.Kind() != reflect.String || target.Kind() != reflect.Map {
			return data, nil
		}

		strMap, _ := data.(string)

		return parseMap(strMap)
	}
}

// parseMap parses a map from a string.
// It assumes the following format: "k1=v1,k2=v2".
func parseMap(strMap string) (map[string]any, error) {
	// keyValues = ["k1=v1", "k2=v2"]
	keyValues := strings.Split(strMap, ",")
	result := make(map[string]any, len(keyValues))

	for _, keyValue := range keyValues {
		// keyValue = "k1=v1"
		values := strings.Split(keyValue, "=")

		if len(values) < 2 {
			err := fmt.Errorf("%w: '%s'", errWrongMapFormat, strMap)

			return make(map[string]any), err
		}

		// Handle case where the string ends with a ','.
		if keyValue == "" {
			continue
		}

		// Remove spaces before and after the values.
		key := strings.Trim(values[0], " ")
		value := strings.Trim(strings.Join(values[1:], "="), " ")

		result[key] = value
	}

	return result, nil
}

// stringToBoolHookFunc converts strings to bool.
// It supports "true", "yes" and "1" as true and "false", "no", "0" as false.
// The conversion is case insensitive.
func stringToBoolHookFunc() mapstructure.DecodeHookFuncType {
	return func(source reflect.Type, target reflect.Type, data any) (any, error) {
		if source.Kind() != reflect.String || target.Kind() != reflect.Bool {
			return data, nil
		}

		str, _ := data.(string)

		return ParseBool(str)
	}
}

// ParseBool works like strconv.ParseBool but also supports "yes" and "no".
func ParseBool(value string) (bool, error) {
	value = strings.ToLower(value)

	result, err := strconv.ParseBool(value)
	if err != nil {
		// We also support "yes" and "no"
		switch value {
		case "yes":
			result = true
			err = nil
		case "no":
			result = false
			err = nil
		}
	}

	return result, err
}

// networkProtocolsNullMeansDefaultHookFunc makes a bare "grpc:"/"http:" key (a YAML null value) under
// opentelemetry.listeners.*.protocols behave exactly like an explicit "grpc: {}"/"http: {}": the
// protocol is enabled with the factory-default endpoint. This matches how every upstream OTel collector
// receiver's own "protocols:" block already works (see receivers.otlp in the OTel collector docs) --
// listing a protocol at all, empty or not, enables it; only a protocol not listed is disabled. Without
// this hook, mapstructure's default behavior treats "key present but null" identically to "key absent"
// (both leave the destination *NetworkEndpoint field nil), which silently produces
// errNetworkListenerNoProtocol at load time for what looks like the most natural way to write "enable
// this with defaults" -- a bare key, the same shorthand used throughout the rest of glouton.conf for
// enabling a section with its defaults.
//
// This only touches the "protocols:" map's own entries (data), not whether "protocols" itself is present:
// an omitted protocols: block, or an omitted grpc/http key, is untouched and still decodes to a nil
// *NetworkEndpoint (disabled), exactly as before.
func networkProtocolsNullMeansDefaultHookFunc() mapstructure.DecodeHookFuncType {
	return func(_ reflect.Type, target reflect.Type, data any) (any, error) {
		if target != reflect.TypeFor[NetworkProtocols]() {
			return data, nil
		}

		rawMap, ok := data.(map[string]any)
		if !ok {
			return data, nil
		}

		for _, key := range []string{"grpc", "http"} {
			if v, present := rawMap[key]; present && v == nil {
				rawMap[key] = map[string]any{}
			}
		}

		return rawMap, nil
	}
}

func StringToIntSliceHookFunc(sep string) mapstructure.DecodeHookFunc {
	expectedToType := reflect.SliceOf(reflect.TypeFor[int]())

	return func(f reflect.Type, t reflect.Type, data any) (any, error) {
		if f.Kind() != reflect.String {
			return data, nil
		}

		if t != expectedToType {
			return data, nil
		}

		raw, _ := data.(string)
		if raw == "" {
			return []int{}, nil
		}

		split := strings.Split(raw, sep)
		ints := make([]int, len(split))

		for i, s := range split {
			n, err := strconv.Atoi(s)
			if err != nil {
				return nil, err
			}

			ints[i] = n
		}

		return ints, nil
	}
}
