package agent

import (
	"agentgo/config"
	"agentgo/logger"
	"fmt"
	"io/ioutil"
	"os"
	"strings"
)

//nolint:gochecknoglobals
var defaultConfig = map[string]interface{}{
	"agent.cloudimage_creation_file":   "cloudimage_creation",
	"agent.facts_file":                 "facts.yaml",
	"agent.http_debug.enabled":         false,
	"agent.http_debug.bind_address":    "localhost:6060",
	"agent.installation_format":        "manual",
	"agent.netstat_file":               "netstat.out",
	"agent.public_ip_indicator":        "https://myip.bleemeo.com",
	"agent.state_file":                 "state.json",
	"agent.upgrade_file":               "upgrade",
	"bleemeo.account_id":               "",
	"bleemeo.api_base":                 "https://api.bleemeo.com/",
	"bleemeo.api_ssl_insecure":         false,
	"bleemeo.enabled":                  true,
	"bleemeo.initial_agent_name":       "",
	"bleemeo.mqtt.cafile":              "",
	"bleemeo.mqtt.host":                "mqtt.bleemeo.com",
	"bleemeo.mqtt.port":                8883,
	"bleemeo.mqtt.ssl_insecure":        false,
	"bleemeo.mqtt.ssl":                 true,
	"bleemeo.registration_key":         "",
	"bleemeo.sentry.dsn":               "",
	"container.pid_namespace_host":     false,
	"container.type":                   "",
	"df.host_mount_point":              "",
	"df.path_ignore":                   []interface{}{},
	"disk_monitor":                     []interface{}{"sd?", "nvme.*"},
	"distribution":                     "",
	"influxdb.db_name":                 "metrics",
	"influxdb.enabled":                 false,
	"influxdb.host":                    "localhost",
	"influxdb.port":                    8086,
	"jmx.enabled":                      true,
	"jmxtrans.config_file":             "/var/lib/jmxtrans/bleemeo-generated.json",
	"kubernetes.enabled":               false,
	"kubernetes.nodename":              "",
	"logging.level":                    "INFO",
	"logging.output":                   "console",
	"logging.package_levels":           "",
	"metric.prometheus":                map[string]interface{}{},
	"metric.pull":                      map[string]interface{}{},
	"metric.softstatus_period_default": 5 * 60,
	"metric.softstatus_period":         map[string]interface{}{},
	"network_interface_blacklist":      []interface{}{"docker", "lo", "veth", "virbr", "vnet", "isatap"},
	"service_ignore_check":             []interface{}{},
	"service_ignore_metrics":           []interface{}{},
	"service":                          []interface{}{},
	"stack":                            "",
	"tags":                             []string{},
	"telegraf.docker_metrics_enabled":  true,
	"telegraf.statsd.address":          "127.0.0.1",
	"telegraf.statsd.enabled":          true,
	"telegraf.statsd.port":             8125,
	"thresholds":                       map[string]interface{}{},
	"web.enabled":                      true,
	"web.listener.address":             "127.0.0.1",
	"web.listener.port":                8015,
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
		if _, err := cfg.Get(key); err != nil && config.IsNotFound(err) {
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
		if _, err := loadEnvironmentVariable(cfg, key, keyToEnvironemntName(key), value); err != nil {
			return nil, err
		}
	}
	return warnings, nil
}

func keyToEnvironemntName(key string) string {
	return "BLEEMEO_AGENT_" + strings.ToUpper((strings.ReplaceAll(key, ".", "_")))
}

func loadEnvironmentVariable(cfg *config.Configuration, key string, envName string, valueSample interface{}) (found bool, err error) {
	varType := config.TypeUnknown
	switch valueSample.(type) {
	case string:
		varType = config.TypeString
	case int:
		varType = config.TypeInteger
	case bool:
		varType = config.TypeBoolean
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

func (a *agent) loadConfiguration() (cfg *config.Configuration, warnings []error, finalError error) {
	cfg = &config.Configuration{}

	if err := configLoadFile("/etc/bleemeo/agent.conf", cfg); err != nil && !os.IsNotExist(err) {
		finalError = err
	}
	if err := cfg.LoadDirectory("/etc/bleemeo/agent.conf.d"); err != nil && !os.IsNotExist(err) {
		finalError = err
	}
	if err := configLoadFile("etc/agent.conf", cfg); err != nil && !os.IsNotExist(err) {
		finalError = err
	}
	if err := cfg.LoadDirectory("etc/agent.conf.d"); err != nil && !os.IsNotExist(err) {
		finalError = err
	}

	warnings, err := loadEnvironmentVariables(cfg)
	if err != nil {
		finalError = err
	}
	loadDefault(cfg)
	return cfg, warnings, finalError
}
