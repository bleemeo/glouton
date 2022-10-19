package config2

import (
	"errors"
	"fmt"
	"glouton/logger"
	"os"
	"path/filepath"
	"strings"

	"github.com/imdario/mergo"
	"github.com/knadh/koanf"
	yamlParser "github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/structs"
	"github.com/mitchellh/mapstructure"
	"gopkg.in/yaml.v3"
)

const (
	envPrefix           = "GLOUTON_"
	deprecatedEnvPrefix = "BLEEMEO_AGENT_"
	delimiter           = "."
)

var errDeprecatedEnv = errors.New("environment variable is deprecated")

//nolint:gochecknoglobals
var defaultConfigFiles = []string{
	"/etc/glouton/glouton.conf",
	"/etc/glouton/conf.d",
	"etc/glouton.conf",
	"etc/conf.d",
	"C:\\ProgramData\\glouton\\glouton.conf",
	"C:\\ProgramData\\glouton\\conf.d",
}

// Config is the structured configuration of the agent.
type Config struct {
	Services                  []Service `koanf:"service"`
	Web                       Web       `koanf:"web"`
	NetworkInterfaceBlacklist []string  `koanf:"network_interface_blacklist"`
	DF                        DF        `koanf:"df"`
	Container                 Container `koanf:"container"`
	Metric                    Metric    `koanf:"metric"`
}

type Metric struct {
	AllowMetrics            []string       `koanf:"allow_metrics"`
	DenyMetrics             []string       `koanf:"deny_metrics"`
	IncludeDefaultMetrics   bool           `koanf:"include_default_metrics"`
	Prometheus              Prometheus     `koanf:"prometheus"`
	SoftStatusPeriodDefault int            `koanf:"softstatus_period_default"`
	SoftStatusPeriod        map[string]int `koanf:"softstatus_period"`
	SNMP                    SNMP           `koanf:"snmp"`
}

type SNMP struct {
	// TODO: Not documented.
	ExporterAddress string       `koanf:"exporter_address"`
	Targets         []SNMPTarget `koanf:"targets"`
}

type SNMPTarget struct {
	InitialName string `koanf:"initial_name"`
	Target      string `koanf:"target"`
}

type Prometheus struct {
	Targets []PrometheusTarget `koanf:"targets"`
}

type PrometheusTarget struct {
	URL  string `koanf:"url"`
	Name string `koanf:"name"`
}

type DF struct {
	HostMountPoint string   `koanf:"host_mount_point"`
	PathIgnore     []string `koanf:"path_ignore"`
	IgnoreFSType   []string `koanf:"ignore_fs_type"`
}

type Web struct {
	Enable bool `koanf:"enable"`
	// TODO: Not documented.
	LocalUI      LocalUI  `koanf:"local_ui"`
	Listener     Listener `koanf:"listener"`
	StaticCDNURL string   `koanf:"static_cdn_url"`
}

type LocalUI struct {
	Enable bool `koanf:"enable"`
}

type Listener struct {
	Address string `koanf:"address"`
	Port    int    `koanf:"port"`
}

type Service struct {
	// The name of the service.
	ID string `koanf:"id"`
	// Instance of the service, used to differentiate between services with the same ID.
	Instance string `koanf:"instance"`
	// The port the service is running on.
	Port int `koanf:"port"`
	// Ports that should be ignored.
	IgnorePorts []int `koanf:"ignore_ports"`
	// The address of the service.
	Address string `koanf:"address"`
	// The delay between two consecutive checks in seconds.
	Interval int `koanf:"interval"`
	// Check type used for custom checks.
	CheckType string `koanf:"check_type"`
	// The path used for HTTP checks.
	HTTPPath string `koanf:"http_path"`
	// The expected status code for HTTP checks.
	HTTPStatusCode int `koanf:"http_status_code"`
	// Host header sent with HTTP checks.
	HTTPHost string `koanf:"http_host"`
	// Regex to match in a process check.
	MatchProcess string `koanf:"match_process"`
	// Command used for a Nagios check.
	CheckCommand string `koanf:"check_command"`
	// TODO: Not documented.
	NagiosNRPEName string `koanf:"nagios_nrpe_name"`
	// Unix socket to connect and gather metric from MySQL.
	MetricsUnixSocket string `koanf:"metrics_unix_socket"`
	// Credentials for services that require authentication.
	Username string `koanf:"username"`
	Password string `koanf:"password"`
	// HAProxy and PHP-FMP stats URL.
	StatsURL string `koanf:"stats_url"`
	// Port of RabbitMQ management interface.
	ManagementPort int `koanf:"mgmt_port"`
	// Detailed monitoring of specific Cassandra tables.
	CassandraDetailedTables []string `koanf:"cassandra_detailed_tables"`
	// JMX services.
	JMXPort     int         `koanf:"jmx_port"`
	JMXUsername string      `koanf:"jmx_username"`
	JMXPassword string      `koanf:"jmx_password"`
	JMXMetrics  []JmxMetric `koanf:"jmx_metrics"`
}

