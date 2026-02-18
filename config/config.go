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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bleemeo/glouton/logger"

	"github.com/go-viper/mapstructure/v2"
	yamlParser "github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/structs"
	"github.com/knadh/koanf/v2"
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
	ErrMissconfiguration   = errors.New("config issue")
)

// Load the configuration from files and environment variables.
// It returns the config, the loaded items, warnings and an error.
func Load(withDefault bool, loadEnviron bool, paths ...string) (Config, []Item, prometheus.MultiError, error) {
	// If no config was given with flags or env variables, fallback on the default files.
	if len(paths) == 0 || len(paths) == 1 && paths[0] == "" {
		paths = DefaultPaths()
	}

	loader := &configLoader{}

	config, warnings, err := load(loader, withDefault, loadEnviron, paths...)

	switch {
	case config.Agent.StateFile != "" && config.Agent.StateDirectory == "":
		config.Agent.StateDirectory = filepath.Dir(config.Agent.StateFile)
	case config.Agent.StateDirectory == "":
		config.Agent.StateDirectory = "."
	case !filepath.IsAbs(config.Agent.StateFile):
		config.Agent.StateFile = filepath.Join(config.Agent.StateDirectory, config.Agent.StateFile)
	}

	if !filepath.IsAbs(config.Agent.StateCacheFile) {
		config.Agent.StateCacheFile = filepath.Join(config.Agent.StateDirectory, config.Agent.StateCacheFile)
	}

	if !filepath.IsAbs(config.Agent.StateResetFile) {
		config.Agent.StateResetFile = filepath.Join(config.Agent.StateDirectory, config.Agent.StateResetFile)
	}

	if !filepath.IsAbs(config.Agent.FactsFile) {
		config.Agent.FactsFile = filepath.Join(config.Agent.StateDirectory, config.Agent.FactsFile)
	}

	if !filepath.IsAbs(config.Agent.NetstatFile) {
		config.Agent.NetstatFile = filepath.Join(config.Agent.StateDirectory, config.Agent.NetstatFile)
	}

	if !filepath.IsAbs(config.Agent.UpgradeFile) {
		config.Agent.UpgradeFile = filepath.Join(config.Agent.StateDirectory, config.Agent.UpgradeFile)
	}

	if !filepath.IsAbs(config.Agent.AutoUpgradeFile) {
		config.Agent.AutoUpgradeFile = filepath.Join(config.Agent.StateDirectory, config.Agent.AutoUpgradeFile)
	}

	if !filepath.IsAbs(config.Agent.CloudImageCreationFile) {
		config.Agent.CloudImageCreationFile = filepath.Join(config.Agent.StateDirectory, config.Agent.CloudImageCreationFile)
	}

	moreWarnings := checkForConfigMistake(config)
	warnings = append(warnings, moreWarnings...)

	return config, loader.items, warnings, err
}

func checkForConfigMistake(cfg Config) prometheus.MultiError {
	var warnings prometheus.MultiError

	if cfg.MQTT.Enable && len(cfg.MQTT.Hosts) == 0 {
		warnings.Append(fmt.Errorf("%w: OpenSource MQTT is enable but with an empty hosts list", ErrMissconfiguration))
	}

	return warnings
}

