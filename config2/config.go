package config2

import (
	"fmt"
	"glouton/logger"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/imdario/mergo"
	"github.com/knadh/koanf"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/structs"
)

const (
	envPrefix = "GLOUTON_"
	delimiter = "."
)

// Config is the structured configuration of the agent.
type Config struct {
	Services                  []Service `koanf:"service"`
	Web                       Web       `koanf:"web"`
	NetworkInterfaceBlacklist []string  `koanf:"network_interface_blacklist"`
	DF                        DF        `koanf:"df"`
	Container                 Container `koanf:"container"`
	Metric                    Metric    `koanf:"metric"`
	// TODO: metric
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
	Enable       bool     `koanf:"enable"`
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
	ID       string `koanf:"id"`
	Port     int    `koanf:"port"`
	Address  string `koanf:"address"`
	Interval int    `koanf:"interval"`
	// TODO: Use an enum?
	CheckType      string `koanf:"check_type"`
	HTTPPath       string `koanf:"http_path"`
	HTTPStatusCode int    `koanf:"http_status_code"`
	CheckCommand   string `koanf:"check_command"`
	MatchProcess   string `koanf:"match_process"`
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
func Load(withDefault bool, confPaths ...string) (Config, error) {
	k, err := load(withDefault, confPaths...)
	if err != nil {
		return Config{}, err
	}

	var config Config

	err = k.Unmarshal("", &config)
	if err != nil {
		logger.Printf("Error loading config: %s", err)
	}

	return config, nil
}

// load the configuration from files and directories.
func load(withDefault bool, confPaths ...string) (*koanf.Koanf, error) {
	k := koanf.New(delimiter)

	// TODO: Handle error.
	warnings, _ := loadPaths(k, confPaths)
	for _, warning := range warnings {
		logger.Printf("Error loading config: %s", warning)
	}

	// TODO: Load old env prefix?
	if err := k.Load(env.Provider(envPrefix, delimiter, envToKeyFunc()), nil); err != nil {
		log.Println("env", err)
	}

	mergeFunc := func(src, dest map[string]interface{}) error {
		// Merge without overwriting, defaults are only applied
		// when the setting has not been modified.
		err := mergo.Merge(&dest, src)
		if err != nil {
			logger.Printf("Error merging config: %s", err)
		}

		return err
	}

	if withDefault {
		err := k.Load(structsProvider(defaultConfig(), "koanf"), nil, koanf.WithMergeFunc(mergeFunc))
		if err != nil {
			return nil, err
		}
	}

	return k, nil
}

// envToKeyFunc returns a function that converts an environment variable to a configuration key.
// Panics if two config keys correspond to the same environment variable.
func envToKeyFunc() func(string) string {
	// Get all config keys from an empty config.
	k := koanf.New(delimiter)
	k.Load(structs.Provider(Config{}, "koanf"), nil)
	allKeys := k.All()

	// Build a map of the environment variables with their corresponding config keys.
	envToKey := make(map[string]string, len(allKeys))

	for key := range allKeys {
		envKey := strings.ToUpper(key)
		envKey = envPrefix + strings.ReplaceAll(envKey, ".", "_")

		if oldKey, exists := envToKey[envKey]; exists {
			panic(fmt.Sprintf("Conflict between config keys, %s and %s both corresponds to the variable %s", oldKey, key, envKey))
		}

		envToKey[envKey] = key
	}

	return func(s string) string {
		return envToKey[s]
	}
}

func loadPaths(k *koanf.Koanf, paths []string) (Warnings, error) {
	// TODO
	// Get config files from env.
	// if _, err := loadEnvironmentVariable(cfg, "config_files", keyToEnvironmentName("config_files"), defaultConfig()["config_files"]); err != nil {
	// 	return cfg, nil, err
	// }

	// if len(configFiles) > 0 && len(configFiles[0]) > 0 {
	// 	cfg.Set("config_files", configFiles)
	// }

	// if _, ok := cfg.Get("config_files"); !ok {
	// 	cfg.Set("config_files", defaultConfig()["config_files"])
	// }

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
			err, warning := loadDirectory(k, path)
			if err != nil {
				warnings = append(warnings, warning)
			}

			if err != nil {
				logger.V(2).Printf("config file: directory %s have ignored some files due to %v", path, err)
			}
		} else {
			warning := loadFile(k, path)
			if err != nil {
				warnings = append(warnings, warning)
			}
		}

		if err != nil {
			finalError = err
		} else {
			logger.V(2).Printf("config file: %s loaded", path)
		}
	}

	// TODO: warnings
	// for _, warning := range warnings {
	// 	cfg.AddWarning(warning.Error())
	// }

	return warnings, finalError
}

func loadDirectory(k *koanf.Koanf, dirPath string) (Warnings, error) {
	files, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, err
	}

	var warn Warnings

	for _, f := range files {
		if !strings.HasSuffix(f.Name(), ".conf") {
			continue
		}

		path := filepath.Join(dirPath, f.Name())

		warning := loadFile(k, path)
		warn = append(warn, warning)
	}

	return warn, nil
}

func loadFile(k *koanf.Koanf, path string) error {
	mergeFunc := func(src, dest map[string]interface{}) error {
		err := mergo.Merge(&dest, src, mergo.WithOverride, mergo.WithAppendSlice)
		if err != nil {
			logger.Printf("Error merging config: %s", err)
		}

		return err
	}

	err := k.Load(file.Provider(path), yaml.Parser(), koanf.WithMergeFunc(mergeFunc))
	if err != nil {
		return err
	}

	return nil
}
