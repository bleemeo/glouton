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
	"errors"
	"fmt"
	"glouton/logger"
	"os"
	"path/filepath"
	"strings"

	"github.com/knadh/koanf"
	yamlParser "github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/structs"
	"github.com/mitchellh/mapstructure"
	"github.com/prometheus/client_golang/prometheus"
	"gopkg.in/yaml.v3"
)

const (
	// Tag used to unmarshal the config.
	// We need to use the "yaml" tag instead of the default "koanf" tag because
	// the config embeds the blackbox module config which uses YAML.
	Tag                   = "yaml"
	EnvGloutonConfigFiles = "GLOUTON_CONFIG_FILES"
	envPrefix             = "GLOUTON_"
	deprecatedEnvPrefix   = "BLEEMEO_AGENT_"
	delimiter             = "."
)

var (
	errDeprecatedEnv       = errors.New("environment variable is deprecated")
	errSettingsDeprecated  = errors.New("setting is deprecated")
	errWrongMapFormat      = errors.New("could not parse map from string")
	errUnsupportedProvider = errors.New("provider not supported by config loader")
	errCannotMerge         = errors.New("cannot merge")
	ErrInvalidValue        = errors.New("invalid config value")
)

// Load loads the configuration from files and directories to a struct.
// Returns the config, warnings and an error.
func Load(withDefault bool, paths ...string) (Config, prometheus.MultiError, error) {
	// If no config was given with flags or env variables, fallback on the default files.
	if len(paths) == 0 || len(paths) == 1 && paths[0] == "" {
		paths = DefaultPaths()
	}

	return loadToStruct(withDefault, paths...)
}

func loadToStruct(withDefault bool, paths ...string) (Config, prometheus.MultiError, error) {
	// Override config files if the files were given from the env.
	if envFiles := os.Getenv(EnvGloutonConfigFiles); envFiles != "" {
		paths = strings.Split(envFiles, ",")
	}

	k, warnings, err := load(withDefault, paths...)

	config, warning := unmarshalConfig(k)
	warnings.Append(warning)

	return config, unwrapErrors(warnings), err
}

// unmarshalConfig convert a koanf to a structured config.
func unmarshalConfig(k *koanf.Koanf) (Config, error) {
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

	err := k.UnmarshalWithConf("", &config, unmarshalConf)

	return config, err
}

// load the configuration from files and directories.
func load(withDefault bool, paths ...string) (*koanf.Koanf, prometheus.MultiError, error) {
	loader := &configLoader{}

	warnings, errors := loadPaths(loader, paths)

	// Load config from environment variables.
	// The warnings are filled only after k.Load is called.
	envToKey, envWarnings := envToKeyFunc()

	moreWarnings := loader.Load("", env.Provider(deprecatedEnvPrefix, delimiter, envToKey), nil)
	warnings = append(warnings, moreWarnings...)

	moreWarnings = loader.Load("", env.Provider(envPrefix, delimiter, envToKey), nil)
	warnings = append(warnings, moreWarnings...)

	if len(*envWarnings) > 0 {
		warnings = append(warnings, *envWarnings...)
	}

	if withDefault {
		moreWarnings = loader.Load("", structs.Provider(DefaultConfig(), Tag), nil)
		warnings = append(warnings, moreWarnings...)
	}

	finalKoanf, moreWarnings := loader.Build()
	warnings = append(warnings, moreWarnings...)

	return finalKoanf, warnings, errors.MaybeUnwrap()
}

