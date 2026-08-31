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
	"maps"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
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
	errDeprecatedEnv          = errors.New("environment variable is deprecated")
	errSettingsDeprecated     = errors.New("setting is deprecated")
	errWrongMapFormat         = errors.New("could not parse map from string")
	errUnsupportedProvider    = errors.New("provider not supported by config loader")
	errCannotMerge            = errors.New("cannot merge")
	errLegacyFilterNameClash  = errors.New("legacy log.inputs filter shares a metric name with another log.inputs entry")
	errLegacyNetworkNameTaken = errors.New("your config already defines the name the legacy log.opentelemetry.grpc/http migration would synthesize")
	ErrInvalidValue           = errors.New("invalid config value")
	ErrMissconfiguration      = errors.New("config issue")
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
		warnings.Append(err)
	}

	if err := validateNetworkListeners(config); err != nil {
		warnings.Append(err)
	}

	if err := validateContainerExcludeRules(config); err != nil {
		warnings.Append(err)
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

		if key, ok := envToKey[s]; ok {
			return key
		}

		if key, ok := resolveDynamicEnvKey(s); ok {
			return key
		}

		return ""
	}

	return envFunc, &warnings
}

type dynamicEnvVar struct {
	envPrefix    string
	configPrefix string
	suffixes     map[string]string
}

// dynamicEnvVarList entries only get the nil-pruning/merge-priority treatment (via
// dynamicEnvVarConfigKeys, used by loader.go) once their configPrefix key is also listed in default.go's
// mapKeys(), which is what collapses the key's dotted leaves into a single map. Keep both in sync when
// adding an entry; Test_dynamicEnvVarListKeysAreInMapKeys enforces it.
var dynamicEnvVarList = []dynamicEnvVar{ //nolint:gochecknoglobals
	{
		// OpenTelemetry Listener
		envPrefix:    "GLOUTON_OPENTELEMETRY_LISTENERS_",
		configPrefix: "opentelemetry.listeners.",
		suffixes: map[string]string{
			"_PROTOCOLS_GRPC_ENDPOINT": "protocols.grpc.endpoint",
			"_PROTOCOLS_HTTP_ENDPOINT": "protocols.http.endpoint",
		},
	},
	{
		// Thresholds
		envPrefix:    "GLOUTON_THRESHOLDS_",
		configPrefix: "thresholds.",
		suffixes: map[string]string{
			"_LOW_WARNING":   "low_warning",
			"_LOW_CRITICAL":  "low_critical",
			"_HIGH_WARNING":  "high_warning",
			"_HIGH_CRITICAL": "high_critical",
		},
	},
}

// resolveDynamicEnvKey resolves an environment variable of the form
// VARIABLE_PREFIX_<name>_VARIABLE_SUFFIX to its config key,
// e.g. "opentelemetry.listeners.<name>.protocols.grpc.endpoint".
// <name> is recovered by trimming the fixed prefix and suffix, it may contain underscores.
// This only handles opentelemetry.listeners and thresholds for now (add entries to dynamicEnvVarList for more).
func resolveDynamicEnvKey(s string) (string, bool) {
	for _, dynamicVar := range dynamicEnvVarList {
		rest, ok := strings.CutPrefix(s, dynamicVar.envPrefix)
		if !ok {
			continue
		}

		for suffix, subKey := range dynamicVar.suffixes {
			name, ok := strings.CutSuffix(rest, suffix)
			if !ok || name == "" {
				continue
			}

			return dynamicVar.configPrefix + strings.ToLower(name) + "." + subKey, true
		}
	}

	return "", false
}

