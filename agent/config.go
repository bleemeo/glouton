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
	"fmt"
	"glouton/config"
	"glouton/logger"
	"io/ioutil"
	"os"
	"strconv"
	"strings"
	"time"
)

//nolint:gochecknoglobals
var defaultConfig = map[string]interface{}{
	"agent.cloudimage_creation_file": "cloudimage_creation",
	"agent.facts_file":               "facts.yaml",
	"agent.http_debug.enabled":       false,
	"agent.http_debug.bind_address":  "localhost:6060",
	"agent.installation_format":      "manual",
	"agent.netstat_file":             "netstat.out",
	"agent.public_ip_indicator":      "https://myip.bleemeo.com",
	"agent.state_file":               "state.json",
	"agent.upgrade_file":             "upgrade",
	"bleemeo.account_id":             "",
	"bleemeo.api_base":               "https://api.bleemeo.com/",
	"bleemeo.api_ssl_insecure":       false,
	"bleemeo.enabled":                true,
	"bleemeo.initial_agent_name":     "",
	"bleemeo.mqtt.cafile":            "",
	"bleemeo.mqtt.host":              "mqtt.bleemeo.com",
	"bleemeo.mqtt.port":              8883,
	"bleemeo.mqtt.ssl_insecure":      false,
	"bleemeo.mqtt.ssl":               true,
	"bleemeo.registration_key":       "",
	"bleemeo.sentry.dsn":             "",
	"config_files": []string{ // This settings could not be overridden by configuration files
		"/etc/glouton/glouton.conf",
		"/etc/glouton/conf.d",
		"etc/glouton.conf",
		"etc/conf.d",
	},
	"container.pid_namespace_host": false,
	"container.type":               "",
	"df.host_mount_point":          "",
	"df.path_ignore": []interface{}{
		"/var/lib/docker/aufs",
		"/var/lib/docker/overlay",
		"/var/lib/docker/overlay2",
		"/var/lib/docker/devicemapper",
		"/var/lib/docker/vfs",
		"/var/lib/docker/btrfs",
		"/var/lib/docker/zfs",
		"/var/lib/docker/plugins",
		"/snap",
	},
	"disk_monitor": []interface{}{
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
	"influxdb.enabled":                 false,
	"influxdb.host":                    "localhost",
	"influxdb.port":                    8086,
	"influxdb.tags":                    map[string]string{},
	"jmx.enabled":                      true, // TODO: JMX metric gathering
	"jmxtrans.config_file":             "/var/lib/jmxtrans/glouton-generated.json",
	"kubernetes.enabled":               false, // TODO: add support for k8s
	"kubernetes.nodename":              "",
	"logging.level":                    "INFO",
	"logging.output":                   "console",
	"logging.package_levels":           "",
	"metric.prometheus":                map[string]interface{}{},
	"metric.softstatus_period_default": 5 * 60,
	"metric.softstatus_period": map[string]interface{}{
		"system_pending_updates":          86400,
		"system_pending_security_updates": 86400,
		"time_elapsed_since_last_data":    0,
	},
	"network_interface_blacklist":     []interface{}{"docker", "lo", "veth", "virbr", "vnet", "isatap"},
	"nrpe.enabled":                    false,
	"nrpe.address":                    "0.0.0.0",
	"nrpe.port":                       5666,
	"nrpe.ssl":                        true,
	"nrpe.conf_paths":                 []interface{}{"/etc/nagios/nrpe.cfg"},
	"service_ignore_check":            []interface{}{},
	"service_ignore_metrics":          []interface{}{},
	"service":                         []interface{}{},
	"stack":                           "",
	"tags":                            []string{},
	"telegraf.docker_metrics_enabled": true,
	"telegraf.statsd.address":         "127.0.0.1",
	"telegraf.statsd.enabled":         true,
	"telegraf.statsd.port":            8125,
	"thresholds":                      map[string]interface{}{},
	"web.enabled":                     true,
	"web.listener.address":            "127.0.0.1",
	"web.listener.port":               8015,
	"zabbix.enabled":                  false,
	"zabbix.address":                  "127.0.0.1",
	"zabbix.port":                     10050,
}

func configLoadFile(filePath string, cfg *config.Configuration) error {
	buffer, err := ioutil.ReadFile(filePath)
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
	for key, value := range defaultConfig {
		if _, ok := cfg.Get(key); !ok {
			cfg.Set(key, value)
		}
	}
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
		value := defaultConfig[key]

		found, err := loadEnvironmentVariable(cfg, key, oldEnv, value)
		if err != nil {
			return nil, err
		}

		if found {
			warnings = append(warnings, fmt.Errorf("environement variable %#v is deprecated, use %#v instead", oldEnv, keyToEnvironemntName(key)))
		}
	}

	for key, value := range defaultConfig {
		if found, err := loadEnvironmentVariable(cfg, key, keyToBleemeoEnvironemntName(key), value); err != nil {
			return nil, err
		} else if found {
			warnings = append(warnings, fmt.Errorf("environement variable %#v is deprecated, use %#v instead", keyToBleemeoEnvironemntName(key), keyToEnvironemntName(key)))
		}

		if _, err := loadEnvironmentVariable(cfg, key, keyToEnvironemntName(key), value); err != nil {
			return nil, err
		}
	}

	return warnings, nil
}