// envToKeyFunc returns a function that converts an environment variable to a configuration key
// and a pointer to Warnings, the warnings are filled only after koanf.Load has been called.
// Panics if two config keys correspond to the same environment variable.
func envToKeyFunc() (func(string) string, *prometheus.MultiError) {
	// Get all config keys from an empty config.
	k := koanf.New(delimiter)
	_ = k.Load(structs.Provider(Config{}, Tag), nil)
	allKeys := k.All()

	// Build a map of the environment variables with their corresponding config keys.
	envToKey := make(map[string]string, len(allKeys))

	for key := range allKeys {
		envKey := toEnvKey(key)

		if oldKey, exists := envToKey[envKey]; exists {
			panic(fmt.Sprintf("Conflict between config keys, %s and %s both corresponds to the variable %s", oldKey, key, envKey))
		}

		envToKey[envKey] = key
	}

	// Build a map of the deprecated environment variables with their corresponding new variable.
	movedEnvKeys := map[string]string{
		"BLEEMEO_AGENT_ACCOUNT":          "GLOUTON_BLEEMEO_ACCOUNT_ID",
		"BLEEMEO_AGENT_REGISTRATION_KEY": "GLOUTON_BLEEMEO_REGISTRATION_KEY",
		"BLEEMEO_AGENT_API_BASE":         "GLOUTON_BLEEMEO_API_BASE",
		"BLEEMEO_AGENT_MQTT_HOST":        "GLOUTON_BLEEMEO_MQTT_HOST",
		"BLEEMEO_AGENT_MQTT_PORT":        "GLOUTON_BLEEMEO_MQTT_PORT",
		"BLEEMEO_AGENT_MQTT_SSL":         "GLOUTON_BLEEMEO_MQTT_SSL",
	}

	for k, v := range movedKeys() {
		movedEnvKeys[toEnvKey(k)] = toEnvKey(v)
		movedEnvKeys[toDeprecatedEnvKey(k)] = toEnvKey(v)
	}

	warnings := make(prometheus.MultiError, 0)
	envFunc := func(s string) string {
		// Migrate deprecated keys.
		if newKey, ok := movedEnvKeys[s]; ok {
			warnings.Append(fmt.Errorf("%w: %s, use %s instead", errDeprecatedEnv, s, newKey))
			s = newKey
		}

		if strings.HasPrefix(s, deprecatedEnvPrefix) {
			newKey := strings.Replace(s, deprecatedEnvPrefix, envPrefix, 1)
			warnings.Append(fmt.Errorf("%w: %s, use %s instead", errDeprecatedEnv, s, newKey))
			s = newKey
		}

		return envToKey[s]
	}

	return envFunc, &warnings
}

// toEnvKey returns the environment variable corresponding to a configuration key.
// For instance: toEnvKey("web.enable") -> GLOUTON_WEB_ENABLE.
func toEnvKey(key string) string {
	envKey := strings.ToUpper(key)
	envKey = envPrefix + strings.ReplaceAll(envKey, ".", "_")

	return envKey
}

// toDeprecatedEnvKey returns the environment variable corresponding to a configuration key
// with the deprecated prefix. For instance: toEnvKey("web.enable") -> BLEEMEO_AGENT_WEB_ENABLE.
func toDeprecatedEnvKey(key string) string {
	envKey := strings.ToUpper(key)
	envKey = deprecatedEnvPrefix + strings.ReplaceAll(envKey, ".", "_")

	return envKey
}

// loadPaths returns the config loaded from the given paths, warnings and errors.
func loadPaths(loader *configLoader, paths []string) (prometheus.MultiError, prometheus.MultiError) {
	var warnings, errors prometheus.MultiError

	for _, path := range paths {
		stat, err := os.Stat(path)
		if err != nil && os.IsNotExist(err) {
			logger.V(2).Printf("config file: %s ignored since it does not exists", path)

			continue
		}

		if err != nil {
			logger.V(2).Printf("config file: %s ignored due to %v", path, err)
			errors.Append(err)

			continue
		}

		if stat.IsDir() {
			moreWarnings, err := loadDirectory(loader, path)
			errors.Append(err)

			if moreWarnings != nil {
				warnings = append(warnings, moreWarnings...)
			}

			if err != nil {
				logger.V(2).Printf("config file: directory %s have ignored some files due to %v", path, err)
			}
		} else {
			warning := loadFile(loader, path)
			warnings = append(warnings, warning...)
		}

		if err == nil {
			logger.V(2).Printf("config file: %s loaded", path)
		}
	}

	return warnings, errors
}

func loadDirectory(loader *configLoader, dirPath string) (prometheus.MultiError, error) {
	files, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, err
	}

	var warnings prometheus.MultiError

	for _, f := range files {
		if !strings.HasSuffix(f.Name(), ".conf") {
			continue
		}

		path := filepath.Join(dirPath, f.Name())

		warning := loadFile(loader, path)
		warnings = append(warnings, warning...)
	}

	return warnings, nil
}

func loadFile(loader *configLoader, path string) prometheus.MultiError {
	// Merge this file with the previous config.
	// Overwrite values, merge maps and append slices.
	err := loader.Load(path, file.Provider(path), yamlParser.Parser())
	if err != nil {
		return err
	}

	return nil
}

// unwrapErrors unwrap all errors in the list than contain multiple errors.
func unwrapErrors(errs prometheus.MultiError) prometheus.MultiError {
	if len(errs) == 0 {
		return nil
	}

	unwrapped := make(prometheus.MultiError, 0, len(errs))

	for _, err := range errs {
		var (
			mapErr  *mapstructure.Error
			yamlErr *yaml.TypeError
		)

		switch {
		case errors.As(err, &mapErr):
			unwrapped = append(unwrapped, mapErr.WrappedErrors()...)
		case errors.As(err, &yamlErr):
			for _, wrappedErr := range yamlErr.Errors {
				unwrapped.Append(errors.New(wrappedErr)) //nolint:goerr113
			}
		default:
			unwrapped.Append(err)
		}
	}

	return unwrapped
}