// dynamicEnvVarConfigKeys returns the set of top-level config keys that dynamicEnvVarList's entries set a
// leaf under (e.g. "opentelemetry.listeners", derived from the listener entry's "opentelemetry.listeners."
// configPrefix). loader.go uses this to know which map-shaped config keys need nil-pruning and
// merge-priority treatment when set from the environment, without hardcoding each key by name -- so a
// future dynamicEnvVarList entry targeting a different config key gets that treatment for free.
func dynamicEnvVarConfigKeys() map[string]bool {
	keys := make(map[string]bool, len(dynamicEnvVarList))

	for _, dynamicVar := range dynamicEnvVarList {
		keys[strings.TrimSuffix(dynamicVar.configPrefix, delimiter)] = true
	}

	return keys
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
// takeNestedMapFromFlatConfig pulls key's whole subtree out of config as one nested map, deleting the
// flat leaves it absorbed. config is migrate()'s k.All(): a *flat* leaf map, so the user's own entries
// under key live in it as dotted leaves ("log.opentelemetry.receivers.myrecv.include"), whether they were
// written in the flat conf.d style or as nested YAML that got flattened on load.
//
// A migration synthesizing an entry must go through this before assigning config[key] back. Leaving the
// parent key sitting next to those leaves makes migrate()'s final confmap load non-deterministic:
// maps.Unflatten walks the map in Go's randomized order, so whichever of the two is applied last replaces
// the other's subtree wholesale -- silently dropping either the user's own receivers or the synthesized
// one, differently from one start to the next.
func takeNestedMapFromFlatConfig(config map[string]any, key string) map[string]any {
	nested, _ := config[key].(map[string]any)
	if nested == nil {
		nested = map[string]any{}
	}

	prefix := key + delimiter

	for flatKey, value := range config {
		relative, found := strings.CutPrefix(flatKey, prefix)
		if !found {
			continue
		}

		delete(config, flatKey)

		parts := strings.Split(relative, delimiter)
		node := nested

		for _, part := range parts[:len(parts)-1] {
			child, _ := node[part].(map[string]any)
			if child == nil {
				child = map[string]any{}
				node[part] = child
			}

			node = child
		}

		node[parts[len(parts)-1]] = value
	}

	return nested
}

func migrate(k *koanf.Koanf, path string, providerType ItemSource) (*koanf.Koanf, prometheus.MultiError) {
	config := k.All()

	warnings := make(prometheus.MultiError, 0, 7)

	warnings = append(warnings, migrateMovedScalarKeys(k, config)...)
	warnings = append(warnings, migrateMovedKeys(k, config)...)
	warnings = append(warnings, migrateLogging(k, config)...)
	warnings = append(warnings, migrateMetricsPrometheus(k, config)...)
	warnings = append(warnings, migrateScrapperMetrics(k, config)...)
	warnings = append(warnings, migrateServices(config)...)
	warnings = append(warnings, warnLegacyNetworkListeners(k, providerType)...)
	warnings = append(warnings, migrateLogInputs(k, config, path)...)
	warnings = append(warnings, migrateRemovedLogKeys(config)...)

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

		// k.Exists, not "value == 0": k.Int returns 0 both when the key is absent and when the user
		// explicitly wrote 0, and treating those the same left an explicit 0 unmigrated (the old key
		// survived to trip the final decode's ErrorUnused check instead of getting a clean deprecation
		// notice).
		if !k.Exists(oldKey) {
			continue
		}

		value := k.Int(oldKey)

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

// mergeLegacyFilters ORs legacy filter regex/exclude into countconnector conditions, coalescing metrics by
// name; returns touched metrics and warnings. inputIndex is the enclosing log.inputs entry's index, used
// only to name the offending filter in a warning.
func mergeLegacyFilters(metricsByName map[string]any, filtersList []any, inputIndex int) ([]string, []error) {
	var (
		touched  []string
		warnings []error
	)

	touchedThisCall := make(map[string]bool)

	for j, filterAny := range filtersList {
		// Every drop below is warned about rather than skipped silently: migrateLogInputs still reports
		// the enclosing entry as successfully migrated and builds it a receiver, so a dropped filter would
		// otherwise leave the user told the migration worked while their metric definition is gone -- and
		// with a receiver that tails the file and persists offsets while neither shipping nor counting.
		filterMap, ok := filterAny.(map[string]any)
		if !ok {
			warnings = append(warnings, fmt.Errorf(
				"%w: log.inputs[%d].filters[%d] is not a valid filter, ignoring it",
				errSettingsDeprecated, inputIndex, j,
			))

			continue
		}

		metric, _ := filterMap["metric"].(string)
		regex, _ := filterMap["regex"].(string)

		if metric == "" || regex == "" {
			warnings = append(warnings, fmt.Errorf(
				"%w: log.inputs[%d].filters[%d] needs both 'metric' and 'regex' set (got metric=%q, regex=%q), ignoring it",
				errSettingsDeprecated, inputIndex, j, metric, regex,
			))

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
					"%w: metric %q, conditions from multiple log.inputs entries are merged into one shared definition, applied to every receiver that touches it",
					errLegacyFilterNameClash, metric,
				))
			}
		}

		conditions, _ := entry["conditions"].([]any)
		entry["conditions"] = append(conditions, condition)
	}

	return touched, warnings
}

