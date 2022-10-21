package config2

import (
	"fmt"
	"reflect"

	"github.com/mitchellh/mapstructure"
	bbConf "github.com/prometheus/blackbox_exporter/config"
	"gopkg.in/yaml.v3"
)

// BlackboxModuleUnmarshallerHookFunc unmarshal Blackbox module config.
// This is needed because we embed the external module config from Blackbox in our own config.
func BlackboxModuleUnmarshallerHookFunc() mapstructure.DecodeHookFuncType {
	return func(source reflect.Type, target reflect.Type, data interface{}) (interface{}, error) {
		module, ok := reflect.New(target).Interface().(*bbConf.Module)
		if !ok {
			return data, nil
		}

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