// load the configuration from files and environment variables.
func load(loader *configLoader, withDefault bool, loadEnviron bool, paths ...string) (Config, prometheus.MultiError, error) {
	// Override config files if the files were given from the env.
	if envFiles := os.Getenv(EnvGloutonConfigFiles); loadEnviron && envFiles != "" {
		paths = strings.Split(envFiles, ",")
	}

	warnings, errors := loadPaths(loader, paths)

	if loadEnviron {
		// Load config from environment variables.
		// The warnings are filled only after Load is called.
		envToKey, envWarnings := envToKeyFunc()

		moreWarnings := loader.Load("", env.Provider(deprecatedEnvPrefix, delimiter, envToKey), nil)
		warnings = append(warnings, moreWarnings...)

		moreWarnings = loader.Load("", env.Provider(envPrefix, delimiter, envToKey), nil)
		warnings = append(warnings, moreWarnings...)

		if len(*envWarnings) > 0 {
			warnings = append(warnings, *envWarnings...)
		}
	}

	// Load default config.
	if withDefault {
		moreWarnings := loader.Load("", structs.Provider(DefaultConfig(), Tag), nil)
		warnings = append(warnings, moreWarnings...)
	}

	// Build the final config from the loaded items.
	finalKoanf, moreWarnings := loader.Build()
	warnings = append(warnings, moreWarnings...)

	// Unmarshal the config.
	var config Config

	// Here we ignore unused keys warnings and most decoder hooks
	// because this processing was already done in the config loader.
	unmarshalConf := koanf.UnmarshalConf{
		DecoderConfig: &mapstructure.DecoderConfig{
			// Keep the blackbox hook to use its custom yaml
			// marshaller that sets default values.
			DecodeHook: blackboxModuleHookFunc(),
			Result:     &config,
		},
		Tag: Tag,
	}

	warning := finalKoanf.UnmarshalWithConf("", &config, unmarshalConf)
	warnings.Append(warning)

	return config, unwrapErrors(warnings), errors.MaybeUnwrap()
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
			logger.V(2).Printf("config file %s ignored because it does not exists", path)

			continue
		}

		if err != nil {
			errors.Append(fmt.Errorf("file %s ignored: %w", path, err))

			continue
		}

		if stat.IsDir() {
			moreWarnings, err := loadDirectory(loader, path)
			if err != nil {
				errors.Append(fmt.Errorf("failed to load directory %s: %w", path, err))
			}

			if moreWarnings != nil {
				warnings = append(warnings, moreWarnings...)
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
	warnings := loader.Load(path, file.Provider(path), yamlParser.Parser())

	// Add path to errors.
	for i, warning := range warnings {
		warnings[i] = fmt.Errorf("%s: %w", path, warning)
	}

	return warnings
}

// unwrapErrors unwrap all errors in the list than contain multiple errors.
func unwrapErrors(errs prometheus.MultiError) prometheus.MultiError {
	if len(errs) == 0 {
		return nil
	}

	unwrapped := make(prometheus.MultiError, 0, len(errs))

	for _, err := range errs {
		for _, subErr := range unwrapRecurse(err) {
			var yamlErr *yaml.TypeError
			if errors.As(subErr, &yamlErr) {
				for _, wrappedErr := range yamlErr.Errors {
					unwrapped.Append(errors.New(wrappedErr)) //nolint:err113
				}
			} else {
				unwrapped.Append(subErr)
			}
		}
	}

	return unwrapped
}

func unwrapRecurse(err error) []error {
	wrapError, isWrapErr := err.(interface{ Unwrap() error })
	if isWrapErr {
		unwrappedErr := wrapError.Unwrap()
		// If err is just an fmt.wrapError that doesn't contain multiple errors, return it as-is.
		if !isMultiUnwrap(unwrappedErr) {
			return []error{err}
		}

		return unwrapRecurse(unwrappedErr)
	}

	joinError, isJoinErr := err.(interface{ Unwrap() []error })
	if isJoinErr {
		var subErrs []error

		for _, subErr := range joinError.Unwrap() {
			subErrs = append(subErrs, unwrapRecurse(subErr)...)
		}

		return subErrs
	}

	return []error{err}
}

func isMultiUnwrap(err error) bool {
	_, isMulti := err.(interface{ Unwrap() []error })
	if isMulti {
		return true
	}

	wrapped, isWrapped := err.(interface{ Unwrap() error })
	if isWrapped {
		return isMultiUnwrap(wrapped.Unwrap())
	}

	return false
}

// movedKeys return all keys that were moved. The map is old key => new key.
func movedKeys() map[string]string {
	keys := map[string]string{
		"agent.absent_service_deactivation_delay": "service_absent_deactivation_delay",
		"agent.http_debug.enable":                 "web.endpoints.debug_enable",
		"agent.http_debug.enabled":                "web.endpoints.debug_enable",
		"agent.node_exporter.enabled":             "agent.node_exporter.enable",
		"agent.process_exporter.enabled":          "agent.process_exporter.enable",
		"agent.telemetry.enabled":                 "agent.telemetry.enable",
		"agent.windows_exporter.enabled":          "agent.windows_exporter.enable",
		"blackbox.enabled":                        "blackbox.enable",
		"bleemeo.enabled":                         "bleemeo.enable",
		"jmx.enabled":                             "jmx.enable",
		"kubernetes.enabled":                      "kubernetes.enable",
		"network_interface_blacklist":             "network_interface_denylist",
		"nrpe.enabled":                            "nrpe.enable",
		"telegraf.docker_metrics_enabled":         "telegraf.docker_metrics_enable",
		"telegraf.statsd.enabled":                 "telegraf.statsd.enable",
		"web.enabled":                             "web.enable",
		"zabbix.enabled":                          "zabbix.enable",
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
func migrateMovedKeys(k *koanf.Koanf, config map[string]any) prometheus.MultiError {
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
func migrateLogging(k *koanf.Koanf, config map[string]any) prometheus.MultiError {
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
func migrateMetricsPrometheus(k *koanf.Koanf, config map[string]any) prometheus.MultiError {
	// metrics.prometheus was renamed metrics.prometheus.targets
	// We guess that old path was used when metrics.prometheus.*.url exist and is a string
	v := k.Get("metric.prometheus")
	if v == nil {
		return nil
	}

	var ( //nolint:prealloc // False positive.
		warnings        prometheus.MultiError
		migratedTargets []any
	)

	vMap, ok := v.(map[string]any)
	if !ok {
		return nil
	}

	for key, dict := range vMap {
		tmp, ok := dict.(map[string]any)
		if !ok {
			continue
		}

		u, ok := tmp["url"].(string)
		if !ok {
			continue
		}

		warnings.Append(fmt.Errorf("%w: metrics.prometheus. See https://go.bleemeo.com/l/doc-prometheus", errSettingsDeprecated))

		migratedTargets = append(migratedTargets, map[string]any{
			"url":  u,
			"name": key,
		})

		delete(config, "metric.prometheus."+key)
		delete(config, fmt.Sprintf("metric.prometheus.%s.url", key))
		delete(config, fmt.Sprintf("metric.prometheus.%s.name", key))
	}

	if len(migratedTargets) > 0 {
		existing := k.Get("metric.prometheus.targets")
		targets, _ := existing.([]any)
		targets = append(targets, migratedTargets...)

		config["metric.prometheus.targets"] = targets
	}

	if k.Bool("metric.prometheus.targets.include_default_metrics") {
		warnings.Append(fmt.Errorf("%w: metrics.prometheus.targets.include_default_metrics. This option does not exists anymore and has no effect", errSettingsDeprecated))
	}

	return warnings
}

func migrateScrapperMetrics(k *koanf.Koanf, config map[string]any) prometheus.MultiError {
	var warnings prometheus.MultiError

	warnings = append(warnings, migrateScrapper(k, config, "metric.prometheus.allow_metrics", "metric.allow_metrics")...)
	warnings = append(warnings, migrateScrapper(k, config, "metric.prometheus.deny_metrics", "metric.deny_metrics")...)
	warnings = append(warnings, migrateScrapper(k, config, "metric.prometheus.allow", "metric.allow_metrics")...)
	warnings = append(warnings, migrateScrapper(k, config, "metric.prometheus.deny", "metric.deny_metrics")...)

	return warnings
}

func migrateScrapper(k *koanf.Koanf, config map[string]any, deprecatedPath string, correctPath string) prometheus.MultiError {
	migratedTargets := []string{}
	v := k.Get(deprecatedPath)

	if v == nil {
		return nil
	}

	vTab, ok := v.([]any)
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
		targets, _ := existing.([]any)

		for _, val := range migratedTargets {
			targets = append(targets, val)
		}

		config[correctPath] = targets
		delete(config, deprecatedPath)
	}

	return warnings
}

// migrateServices migrates deprecated service options.
func migrateServices(config map[string]any) prometheus.MultiError {
	migratedOptions := map[string]string{
		"cassandra_detailed_tables": "detailed_items",
		"id":                        "type",
		"mgmt_port":                 "stats_port",
	}

	var warnings prometheus.MultiError

	servicesInt := config["service"]

	servicesList, ok := servicesInt.([]any)
	if !ok {
		return nil
	}

	for _, serviceInt := range servicesList {
		serviceMap, ok := serviceInt.(map[string]any)
		if !ok {
			continue
		}

		var serviceType string

		serviceTypeInt, ok := serviceMap["type"]
		if !ok {
			serviceTypeInt, ok = serviceMap["id"]
		}

		if ok {
			tmp, ok := serviceTypeInt.(string)
			if ok {
				serviceType = " for " + tmp
			}
		}

		for deprecatedOpt, newOpt := range migratedOptions {
			detailedTablesInt, ok := serviceMap[deprecatedOpt]
			if !ok {
				continue
			}

			serviceMap[newOpt] = detailedTablesInt
			delete(serviceMap, deprecatedOpt)

			warnings.Append(fmt.Errorf("%w in 'service' override%s: '%s', use '%s' instead", errSettingsDeprecated, serviceType, deprecatedOpt, newOpt))
		}
	}

	config["service"] = servicesInt

	return warnings
}

// Dump return a copy of the whole configuration, with secrets retracted.
// secret is any key containing "key", "secret", "password" or "passwd".
func Dump(config Config) map[string]any {
	k := koanf.New(delimiter)
	_ = k.Load(structs.Provider(config, Tag), nil)

	return dumpMap(k.Raw())
}

func dumpMap(root map[string]any) map[string]any {
	censored := make(map[string]any, len(root))

	for k, v := range root {
		censored[k] = CensorSecretItem(k, v)
	}

	return censored
}

// CensorSecretItem returns the censored item value with secrets
// and password removed for safe external use.
func CensorSecretItem(key string, value any) any {
	if isSecret(key) {
		// Don't censor unset secrets.
		if valueStr, ok := value.(string); ok && valueStr == "" {
			return ""
		}

		return "*****"
	}

	switch value := value.(type) {
	case map[string]any:
		return dumpMap(value)
	case []any:
		return dumpList(value)
	default:
		return value
	}
}

// isSecret returns whether the given config key corresponds to a secret.
func isSecret(key string) bool {
	for _, name := range []string{"key", "secret", "password", "passwd"} {
		if strings.Contains(key, name) {
			return true
		}
	}

	return false
}

func dumpList(root []any) []any {
	for i, v := range root {
		switch v := v.(type) {
		case map[string]any:
			root[i] = dumpMap(v)
		case []any:
			root[i] = dumpList(v)
		default:
			root[i] = v
		}
	}

	return root
}