type JmxMetric struct {
	Name      string  `koanf:"name"`
	MBean     string  `koanf:"mbean"`
	Attribute string  `koanf:"attribute"`
	Path      string  `koanf:"path"`
	Scale     float64 `koanf:"scale"`
	Derive    bool    `koanf:"derive"`
	// TODO: Not documented.
	Sum       bool     `koanf:"sum"`
	TypeNames []string `koanf:"type_names"`
	Ratio     string   `koanf:"ratio"`
}

type Container struct {
	Filter           Filter           `koanf:"filter"`
	Type             string           `koanf:"type"`
	PIDNamespaceHost bool             `koanf:"pid_namespace_host"`
	Runtime          ContainerRuntime `koanf:"runtime"`
}

type Filter struct {
	AllowByDefault bool     `koanf:"allow_by_default"`
	AllowList      []string `koanf:"allow_list"`
	DenyList       []string `koanf:"deny_list"`
}

type ContainerRuntime struct {
	Docker     ContainerRuntimeAddresses `koanf:"docker"`
	ContainerD ContainerRuntimeAddresses `koanf:"containerd"`
}

type ContainerRuntimeAddresses struct {
	Addresses      []string `koanf:"addresses"`
	PrefixHostRoot bool     `koanf:"prefix_hostroot"`
}

type Warnings []error

// Load loads the configuration from files and directories to a struct.
func Load(withDefault bool, paths ...string) (Config, Warnings, error) {
	// Add config envFiles from env.
	envFiles := os.Getenv("GLOUTON_CONFIG_FILES")

	if len(paths) == 0 && envFiles != "" {
		paths = strings.Split(envFiles, ",")
	}

	// If no config was given with flags or env variables, fallback on the default files.
	if len(paths) == 0 || len(paths) == 1 && paths[0] == "" {
		paths = defaultConfigFiles
	}

	k, warnings, err := load(withDefault, paths...)

	var config Config

	if warning := k.Unmarshal("", &config); warning != nil {
		warnings = append(warnings, warning)
	}

	return config, unwrapErrors(warnings), err
}

// load the configuration from files and directories.
func load(withDefault bool, paths ...string) (*koanf.Koanf, Warnings, error) {
	k := koanf.New(delimiter)
	warnings, finalErr := loadPaths(k, paths)

	// The warnings are filled only after k.Load is called.
	envToKey, envWarnings := envToKeyFunc()

	// Load config from environment variables.
	if err := k.Load(env.Provider(deprecatedEnvPrefix, delimiter, envToKey), nil); err != nil {
		warnings = append(warnings, err)
	}

	if err := k.Load(env.Provider(envPrefix, delimiter, envToKey), nil); err != nil {
		warnings = append(warnings, err)
	}

	if len(*envWarnings) > 0 {
		warnings = append(warnings, *envWarnings...)
	}

	if withDefault {
		mergeFunc := func(src, dest map[string]interface{}) error {
			// Merge without overwriting, defaults are only applied
			// when the setting has not been modified.
			err := mergo.Merge(&dest, src)
			if err != nil {
				logger.Printf("Error merging config: %s", err)
			}

			return err
		}

		err := k.Load(structsProvider(defaultConfig(), "koanf"), nil, koanf.WithMergeFunc(mergeFunc))
		if err != nil {
			finalErr = err
		}
	}

	return k, warnings, finalErr
}

