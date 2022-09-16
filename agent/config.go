// Copyright 2015-2019 Bleemeo
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

package agent

import (
	"errors"
	"fmt"
	"glouton/config"
	"glouton/discovery"
	"glouton/logger"
	"glouton/prometheus/exporter/snmp"
	"glouton/types"
	"glouton/version"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/common/model"
)

var (
	errUpdateFromEnv      = errors.New("update from environment variable is not supported")
	errDeprecatedEnv      = errors.New("environment variable is deprecated")
	errSettingsDeprecated = errors.New("setting is deprecated")
	ErrInvalidValue       = errors.New("invalid config value")
)

// Config is the structured configuration of the agent.
// Currently not all settings are converted (and some still use old config.Get() method).
// New settings should use Config.
type Config struct {
	Services  Services
	SNMP      SNMP
	Container Container
}

type Services []Service

type Service struct {
	ID             string
	Instance       string
	NagiosNRPEName string
	IgnoredPorts   []int
	Interval       time.Duration
	ExtraAttribute map[string]string
}

type SNMP struct {
	Targets     SNMPTargets
	ExporterURL *url.URL
}

type SNMPTargets []SNMPTarget

type SNMPTarget struct {
	Address     string
	InitialName string
}

type Container struct {
	DisabledByDefault bool
	AllowPatternList  []string
	DenyPatternList   []string
	Runtime           ContainerRuntime
}

type ContainerRuntime struct {
	Docker     ContainerRuntimeAddresses
	ContainerD ContainerRuntimeAddresses
}

type ContainerRuntimeAddresses struct {
	Addresses             []string
	DisablePrefixHostRoot bool
}

func (srvs Services) ToDiscoveryMap() map[discovery.NameInstance]discovery.ServiceOverride {
	result := make(map[discovery.NameInstance]discovery.ServiceOverride, len(srvs))

	for _, v := range srvs {
		key := discovery.NameInstance{
			Name:     v.ID,
			Instance: v.Instance,
		}
		result[key] = discovery.ServiceOverride{
			IgnoredPorts:   v.IgnoredPorts,
			Interval:       v.Interval,
			ExtraAttribute: v.ExtraAttribute,
		}
	}

	return result
}

func (srvs Services) ToNRPEMap() map[string]discovery.NameInstance {
	result := make(map[string]discovery.NameInstance)

	for _, v := range srvs {
		if v.NagiosNRPEName == "" {
			continue
		}

		result[v.NagiosNRPEName] = discovery.NameInstance{
			Name:     v.ID,
			Instance: v.Instance,
		}
	}

	return result
}

// Name return a human name of this service.
func (srv Service) Name() string {
	if srv.ID == "" {
		return "unknown service (ID is absent)"
	}

	if srv.Instance == "" {
		return srv.ID
	}

	return fmt.Sprintf("%s on %s", srv.ID, srv.Instance)
}

func (snmps SNMPTargets) ToTargetOptions() []snmp.TargetOptions {
	result := make([]snmp.TargetOptions, 0, len(snmps))

	for _, t := range snmps {
		result = append(result, snmp.TargetOptions{
			Address:     t.Address,
			InitialName: t.InitialName,
		})
	}

	return result
}

func (a ContainerRuntimeAddresses) ExpandAddresses(hostRoot string) []string {
	if a.DisablePrefixHostRoot {
		return a.Addresses
	}

	if hostRoot == "" || hostRoot == "/" {
		return a.Addresses
	}

	result := make([]string, 0, len(a.Addresses)*2)

	for _, path := range a.Addresses {
		result = append(result, path)

		if path == "" {
			// This is a special value that means "use default of the runtime".
			// Prefixing with the hostRoot don't make sense.
			continue
		}

		switch {
		case strings.HasPrefix(path, "unix://"):
			path = strings.TrimPrefix(path, "unix://")
			result = append(result, "unix://"+filepath.Join(hostRoot, path))
		case strings.HasPrefix(path, "/"): // ignore non-absolute path. This will also ignore URL (like http://localhost:3000)
			result = append(result, filepath.Join(hostRoot, path))
		}
	}

	return result
}