// migrateLogInputs folds the legacy log.inputs[].filters entries (the original, Fluent Bit-era log-to-metric source) into the
// equivalent log.opentelemetry.receivers shape, each migrated metric embedded inline in its receiver's own metrics: list
// (not routed through a shared/named log.metrics_rules entry, which could otherwise silently collide with -- and be
// shadowed by -- a hand-written rule of the same auto-derived name). Every migrated metric gets item: "" unconditionally,
// to avoid changing the identity of an already-existing metric series for currently-deployed users.
// providerPath identifies the provider this call is migrating (e.g. a config file path); migrate() runs once per provider
// with the loop index i restarting from 0 each time, so providerPath must be folded into the generated receiver name to
// avoid two files each declaring one log.inputs entry from both producing "legacy_input_0" and overwriting one another.
// Every original entry is consumed one way or another: translated into a receiver, or dropped with a warning (malformed,
// no filters at all, or filters with no path/container_name/container_selectors to attach them to) -- none of them are
// ever written back to log.inputs, so nothing downstream needs to keep reading that key once migration has run.
func migrateLogInputs(k *koanf.Koanf, config map[string]any, providerPath string) prometheus.MultiError {
	var warnings prometheus.MultiError

	inputs, ok := k.Get("log.inputs").([]any)
	if !ok || len(inputs) == 0 {
		return nil
	}

	// Every entry below is either translated into a receiver or dropped with a warning: none of them
	// need to survive as log.inputs afterward.
	delete(config, "log.inputs")

	receivers := takeNestedMapFromFlatConfig(config, "log.opentelemetry.receivers")

	// Shared across every mergeLegacyFilters call below -- see its doc comment.
	metricsByName := map[string]any{}

	// receiverMetrics tracks, per generated receiver name, which metric names it touches. The actual
	// entries are embedded once every log.inputs entry has been processed, so a metric name shared by
	// several log.inputs entries (and therefore several receivers) is fully merged in metricsByName
	// before any receiver gets its copy.
	receiverMetrics := map[string][]string{}

	translated := false

	for i, inputAny := range inputs {
		inputMap, ok := inputAny.(map[string]any)
		if !ok {
			warnings.Append(fmt.Errorf("%w: log.inputs[%d] is not a valid entry, ignoring it", errSettingsDeprecated, i))

			continue
		}

		filtersList, ok := inputMap["filters"].([]any)
		if !ok || len(filtersList) == 0 {
			// No filters: this entry never did anything for log-to-metric even before this migration.
			warnings.Append(fmt.Errorf("%w: log.inputs[%d] has no filters, it never produced a metric, ignoring it", errSettingsDeprecated, i))

			continue
		}

		path, _ := inputMap["path"].(string)
		containerName, _ := inputMap["container_name"].(string)
		selectors, _ := inputMap["container_selectors"].(map[string]any)

		if path == "" && containerName == "" && len(selectors) == 0 {
			warnings.Append(fmt.Errorf("%w: log.inputs[%d] has filters but no path/container_name/container_selectors set, filters were dropped", errSettingsDeprecated, i))

			continue
		}

		touchedMetrics, mergeWarnings := mergeLegacyFilters(metricsByName, filtersList, i)
		for _, w := range mergeWarnings {
			warnings.Append(w)
		}

		// Legacy log.inputs was metrics-only, so the migrated receiver never ships logs either.
		receiver := map[string]any{
			"send_logs": false,
		}

		// path wins outright, as it did before: Fluent Bit's inputLogPaths returned the configured path and
		// nothing else whenever one was set ("The configured path has priority over the container name and
		// selectors"), so a legacy input carrying both only ever counted that file's lines. Writing all
		// three onto one receiver instead would change what the metric counts on upgrade, since the
		// receivers here treat include patterns and container matchers as independent sources feeding the
		// same fan-out -- the container's matching lines would start being counted too and the series would
		// jump. Warned about rather than dropped quietly, since the config keeps saying otherwise.
		switch {
		case path != "":
			receiver["include"] = []any{path}

			if containerName != "" || len(selectors) > 0 {
				warnings.Append(fmt.Errorf(
					"%w: log.inputs[%d] sets path as well as container_name/container_selectors, which never had"+
						" any effect alongside a path -- only %q is migrated; drop path to count the container's"+
						" lines instead",
					errSettingsDeprecated, i, path,
				))
			}
		default:
			if containerName != "" {
				receiver["container_name"] = containerName
			}

			if len(selectors) > 0 {
				receiver["container_selectors"] = selectors
			}
		}

		name := legacyInputReceiverName(providerPath, i)
		receivers[name] = receiver
		receiverMetrics[name] = touchedMetrics

		warnings.Append(fmt.Errorf("%w: log.inputs[%d].filters, use log.opentelemetry.receivers/log.metrics_rules instead", errSettingsDeprecated, i))

		translated = true
	}

	if !translated {
		return warnings
	}

	// Every log.inputs entry has now been folded into metricsByName, so each metric's entry holds its
	// final, fully-merged condition list: embed a copy directly into every receiver that touches it.
	for name, metricNames := range receiverMetrics {
		receiver, _ := receivers[name].(map[string]any)

		metrics := make([]any, 0, len(metricNames))
		for _, metric := range metricNames {
			metrics = append(metrics, cloneMetricEntry(metricsByName[metric]))
		}

		receiver["metrics"] = metrics
	}

	config["log.opentelemetry.receivers"] = receivers

	return warnings
}

