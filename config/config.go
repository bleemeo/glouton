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
	"errors"
	"fmt"
	"hash/fnv"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/prometheus/scrapper"
	"github.com/bleemeo/glouton/types"

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
	// Tag used to unmarshal the config; "yaml" instead of "koanf" because the config embeds the blackbox module config which uses YAML.
	Tag                   = "yaml"
	EnvGloutonConfigFiles = "GLOUTON_CONFIG_FILES"
	envPrefix             = "GLOUTON_"
	deprecatedEnvPrefix   = "BLEEMEO_AGENT_"
	delimiter             = "."

	// CensoredValue is the replacement string used when redacting secrets.
	CensoredValue = "*****"

	// Common map key names used in config migration and service overrides.
	keyURL           = "url"
	keyName          = "name"
	keyType          = "type"
	keyPassword      = "password"
	keyThresholds    = "thresholds"
	keyDetailedItems = "detailed_items"
	keyStatsPort     = "stats_port"
	keyCheckCommand  = "check_command"
)

var (
	errDeprecatedEnv         = errors.New("environment variable is deprecated")
	errSettingsDeprecated    = errors.New("setting is deprecated")
	errWrongMapFormat        = errors.New("could not parse map from string")
	errUnsupportedProvider   = errors.New("provider not supported by config loader")
	errCannotMerge           = errors.New("cannot merge")
	errLegacyFilterNameClash = errors.New("legacy log.inputs filter shares a metric name with another log.metrics.count entry")
	ErrInvalidValue          = errors.New("invalid config value")
	ErrMissconfiguration     = errors.New("config issue")
)

// Load loads the configuration from files and environment variables, returning the config, loaded items, warnings and an error.
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

	return config, loader.items, warnings, err
}

func isUserSet(items []Item, key string) bool {
	for _, row := range items {
		if row.Key == key && row.Source != SourceDefault {
			return true
		}
	}

	return false
}

func checkForConfigMistake(cfg Config, items []Item) prometheus.MultiError {
	var warnings prometheus.MultiError

	if cfg.MQTT.Enable && len(cfg.MQTT.Hosts) == 0 {
		warnings.Append(fmt.Errorf("%w: OpenSource MQTT is enable but with an empty hosts list", ErrMissconfiguration))
	}

	if cfg.Log.OpenTelemetry.AutoDiscovery.AllEnable {
		if !cfg.Log.OpenTelemetry.AutoDiscovery.AuditdEnable && isUserSet(items, "log.opentelemetry.auto_discovery.auditd_enable") {
			warnings.Append(fmt.Errorf("%w: log.opentelemetry.auto_discovery.auditd_enable can't disable when all_enable is active", ErrMissconfiguration))
		}

		if !cfg.Log.OpenTelemetry.AutoDiscovery.ContainerAndServiceEnable && isUserSet(items, "log.opentelemetry.auto_discovery.container_and_service_enable") {
			warnings.Append(fmt.Errorf("%w: log.opentelemetry.auto_discovery.container_and_service_enable can't disable when all_enable is active", ErrMissconfiguration))
		}

		if !cfg.Log.OpenTelemetry.AutoDiscovery.JournaldEnable && isUserSet(items, "log.opentelemetry.auto_discovery.journald_enable") {
			warnings.Append(fmt.Errorf("%w: log.opentelemetry.auto_discovery.journald_enable can't disable when all_enable is active", ErrMissconfiguration))
		}

		if !cfg.Log.OpenTelemetry.AutoDiscovery.SyslogEnable && isUserSet(items, "log.opentelemetry.auto_discovery.syslog_enable") {
			warnings.Append(fmt.Errorf("%w: log.opentelemetry.auto_discovery.syslog_enable can't disable when all_enable is active", ErrMissconfiguration))
		}
	}

	return warnings
}