func defaultConfig() map[string]interface{} {
	return map[string]interface{}{
		"blackbox.enable":            true,
		"blackbox.scraper_name":      "",
		"blackbox.scraper_send_uuid": true,
		"blackbox.user_agent":        version.UserAgent(),
		"blackbox.targets":           []interface{}{},
		"blackbox.modules": map[string]interface{}{
			"http": map[string]interface{}{
				"prober": "http",
				"http": map[string]interface{}{
					// we default to IPv4 as the ip_protocol_fallback option does not
					// retry a request with a different IP version, but only has an
					// effect when resolving the target
					"preferred_ip_protocol": "ip4",
				},
			},
		},
		"agent.cloudimage_creation_file": "cloudimage_creation",
		"agent.facts_file":               "facts.yaml",
		"agent.http_debug.enable":        false,
		"agent.http_debug.bind_address":  "localhost:6060",
		"agent.installation_format":      "manual",
		"agent.netstat_file":             "netstat.out",
		"agent.process_exporter.enable":  true,
		"agent.public_ip_indicator":      "https://myip.bleemeo.com",
		"agent.state_file":               "state.json",
		"agent.state_cache_file":         "", // by default is based on state_file. It add ".cache" before the extension.
		"agent.state_reset_file":         "state.reset",
		"agent.deprecated_state_file":    "",
		"agent.upgrade_file":             "upgrade",
		"agent.auto_upgrade_file":        "auto_upgrade",
		"agent.metrics_format":           "Bleemeo",
		"agent.node_exporter.enable":     true,
		"agent.node_exporter.collectors": []string{
			"cpu", "diskstats", "filesystem", "loadavg", "meminfo", "netdev",
		},
		"agent.telemetry.enable":                       true,
		"agent.telemetry.address":                      "https://telemetry.bleemeo.com/v1/telemetry/",
		"agent.windows_exporter.enable":                true,
		"agent.windows_exporter.collectors":            []string{"cpu", "cs", "logical_disk", "logon", "memory", "net", "os", "system", "tcp"},
		"bleemeo.account_id":                           "",
		"bleemeo.api_base":                             "https://api.bleemeo.com/",
		"bleemeo.api_ssl_insecure":                     false,
		"bleemeo.container_registration_delay_seconds": 30,
		"bleemeo.enable":                               true,
		"bleemeo.initial_agent_name":                   "",
		"bleemeo.initial_server_group_name":            "",
		"bleemeo.initial_server_group_name_for_snmp":   "",
		"bleemeo.mqtt.cafile":                          "",
		"bleemeo.mqtt.host":                            "mqtt.bleemeo.com",
		"bleemeo.mqtt.port":                            8883,
		"bleemeo.mqtt.ssl_insecure":                    false,
		"bleemeo.mqtt.ssl":                             true,
		"bleemeo.registration_key":                     "",
		"bleemeo.sentry.dsn":                           "https://55b4938036a1488ca0362792a77ac3e2@errors.bleemeo.work/4",
		"config_files": []string{ // This settings could not be overridden by configuration files
			"/etc/glouton/glouton.conf",
			"/etc/glouton/conf.d",
			"etc/glouton.conf",
			"etc/conf.d",
			"C:\\ProgramData\\glouton\\glouton.conf",
			"C:\\ProgramData\\glouton\\conf.d",
		},
		"container.pid_namespace_host":      false,
		"container.type":                    "",
		"container.filter.allow_by_default": true,
		"container.filter.allow_list":       []string{},
		"container.filter.deny_list":        []string{},
		"container.runtime.docker.addresses": []string{
			"",
			"unix:///run/docker.sock",
			"unix:///var/run/docker.sock",
		},
		"container.runtime.docker.prefix_hostroot": true,
		"container.runtime.containerd.addresses": []string{
			"/run/containerd/containerd.sock",
			"/run/k3s/containerd/containerd.sock",
		},
		"container.runtime.containerd.prefix_hostroot": true,
		"df.host_mount_point":                          "",
		"df.ignore_fs_type": []string{
			"^(autofs|binfmt_misc|bpf|cgroup2?|configfs|debugfs|devpts|devtmpfs|fusectl|hugetlbfs|iso9660|mqueue|nsfs|overlay|proc|procfs|pstore|rpc_pipefs|securityfs|selinuxfs|squashfs|sysfs|tracefs|devfs|aufs)$",
			"tmpfs",
			"efivarfs",
			".*gvfs.*",
		},
		"df.path_ignore": []interface{}{
			"/var/lib/docker/aufs",
			"/var/lib/docker/overlay",
			"/var/lib/docker/overlay2",
			"/var/lib/docker/devicemapper",
			"/var/lib/docker/vfs",
			"/var/lib/docker/btrfs",
			"/var/lib/docker/zfs",
			"/var/lib/docker/plugins",
			"/var/lib/docker/containers",
			"/snap",
			"/run/snapd",
			"/run/docker/runtime-runc",
		},
		"disk_ignore": []string{
			"^(ram|loop|fd|(h|s|v|xv)d[a-z]|nvme\\d+n\\d+p)\\d+$",
			"^dm-[0-9]+$",
			// Ignore partition
			"^(hd|sd|vd|xvd|fio|rssd)[a-z][0-9]+$",
			"^(mmcblk|nvme[0-9]n|drbd|rbd|skd|rsxx)[0-9]p[0-9]+$",
		},
		"disk_monitor": []string{
			"^(hd|sd|vd|xvd)[a-z]$",
			"^mmcblk[0-9]$",
			"^nvme[0-9]n[0-9]$",
			"^fio[a-z]$",
			"^drbd[0-9]$",
			"^rbd[0-9]$",
			"^rssd[a-z]$",
			"^skd[0-9]$",
			"^rsxx[0-9]$",
			"^[A-Z]:$",
		},
		"influxdb.db_name":                 "glouton",
		"influxdb.enable":                  false,
		"influxdb.host":                    "localhost",
		"influxdb.port":                    8086,
		"influxdb.tags":                    map[string]string{},
		"jmx.enable":                       true,
		"jmxtrans.config_file":             "/var/lib/jmxtrans/glouton-generated.json",
		"jmxtrans.file_permission":         "0640",
		"jmxtrans.graphite_port":           2004,
		"kubernetes.enable":                false,
		"kubernetes.nodename":              "",
		"kubernetes.clustername":           "",
		"kubernetes.kubeconfig":            "",
		"logging.buffer.head_size_bytes":   500000,
		"logging.buffer.tail_size_bytes":   500000,
		"logging.level":                    "INFO",
		"logging.output":                   "console",
		"logging.filename":                 "",
		"logging.package_levels":           "",
		"metric.prometheus.targets":        []interface{}{},
		"metric.snmp.exporter_address":     "http://localhost:9116",
		"metric.snmp.targets":              []interface{}{},
		"metric.include_default_metrics":   true,
		"metric.allow_metrics":             []interface{}{},
		"metric.deny_metrics":              []interface{}{},
		"metric.softstatus_period_default": 5 * 60,
		"metric.softstatus_period": map[string]interface{}{
			"system_pending_updates":          86400,
			"system_pending_security_updates": 86400,
			"time_elapsed_since_last_data":    0,
			"time_drift":                      0,
		},
		"network_interface_blacklist": []interface{}{
			"docker",
			"lo",
			"veth",
			"virbr",
			"vnet",
			"isatap",
			"fwbr",
			"fwpr",
			"fwln",
		},
		"nrpe.enable":                    false,
		"nrpe.address":                   "0.0.0.0",
		"nrpe.port":                      5666,
		"nrpe.ssl":                       true,
		"nrpe.conf_paths":                []interface{}{"/etc/nagios/nrpe.cfg"},
		"service_ignore_check":           []interface{}{},
		"service_ignore_metrics":         []interface{}{},
		"service":                        []interface{}{},
		"stack":                          "",
		"tags":                           []string{},
		"telegraf.docker_metrics_enable": true,
		"telegraf.statsd.address":        "127.0.0.1",
		"telegraf.statsd.enable":         true,
		"telegraf.statsd.port":           8125,
		"thresholds":                     map[string]interface{}{},
		"web.enable":                     true,
		"web.listener.address":           "127.0.0.1",
		"web.listener.port":              8015,
		"web.local_ui.enable":            true,
		"web.static_cdn_url":             "/static/",
		"zabbix.enable":                  false,
		"zabbix.address":                 "127.0.0.1",
		"zabbix.port":                    10050,
	}
}