// movedKeys return all keys that were moved. The map is old key => new key.
func movedKeys() map[string]string {
	keys := map[string]string{
		"agent.windows_exporter.enabled":  "agent.windows_exporter.enable",
		"agent.http_debug.enabled":        "agent.http_debug.enable",
		"kubernetes.enabled":              "kubernetes.enable",
		"blackbox.enabled":                "blackbox.enable",
		"agent.process_exporter.enabled":  "agent.process_exporter.enable",
		"web.enabled":                     "web.enable",
		"bleemeo.enabled":                 "bleemeo.enable",
		"jmx.enabled":                     "jmx.enable",
		"nrpe.enabled":                    "nrpe.enable",
		"zabbix.enabled":                  "zabbix.enable",
		"influxdb.enabled":                "influxdb.enable",
		"telegraf.statsd.enabled":         "telegraf.statsd.enable",
		"agent.telemetry.enabled":         "agent.telemetry.enable",
		"agent.node_exporter.enabled":     "agent.node_exporter.enable",
		"telegraf.docker_metrics_enabled": "telegraf.docker_metrics_enable",
	}

	return keys
}

// migrate upgrade the configuration when Glouton changes its settings.
func migrate(k *koanf.Koanf) (*koanf.Koanf, prometheus.MultiError) {
	config := k.All()

	var warnings prometheus.MultiError

	warnings = append(warnings, migrateMovedKeys(k, config)...)
	warnings = append(warnings, migrateLogging(k, config)...)
	warnings = append(warnings, migrateMetricsPrometheus(k, config)...)
	warnings = append(warnings, migrateScrapperMetrics(k, config)...)
	warnings = append(warnings, migrateServices(config)...)

	// We can't reuse the previous Koanf because it doesn't allow removing keys.
	newConfig := koanf.New(delimiter)

	warning := newConfig.Load(confmap.Provider(config, delimiter), nil)
	warnings.Append(warning)

	return newConfig, warnings
}

// migrateMovedKeys migrate the config settings that were simply moved.
func migrateMovedKeys(k *koanf.Koanf, config map[string]interface{}) prometheus.MultiError {
	var warnings prometheus.MultiError

	keys := movedKeys()

	for oldKey, newKey := range keys {
		val := k.Get(oldKey)
		if val == nil {
			continue
		}

		config[newKey] = val
		delete(config, oldKey)

		warnings.Append(fmt.Errorf("%w: %s, use %s instead", errSettingsDeprecated, oldKey, newKey))
	}

	return warnings
}

// migrateLogging migrates the logging settings.
func migrateLogging(k *koanf.Koanf, config map[string]interface{}) prometheus.MultiError {
	var warnings prometheus.MultiError

	for _, name := range []string{"tail_size", "head_size"} {
		oldKey := "logging.buffer." + name
		newKey := "logging.buffer." + name + "_bytes"

		value := k.Int(oldKey)
		if value == 0 {
			continue
		}

		config[newKey] = value * 100
		delete(config, oldKey)

		warnings.Append(fmt.Errorf("%w: %s, use %s instead", errSettingsDeprecated, oldKey, newKey))
	}

	return warnings
}

// migrateMetricsPrometheus migrates Prometheus settings.
func migrateMetricsPrometheus(k *koanf.Koanf, config map[string]interface{}) prometheus.MultiError {
	// metrics.prometheus was renamed metrics.prometheus.targets
	// We guess that old path was used when metrics.prometheus.*.url exist and is a string
	v := k.Get("metric.prometheus")
	if v == nil {
		return nil
	}

	var ( //nolint:prealloc // False positive.
		warnings        prometheus.MultiError
		migratedTargets []interface{}
	)

	vMap, ok := v.(map[string]interface{})
	if !ok {
		return nil
	}

	for key, dict := range vMap {
		tmp, ok := dict.(map[string]interface{})
		if !ok {
			continue
		}

		u, ok := tmp["url"].(string)
		if !ok {
			continue
		}

		warnings.Append(fmt.Errorf("%w: metrics.prometheus. See https://go.bleemeo.com/l/doc-prometheus", errSettingsDeprecated))

		migratedTargets = append(migratedTargets, map[string]interface{}{
			"url":  u,
			"name": key,
		})

		delete(config, fmt.Sprintf("metric.prometheus.%s", key))
		delete(config, fmt.Sprintf("metric.prometheus.%s.url", key))
		delete(config, fmt.Sprintf("metric.prometheus.%s.name", key))
	}

	if len(migratedTargets) > 0 {
		existing := k.Get("metric.prometheus.targets")
		targets, _ := existing.([]interface{})
		targets = append(targets, migratedTargets...)

		config["metric.prometheus.targets"] = targets
	}

	if k.Bool("metric.prometheus.targets.include_default_metrics") {
		warnings.Append(fmt.Errorf("%w: metrics.prometheus.targets.include_default_metrics. This option does not exists anymore and has no effect", errSettingsDeprecated))
	}

	return warnings
}