// cloneMetricEntry copies a mergeLegacyFilters entry so embedding it into several receivers' own metrics:
// list leaves each with its own map/slice instead of every receiver aliasing (and being able to mutate)
// the exact same one.
func cloneMetricEntry(entryAny any) map[string]any {
	entry, _ := entryAny.(map[string]any)
	clone := make(map[string]any, len(entry))

	for k, v := range entry {
		switch val := v.(type) {
		case []any:
			clone[k] = append([]any(nil), val...)
		case map[string]any:
			clone[k] = maps.Clone(val)
		default:
			clone[k] = v
		}
	}

	return clone
}

// legacyNetworkReceiverNames returns the fixed names synthesizeLegacyNetworkListener uses for its
// receiver (log.opentelemetry.receivers key) and network listener (opentelemetry.listeners key).
// Unlike migrateLogInputs' entries, the legacy log.opentelemetry.grpc/http shape is a single flat
// scalar setting with no name of its own to key on -- the legacy Fluent-Bit-era system only ever had
// one such listener, and two files setting it both merge into that one listener (last file wins per
// field, same as any other scalar setting), not two independent listeners. Fixed names are what let the
// synthesized entries land on that one listener no matter which providers contributed to it.
func legacyNetworkReceiverNames() (receiverKey, listenerKey string) {
	return "legacy_network", "legacy-network"
}

// legacyNetworkListenerBool reads a legacy log.opentelemetry.grpc/http ".enable" leaf straight from koanf,
// bypassing the mapstructure decode (and its stringToBoolHookFunc) that normally tolerates a string-typed
// YAML value here (enable: "true"), so that spelling must be handled explicitly too.
func legacyNetworkListenerBool(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		parsed, err := ParseBool(v)

		return err == nil && parsed
	default:
		return false
	}
}

