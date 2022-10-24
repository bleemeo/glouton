package config

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/mitchellh/mapstructure"
	bbConf "github.com/prometheus/blackbox_exporter/config"
	"gopkg.in/yaml.v3"
)

// blackboxModuleHookFunc unmarshals Blackbox module config.
// This is needed because we embed the external module config from Blackbox in our own config.
func blackboxModuleHookFunc() mapstructure.DecodeHookFuncType {
	return func(source reflect.Type, target reflect.Type, data interface{}) (interface{}, error) {
		module, ok := reflect.New(target).Interface().(*bbConf.Module)
		if !ok {
			return data, nil
		}

		// The data is a map[string]interface{}.
		marshalled, err := yaml.Marshal(data)
		if err != nil {
			return nil, fmt.Errorf("cannot marshal blackbox_exporter module configuration: %s", err)
		}

		if err := yaml.Unmarshal(marshalled, &module); err != nil {
			return nil, fmt.Errorf("cannot parse blackbox_exporter module configuration: %s", err)
		}

		return module, nil
	}
}

// stringToMapHookFunc converts a string to map.
// It assumes the following format: "k1=v1,k2=v2".
// This is used to override map settings from environment variables.
func stringToMapHookFunc() mapstructure.DecodeHookFuncType {
	return func(source reflect.Type, target reflect.Type, data interface{}) (interface{}, error) {
		if source.Kind() != reflect.String || target.Kind() != reflect.Map {
			return data, nil
		}

		strMap, _ := data.(string)

		result := make(map[string]interface{})

		elementsList := strings.Split(strMap, ",")
		for i, element := range elementsList {
			values := strings.Split(element, "=")

			if i == len(elementsList)-1 && element == "" {
				return result, nil
			}

			if len(values) < 2 {
				err := fmt.Errorf("%w: '%s'", errWrongMapFormat, strMap)

				return make(map[string]interface{}), err
			}

			result[strings.TrimLeft(values[0], " ")] = strings.TrimRight(strings.Join(values[1:], "="), " ")
		}

		return result, nil
	}
}