func configLoadFile(filePath string, cfg *config.Configuration) error {
	buffer, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	err = cfg.LoadByte(buffer)
	if err != nil {
		logger.Printf("Unable to load %#v: %v", filePath, err)
	}

	return err
}

func loadDefault(cfg *config.Configuration) {
	for key, value := range defaultConfig() {
		if _, ok := cfg.Get(key); !ok {
			cfg.Set(key, value)
		}
	}
}

func migrateScrapperMetrics(cfg *config.Configuration) (warnings []error) {
	warnings = append(warnings, migrateScrapper(cfg, "metric.prometheus.allow_metrics", "metric.allow_metrics")...)
	warnings = append(warnings, migrateScrapper(cfg, "metric.prometheus.deny_metrics", "metric.deny_metrics")...)
	warnings = append(warnings, migrateScrapper(cfg, "metric.prometheus.allow", "metric.allow_metrics")...)
	warnings = append(warnings, migrateScrapper(cfg, "metric.prometheus.deny", "metric.deny_metrics")...)

	return warnings
}

func migrateScrapper(cfg *config.Configuration, deprecatedPath string, correctPath string) (warnings []error) {
	migratedTargets := []string{}
	v, ok := cfg.Get(deprecatedPath)

	if !ok {
		return warnings
	}

	vTab, ok := v.([]interface{})
	if !ok {
		return warnings
	}

	if len(vTab) > 0 {
		warnings = append(warnings, fmt.Errorf("%w: %s. Please use %s", errSettingsDeprecated, deprecatedPath, correctPath))

		for _, val := range vTab {
			s, _ := val.(string)
			if s != "" {
				migratedTargets = append(migratedTargets, s)
			}
		}
	}

	if len(migratedTargets) > 0 {
		existing, _ := cfg.Get(correctPath)
		targets, _ := existing.([]interface{})

		for _, val := range migratedTargets {
			targets = append(targets, val)
		}

		cfg.Set(correctPath, targets)
		cfg.Delete(deprecatedPath)
	}

	return warnings
}