// legacyNetworkListenerPort reads a legacy log.opentelemetry.grpc/http ".port" leaf, which arrives as
// whatever its provider produced: an int from a YAML scalar, an int64/float64 from a JSON round trip, or
// a string from an environment variable. set is false for an absent, zero or unparseable port, which
// must fall back to the protocol's own default rather than to :0 -- the defaults provider materializes
// this leaf as an explicit 0 on every load (see OpenTelemetry.GRPC), so 0 cannot mean anything else.
func legacyNetworkListenerPort(value any) (port int, set bool) {
	switch v := value.(type) {
	case int:
		port = v
	case int64:
		port = int(v)
	case float64:
		port = int(v)
	case string:
		parsed, err := strconv.Atoi(v)
		if err != nil {
			return 0, false
		}

		port = parsed
	default:
		return 0, false
	}

	return port, port != 0
}

// warnLegacyNetworkListeners warns, once per provider, that this provider still uses the deprecated
// log.opentelemetry.grpc/http {enable, address, port} shape. It deliberately does NOT translate it: the
// keys are real Config fields (see OpenTelemetry.GRPC) so they survive the strict struct decode and merge
// per-leaf across providers, and the actual translation runs once on the merged result, in
// synthesizeLegacyNetworkListener. Only the warning stays per-provider, so it can name the file at fault.
func warnLegacyNetworkListeners(k *koanf.Koanf, providerType ItemSource) prometheus.MultiError {
	var warnings prometheus.MultiError

	const path = "log.opentelemetry"

	// The defaults provider is a structs provider over Config{}, so it materializes all six legacy leaves
	// as explicit zero values on every load (they're real Config fields now -- see OpenTelemetry.GRPC).
	// Only files and environment variables carry what a user actually wrote, so only they can be
	// deprecating anything; without this the warning would fire on every config that never mentions the
	// legacy shape at all.
	if providerType == SourceDefault {
		return nil
	}

	// k.Exists on the full leaf path (not k.Exists(path+".grpc") for the whole submap): koanf only builds
	// an intermediate "grpc"/"http" map node when the source YAML was itself written with real nesting --
	// a flat "log.opentelemetry.grpc.enable: true" key (just as valid, and the more common conf.d style)
	// is stored as one opaque dotted key, so a parent-node check would silently miss that spelling.
	var found bool

	for _, sub := range []string{".grpc", ".http"} {
		for _, leaf := range []string{".enable", ".address", ".port"} {
			if k.Exists(path + sub + leaf) {
				found = true
			}
		}
	}

	if !found {
		return nil
	}

	warnings.Append(fmt.Errorf(
		"%w: %s.grpc/http {enable, address, port}, use opentelemetry.listeners + a log.opentelemetry.receivers entry's from_listener field instead",
		errSettingsDeprecated, path,
	))

	return warnings
}