func migrateScrapperMetrics(k *koanf.Koanf, config map[string]interface{}) prometheus.MultiError {
	var warnings prometheus.MultiError

	warnings = append(warnings, migrateScrapper(k, config, "metric.prometheus.allow_metrics", "metric.allow_metrics")...)
	warnings = append(warnings, migrateScrapper(k, config, "metric.prometheus.deny_metrics", "metric.deny_metrics")...)
	warnings = append(warnings, migrateScrapper(k, config, "metric.prometheus.allow", "metric.allow_metrics")...)
	warnings = append(warnings, migrateScrapper(k, config, "metric.prometheus.deny", "metric.deny_metrics")...)

	return warnings
}

func migrateScrapper(k *koanf.Koanf, config map[string]interface{}, deprecatedPath string, correctPath string) prometheus.MultiError {
	migratedTargets := []string{}
	v := k.Get(deprecatedPath)

	if v == nil {
		return nil
	}

	vTab, ok := v.([]interface{})
	if !ok {
		return nil
	}

	var warnings prometheus.MultiError

	if len(vTab) > 0 {
		warnings.Append(fmt.Errorf("%w: %s, use %s", errSettingsDeprecated, deprecatedPath, correctPath))

		for _, val := range vTab {
			s, _ := val.(string)
			if s != "" {
				migratedTargets = append(migratedTargets, s)
			}
		}
	}

	if len(migratedTargets) > 0 {
		existing := k.Get(correctPath)
		targets, _ := existing.([]interface{})

		for _, val := range migratedTargets {
			targets = append(targets, val)
		}

		config[correctPath] = targets
		delete(config, deprecatedPath)
	}

	return warnings
}

// migrateServices migrates deprecated service options.
func migrateServices(config map[string]interface{}) prometheus.MultiError {
	migratedOptions := map[string]string{
		"cassandra_detailed_tables": "detailed_items",
		"mgmt_port":                 "stats_port",
	}

	var warnings prometheus.MultiError

	servicesInt := config["service"]

	servicesList, ok := servicesInt.([]interface{})
	if !ok {
		return nil
	}

	for _, serviceInt := range servicesList {
		serviceMap, ok := serviceInt.(map[string]interface{})
		if !ok {
			continue
		}

		for deprecatedOpt, newOpt := range migratedOptions {
			detailedTablesInt, ok := serviceMap[deprecatedOpt]
			if !ok {
				continue
			}

			serviceMap[newOpt] = detailedTablesInt
			delete(serviceMap, deprecatedOpt)

			warnings.Append(fmt.Errorf("%w: '%s', use '%s' instead", errSettingsDeprecated, deprecatedOpt, newOpt))
		}
	}

	config["service"] = servicesInt

	return warnings
}

// Dump return a copy of the whole configuration, with secrets retracted.
// secret is any key containing "key", "secret", "password" or "passwd".
func Dump(config Config) map[string]interface{} {
	k := koanf.New(delimiter)
	_ = k.Load(structs.Provider(config, Tag), nil)

	return dump(k.Raw())
}

func dump(root map[string]interface{}) map[string]interface{} {
	secretKey := []string{"key", "secret", "password", "passwd"}

	for k, v := range root {
		isSecret := false

		for _, name := range secretKey {
			if strings.Contains(k, name) {
				isSecret = true

				break
			}
		}

		if isSecret {
			root[k] = "*****"

			continue
		}

		switch v := v.(type) {
		case map[string]interface{}:
			root[k] = dump(v)
		case []interface{}:
			root[k] = dumpList(v)
		default:
			root[k] = v
		}
	}

	return root
}

func dumpList(root []interface{}) []interface{} {
	for i, v := range root {
		switch v := v.(type) {
		case map[string]interface{}:
			root[i] = dump(v)
		case []interface{}:
			root[i] = dumpList(v)
		default:
			root[i] = v
		}
	}

	return root
}