// envToKeyFunc returns a function that converts an environment variable to a configuration key
// and a pointer to Warnings, the warnings are filled only after koanf.Load has been called.
// Panics if two config keys correspond to the same environment variable.
func envToKeyFunc() (func(string) string, *Warnings) {
	// Get all config keys from an empty config.
	k := koanf.New(delimiter)
	k.Load(structs.Provider(Config{}, "koanf"), nil)
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
	movedEnvKeys := make(map[string]string, len(movedKeys()))
	for k, v := range movedKeys() {
		movedEnvKeys[toEnvKey(k)] = toEnvKey(v)
	}

	warnings := make(Warnings, 0)
	envFunc := func(s string) string {
		// Migrate environment variables with the deprecated "BLEEMEO_AGENT_" prefix.
		if strings.HasPrefix(s, deprecatedEnvPrefix) {
			deprecated := s
			s = strings.Replace(deprecated, deprecatedEnvPrefix, envPrefix, 1)

			warnings = append(warnings, fmt.Errorf("%w: %s, use %s instead", errDeprecatedEnv, deprecated, s))
		}

		// Migrate other deprecated keys.
		if newKey, ok := movedEnvKeys[s]; ok {
			warnings = append(warnings, fmt.Errorf("%w: %s, use %s instead", errDeprecatedEnv, s, newKey))
			s = newKey
		}

		return envToKey[s]
	}

	return envFunc, &warnings
}

// toEnvKey returns the environment variable corresponding to a configuration key.
// For instance: toEnvKey("web.enable") -> GLOUTON_WEB_ENABLE
func toEnvKey(key string) string {
	envKey := strings.ToUpper(key)
	envKey = envPrefix + strings.ReplaceAll(envKey, ".", "_")

	return envKey
}

func loadPaths(k *koanf.Koanf, paths []string) (Warnings, error) {
	var (
		finalError error
		warnings   Warnings
	)

	for _, path := range paths {
		stat, err := os.Stat(path)
		if err != nil && os.IsNotExist(err) {
			logger.V(2).Printf("config file: %s ignored since it does not exists", path)

			continue
		}

		if err != nil {
			logger.V(2).Printf("config file: %s ignored due to %v", path, err)

			finalError = err

			continue
		}

		if stat.IsDir() {
			warning, err := loadDirectory(k, path)
			if err != nil {
				finalError = err
			}

			if warning != nil {
				warnings = append(warnings, warning)
			}

			if err != nil {
				logger.V(2).Printf("config file: directory %s have ignored some files due to %v", path, err)
			}
		} else {
			warning := loadFile(k, path)
			if warning != nil {
				warnings = append(warnings, warning)
			}
		}

		if err == nil {
			logger.V(2).Printf("config file: %s loaded", path)
		}
	}

	return warnings, finalError
}

func loadDirectory(k *koanf.Koanf, dirPath string) (warning error, err error) {
	files, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, err
	}

	for _, f := range files {
		if !strings.HasSuffix(f.Name(), ".conf") {
			continue
		}

		path := filepath.Join(dirPath, f.Name())
		warning = loadFile(k, path)
	}

	return warning, nil
}

func loadFile(k *koanf.Koanf, path string) error {
	mergeFunc := func(src, dest map[string]interface{}) error {
		err := mergo.Merge(&dest, src, mergo.WithOverride, mergo.WithAppendSlice)
		if err != nil {
			logger.Printf("Error merging config: %s", err)
		}

		return err
	}

	err := k.Load(file.Provider(path), yamlParser.Parser(), koanf.WithMergeFunc(mergeFunc))
	if err != nil {
		return err
	}

	return nil
}

// unwrapErrors unwrap all errors in the list than contain multiple errors.
func unwrapErrors(errs []error) []error {
	if len(errs) == 0 {
		return nil
	}

	unwrapped := make([]error, 0, len(errs))

	for _, err := range errs {
		var (
			mapErr  *mapstructure.Error
			yamlErr *yaml.TypeError
		)

		switch {
		case errors.As(err, &mapErr):
			for _, wrappedErr := range mapErr.WrappedErrors() {
				unwrapped = append(unwrapped, wrappedErr)
			}
		case errors.As(err, &yamlErr):
			for _, wrappedErr := range yamlErr.Errors {
				unwrapped = append(unwrapped, errors.New(wrappedErr))
			}
		default:
			unwrapped = append(unwrapped, err)
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