// synthesizeLegacyNetworkListener folds the deprecated log.opentelemetry.grpc/http {enable, address,
// port} shape into a synthesized "legacy_network" receiver plus an opentelemetry.listeners entry,
// preserving the address/port and the unconditional shipping behavior (send_logs: true).
//
// Runs on the fully merged config, unlike the per-provider migrations in migrate(): the new shape fuses
// address and port into one "host:port" endpoint string, so translating per provider could only ever
// build an endpoint out of the halves a single file happened to set, and a second conf.d file overriding
// just "port" (or setting only "port" via an environment variable) contributed no complete endpoint and
// was silently discarded. Deferring to here lets the three legacy leaves merge as ordinary sibling
// scalars first -- last provider wins per leaf, exactly as pre-deprecation -- and fuses once, afterwards.
//
// config is the merged flat config map: top-level keys are dot-joined, values may be nested maps.
func synthesizeLegacyNetworkListener(config map[string]any) prometheus.MultiError {
	var warnings prometheus.MultiError

	const (
		path            = "log.opentelemetry"
		defaultGRPCPort = 4317
		defaultHTTPPort = 4318
	)

	receiverKey, listenerKey := legacyNetworkReceiverNames()

	endpointOf := func(sub string, defaultPort int) string {
		if !legacyNetworkListenerBool(config[path+"."+sub+".enable"]) {
			return ""
		}

		address, _ := config[path+"."+sub+".address"].(string)
		if address == "" {
			address = DefaultLocalhost
		}

		port := defaultPort
		if p, set := legacyNetworkListenerPort(config[path+"."+sub+".port"]); set {
			port = p
		}

		return net.JoinHostPort(address, strconv.Itoa(port))
	}

	grpcEndpoint := endpointOf("grpc", defaultGRPCPort)
	httpEndpoint := endpointOf("http", defaultHTTPPort)

	// Consumed either way, so the legacy keys never reach the final typed Config: nothing downstream
	// reads OpenTelemetry.GRPC/HTTP, and leaving them set would show phantom settings in diagnostics.
	for key := range config {
		if strings.HasPrefix(key, path+".grpc.") || strings.HasPrefix(key, path+".http.") {
			delete(config, key)
		}
	}

	if grpcEndpoint == "" && httpEndpoint == "" {
		return warnings // both were disabled (or never set): no network participation to migrate
	}

	protocols := map[string]any{}
	if grpcEndpoint != "" {
		protocols["grpc"] = map[string]any{"endpoint": grpcEndpoint}
	}

	if httpEndpoint != "" {
		protocols["http"] = map[string]any{"endpoint": httpEndpoint}
	}

	// Both entries below are only synthesized into a name the user hasn't taken. Assigning over it used to
	// destroy their entry outright and silently: a receiver of that name lost its include patterns and its
	// metrics, so their file stopped being tailed and their metric disappeared. That is reachable on the
	// very migration path the deprecation warning sends people down -- copy the effective legacy listener
	// out, correct its endpoint, forget to delete the old grpc/http keys -- where it silently reverted the
	// correction. Theirs wins instead, and the warning says so.
	//
	// The two names are handled independently: keeping a user's listener while still synthesizing the
	// receiver is exactly what makes that migration flow work (their endpoint, shipped by the legacy shim).
	networkListeners := takeNestedMapFromFlatConfig(config, "opentelemetry.listeners")

	if _, taken := networkListeners[listenerKey]; taken {
		warnings.Append(fmt.Errorf(
			"%w: opentelemetry.listeners.%s -- keeping yours, so %s.grpc/http's address and port are ignored;"+
				" delete those keys once you've checked the endpoint",
			errLegacyNetworkNameTaken, listenerKey, path,
		))
	} else {
		networkListeners[listenerKey] = map[string]any{"protocols": protocols}
	}

	config["opentelemetry.listeners"] = networkListeners

	receivers := takeNestedMapFromFlatConfig(config, "log.opentelemetry.receivers")

	if _, taken := receivers[receiverKey]; taken {
		warnings.Append(fmt.Errorf(
			"%w: log.opentelemetry.receivers.%s -- keeping yours, so it must carry from_listeners: [%s]"+
				" itself for %s.grpc/http to still ship anything",
			errLegacyNetworkNameTaken, receiverKey, listenerKey, path,
		))
	} else {
		receivers[receiverKey] = map[string]any{
			"from_listeners": []any{listenerKey},
			"send_logs":      true,
		}
	}

	config["log.opentelemetry.receivers"] = receivers

	return warnings
}

// removedLogKeys are the pre-OpenTelemetry log settings that no longer exist in any form. Both are set
// together by the conf.d snippet the bleemeo-agent-logs package ships ("bleemeo-agent-logs overrides the
// URL and set an empty host root prefix"), so handling only one of them still leaves that snippet
// reporting a config error on every start.
var removedLogKeys = []string{ //nolint:gochecknoglobals
	"log.fluentbit_url",
	"log.hostroot_prefix",
}

// migrateRemovedLogKeys drops every removedLogKeys entry with a deprecation warning, instead of letting
// it fail the strict struct decode as an unknown key -- which otherwise looks like a config error on
// every upgrade instead of a no-op.
func migrateRemovedLogKeys(config map[string]any) prometheus.MultiError {
	var warnings prometheus.MultiError

	for _, key := range removedLogKeys {
		if _, ok := config[key]; !ok {
			continue
		}

		delete(config, key)

		warnings.Append(fmt.Errorf("%w: %s. This option does not exists anymore and has no effect", errSettingsDeprecated, key))
	}

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