func keyToBleemeoEnvironemntName(key string) string {
	return "BLEEMEO_AGENT_" + strings.ToUpper((strings.ReplaceAll(key, ".", "_")))
}

func keyToEnvironemntName(key string) string {
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
		return false, fmt.Errorf("update %#v from environment variable %#v is not supported", key, envName)
	}

	if err != nil && varType != config.TypeUnknown {
		return false, fmt.Errorf("bad environ variable %v: %v", envName, err)
	}

	return found, nil
}

func (a *agent) loadConfiguration(configFiles []string) (cfg *config.Configuration, warnings []error, finalError error) {
	cfg = &config.Configuration{}

	if _, err := loadEnvironmentVariable(cfg, "config_files", keyToEnvironemntName("config_files"), defaultConfig["config_files"]); err != nil {
		return cfg, nil, err
	}

	if len(configFiles) > 0 && len(configFiles[0]) > 0 {
		cfg.Set("config_files", configFiles)
	}

	if _, ok := cfg.Get("config_files"); !ok {
		cfg.Set("config_files", defaultConfig["config_files"])
	}

	for _, filename := range cfg.StringList("config_files") {
		stat, err := os.Stat(filename)
		if err != nil && os.IsNotExist(err) {
			continue
		}

		if err != nil {
			finalError = err
			continue
		}

		if stat.IsDir() {
			err = cfg.LoadDirectory(filename)
		} else {
			err = configLoadFile(filename, cfg)
		}

		if err != nil {
			finalError = err
		}
	}

	moreMarnings, err := loadEnvironmentVariables(cfg)
	if err != nil {
		finalError = err
	}

	loadDefault(cfg)

	return cfg, append(warnings, moreMarnings...), finalError
}

func convertToMap(input interface{}) (result map[string]interface{}, ok bool) {
	result, ok = input.(map[string]interface{})
	if ok {
		return
	}

	tmp, ok := input.(map[interface{}]interface{})
	if !ok {
		return nil, false
	}

	result = make(map[string]interface{}, len(tmp))

	for k, v := range tmp {
		result[convertToString(k)] = v
	}

	return result, true
}

func convertToString(rawValue interface{}) string {
	switch value := rawValue.(type) {
	case string:
		return value
	case fmt.Stringer:
		return value.String()
	case int:
		return strconv.FormatInt(int64(value), 10)
	default:
		return fmt.Sprintf("%v", rawValue)
	}
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
		vMap, ok := convertToMap(v)
		if !ok {
			logger.Printf("%s entry #%d is not a map, ignoring, %#v", confType, i, v)
			continue
		}

		override := make(map[string]string, len(vMap))

		for k, v := range vMap {
			override[k] = convertToString(v)
		}

		result = append(result, override)
	}

	return result
}

func softPeriodsFromInterface(input interface{}) map[string]time.Duration {
	if input == nil {
		return nil
	}

	inputMap, ok := convertToMap(input)
	if !ok {
		logger.Printf("softstatus period in configuration file is not a map")
		return nil
	}

	result := make(map[string]time.Duration, len(inputMap))

	for k, rawValue := range inputMap {
		var duration time.Duration
		switch value := rawValue.(type) {
		case int:
			duration = time.Duration(value) * time.Second
		case float64:
			duration = time.Duration(int(value/1000)) * time.Millisecond
		case string:
			var err error

			duration, err = time.ParseDuration(value)
			if err != nil {
				continue
			}
		default:
			continue
		}

		result[k] = duration
	}

	return result
}