// movedKeys return all keys that are migration. The map is old key => new key.
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

func migrateMovedKeys(cfg *config.Configuration) (warnings []error) {
	keys := movedKeys()

	for oldKey, newKey := range keys {
		val, found := cfg.Get(oldKey)
		if !found {
			continue
		}

		cfg.Set(newKey, val)
		cfg.Delete(oldKey)

		warnings = append(warnings, fmt.Errorf("%w: %s. Please use %s", errSettingsDeprecated, oldKey, newKey))
	}

	return warnings
}

func migrateLogging(cfg *config.Configuration) (warnings []error) {
	for _, name := range []string{"tail_size", "head_size"} {
		oldKey := "logging.buffer." + name
		newKey := "logging.buffer." + name + "_bytes"

		value := cfg.Int(oldKey)
		if value == 0 {
			continue
		}

		cfg.Set(newKey, value*100)
		cfg.Delete(oldKey)

		warnings = append(warnings, fmt.Errorf("%w: %s. Please use %s", errSettingsDeprecated, oldKey, newKey))
	}

	return warnings
}

func migrateMetricsPrometheus(cfg *config.Configuration) (warnings []error) {
	// metrics.prometheus was renamed metrics.prometheus.targets
	// We guess that old path was used when metrics.prometheus.*.url exist and is a string
	v, ok := cfg.Get("metric.prometheus")
	if ok {
		var migratedTargets []interface{}

		if vMap, ok := v.(map[string]interface{}); ok {
			for key, dict := range vMap {
				if tmp, ok := dict.(map[string]interface{}); ok {
					if u, ok := tmp["url"].(string); ok {
						warnings = append(warnings, fmt.Errorf("%w: metrics.prometheus. See https://docs.bleemeo.com/metrics-sources/prometheus", errSettingsDeprecated))

						migratedTargets = append(migratedTargets, map[string]interface{}{
							"url":  u,
							"name": key,
						})

						cfg.Delete(fmt.Sprintf("metric.prometheus.%s", key))
					}
				}
			}
		}

		if len(migratedTargets) > 0 {
			existing, _ := cfg.Get("metric.prometheus.targets")
			targets, _ := existing.([]interface{})
			targets = append(targets, migratedTargets...)

			cfg.Set("metric.prometheus.targets", targets)
		}
	}

	_, found := cfg.Get("metric.prometheus.targets.include_default_metrics")
	if found {
		warnings = append(warnings, fmt.Errorf("%w: metrics.prometheus.targets.include_default_metrics. This option does not exists anymore and has not effects", errSettingsDeprecated))
	}

	return warnings
}