func applyConfigTransformation(cfg Config) Config {
	if cfg.Log.OpenTelemetry.AutoDiscovery.AllEnable {
		cfg.Log.OpenTelemetry.AutoDiscovery.AuditdEnable = true
		cfg.Log.OpenTelemetry.AutoDiscovery.JournaldEnable = true
		cfg.Log.OpenTelemetry.AutoDiscovery.SyslogEnable = true
		cfg.Log.OpenTelemetry.AutoDiscovery.ContainerAndServiceEnable = true
	}

	return cfg
}

// load the configuration from files and environment variables.
func load(loader *configLoader, withDefault bool, loadEnviron bool, paths ...string) (Config, prometheus.MultiError, error) {
	// Override config files if the files were given from the env.
	if envFiles := os.Getenv(EnvGloutonConfigFiles); loadEnviron && envFiles != "" {
		paths = strings.Split(envFiles, ",")
	}

	warnings, errors := loadPaths(loader, paths)

	if loadEnviron {
		// Load config from environment variables; warnings filled after Load.
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

	// Most decoder hooks ignored here; already handled in config loader.
	unmarshalConf := koanf.UnmarshalConf{
		DecoderConfig: &mapstructure.DecoderConfig{
			// Blackbox hook uses custom yaml marshaller for defaults.
			DecodeHook: blackboxModuleHookFunc(),
			Result:     &config,
		},
		Tag: Tag,
	}

	warning := finalKoanf.UnmarshalWithConf("", &config, unmarshalConf)
	warnings.Append(warning)

	moreWarnings = checkForConfigMistake(config, loader.items)
	warnings = append(warnings, moreWarnings...)

	config = applyConfigTransformation(config)

	if err := validateLogReceivers(config); err != nil {
		errors.Append(err)
	}

	return config, unwrapErrors(warnings), errors.MaybeUnwrap()
}

// envToKeyFunc returns a function converting an env variable to a config key, plus warnings filled only after koanf.Load has been called.
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

// toDeprecatedEnvKey returns the environment variable with the deprecated prefix (e.g. "web.enable" -> BLEEMEO_AGENT_WEB_ENABLE).
func toDeprecatedEnvKey(key string) string {
	envKey := strings.ToUpper(key)
	envKey = deprecatedEnvPrefix + strings.ReplaceAll(envKey, ".", "_")

	return envKey
}

// loadPaths returns the config loaded from the given paths, warnings and errors.
func loadPaths(loader *configLoader, paths []string) (prometheus.MultiError, prometheus.MultiError) {
	var warnings, errors prometheus.MultiError

	for _, path := range paths {
		stat, err := os.Stat(path) //nolint:gosec // path comes from config, not user input
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
	// Merge this file with previous config, overwriting values, merging maps, appending slices.
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
		"agent.absent_service_deactivation_delay":                       "service_absent_deactivation_delay",
		"agent.http_debug.enable":                                       "web.endpoints.debug_enable",
		"agent.http_debug.enabled":                                      "web.endpoints.debug_enable",
		"agent.node_exporter.enabled":                                   "agent.node_exporter.enable",
		"agent.process_exporter.enabled":                                "agent.process_exporter.enable",
		"agent.telemetry.enabled":                                       "agent.telemetry.enable",
		"agent.windows_exporter.enabled":                                "agent.windows_exporter.enable",
		"blackbox.enabled":                                              "blackbox.enable",
		"bleemeo.enabled":                                               "bleemeo.enable",
		"jmx.enabled":                                                   "jmx.enable",
		"kubernetes.enabled":                                            "kubernetes.enable",
		"log.opentelemetry.auto_discovery.enable_all":                   "log.opentelemetry.auto_discovery.all_enable",
		"log.opentelemetry.auto_discovery.enable_auditd":                "log.opentelemetry.auto_discovery.auditd_enable",
		"log.opentelemetry.auto_discovery.enable_container_and_service": "log.opentelemetry.auto_discovery.container_and_service_enable",
		"log.opentelemetry.auto_discovery.enable_journalctl":            "log.opentelemetry.auto_discovery.journald_enable",
		"log.opentelemetry.auto_discovery.journalctl_enable":            "log.opentelemetry.auto_discovery.journald_enable",
		"log.opentelemetry.auto_discovery.enable_syslog":                "log.opentelemetry.auto_discovery.syslog_enable",
		"log.opentelemetry.enable":                                      "log.opentelemetry.shipping_enable",
		"network_interface_blacklist":                                   "network_interface_denylist",
		"nrpe.enabled":                                                  "nrpe.enable",
		"telegraf.docker_metrics_enabled":                               "telegraf.docker_metrics_enable",
		"telegraf.statsd.enabled":                                       "telegraf.statsd.enable",
		"web.enabled":                                                   "web.enable",
		"zabbix.enabled":                                                "zabbix.enable",
	}

	return keys
}

// movedScalarKeys return all keys that were moved. The map is old key => new key.
func movedScalarKeys() map[string]string {
	keys := map[string]string{
		"log.opentelemetry.auto_discovery": "log.opentelemetry.auto_discovery.all_enable",
	}

	return keys
}

// migrate upgrade the configuration when Glouton changes its settings.
// path identifies the provider being migrated (e.g. a config file path); it's used to keep
// generated keys unique when the same migration runs once per provider (see migrateLogInputs).
func migrate(k *koanf.Koanf, path string) (*koanf.Koanf, prometheus.MultiError) {
	config := k.All()

	warnings := make(prometheus.MultiError, 0, 7)

	warnings = append(warnings, migrateMovedScalarKeys(k, config)...)
	warnings = append(warnings, migrateMovedKeys(k, config)...)
	warnings = append(warnings, migrateLogging(k, config)...)
	warnings = append(warnings, migrateMetricsPrometheus(k, config)...)
	warnings = append(warnings, migrateScrapperMetrics(k, config)...)
	warnings = append(warnings, migrateServices(config)...)
	warnings = append(warnings, migrateLegacyNetworkListeners(k, config)...)
	warnings = append(warnings, migrateLogInputs(k, config, path)...)
	warnings = append(warnings, migrateLogFluentBitURL(config)...)

	// We can't reuse the previous Koanf because it doesn't allow removing keys.
	newConfig := koanf.New(delimiter)

	warning := newConfig.Load(confmap.Provider(config, delimiter), nil)
	warnings.Append(warning)

	return newConfig, warnings
}

func isScalar(val any) bool {
	switch val.(type) {
	case string, int, float64, bool:
		return true
	}

	return false
}

// migrateMovedScalarKeys migrates scalar (string, int, bool) config settings that were simply moved into a sub-field under the same name,
// e.g. log.opentelemetry.auto_discovery -> log.opentelemetry.auto_discovery.enable.
func migrateMovedScalarKeys(k *koanf.Koanf, config map[string]any) prometheus.MultiError {
	var warnings prometheus.MultiError

	keys := movedScalarKeys()

	for oldKey, newKey := range keys {
		val := k.Get(oldKey)
		if val == nil {
			continue
		}

		if !isScalar(val) {
			continue
		}

		config[newKey] = val
		delete(config, oldKey)

		warnings.Append(fmt.Errorf("%w: %s, use %s instead", errSettingsDeprecated, oldKey, newKey))
	}

	return warnings
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
	// metrics.prometheus was renamed metrics.prometheus.targets; the old path is detected when metrics.prometheus.*.url exists and is a string.
	v := k.Get("metric.prometheus")
	if v == nil {
		return nil
	}

	var (
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

		u, ok := tmp[keyURL].(string)
		if !ok {
			continue
		}

		warnings.Append(fmt.Errorf("%w: metrics.prometheus. See https://go.bleemeo.com/l/doc-prometheus", errSettingsDeprecated))

		migratedTargets = append(migratedTargets, map[string]any{
			keyURL:  u,
			keyName: key,
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
	warnings := make(prometheus.MultiError, 0, 4)

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

// legacyMetricsRuleName namespaces a migrated log.inputs metric name so it can't collide with a hand-written log.metrics_rules entry of the same name.
func legacyMetricsRuleName(metric string) string {
	return "legacy_log_inputs_metric_" + metric
}

// legacyInputReceiverName builds a receiver name for a migrated log.inputs[i] entry that's unique across every
// provider (config file), not just within one: migrate() runs once per provider with i restarting from 0 each
// time, so two files each declaring one log.inputs entry would otherwise both produce "legacy_input_0".
func legacyInputReceiverName(path string, i int) string {
	if path == "" {
		return fmt.Sprintf("legacy_input_%d", i)
	}

	h := fnv.New32a()
	_, _ = h.Write([]byte(path))

	return fmt.Sprintf("legacy_input_%08x_%d", h.Sum32(), i)
}

// mergeLegacyFilters ORs legacy filter regex/exclude into countconnector conditions, coalescing metrics by name; returns touched metrics and warnings.
func mergeLegacyFilters(metricsByName map[string]any, filtersList []any) ([]string, []error) {
	var (
		touched  []string
		warnings []error
	)

	touchedThisCall := make(map[string]bool)

	for _, filterAny := range filtersList {
		filterMap, ok := filterAny.(map[string]any)
		if !ok {
			continue
		}

		metric, _ := filterMap["metric"].(string)
		regex, _ := filterMap["regex"].(string)

		if metric == "" || regex == "" {
			continue
		}

		exclude, _ := filterMap["exclude"].(string)

		condition := fmt.Sprintf("IsMatch(body, %q)", regex)
		if exclude != "" {
			condition = fmt.Sprintf("%s and not IsMatch(body, %q)", condition, exclude)
		}

		entry, existed := metricsByName[metric].(map[string]any)
		if !existed {
			entry = map[string]any{"metric": metric, "item": ""}

			if labels, ok := filterMap["labels"].(map[string]any); ok && len(labels) > 0 {
				entry["labels"] = labels
			}

			metricsByName[metric] = entry
		}

		if !touchedThisCall[metric] {
			touchedThisCall[metric] = true

			touched = append(touched, metric)

			if existed {
				warnings = append(warnings, fmt.Errorf(
					"%w: metric %q, conditions from multiple log.inputs entries are merged into one shared log.metrics_rules.%s entry",
					errLegacyFilterNameClash, metric, legacyMetricsRuleName(metric),
				))
			}
		}

		conditions, _ := entry["conditions"].([]any)
		entry["conditions"] = append(conditions, condition)
	}

	return touched, warnings
}

// migrateLogInputs folds the legacy log.inputs[].filters entries (the original, Fluent Bit-era log-to-metric source) into the
// equivalent log.opentelemetry.receivers/log.metrics_rules shape. Every migrated metric gets item: "" unconditionally, to avoid
// changing the identity of an already-existing metric series for currently-deployed users.
// providerPath identifies the provider this call is migrating (e.g. a config file path); migrate() runs once per provider
// with the loop index i restarting from 0 each time, so providerPath must be folded into the generated receiver name to
// avoid two files each declaring one log.inputs entry from both producing "legacy_input_0" and overwriting one another.
func migrateLogInputs(k *koanf.Koanf, config map[string]any, providerPath string) prometheus.MultiError {
	var warnings prometheus.MultiError

	inputs, ok := k.Get("log.inputs").([]any)
	if !ok || len(inputs) == 0 {
		return nil
	}

	// Read from config, not k: migrateLegacyNetworkListeners may already have written a "legacy_network" receiver here.
	receivers, _ := config["log.opentelemetry.receivers"].(map[string]any)
	if receivers == nil {
		receivers, _ = k.Get("log.opentelemetry.receivers").(map[string]any)
	}

	if receivers == nil {
		receivers = map[string]any{}
	}

	metricsRules, _ := config["log.metrics_rules"].(map[string]any)
	if metricsRules == nil {
		metricsRules, _ = k.Get("log.metrics_rules").(map[string]any)
	}

	if metricsRules == nil {
		metricsRules = map[string]any{}
	}

	// Shared across every mergeLegacyFilters call below -- see its doc comment.
	metricsByName := map[string]any{}

	remainingInputs := make([]any, 0, len(inputs))
	translated := false

	for i, inputAny := range inputs {
		inputMap, ok := inputAny.(map[string]any)
		if !ok {
			remainingInputs = append(remainingInputs, inputAny)

			continue
		}

		filtersList, ok := inputMap["filters"].([]any)
		if !ok || len(filtersList) == 0 {
			remainingInputs = append(remainingInputs, inputAny) // no filters: this entry never did anything for log-to-metric

			continue
		}

		// Drop the consumed key so it doesn't trip the strict struct decode (errors on unknown keys).
		delete(inputMap, "filters")

		path, _ := inputMap["path"].(string)
		containerName, _ := inputMap["container_name"].(string)
		selectors, _ := inputMap["container_selectors"].(map[string]any)

		if path == "" && containerName == "" && len(selectors) == 0 {
			warnings.Append(fmt.Errorf("%w: log.inputs[%d] has filters but no path/container_name/container_selectors set, filters were dropped", errSettingsDeprecated, i))

			remainingInputs = append(remainingInputs, inputAny)

			continue
		}

		touchedMetrics, mergeWarnings := mergeLegacyFilters(metricsByName, filtersList)
		for _, w := range mergeWarnings {
			warnings.Append(w)
		}

		metrics := make([]any, 0, len(touchedMetrics))
		for _, metric := range touchedMetrics {
			metrics = append(metrics, map[string]any{"include": legacyMetricsRuleName(metric)})
		}

		// Legacy log.inputs was metrics-only, so the migrated receiver never ships logs either.
		receiver := map[string]any{
			"send_logs": false,
			"metrics":   metrics,
		}

		if path != "" {
			receiver["include"] = []any{path}
		}

		if containerName != "" {
			receiver["container_name"] = containerName
		}

		if len(selectors) > 0 {
			receiver["container_selectors"] = selectors
		}

		receivers[legacyInputReceiverName(providerPath, i)] = receiver

		warnings.Append(fmt.Errorf("%w: log.inputs[%d].filters, use log.opentelemetry.receivers/log.metrics_rules instead", errSettingsDeprecated, i))

		translated = true
	}

	if !translated {
		return nil
	}

	for metric, entry := range metricsByName {
		ruleName := legacyMetricsRuleName(metric)

		if _, exists := metricsRules[ruleName]; exists {
			warnings.Append(fmt.Errorf(
				"%w: log.metrics_rules.%s already exists, keeping it as-is instead of overwriting it with the migrated log.inputs metric %q",
				errSettingsDeprecated, ruleName, metric,
			))

			continue
		}

		metricsRules[ruleName] = []any{entry}
	}

	config["log.opentelemetry.receivers"] = receivers
	config["log.metrics_rules"] = metricsRules
	config["log.inputs"] = remainingInputs

	return warnings
}

// migrateLegacyNetworkListeners folds log.opentelemetry.grpc/http's old, pre-network-receivers {enable, address, port} shape into a
// synthesized "legacy_network" receiver under opentelemetry.network.receivers, preserving the address/port and the unconditional
// shipping behavior (send_logs: true).
func migrateLegacyNetworkListeners(k *koanf.Koanf, config map[string]any) prometheus.MultiError {
	var warnings prometheus.MultiError

	const (
		path            = "log.opentelemetry"
		defaultGRPCPort = 4317
		defaultHTTPPort = 4318
		receiverName    = "legacy-network"
	)

	legacyGRPC, hasGRPC := k.Get(path + ".grpc").(map[string]any)
	legacyHTTP, hasHTTP := k.Get(path + ".http").(map[string]any)

	if !hasGRPC && !hasHTTP {
		return nil
	}

	// Drop the consumed keys so they don't trip the strict struct decode (errors on unknown keys).
	delete(config, path+".grpc")
	delete(config, path+".http")

	warnings.Append(fmt.Errorf(
		"%w: %s.grpc/http {enable, address, port}, use opentelemetry.network.receivers + a log.opentelemetry.receivers entry's network field instead",
		errSettingsDeprecated, path,
	))

	endpointOf := func(legacy map[string]any, defaultPort int) string {
		enable, _ := legacy["enable"].(bool)
		if !enable {
			return ""
		}

		address, _ := legacy["address"].(string)
		if address == "" {
			address = DefaultLocalhost
		}

		port := defaultPort

		switch p := legacy["port"].(type) {
		case int:
			port = p
		case int64:
			port = int(p)
		case float64:
			port = int(p)
		}

		return fmt.Sprintf("%s:%d", address, port)
	}

	var grpcEndpoint, httpEndpoint string
	if hasGRPC {
		grpcEndpoint = endpointOf(legacyGRPC, defaultGRPCPort)
	}

	if hasHTTP {
		httpEndpoint = endpointOf(legacyHTTP, defaultHTTPPort)
	}

	if grpcEndpoint == "" && httpEndpoint == "" {
		return warnings // both were disabled: no network participation to migrate
	}

	protocols := map[string]any{}
	if grpcEndpoint != "" {
		protocols["grpc"] = map[string]any{"endpoint": grpcEndpoint}
	}

	if httpEndpoint != "" {
		protocols["http"] = map[string]any{"endpoint": httpEndpoint}
	}

	networkReceivers, _ := k.Get("opentelemetry.network.receivers").(map[string]any)
	if networkReceivers == nil {
		networkReceivers = map[string]any{}
	}

	networkReceivers[receiverName] = map[string]any{"protocols": protocols}
	config["opentelemetry.network.receivers"] = networkReceivers

	receivers, _ := k.Get("log.opentelemetry.receivers").(map[string]any)
	if receivers == nil {
		receivers = map[string]any{}
	}

	receivers["legacy_network"] = map[string]any{
		"network":   map[string]any{"receivers": []any{receiverName}},
		"send_logs": true,
	}
	config["log.opentelemetry.receivers"] = receivers

	return warnings
}

// migrateLogFluentBitURL drops the pre-OpenTelemetry log.fluentbit_url setting (once overridden by the
// bleemeo-agent-logs package) with a deprecation warning, instead of letting it fail the strict struct
// decode as an unknown key -- which otherwise looks like a config error on every upgrade instead of a no-op.
func migrateLogFluentBitURL(config map[string]any) prometheus.MultiError {
	if _, ok := config["log.fluentbit_url"]; !ok {
		return nil
	}

	delete(config, "log.fluentbit_url")

	var warnings prometheus.MultiError

	warnings.Append(fmt.Errorf("%w: log.fluentbit_url. This option does not exists anymore and has no effect", errSettingsDeprecated))

	return warnings
}

// migrateServices migrates deprecated service options.
func migrateServices(config map[string]any) prometheus.MultiError {
	migratedOptions := map[string]string{
		"cassandra_detailed_tables": keyDetailedItems,
		"id":                        keyType,
		"mgmt_port":                 keyStatsPort,
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

		serviceTypeInt, ok := serviceMap[keyType]
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

// Dump returns a copy of the whole configuration with secrets retracted (any key containing "key", "secret", "password" or "passwd").
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

// CensorSecretItem returns the item value with secrets and passwords redacted for safe external use.
func CensorSecretItem(key string, value any) any {
	if isSecret(key) {
		// Don't censor unset secrets.
		if valueStr, ok := value.(string); ok && valueStr == "" {
			return ""
		}

		return CensoredValue
	}

	switch value := value.(type) {
	case map[string]any:
		return dumpMap(value)
	case []any:
		return dumpList(value)
	case string:
		// Redact credentials embedded in URL values (e.g. proxy_url=http://user:pass@host) not caught by isSecret.
		return CensorURLCredentials(value)
	default:
		return value
	}
}

// isSecret returns whether the given config key corresponds to a secret.
func isSecret(key string) bool {
	for _, name := range []string{"key", "secret", keyPassword, "passwd", "token", "credentials"} {
		if strings.Contains(key, name) {
			return true
		}
	}

	return false
}

// CensorURLCredentials redacts the password embedded in an URL's userinfo (e.g. "http://user:pass@host" becomes
// "http://user:*****@host"). Non-URL strings and URLs without credentials are returned unchanged.
func CensorURLCredentials(value string) string {
	if !strings.Contains(value, "@") {
		return value
	}

	u, err := url.Parse(value)
	if err != nil || u.User == nil {
		return value
	}

	if _, hasPassword := u.User.Password(); !hasPassword {
		return value
	}

	// Keep only the username then splice the censored password back in; url.UserPassword would percent-encode the placeholder, making it unreadable.
	u.User = url.User(u.User.Username())
	censored := u.String()
	at := strings.Index(censored, "@")

	return censored[:at] + ":" + CensoredValue + censored[at:]
}

// CensorURLSecrets redacts an URL's userinfo credentials and secret-looking query parameters (see CensorURLCredentials, isSecret).
// Meant for diagnostic output; metric labels keep the original URL to preserve metric identity.
func CensorURLSecrets(value string) string {
	return CensorURLCredentials(censorURLQuerySecrets(value))
}

// censorURLQuerySecrets redacts query-string parameter values whose name looks like a secret (e.g. "?token=abc" becomes
// "?token=*****"), preserving the order and encoding of the rest.
func censorURLQuerySecrets(value string) string {
	u, err := url.Parse(value)
	if err != nil || u.RawQuery == "" {
		return value
	}

	params := strings.Split(u.RawQuery, "&")
	changed := false

	for i, param := range params {
		name, paramValue, found := strings.Cut(param, "=")
		if !found || paramValue == "" {
			continue
		}

		decodedName, err := url.QueryUnescape(name)
		if err != nil {
			decodedName = name
		}

		if isSecret(strings.ToLower(decodedName)) {
			params[i] = name + "=" + CensoredValue
			changed = true
		}
	}

	if !changed {
		return value
	}

	u.RawQuery = strings.Join(params, "&")

	return u.String()
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

// PrometheusConfigToURLs converts metric.prometheus.targets config to a list of targets, and returns some warnings.
func PrometheusConfigToURLs(configTargets []PrometheusTarget) ([]*scrapper.Target, prometheus.MultiError) {
	var warnings prometheus.MultiError

	targets := make([]*scrapper.Target, 0, len(configTargets))

	for _, configTarget := range configTargets {
		targetURL, err := url.Parse(configTarget.URL)
		if err != nil {
			warnings.Append(fmt.Errorf("%w: invalid prometheus target URL: %s", ErrInvalidValue, err))

			continue
		}

		target := &scrapper.Target{
			ExtraLabels: map[string]string{
				types.LabelMetaScrapeJob: configTarget.Name,
				// HostPort could be empty; Registry correctly drops empty label values.
				types.LabelMetaScrapeInstance: scrapper.HostPort(targetURL),
			},
			URL:       targetURL,
			AllowList: configTarget.AllowMetrics,
			DenyList:  configTarget.DenyMetrics,
		}

		targets = append(targets, target)
	}

	return targets, warnings
}
