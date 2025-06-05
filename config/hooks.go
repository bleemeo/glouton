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