// migrate upgrade the configuration when Glouton change it settings
// The list returned are actually warnings, not errors.
func migrate(cfg *config.Configuration) (warnings []error) {
	warnings = append(warnings, migrateMovedKeys(cfg)...)
	warnings = append(warnings, migrateLogging(cfg)...)
	warnings = append(warnings, migrateMetricsPrometheus(cfg)...)
	warnings = append(warnings, migrateScrapperMetrics(cfg)...)

	return warnings
}

func loadEnvironmentVariables(cfg *config.Configuration) (warnings []error, err error) {
	warnings = make([]error, 0)

	deprecatedEnvNames := map[string]string{
		"BLEEMEO_AGENT_ACCOUNT":          "bleemeo.account_id",
		"BLEEMEO_AGENT_REGISTRATION_KEY": "bleemeo.registration_key",
		"BLEEMEO_AGENT_API_BASE":         "bleemeo.api_base",
		"BLEEMEO_AGENT_MQTT_HOST":        "bleemeo.mqtt.host",
		"BLEEMEO_AGENT_MQTT_PORT":        "bleemeo.mqtt.port",
		"BLEEMEO_AGENT_MQTT_SSL":         "bleemeo.mqtt.ssl",
	}
	for oldEnv, key := range deprecatedEnvNames {
		value := defaultConfig()[key]

		found, err := loadEnvironmentVariable(cfg, key, oldEnv, value)
		if err != nil {
			return nil, err
		}

		if found {
			warnings = append(warnings, fmt.Errorf("%w: %s, use %s instead", errDeprecatedEnv, oldEnv, keyToEnvironmentName(key)))
		}
	}

	for oldKey, newKey := range movedKeys() {
		value := defaultConfig()[newKey]

		if found, err := loadEnvironmentVariable(cfg, newKey, keyToBleemeoEnvironemntName(oldKey), value); err != nil {
			return nil, err
		} else if found {
			warnings = append(warnings, fmt.Errorf("%w: %s, use %s instead", errDeprecatedEnv, keyToBleemeoEnvironemntName(oldKey), keyToEnvironmentName(newKey)))
		}

		if found, err := loadEnvironmentVariable(cfg, newKey, keyToEnvironmentName(oldKey), value); err != nil {
			return nil, err
		} else if found {
			warnings = append(warnings, fmt.Errorf("%w: %s, use %s instead", errDeprecatedEnv, keyToEnvironmentName(oldKey), keyToEnvironmentName(newKey)))
		}
	}

	for key, value := range defaultConfig() {
		if found, err := loadEnvironmentVariable(cfg, key, keyToBleemeoEnvironemntName(key), value); err != nil {
			return nil, err
		} else if found {
			warnings = append(warnings, fmt.Errorf("%w: %s, use %s instead", errDeprecatedEnv, keyToBleemeoEnvironemntName(key), keyToEnvironmentName(key)))
		}

		if _, err := loadEnvironmentVariable(cfg, key, keyToEnvironmentName(key), value); err != nil {
			return nil, err
		}
	}

	return warnings, nil
}

func keyToBleemeoEnvironemntName(key string) string {
	return "BLEEMEO_AGENT_" + strings.ToUpper((strings.ReplaceAll(key, ".", "_")))
}

func keyToEnvironmentName(key string) string {
	return "GLOUTON_" + strings.ToUpper((strings.ReplaceAll(key, ".", "_")))
}

func loadEnvironmentVariable(cfg *config.Configuration, key string, envName string, valueSample interface{}) (found bool, err error) {
	varType := config.TypeUnknown

	switch valueSample.(type) {
	case string:
		varType = config.TypeString
	case []string:
		varType = config.TypeStringList
	case int:
		varType = config.TypeInteger
	case bool:
		varType = config.TypeBoolean
	case map[string]string:
		varType = config.TypeMap
	}

	found, err = cfg.LoadEnv(key, varType, envName)
	if varType == config.TypeUnknown && found {
		return false, fmt.Errorf("%w: env = %s key = %s", errUpdateFromEnv, envName, key)
	}

	if err != nil && varType != config.TypeUnknown {
		return false, fmt.Errorf("bad environment variable %s: %w", envName, err)
	}

	return found, nil
}

func loadConfiguration(configFiles []string, mockLookupEnv func(string) (string, bool)) (cfg Config, oldCfg *config.Configuration, warnings []error, finalError error) {
	oldCfg, warnings, finalError = loadOldConfiguration(configFiles, mockLookupEnv)
	cfg, moreWarnings := convertConfig(oldCfg)

	warnings = append(warnings, moreWarnings...)

	return cfg, oldCfg, warnings, finalError
}

func loadOldConfiguration(configFiles []string, mockLookupEnv func(string) (string, bool)) (cfg *config.Configuration, warnings []error, finalError error) {
	cfg = &config.Configuration{}

	cfg.MockLookupEnv(mockLookupEnv)

	if _, err := loadEnvironmentVariable(cfg, "config_files", keyToEnvironmentName("config_files"), defaultConfig()["config_files"]); err != nil {
		return cfg, nil, err
	}

	if len(configFiles) > 0 && len(configFiles[0]) > 0 {
		cfg.Set("config_files", configFiles)
	}

	if _, ok := cfg.Get("config_files"); !ok {
		cfg.Set("config_files", defaultConfig()["config_files"])
	}

	for _, filename := range cfg.StringList("config_files") {
		stat, err := os.Stat(filename)
		if err != nil && os.IsNotExist(err) {
			logger.V(2).Printf("config file: %s ignored since it does not exists", filename)

			continue
		}

		if err != nil {
			logger.V(2).Printf("config file: %s ignored due to %v", filename, err)

			finalError = err

			continue
		}

		if stat.IsDir() {
			err = cfg.LoadDirectory(filename)

			if err != nil {
				logger.V(2).Printf("config file: directory %s have ignored some files due to %v", filename, err)
			}
		} else {
			err = configLoadFile(filename, cfg)

			if err != nil {
				logger.V(2).Printf("config file: %s ignored due to %v", filename, err)
			}
		}

		if err != nil {
			finalError = err
		} else {
			logger.V(2).Printf("config file: %s loaded", filename)
		}
	}

	moreWarnings, err := loadEnvironmentVariables(cfg)
	if err != nil {
		finalError = err
	}

	warnings = append(warnings, moreWarnings...)
	moreWarnings = migrate(cfg)
	warnings = append(warnings, moreWarnings...)

	loadDefault(cfg)

	for _, warning := range warnings {
		cfg.AddWarning(warning.Error())
	}

	return cfg, warnings, finalError
}

func convertConfig(cfg *config.Configuration) (agentConfig Config, warnings []error) {
	services, _ := cfg.Get("service")
	overrideServices := confFieldToSliceMap(services, "service override")
	agentConfig.Services = make([]Service, 0, len(overrideServices))

	for _, fragment := range overrideServices {
		srv := Service{}

		moreWarning := srv.fromMap(fragment)
		if moreWarning != nil {
			warnings = append(warnings, fmt.Errorf("service %s: %w", srv.Name(), moreWarning))
		}

		agentConfig.Services = append(agentConfig.Services, srv)
	}

	agentConfig.parseContainer(cfg)

	warnings = append(warnings, agentConfig.parseSNMP(cfg)...)
	warnings = append(warnings, agentConfig.validate()...)

	return agentConfig, warnings
}

func (cfg *Config) parseSNMP(oldCfg *config.Configuration) []error {
	u, err := url.Parse(oldCfg.String("metric.snmp.exporter_address"))
	if err != nil {
		return []error{err}
	}

	u, err = u.Parse("snmp")
	if err != nil {
		return []error{err}
	}

	cfg.SNMP.ExporterURL = u

	tmp, ok := oldCfg.Get("metric.snmp.targets")
	if !ok {
		return nil
	}

	confList, ok := tmp.([]interface{})
	if !ok {
		return []error{fmt.Errorf("%w: metric.snmp.targets should be a list", ErrInvalidValue)}
	}

	var errs []error

	seenAddress := make(map[string]bool, len(confList))

	for i, iMap := range confList {
		tmp, ok := iMap.(map[string]interface{})

		if !ok {
			errs = append(errs, fmt.Errorf("%w: metric.snmp.targets[%d] should be a map", ErrInvalidValue, i))

			continue
		}

		target, ok := tmp["target"].(string)
		if !ok {
			errs = append(errs, fmt.Errorf("%w: metric.snmp.targets[%d] must have a target value", ErrInvalidValue, i))

			continue
		}

		if seenAddress[target] {
			errs = append(errs, fmt.Errorf("%w: the SNMP target %s is duplicated", ErrInvalidValue, target))

			continue
		}

		seenAddress[target] = true

		initialName, _ := tmp["initial_name"].(string)

		cfg.SNMP.Targets = append(cfg.SNMP.Targets, SNMPTarget{
			Address:     target,
			InitialName: initialName,
		})
	}

	return errs
}

func (cfg *Config) parseContainer(oldCfg *config.Configuration) {
	enable := oldCfg.Bool("container.filter.allow_by_default")

	cfg.Container.DisabledByDefault = !enable
	cfg.Container.AllowPatternList = oldCfg.StringList("container.filter.allow_list")
	cfg.Container.DenyPatternList = oldCfg.StringList("container.filter.deny_list")

	cfg.Container.Runtime.Docker.Addresses = oldCfg.StringList("container.runtime.docker.addresses")
	cfg.Container.Runtime.Docker.DisablePrefixHostRoot = !oldCfg.Bool("container.runtime.docker.prefix_hostroot")
	cfg.Container.Runtime.ContainerD.Addresses = oldCfg.StringList("container.runtime.containerd.addresses")
	cfg.Container.Runtime.ContainerD.DisablePrefixHostRoot = !oldCfg.Bool("container.runtime.containerd.prefix_hostroot")
}

func (cfg *Config) validate() []error {
	var warnings []error

	replacer := strings.NewReplacer(".", "_", "-", "_")

	i := 0

	for _, srv := range cfg.Services {
		if srv.ID == "" {
			warnings = append(warnings, fmt.Errorf("%w: a key \"id\" is missing in one of your service override", ErrInvalidValue))

			continue
		}

		if !model.IsValidMetricName(model.LabelValue(srv.ID)) {
			newID := replacer.Replace(srv.ID)
			if !model.IsValidMetricName(model.LabelValue(newID)) {
				warnings = append(warnings, fmt.Errorf("%w: service id \"%s\" can only contains letters, digits and underscore", ErrInvalidValue, srv.ID))

				continue
			}

			warnings = append(warnings, fmt.Errorf("%w: service id \"%s\" can not contains dot (.) or dash (-). Changed to \"%s\"", ErrInvalidValue, srv.ID, newID))

			srv.ID = newID
		}

		cfg.Services[i] = srv
		i++
	}

	cfg.Services = cfg.Services[:i]

	return warnings
}

func (srv *Service) fromMap(fragment map[string]string) (warning error) {
	var errs types.MultiErrors

	for k, v := range fragment {
		switch k {
		case "id":
			srv.ID = v
		case "instance":
			srv.Instance = v
		case "nagios_nrpe_name":
			srv.NagiosNRPEName = v
		case "ignore_ports":
			values := strings.Split(v, ",")

			for _, s := range values {
				port, err := strconv.Atoi(strings.TrimSpace(s))
				if err != nil {
					errs = append(errs, fmt.Errorf("ignore_ports \"%s\": %w", s, err))

					continue
				}

				if port > 65535 {
					errs = append(errs, fmt.Errorf("%w: ignore_ports %d is larger than 65535", ErrInvalidValue, port))
				}

				srv.IgnoredPorts = append(srv.IgnoredPorts, port)
			}
		case "interval":
			interval, err := strconv.Atoi(v)
			if err != nil {
				errs = append(errs, fmt.Errorf("interval \"%s\": %w", v, err))

				continue
			}

			srv.Interval = time.Duration(interval) * time.Second
		default:
			if srv.ExtraAttribute == nil {
				srv.ExtraAttribute = make(map[string]string)
			}

			srv.ExtraAttribute[k] = v
		}
	}

	if len(errs) > 0 {
		return errs
	}

	return nil
}

func confFieldToSliceMap(input interface{}, confType string) []map[string]string {
	if input == nil {
		return nil
	}

	inputMap, ok := input.([]interface{})
	if !ok {
		logger.Printf("%s in configuration file is not a list", confType)

		return nil
	}

	result := make([]map[string]string, 0, len(inputMap))

	for i, v := range inputMap {
		vMap, ok := config.ConvertToMap(v)
		if !ok {
			logger.Printf("%s entry #%d is not a map, ignoring, %#v", confType, i, v)

			continue
		}

		override := make(map[string]string, len(vMap))

		for k, v := range vMap {
			override[k] = config.ConvertToString(v)
		}

		result = append(result, override)
	}

	return result
}
