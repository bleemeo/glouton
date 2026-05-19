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
	"net/url"
	"testing"
	"time"

	"dario.cat/mergo"
	"github.com/bleemeo/glouton/prometheus/scrapper"
	"github.com/bleemeo/glouton/types"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/knadh/koanf/providers/structs"
	"github.com/knadh/koanf/v2"
	bbConf "github.com/prometheus/blackbox_exporter/config"
	"github.com/prometheus/common/config"
)

// Test constants shared across config_test.go, loader_test.go and default_test.go.
const (
	testCPU                     = "cpu"
	testMymodule                = "mymodule"
	testRedis                   = "redis"
	testTmpfs                   = "tmpfs"
	testSda                     = "sda"
	testERROR                   = "ERROR"
	testMinLevelInfo            = "min_level_info"
	testService1                = "service1"
	testNagios                  = "nagios"
	testPostgresql              = "postgresql"
	testCPUUsed                 = "cpu_used"
	testEth0                    = "eth0"
	testOldPromTargetsConf      = "testdata/old-prometheus-targets.conf"
	testTest1                   = "test1"
	testLocalhostMetricsURL     = "http://localhost:9090/metrics"
	testSimplePath              = "/simple"
	testCassandra               = "cassandra"
	testApache                  = "apache"
	testMySQL                   = "mysql"
	testGloutonCloudimageCreate = "/var/lib/glouton/cloudimage_creation"
	testGloutonFactsYaml        = "/var/lib/glouton/facts.yaml"
	testGloutonNetstatOut       = "/var/lib/glouton/netstat.out"
	testGloutonStateDir         = "/var/lib/glouton"
	testGloutonStateJSON        = "/var/lib/glouton/state.json"
	testGloutonStateCacheJSON   = "/var/lib/glouton/state.cache.json"
	testGloutonStateReset       = "/var/lib/glouton/state.reset"
	testGloutonUpgrade          = "/var/lib/glouton/upgrade"
	testGloutonAutoUpgrade      = "/var/lib/glouton/auto_upgrade"
	testInDump                  = "in-dump"
	testNotInDump               = "not-in-dump"
	testNew                     = "new"
	testOld                     = "old"
	testLocalhostPort           = "localhost:9090"
	testOldPort                 = "old:9090"
	testNginx                   = "nginx"
	testInstance                = "instance"
	testRegex                   = "regex"
)

func compareConfig(expected, got Config, opts ...cmp.Option) string {
	ignoreUnexported := cmpopts.IgnoreUnexported(bbConf.Module{}.HTTP.HTTPClientConfig.ProxyConfig)
	opts = append(opts, ignoreUnexported)

	return cmp.Diff(expected, got, opts...)
}

// TestStructuredConfig tests loading the full configuration file.
func TestStructuredConfig(t *testing.T) { //nolint:maintidx
	expectedConfig := Config{
		Agent: Agent{
			CloudImageCreationFile: "cloudimage_creation",
			FactsFile:              DefaultFactsFile,
			InstallationFormat:     DefaultInstallFormat,
			NetstatFile:            "netstat.out",
			StateDirectory:         ".",
			StateFile:              "state.json",
			StateCacheFile:         DefaultStateCacheFile,
			StateResetFile:         "state.reset",
			DeprecatedStateFile:    "state.deprecated",
			EnableCrashReporting:   true,
			MaxCrashReportsCount:   2,
			UpgradeFile:            "upgrade",
			AutoUpgradeFile:        "auto-upgrade",
			NodeExporter: NodeExporter{
				Enable:     true,
				Collectors: []string{"disk"},
			},
			ProcessExporter: ProcessExporter{
				Enable: true,
			},
			PublicIPIndicator: "https://myip.bleemeo.com",
			WindowsExporter: NodeExporter{
				Enable:     true,
				Collectors: []string{testCPU},
			},
			Telemetry: Telemetry{
				Enable:  true,
				Address: "http://example.com",
			},
		},
		Blackbox: Blackbox{
			Enable:          true,
			ScraperName:     keyName,
			ScraperSendUUID: true,
			Targets: []BlackboxTarget{
				{
					Name:   "myname",
					URL:    "https://bleemeo.com",
					Module: testMymodule,
				},
			},
			Modules: map[string]bbConf.Module{
				testMymodule: {
					Prober:  defaultHTTP,
					Timeout: 5 * time.Second,
					HTTP: bbConf.HTTPProbe{
						IPProtocol:       "ip4",
						ValidStatusCodes: []int{200},
						FailIfSSL:        true,
						// Default values assigned by blackbox YAML unmarshaller.
						IPProtocolFallback: true,
						HTTPClientConfig:   config.DefaultHTTPClientConfig,
					},
					TCP:  bbConf.DefaultTCPProbe,
					ICMP: bbConf.DefaultICMPProbe,
					DNS:  bbConf.DefaultModule.DNS,
					GRPC: bbConf.DefaultModule.GRPC,
				},
			},
			UserAgent: "my-user-agent",
		},
		Bleemeo: Bleemeo{
			AccountID: "myid",
			APIBase:   "https://api.bleemeo.com",
			Cache: BleemeoCache{
				DeactivatedMetricsExpirationDays: 200,
			},
			APISSLInsecure:                    true,
			ContainerRegistrationDelaySeconds: 30,
			Enable:                            true,
			InitialAgentName:                  "name1",
			InitialServerGroupName:            "name2",
			InitialServerGroupNameForSNMP:     "name3",
			InitialServerGroupNameForVSphere:  "name4",
			MQTT: BleemeoMQTT{
				CAFile:      "/myca",
				Host:        "mqtt.bleemeo.com",
				Port:        8883,
				SSLInsecure: true,
				SSL:         true,
			},
			RegistrationKey: "mykey",
			Sentry: Sentry{
				DSN: "my-dsn",
			},
		},
		Container: Container{
			Filter: ContainerFilter{
				AllowByDefault: true,
				AllowList:      []string{testRedis},
				DenyList:       []string{"postgres"},
			},
			Type:             "docker",
			PIDNamespaceHost: true,
			Runtime: ContainerRuntime{
				Docker: ContainerRuntimeAddresses{
					Addresses:      []string{"unix:///run/docker.sock"},
					PrefixHostRoot: true,
				},
				ContainerD: ContainerRuntimeAddresses{
					Addresses:      []string{"/run/containerd/containerd.sock"},
					PrefixHostRoot: true,
				},
			},
		},
		DF: DF{
			HostMountPoint: "/host-root",
			PathIgnore:     []string{"/"},
			IgnoreFSType:   []string{testTmpfs},
		},
		DiskIgnore:  []string{"^(ram|loop|fd|(h|s|v|xv)d[a-z]|nvme\\d+n\\d+p)\\d+$"},
		DiskMonitor: []string{testSda},
		JMX: JMX{
			Enable: true,
		},
		JMXTrans: JMXTrans{
			ConfigFile:     "/var/lib/jmxtrans/glouton-generated.json",
			FilePermission: "0640",
			GraphitePort:   2004,
		},
		Kubernetes: Kubernetes{
			Enable:              true,
			AllowClusterMetrics: true,
			NodeName:            "mynode",
			ClusterName:         "mycluster",
			KubeConfig:          "/config",
		},
		Log: Log{
			FluentBitURL:   "http://localhost:2020",
			HostRootPrefix: "/hostroot",
			Inputs: []LogInput{
				{
					Path: "/var/log/apache/access.log",
					Filters: []LogFilter{
						{
							Metric: "apache_errors_count",
							Regex:  "\\[error\\]",
						},
					},
				},
				{
					ContainerName: testRedis,
					Filters: []LogFilter{
						{
							Metric: "redis_errors_count",
							Regex:  testERROR,
						},
					},
				},
				{
					Selectors: map[string]string{"app": "postgres"},
					Filters: []LogFilter{
						{
							Metric: "postgres_errors_count",
							Regex:  "error",
						},
					},
				},
			},
			OpenTelemetry: OpenTelemetry{
				Enable: true,
				AutoDiscovery: AutoDiscovery{
					AllEnable:                 true,
					JournaldEnable:            true,
					SyslogEnable:              true,
					AuditdEnable:              true,
					ContainerAndServiceEnable: true,
				},
				GRPC: EnableListener{
					Enable:  true,
					Address: DefaultLocalhost,
					Port:    4317,
				},
				HTTP: EnableListener{
					Enable:  true,
					Address: DefaultLocalhost,
					Port:    4318,
				},
				KnownLogFormats: map[string][]OTELOperator{
					"format-1": {
						{
							keyType: "add",
							"field": "resource['service.name']",
							"value": "apache_server",
						},
					},
					"app_format": {
						{
							keyType: "noop",
						},
					},
				},
				Receivers: map[string]OTLPReceiver{
					"filelog/recv": {
						Include: []string{"/var/log/apache/access.log", "/var/log/apache/error.log"},
						Operators: []OTELOperator{
							{
								keyType: "add",
								"field": "resource['service.name']",
								"value": "apache_server",
							},
						},
					},
				},
				ContainerFormat: map[string]string{
					"ctr-1": "format-1",
				},
				GlobalFilters: OTELFilters{
					"log_record": []any{
						`HasPrefix(resource.attributes["service.name"], "private_")`,
					},
				},
				KnownLogFilters: map[string]OTELFilters{
					testMinLevelInfo: {
						"include": map[string]any{
							"severity_number": map[string]any{
								"min": "9",
							},
						},
					},
				},
				ContainerFilter: map[string]string{
					"ctr-1": testMinLevelInfo,
				},
			},
		},
		Logging: Logging{
			Buffer: LoggingBuffer{
				HeadSizeBytes: 500000,
				TailSizeBytes: 5000000,
			},
			Level:         DefaultLogLevel,
			Output:        "console",
			FileName:      keyName,
			PackageLevels: "bleemeo=1",
		},
		Mdstat: Mdstat{
			Enable:    true,
			PathMdadm: "mdadm",
			UseSudo:   true,
		},
		Metric: Metric{
			AllowMetrics:          []string{"allowed"},
			DenyMetrics:           []string{"denied"},
			IncludeDefaultMetrics: true,
			Prometheus: Prometheus{
				Targets: []PrometheusTarget{
					{
						URL:          "http://localhost:8080/metrics",
						Name:         "my_app",
						AllowMetrics: []string{"metric1"},
						DenyMetrics:  []string{"metric2"},
					},
				},
			},
			SoftStatusPeriodDefault: 100,
			SoftStatusPeriod: map[string]int{
				metricSystemPendingUpdates:        100,
				"system_pending_security_updates": 200,
			},
			SNMP: SNMP{
				ExporterAddress: DefaultLocalhost,
				Targets: []SNMPTarget{
					{
						InitialName: "AP Wifi",
						Target:      DefaultLoopback,
					},
				},
			},
		},
		MQTT: OpenSourceMQTT{
			Enable:      true,
			Hosts:       []string{DefaultLocalhost},
			Port:        1883,
			Username:    "user",
			Password:    "pass",
			SSL:         true,
			SSLInsecure: true,
			CAFile:      "/myca",
		},
		NetworkInterfaceDenylist: []string{"lo", "veth"},
		NRPE: NRPE{
			Enable:    true,
			Address:   "0.0.0.0",
			Port:      5666,
			SSL:       true,
			ConfPaths: []string{"/etc/nagios/nrpe.cfg"},
		},
		NvidiaSMI: NvidiaSMI{
			Enable:  true,
			BinPath: "/usr/bin/nvidia-smi",
			Timeout: 5,
		},
		Services: []Service{
			{
				Type:              testService1,
				Instance:          "instance1",
				Port:              8080,
				IgnorePorts:       []int{8081},
				Address:           DefaultLoopback,
				Tags:              []string{"mytag1", "mytag2"},
				Interval:          60,
				CheckType:         testNagios,
				HTTPPath:          "/check/",
				HTTPStatusCode:    200,
				HTTPHost:          "host",
				MatchProcess:      "/usr/bin/dockerd",
				CheckCommand:      "/path/to/bin --with-option",
				NagiosNRPEName:    testNagios,
				MetricsUnixSocket: "/path/mysql.sock",
				Username:          "user",
				Password:          "password",
				StatsURL:          "http://nginx/stats",
				StatsPort:         9090,
				StatsProtocol:     "http",
				DetailedItems:     []string{"mytopic"},
				JMXPort:           1200,
				JMXUsername:       "jmx_user",
				JMXPassword:       "jmx_pass",
				JMXMetrics: []JmxMetric{
					{
						Name:      "heap_size_mb",
						MBean:     "java.lang:type=Memory",
						Attribute: "HeapMemoryUsage",
						Path:      "used",
						Scale:     0.1,
						Derive:    true,
						Sum:       true,
						Ratio:     "a",
						TypeNames: []string{keyName},
					},
				},
				SSL:           true,
				SSLInsecure:   true,
				StartTLS:      true,
				CAFile:        "/myca.pem",
				CertFile:      "/mycert.pem",
				KeyFile:       "/mykey.pem",
				IncludedItems: []string{"included"},
				ExcludedItems: []string{"excluded"},
				LogFiles: []ServiceLogFile{
					{
						FilePath:  "/var/log/app.log",
						LogFormat: "app_format",
						LogFilter: testMinLevelInfo,
					},
				},
				LogFormat: "nginx_both",
			},
		},
		ServiceAbsentDeactivationDelay: 7 * 24 * time.Hour,
		ServiceIgnore: []NameInstance{
			{
				Name:     "nginx",
				Instance: "container:*",
			},
		},
		ServiceIgnoreMetrics: []NameInstance{
			{
				Name:     testRedis,
				Instance: "host:*",
			},
		},
		ServiceIgnoreCheck: []NameInstance{
			{
				Name:     "postgresql",
				Instance: "host:* container:*",
			},
		},
		Smart: Smart{
			Enable:         true,
			PathSmartctl:   "/smartctl",
			Devices:        []string{"/dev/sda"},
			Excludes:       []string{"/dev/sdb"},
			MaxConcurrency: 42,
		},
		Tags: []string{"mytag"},
		Telegraf: Telegraf{
			DockerMetricsEnable: true,
			StatsD: StatsD{
				Enable:  true,
				Address: DefaultLoopback,
				Port:    8125,
			},
		},
		Thresholds: map[string]Threshold{
			testCPUUsed: {
				LowWarning:   newFloatPointer(2),
				LowCritical:  newFloatPointer(1.5),
				HighWarning:  newFloatPointer(80.2),
				HighCritical: newFloatPointer(90),
			},
			"disk_used": {
				LowWarning:   nil,
				LowCritical:  newFloatPointer(2),
				HighWarning:  newFloatPointer(90.5),
				HighCritical: nil,
			},
		},
		VSphere: []VSphere{
			{
				URL:                "https://esxi.test",
				Username:           "root",
				Password:           "passwd",
				InsecureSkipVerify: false,
				SkipMonitorVMs:     false,
			},
		},
		Web: Web{
			Enable: true,
			Endpoints: WebEndpoints{
				DebugEnable: true,
			},
			LocalUI: LocalUI{
				Enable: true,
			},
			Listener: Listener{
				Address: "192.168.0.1",
				Port:    8016,
			},
			StaticCDNURL: "/",
		},
		Zabbix: Zabbix{
			Enable:  true,
			Address: "zabbix",
			Port:    7000,
		},
	}

	config, warnings, err := load(&configLoader{}, false, false, "testdata/full.conf")
	if warnings != nil {
		t.Fatalf("Warning while loading config: %s", warnings)
	}

	if err != nil {
		t.Fatalf("Failed to load config: %s", err)
	}

	if diff := compareConfig(expectedConfig, config); diff != "" {
		t.Fatalf("Unexpected config loaded:\n%s", diff)
	}
}

func newFloatPointer(value float64) *float64 {
	p := new(float64)
	*p = value

	return p
}

// Test that users are able to override default settings.
func TestOverrideDefault(t *testing.T) {
	expectedConfig := DefaultConfig()
	expectedConfig.NetworkInterfaceDenylist = []string{"override"}
	expectedConfig.DF.PathIgnore = []string{"/override"}
	expectedConfig.Bleemeo.APIBase = ""
	expectedConfig.Bleemeo.Enable = false
	expectedConfig.Bleemeo.MQTT.SSL = false

	t.Setenv("GLOUTON_BLEEMEO_ENABLE", "false")

	config, warnings, err := load(&configLoader{}, true, true, "testdata/override_default.conf")
	if warnings != nil {
		t.Fatalf("Warning while loading config: %s", warnings)
	}

	if err != nil {
		t.Fatalf("Failed to load config: %s", err)
	}

	if diff := compareConfig(expectedConfig, config); diff != "" {
		t.Fatalf("Default value modified:\n%s", diff)
	}
}

// TestMergeWithDefault tests that the config files and the environment variables
// are correctly merge.
// For files, basic types (string, int, ...) are overwritten, maps are merged and arrays are concatenated.
// Files overwrite default values but merges maps with the defaults.
// Environment variables always overwrite the existing config.
func TestMergeWithDefault(t *testing.T) {
	expectedConfig := DefaultConfig()
	expectedConfig.Bleemeo.Enable = false
	expectedConfig.Bleemeo.MQTT.SSLInsecure = true
	expectedConfig.Bleemeo.MQTT.Host = "b"
	expectedConfig.MQTT.Hosts = []string{}
	expectedConfig.Metric.AllowMetrics = []string{"mymetric", "mymetric2"}
	expectedConfig.Metric.DenyMetrics = []string{testCPUUsed}
	expectedConfig.Metric.SoftStatusPeriod = map[string]int{
		metricSystemPendingUpdates: 500,
	}
	expectedConfig.Thresholds = map[string]Threshold{
		"mymetric": {
			LowWarning: newFloatPointer(1),
		},
		"mymetric2": {
			HighCritical: newFloatPointer(90),
		},
		"mymetric3": {
			HighWarning: newFloatPointer(80),
		},
	}
	expectedConfig.NetworkInterfaceDenylist = []string{testEth0, "eth1", "eth1", "eth2"}

	t.Setenv("GLOUTON_MQTT_HOSTS", "")
	t.Setenv("GLOUTON_METRIC_DENY_METRICS", testCPUUsed)
	t.Setenv("GLOUTON_METRIC_SOFTSTATUS_PERIOD", "system_pending_updates=500")

	config, warnings, err := load(&configLoader{}, true, true, "testdata/merge")
	if warnings != nil {
		t.Fatalf("Warning while loading config: %s", warnings)
	}

	if err != nil {
		t.Fatalf("Failed to load config: %s", err)
	}

	if diff := compareConfig(expectedConfig, config); diff != "" {
		t.Fatalf("Default value modified:\n%s", diff)
	}
}

// Test that the config loaded with no config file has default values.
func TestDefaultNoFile(t *testing.T) {
	config, warnings, err := load(&configLoader{}, true, false)
	if warnings != nil {
		t.Fatalf("Warning while loading config: %s", warnings)
	}

	if err != nil {
		t.Fatalf("Failed to load config: %s", err)
	}

	if diff := compareConfig(DefaultConfig(), config, cmpopts.EquateEmpty()); diff != "" {
		t.Fatalf("Default value modified:\n%s", diff)
	}
}

// Testload tests loading the config and the warnings and errors returned.
func TestLoad(t *testing.T) { //nolint:maintidx
	tests := []struct {
		Name         string
		Files        []string
		Environment  map[string]string
		WantConfig   Config
		WantWarnings []string
		WantError    error
	}{
		{
			Name:  "wrong type",
			Files: []string{"testdata/bad_wrong_type.conf"},
			WantWarnings: []string{
				`'metric.softstatus_period_default' cannot parse value as 'int': strconv.ParseInt: invalid syntax`,
				`'metric.softstatus_period[1][system_pending_security_updates]' cannot parse value as 'int': strconv.ParseInt: invalid syntax`,
			},
			WantConfig: Config{
				Metric: Metric{
					SoftStatusPeriod: map[string]int{metricSystemPendingUpdates: 100},
				},
			},
		},
		{
			Name:  "invalid yaml",
			Files: []string{"testdata/bad_yaml.conf"},
			WantWarnings: []string{
				"line 1: cannot unmarshal !!str `bad:bad` into map[string]interface {}",
			},
		},
		{
			Name:  "invalid yaml multiple files",
			Files: []string{"testdata/invalid"},
			WantWarnings: []string{
				"testdata/invalid/10-invalid.conf: yaml: line 2: found character that cannot start any token",
			},
			WantConfig: Config{
				Agent: Agent{
					FactsFile: DefaultFactsFile,
				},
				Bleemeo: Bleemeo{
					APIBase: "base",
				},
			},
		},
		{
			Name: "deprecated env",
			Environment: map[string]string{
				"BLEEMEO_AGENT_ACCOUNT": "my-account",
				"GLOUTON_WEB_ENABLED":   "true",
			},
			WantWarnings: []string{
				"environment variable is deprecated: BLEEMEO_AGENT_ACCOUNT, use GLOUTON_BLEEMEO_ACCOUNT_ID instead",
				"environment variable is deprecated: GLOUTON_WEB_ENABLED, use GLOUTON_WEB_ENABLE instead",
			},
			WantConfig: Config{
				Web: Web{
					Enable: true,
				},
				Bleemeo: Bleemeo{
					AccountID: "my-account",
				},
			},
		},
		{
			Name:  "deprecated config",
			Files: []string{"testdata/deprecated.conf"},
			WantWarnings: []string{
				"testdata/deprecated.conf: setting is deprecated: web.enabled, use web.enable instead",
			},
			WantConfig: Config{
				Web: Web{
					Enable: true,
				},
			},
		},
		{
			Name:  "migration file",
			Files: []string{testOldPromTargetsConf},
			WantWarnings: []string{
				"testdata/old-prometheus-targets.conf: setting is deprecated: metrics.prometheus. " +
					"See https://go.bleemeo.com/l/doc-prometheus",
			},
			WantConfig: Config{
				Metric: Metric{
					Prometheus: Prometheus{
						Targets: []PrometheusTarget{
							{
								Name: testTest1,
								URL:  testLocalhostMetricsURL,
							},
						},
					},
				},
			},
		},
		{
			Name: "slice from env",
			Environment: map[string]string{
				"GLOUTON_METRIC_ALLOW_METRICS": "metric1,metric2",
				"GLOUTON_METRIC_DENY_METRICS":  "metric3",
			},
			WantConfig: Config{
				Metric: Metric{
					AllowMetrics: []string{"metric1", "metric2"},
					DenyMetrics:  []string{"metric3"},
				},
			},
		},
		{
			Name: "map from env",
			Environment: map[string]string{
				"GLOUTON_METRIC_SOFTSTATUS_PERIOD": "cpu_used=10,disk_used=20",
				"GLOUTON_METRIC_ALLOW_METRICS":     testCPUUsed,
			},
			WantConfig: Config{
				Metric: Metric{
					SoftStatusPeriod: map[string]int{
						testCPUUsed: 10,
						"disk_used": 20,
					},
					AllowMetrics: []string{testCPUUsed},
				},
			},
		},
		{
			Name: "map from env invalid",
			Environment: map[string]string{
				"GLOUTON_METRIC_SOFTSTATUS_PERIOD": "cpu_used=10,disk_used",
			},
			WantWarnings: []string{
				`'metric.softstatus_period' could not parse map from string: 'cpu_used=10,disk_used'`,
			},
		},
		{
			Name: "enabled renamed",
			Files: []string{
				"testdata/enabled.conf",
			},
			WantConfig: Config{
				Agent: Agent{
					WindowsExporter: NodeExporter{
						Enable: true,
					},
				},
				Telegraf: Telegraf{
					DockerMetricsEnable: true,
				},
			},
			WantWarnings: []string{
				"testdata/enabled.conf: setting is deprecated: agent.windows_exporter.enabled, use agent.windows_exporter.enable instead",
				"testdata/enabled.conf: setting is deprecated: telegraf.docker_metrics_enabled, use telegraf.docker_metrics_enable instead",
			},
		},
		{
			Name: "folder",
			Files: []string{
				"testdata/folder1",
			},
			WantConfig: Config{
				Bleemeo: Bleemeo{
					Enable:    false,
					AccountID: "second",
				},
			},
			WantWarnings: []string{
				"testdata/folder1/00-first.conf: setting is deprecated: bleemeo.enabled, use bleemeo.enable instead",
			},
		},
		{
			Name:  "bleemeo-agent envs",
			Files: []string{},
			Environment: map[string]string{
				"BLEEMEO_AGENT_KUBERNETES_ENABLED": "true",
				"BLEEMEO_AGENT_BLEEMEO_MQTT_HOST":  "myhost",
			},
			WantConfig: Config{
				Bleemeo: Bleemeo{
					MQTT: BleemeoMQTT{
						Host: "myhost",
					},
				},
				Kubernetes: Kubernetes{
					Enable: true,
				},
			},
			WantWarnings: []string{
				"environment variable is deprecated: BLEEMEO_AGENT_KUBERNETES_ENABLED, use GLOUTON_KUBERNETES_ENABLE instead",
				"environment variable is deprecated: BLEEMEO_AGENT_BLEEMEO_MQTT_HOST, use GLOUTON_BLEEMEO_MQTT_HOST instead",
			},
		},
		{
			Name: "old logging",
			Files: []string{
				"testdata/old-logging.conf",
			},
			WantConfig: Config{
				Logging: Logging{
					Buffer: LoggingBuffer{
						HeadSizeBytes: 4200,
						TailSizeBytes: 4800,
					},
				},
			},
			WantWarnings: []string{
				"testdata/old-logging.conf: setting is deprecated: logging.buffer.head_size, use logging.buffer.head_size_bytes instead",
				"testdata/old-logging.conf: setting is deprecated: logging.buffer.tail_size, use logging.buffer.tail_size_bytes instead",
			},
		},
		{
			Name: "unused keys",
			Files: []string{
				"testdata/unused.conf",
			},
			WantConfig: Config{
				Services: []Service{
					{
						Type:         testService1,
						CheckType:    "nagios",
						CheckCommand: "/path/to/bin --with-option",
					},
				},
			},
			WantWarnings: []string{
				"'bleemeo' has invalid keys: unused_key",
				"'service[0]' has invalid keys: another_key",
			},
		},
		{
			Name:  "override values",
			Files: []string{"testdata/override"},
			Environment: map[string]string{
				"GLOUTON_BLEEMEO_MQTT_HOST": "",
			},
			WantConfig: Config{
				Bleemeo: Bleemeo{
					APIBase: "",
					MQTT: BleemeoMQTT{
						Host:   "",
						CAFile: "myfile",
						Port:   1884,
						SSL:    true,
					},
					Enable: false,
				},
			},
		},
		{
			Name:  "convert boolean",
			Files: []string{"testdata/bool.conf"},
			Environment: map[string]string{
				"GLOUTON_ZABBIX_ENABLE": "Yes",
			},
			WantWarnings: []string{
				`'mqtt.ssl_insecure' strconv.ParseBool: parsing "invalid": invalid syntax`,
			},
			WantConfig: Config{
				Agent: Agent{
					NodeExporter: NodeExporter{
						Enable: true,
					},
					ProcessExporter: ProcessExporter{
						Enable: true,
					},
					WindowsExporter: NodeExporter{
						Enable: true,
					},
					Telemetry: Telemetry{
						Enable: true,
					},
				},
				Blackbox: Blackbox{
					Enable:          true,
					ScraperSendUUID: true,
				},
				Bleemeo: Bleemeo{
					Enable: true,
				},
				JMX: JMX{
					Enable: false,
				},
				Kubernetes: Kubernetes{
					Enable: false,
				},
				MQTT: OpenSourceMQTT{
					Enable: false,
					SSL:    false,
				},
				NRPE: NRPE{
					Enable: false,
					SSL:    false,
				},
				Zabbix: Zabbix{
					Enable: true,
				},
				Web: Web{
					Endpoints: WebEndpoints{
						DebugEnable: false,
					},
				},
			},
		},
		{
			Name: "config file from env",
			Environment: map[string]string{
				EnvGloutonConfigFiles: "testdata/simple.conf",
			},
			WantConfig: Config{
				Web: Web{
					StaticCDNURL: testSimplePath,
				},
			},
		},
		{
			Name: "empty file",
			Files: []string{
				"testdata/empty.conf",
				"testdata/simple.conf",
			},
			WantConfig: Config{
				Web: Web{
					StaticCDNURL: testSimplePath,
				},
			},
		},
		{
			Name:  "log-auto-discovery-one-by-one",
			Files: []string{"testdata/log-auto-discovery-one-by-one.conf"},
			WantConfig: Config{
				Log: Log{
					OpenTelemetry: OpenTelemetry{
						AutoDiscovery: AutoDiscovery{
							AllEnable:                 false,
							JournaldEnable:            true,
							SyslogEnable:              false,
							AuditdEnable:              true,
							ContainerAndServiceEnable: false,
						},
					},
				},
			},
		},
		{
			Name:  "log-auto-discovery-all",
			Files: []string{"testdata/log-auto-discovery-all.conf"},
			WantWarnings: []string{
				"config issue: log.opentelemetry.auto_discovery.auditd_enable can't disable when all_enable is active",
			},
			WantConfig: Config{
				Log: Log{
					OpenTelemetry: OpenTelemetry{
						AutoDiscovery: AutoDiscovery{
							AllEnable:                 true,
							JournaldEnable:            true,
							SyslogEnable:              true,
							AuditdEnable:              true,
							ContainerAndServiceEnable: true,
						},
					},
				},
			},
		},
		{
			Name:  "deprecated cassandra_detailed_tables",
			Files: []string{"testdata/deprecated_cassandra.conf"},
			WantWarnings: []string{
				"testdata/deprecated_cassandra.conf: setting is deprecated in 'service' override for cassandra: 'cassandra_detailed_tables'" +
					", use 'detailed_items' instead",
			},
			WantConfig: Config{
				Services: []Service{
					{
						Type: testCassandra,
						DetailedItems: []string{
							"keyspace.table1",
							"keyspace.table2",
						},
					},
				},
			},
		},
		{
			Name:  "deprecated mgmt_port",
			Files: []string{"testdata/deprecated_mgmt_port.conf"},
			WantWarnings: []string{
				"testdata/deprecated_mgmt_port.conf: setting is deprecated in 'service' override for service1: 'mgmt_port', use 'stats_port' instead",
			},
			WantConfig: Config{
				Services: []Service{
					{
						Type:      testService1,
						StatsPort: 9090,
					},
				},
			},
		},
		{
			Name:  "deprecated network_interface_blacklist",
			Files: []string{"testdata/deprecated_blacklist.conf"},
			WantWarnings: []string{
				"testdata/deprecated_blacklist.conf: setting is deprecated: network_interface_blacklist, " +
					"use network_interface_denylist instead",
			},
			WantConfig: Config{
				NetworkInterfaceDenylist: []string{testEth0},
			},
		},
		{
			Name:  "deprecated_service_id",
			Files: []string{"testdata/deprecated_service_id.conf"},
			WantWarnings: []string{
				"testdata/deprecated_service_id.conf: setting is deprecated in 'service' override for apache: 'id', use 'type' instead",
			},
			WantConfig: Config{
				Services: []Service{
					{
						Type: testApache,
						Port: 1234,
					},
				},
			},
		},
		{
			Name:  "deprecated_service_id_with_instance",
			Files: []string{"testdata/deprecated_service_id_with_instance.conf"},
			WantWarnings: []string{
				"testdata/deprecated_service_id_with_instance.conf: setting is deprecated in 'service' override for apache: 'id', use 'type' instead",
			},
			WantConfig: Config{
				Services: []Service{
					{
						Type:     testApache,
						Instance: "my_container",
						Port:     1234,
					},
				},
			},
		},
		{
			Name:  "deprecated_service_absent_deactivation_delay",
			Files: []string{"testdata/deprecated_service_absent_deactivation_delay.conf"},
			WantWarnings: []string{
				"testdata/deprecated_service_absent_deactivation_delay.conf: setting is deprecated: agent.absent_service_deactivation_delay, use service_absent_deactivation_delay instead",
			},
			WantConfig: Config{
				ServiceAbsentDeactivationDelay: 42 * time.Hour,
			},
		},
		{
			Name:  "deprecated_auto_discovery",
			Files: []string{"testdata/deprecated_auto_discovery.conf"},
			WantWarnings: []string{
				"testdata/deprecated_auto_discovery.conf: setting is deprecated: log.opentelemetry.auto_discovery, use log.opentelemetry.auto_discovery.all_enable instead",
			},
			WantConfig: Config{
				Log: Log{
					OpenTelemetry: OpenTelemetry{
						AutoDiscovery: AutoDiscovery{
							AllEnable:                 true,
							JournaldEnable:            true,
							SyslogEnable:              true,
							AuditdEnable:              true,
							ContainerAndServiceEnable: true,
						},
					},
				},
			},
		},
		{
			Name:  "deprecated_auto_discovery2",
			Files: []string{"testdata/deprecated_auto_discovery2.conf"},
			WantWarnings: []string{
				"testdata/deprecated_auto_discovery2.conf: setting is deprecated: log.opentelemetry.auto_discovery.enable_all, use log.opentelemetry.auto_discovery.all_enable instead",
			},
			WantConfig: Config{
				Log: Log{
					OpenTelemetry: OpenTelemetry{
						AutoDiscovery: AutoDiscovery{
							AllEnable:                 true,
							JournaldEnable:            true,
							SyslogEnable:              true,
							AuditdEnable:              true,
							ContainerAndServiceEnable: true,
						},
					},
				},
			},
		},
		{
			Name:  "deprecated_auto_discovery3",
			Files: []string{"testdata/deprecated_auto_discovery3.conf"},
			WantWarnings: []string{
				"testdata/deprecated_auto_discovery3.conf: setting is deprecated: log.opentelemetry.auto_discovery.enable_journalctl, use log.opentelemetry.auto_discovery.journald_enable instead",
				"testdata/deprecated_auto_discovery3.conf: setting is deprecated: log.opentelemetry.auto_discovery.enable_syslog, use log.opentelemetry.auto_discovery.syslog_enable instead",
				"testdata/deprecated_auto_discovery3.conf: setting is deprecated: log.opentelemetry.auto_discovery.enable_auditd, use log.opentelemetry.auto_discovery.auditd_enable instead",
				"testdata/deprecated_auto_discovery3.conf: setting is deprecated: log.opentelemetry.auto_discovery.enable_container_and_service, use log.opentelemetry.auto_discovery.container_and_service_enable instead",
			},
			WantConfig: Config{
				Log: Log{
					OpenTelemetry: OpenTelemetry{
						AutoDiscovery: AutoDiscovery{
							AllEnable:                 false,
							JournaldEnable:            true,
							SyslogEnable:              true,
							AuditdEnable:              true,
							ContainerAndServiceEnable: true,
						},
					},
				},
			},
		},
		{
			Name:  "deprecated_auto_discovery4",
			Files: []string{"testdata/deprecated_auto_discovery4.conf"},
			WantWarnings: []string{
				"testdata/deprecated_auto_discovery4.conf: setting is deprecated: log.opentelemetry.auto_discovery.journalctl_enable, use log.opentelemetry.auto_discovery.journald_enable instead",
			},
			WantConfig: Config{
				Log: Log{
					OpenTelemetry: OpenTelemetry{
						AutoDiscovery: AutoDiscovery{
							AllEnable:                 false,
							JournaldEnable:            true,
							SyslogEnable:              false,
							AuditdEnable:              false,
							ContainerAndServiceEnable: false,
						},
					},
				},
			},
		},
		{
			Name:  "multiple_deprecated_same_file",
			Files: []string{"testdata/multiple_deprecated_same_file.conf", "testdata/multiple_deprecated_same_file2.conf"},
			WantWarnings: []string{
				"testdata/multiple_deprecated_same_file.conf: setting is deprecated in 'service' override for apache: 'id', use 'type' instead",
				"testdata/multiple_deprecated_same_file.conf: setting is deprecated in 'service' override for nginx: 'id', use 'type' instead",
				"testdata/multiple_deprecated_same_file.conf: setting is deprecated in 'service' override for cassandra: 'cassandra_detailed_tables', use 'detailed_items' instead",
				"testdata/multiple_deprecated_same_file2.conf: setting is deprecated in 'service' override for mysql: 'id', use 'type' instead",
			},
			WantConfig: Config{
				Services: []Service{
					{
						Type: testApache,
						Port: 1234,
					},
					{
						Type: "nginx",
						Port: 1235,
					},
					{
						Type:          testCassandra,
						Port:          1236,
						DetailedItems: []string{"table1"},
					},
					{
						Type: "mysql",
						Port: 1237,
					},
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			for k, v := range test.Environment {
				t.Setenv(k, v)
			}

			config, warnings, err := load(&configLoader{}, false, true, test.Files...)
			if diff := cmp.Diff(test.WantError, err); diff != "" {
				t.Fatalf("Unexpected error for files %s\n%s", test.Files, diff)
			}

			var strWarnings []string

			for _, warning := range warnings {
				strWarnings = append(strWarnings, warning.Error())
			}

			lessFunc := func(a, b string) bool {
				return a < b
			}

			if diff := cmp.Diff(test.WantWarnings, strWarnings, cmpopts.SortSlices(lessFunc)); diff != "" {
				t.Errorf("Unexpected warnings:\n%s", diff)
			}

			if diff := compareConfig(test.WantConfig, config, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("Unexpected config (-want +got):\n%s", diff)
			}
		})
	}

	// This subtest is apart because needs a slightly different setup than the other cases.
	t.Run("config contains null parts", func(t *testing.T) {
		config, warnings, err := load(&configLoader{}, true, false, "testdata/null-parts.conf")
		if err != nil {
			t.Fatal("Unexpected error:", err)
		}

		expectedWarning := "1 error(s) occurred:\n* testdata/null-parts.conf: \"blackbox\" config entry has a null value, ignoring it"

		if diff := cmp.Diff(expectedWarning, warnings.Error()); diff != "" {
			t.Fatalf("Unexpected warnings:\n%s", diff)
		}

		expectedConfig := DefaultConfig()
		expectedConfig.Bleemeo.APIBase = "not/null"
		expectedConfig.Bleemeo.ContainerRegistrationDelaySeconds = 0
		// TODO: this should be true (or a warning should be raised).
		// currently we silently ignore the value entered by user.
		expectedConfig.Bleemeo.Enable = false
		expectedConfig.Web.StaticCDNURL = testSimplePath

		if diff := compareConfig(expectedConfig, config); diff != "" {
			t.Fatalf("Unexpected config:\n%s", diff)
		}
	})
}

func TestStateLoading(t *testing.T) {
	defaultAgentCfg := DefaultConfig().Agent
	agentCfg := Agent{ // Avoids repeating all these lines in every test case
		EnableCrashReporting: defaultAgentCfg.EnableCrashReporting,
		MaxCrashReportsCount: defaultAgentCfg.MaxCrashReportsCount,
		ProcessExporter:      defaultAgentCfg.ProcessExporter,
		PublicIPIndicator:    defaultAgentCfg.PublicIPIndicator,
		NodeExporter:         defaultAgentCfg.NodeExporter,
		WindowsExporter:      defaultAgentCfg.WindowsExporter,
		Telemetry:            defaultAgentCfg.Telemetry,
		LocalStore:           defaultAgentCfg.LocalStore,
	}

	cases := []struct {
		Name       string
		Files      []string
		WantConfig Agent
	}{
		{
			Name:  "Glouton as a package",
			Files: []string{"testdata/state-package.conf"},
			WantConfig: Agent{
				InstallationFormat:     "Package (deb)",
				CloudImageCreationFile: testGloutonCloudimageCreate,
				FactsFile:              testGloutonFactsYaml,
				NetstatFile:            testGloutonNetstatOut,
				StateDirectory:         testGloutonStateDir,
				StateFile:              testGloutonStateJSON,
				StateCacheFile:         testGloutonStateCacheJSON,
				StateResetFile:         testGloutonStateReset,
				UpgradeFile:            testGloutonUpgrade,
				AutoUpgradeFile:        testGloutonAutoUpgrade,
			},
		},
		{
			Name:  "Glouton as a Docker image",
			Files: []string{"testdata/state-docker.conf"},
			WantConfig: Agent{
				InstallationFormat:     "Docker image",
				CloudImageCreationFile: testGloutonCloudimageCreate,
				FactsFile:              testGloutonFactsYaml,
				NetstatFile:            testGloutonNetstatOut,
				StateDirectory:         testGloutonStateDir,
				StateFile:              testGloutonStateJSON,
				StateCacheFile:         testGloutonStateCacheJSON,
				StateResetFile:         testGloutonStateReset,
				UpgradeFile:            testGloutonUpgrade,
				AutoUpgradeFile:        testGloutonAutoUpgrade,
				DeprecatedStateFile:    "/var/lib/bleemeo/state.json",
			},
		},
		// Testing for Windows is impossible to do from a unix Go runtime,
		// because path/filepath functions exclusively use / as the path separator.
		/*{
			Name:  "Glouton on Windows",
			Files: []string{"testdata/state-windows.conf"},
			WantConfig: Agent{
				InstallationFormat:     "Package (Windows)",
				CloudImageCreationFile: `C:\ProgramData\glouton\cloudimage_creation`,
				FactsFile:              `C:\ProgramData\glouton\facts.yaml`,
				NetstatFile:            `C:\ProgramData\glouton\netstat.out`,
				StateDirectory:         `C:\ProgramData\glouton`,
				StateFile:              `C:\ProgramData\glouton\state.json`,
				StateCacheFile:         `C:\ProgramData\glouton\state.cache.json`,
				StateResetFile:         `C:\ProgramData\glouton\state.reset`,
				UpgradeFile:            `C:\ProgramData\glouton\upgrade`,
				AutoUpgradeFile:        `C:\ProgramData\glouton\auto_upgrade`,
			},
		},*/
		{
			Name: "Glouton as dev",
			WantConfig: Agent{
				InstallationFormat:     DefaultInstallFormat,
				CloudImageCreationFile: defaultAgentCfg.CloudImageCreationFile,
				FactsFile:              defaultAgentCfg.FactsFile,
				NetstatFile:            defaultAgentCfg.NetstatFile,
				StateFile:              defaultAgentCfg.StateFile,
				StateCacheFile:         DefaultStateCacheFile,
				StateResetFile:         defaultAgentCfg.StateResetFile,
				StateDirectory:         ".",
				UpgradeFile:            defaultAgentCfg.UpgradeFile,
				AutoUpgradeFile:        defaultAgentCfg.AutoUpgradeFile,
			},
		},
		{
			Name:  "Glouton custom",
			Files: []string{"testdata/state-custom.conf"},
			WantConfig: Agent{
				InstallationFormat:     DefaultInstallFormat,
				CloudImageCreationFile: testGloutonCloudimageCreate,
				FactsFile:              testGloutonFactsYaml,
				NetstatFile:            testGloutonNetstatOut,
				StateDirectory:         testGloutonStateDir,
				StateFile:              testGloutonStateJSON,
				StateCacheFile:         testGloutonStateCacheJSON,
				StateResetFile:         testGloutonStateReset,
				UpgradeFile:            testGloutonUpgrade,
				AutoUpgradeFile:        testGloutonAutoUpgrade,
			},
		},
		{
			Name:  "Glouton custom 2 with system",
			Files: []string{"testdata/state-package.conf", "testdata/state-custom2.conf"},
			WantConfig: Agent{
				InstallationFormat:     "Package (deb)",
				CloudImageCreationFile: testGloutonCloudimageCreate,
				FactsFile:              testGloutonFactsYaml,
				NetstatFile:            testGloutonNetstatOut,
				StateDirectory:         testGloutonStateDir,
				StateFile:              testGloutonStateJSON,
				StateCacheFile:         testGloutonStateCacheJSON,
				StateResetFile:         testGloutonStateReset,
				UpgradeFile:            testGloutonUpgrade,
				AutoUpgradeFile:        testGloutonAutoUpgrade,
			},
		},
		{
			Name:  "Glouton custom 2 without system",
			Files: []string{"testdata/state-custom2.conf"},
			WantConfig: Agent{
				InstallationFormat:     DefaultInstallFormat,
				CloudImageCreationFile: testGloutonCloudimageCreate,
				FactsFile:              testGloutonFactsYaml,
				NetstatFile:            testGloutonNetstatOut,
				StateDirectory:         testGloutonStateDir,
				StateFile:              testGloutonStateJSON,
				StateCacheFile:         testGloutonStateCacheJSON,
				StateResetFile:         testGloutonStateReset,
				UpgradeFile:            testGloutonUpgrade,
				AutoUpgradeFile:        testGloutonAutoUpgrade,
			},
		},
		{
			Name:  "Glouton custom 3",
			Files: []string{"testdata/state-custom3.conf"},
			WantConfig: Agent{
				InstallationFormat:     DefaultInstallFormat,
				CloudImageCreationFile: "/home/glouton/data/cloudimage_creation",
				FactsFile:              "/home/glouton/data/facts.yaml",
				NetstatFile:            "/home/glouton/data/netstat.out",
				StateDirectory:         "/home/glouton/data",
				StateFile:              "/home/glouton/data/state.json",
				StateCacheFile:         "/home/glouton/data/state.cache.json",
				StateResetFile:         "/home/glouton/data/state.reset",
				UpgradeFile:            "/home/glouton/data/upgrade",
				AutoUpgradeFile:        "/home/glouton/data/auto_upgrade",
			},
		},
		{
			Name:  "Glouton custom 4",
			Files: []string{"testdata/state-custom4.conf"},
			WantConfig: Agent{
				InstallationFormat:     DefaultInstallFormat,
				CloudImageCreationFile: "myfolder/data/cloudimage_creation",
				FactsFile:              "myfolder/data/facts.yaml",
				NetstatFile:            "myfolder/data/netstat.out",
				StateDirectory:         "myfolder/data",
				StateFile:              "myfolder/data/state.json",
				StateCacheFile:         "myfolder/data/state.cache.json",
				StateResetFile:         "myfolder/data/state.reset",
				UpgradeFile:            "myfolder/data/upgrade",
				AutoUpgradeFile:        "myfolder/data/auto_upgrade",
			},
		},
	}

	for _, tc := range cases {
		// This action is not specific to the current test case.
		err := mergo.Merge(&tc.WantConfig, agentCfg)
		if err != nil {
			t.Fatal("Failed to merge default agent config:", err)
		}

		t.Run(tc.Name, func(t *testing.T) {
			t.Parallel()

			config, _, warnings, err := Load(true, false, tc.Files...)
			if err != nil {
				t.Fatal("Error while loading config:", err)
			}

			if len(warnings) != 0 {
				t.Error("Got some warnings while loading config:", warnings.Error())
			}

			if diff := cmp.Diff(tc.WantConfig, config.Agent); diff != "" {
				t.Errorf("Unexpected agent config: (-want +got)\n%s", diff)
			}
		})
	}
}

// TestDump tests that secrets are redacted when the config is dumped.
func TestDump(t *testing.T) {
	config := Config{
		Bleemeo: Bleemeo{
			AccountID:       testInDump,
			RegistrationKey: testNotInDump,
		},
		MQTT: OpenSourceMQTT{
			Password: testNotInDump,
		},
		Services: []Service{
			{
				Type:        testInDump,
				Password:    testNotInDump,
				JMXPassword: testNotInDump,
				KeyFile:     testNotInDump,
			},
			{
				Type:        "in-dump-2",
				Password:    "",
				JMXPassword: "",
				KeyFile:     "",
			},
		},
	}

	wantConfig := Config{
		Bleemeo: Bleemeo{
			AccountID:       testInDump,
			RegistrationKey: CensoredValue,
		},
		MQTT: OpenSourceMQTT{
			Password: CensoredValue,
		},
		Services: []Service{
			{
				Type:        testInDump,
				Password:    CensoredValue,
				JMXPassword: CensoredValue,
				KeyFile:     CensoredValue,
			},
			{
				Type: "in-dump-2",
				// In dump because these fields were unset.
				Password:    "",
				JMXPassword: "",
				KeyFile:     "",
			},
		},
	}

	k := koanf.New(delimiter)
	_ = k.Load(structs.Provider(wantConfig, Tag), nil)
	wantMap := k.Raw()

	dump := Dump(config)

	if diff := cmp.Diff(wantMap, dump, cmpopts.EquateEmpty()); diff != "" {
		t.Fatalf("Config dump didn't redact secrets correctly:\n%s", diff)
	}
}

func Test_migrate(t *testing.T) {
	tests := []struct {
		Name       string
		ConfigFile string
		WantConfig Config
	}{
		{
			Name:       "new-prometheus-targets",
			ConfigFile: "testdata/new-prometheus-targets.conf",
			WantConfig: Config{
				Metric: Metric{
					Prometheus: Prometheus{
						Targets: []PrometheusTarget{
							{
								Name: testTest1,
								URL:  testLocalhostMetricsURL,
							},
						},
					},
				},
			},
		},
		{
			Name:       "old-prometheus-targets",
			ConfigFile: testOldPromTargetsConf,
			WantConfig: Config{
				Metric: Metric{
					Prometheus: Prometheus{
						Targets: []PrometheusTarget{
							{
								Name: testTest1,
								URL:  testLocalhostMetricsURL,
							},
						},
					},
				},
			},
		},
		{
			Name:       "both-prometheus-targets",
			ConfigFile: "testdata/both-prometheus-targets.conf",
			WantConfig: Config{
				Metric: Metric{
					Prometheus: Prometheus{
						Targets: []PrometheusTarget{
							{
								Name: testNew,
								URL:  "http://new:9090/metrics",
							},
							{
								Name: testOld,
								URL:  "http://old:9090/metrics",
							},
						},
					},
				},
			},
		},
		{
			Name:       "old-prometheus-allow/deny_metrics",
			ConfigFile: "testdata/old-prometheus-metrics.conf",
			WantConfig: Config{
				Metric: Metric{
					AllowMetrics: []string{
						"test4",
						testTest1,
						"test2",
					},
					DenyMetrics: []string{
						"test5",
						"test3",
					},
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			config, _, err := load(&configLoader{}, false, false, test.ConfigFile)
			if err != nil {
				t.Fatalf("Failed to load config: %s", err)
			}

			if diff := compareConfig(test.WantConfig, config); diff != "" {
				t.Fatalf("Unexpected config:\n%s", diff)
			}
		})
	}
}

//nolint:dupl
func Test_prometheusConfigToURLs(t *testing.T) {
	mustParse := func(text string) *url.URL {
		u, err := url.Parse(text)
		if err != nil {
			t.Fatal(err)
		}

		return u
	}

	tests := []struct {
		name        string
		cfgFilename string
		want        []*scrapper.Target
	}{
		{
			name:        "old",
			cfgFilename: testOldPromTargetsConf,
			want: []*scrapper.Target{
				{
					ExtraLabels: map[string]string{
						types.LabelMetaScrapeJob:      testTest1,
						types.LabelMetaScrapeInstance: testLocalhostPort,
					},
					URL: mustParse(testLocalhostMetricsURL),
				},
			},
		},
		{
			name:        "new",
			cfgFilename: "testdata/new-prometheus-targets.conf",
			want: []*scrapper.Target{
				{
					ExtraLabels: map[string]string{
						types.LabelMetaScrapeJob:      testTest1,
						types.LabelMetaScrapeInstance: testLocalhostPort,
					},
					URL: mustParse(testLocalhostMetricsURL),
				},
			},
		},
		{
			name:        "both",
			cfgFilename: "testdata/both-prometheus-targets.conf",
			want: []*scrapper.Target{
				{
					ExtraLabels: map[string]string{
						types.LabelMetaScrapeJob:      testNew,
						types.LabelMetaScrapeInstance: "new:9090",
					},
					URL: mustParse("http://new:9090/metrics"),
				},
				{
					ExtraLabels: map[string]string{
						types.LabelMetaScrapeJob:      testOld,
						types.LabelMetaScrapeInstance: testOldPort,
					},
					URL: mustParse("http://old:9090/metrics"),
				},
			},
		},
		{
			name:        "test-with-allow-deny",
			cfgFilename: "testdata/test-prometheus-targets.conf",
			want: []*scrapper.Target{
				{
					ExtraLabels: map[string]string{
						types.LabelMetaScrapeJob:      "use-global",
						types.LabelMetaScrapeInstance: testLocalhostPort,
					},
					URL: mustParse(testLocalhostMetricsURL),
				},
				{
					ExtraLabels: map[string]string{
						types.LabelMetaScrapeJob:      "reset-global",
						types.LabelMetaScrapeInstance: testLocalhostPort,
					},
					URL:       mustParse(testLocalhostMetricsURL),
					AllowList: []string{},
					DenyList:  []string{},
				},
				{
					ExtraLabels: map[string]string{
						types.LabelMetaScrapeJob:      "set-allow",
						types.LabelMetaScrapeInstance: testLocalhostPort,
					},
					AllowList: []string{"local2{item=~\"plop\"}"},
					URL:       mustParse(testLocalhostMetricsURL),
				},
				{
					ExtraLabels: map[string]string{
						types.LabelMetaScrapeJob:      "set-deny",
						types.LabelMetaScrapeInstance: testLocalhostPort,
					},
					DenyList: []string{"local1", "local2{item!~\"plop\"}"},
					URL:      mustParse(testLocalhostMetricsURL),
				},
				{
					ExtraLabels: map[string]string{
						types.LabelMetaScrapeJob:      "set-all",
						types.LabelMetaScrapeInstance: testLocalhostPort,
					},
					AllowList: []string{"hello", "world"},
					DenyList:  []string{"test"},
					URL:       mustParse(testLocalhostMetricsURL),
				},
				{
					ExtraLabels: map[string]string{
						types.LabelMetaScrapeJob:      testOld,
						types.LabelMetaScrapeInstance: testOldPort,
					},
					URL: mustParse("http://old:9090/metrics"),
				},
			},
		},
		{
			name:        "test-with-allow-deny-2",
			cfgFilename: "testdata/test-prometheus-targets.conf",
			want: []*scrapper.Target{
				{
					ExtraLabels: map[string]string{
						types.LabelMetaScrapeJob:      "use-global",
						types.LabelMetaScrapeInstance: testLocalhostPort,
					},
					URL: mustParse(testLocalhostMetricsURL),
				},
				{
					ExtraLabels: map[string]string{
						types.LabelMetaScrapeJob:      "reset-global",
						types.LabelMetaScrapeInstance: testLocalhostPort,
					},
					URL:       mustParse(testLocalhostMetricsURL),
					AllowList: []string{},
					DenyList:  []string{},
				},
				{
					ExtraLabels: map[string]string{
						types.LabelMetaScrapeJob:      "set-allow",
						types.LabelMetaScrapeInstance: testLocalhostPort,
					},
					AllowList: []string{"local2{item=~\"plop\"}"},
					URL:       mustParse(testLocalhostMetricsURL),
				},
				{
					ExtraLabels: map[string]string{
						types.LabelMetaScrapeJob:      "set-deny",
						types.LabelMetaScrapeInstance: testLocalhostPort,
					},
					DenyList: []string{"local1", "local2{item!~\"plop\"}"},
					URL:      mustParse(testLocalhostMetricsURL),
				},
				{
					ExtraLabels: map[string]string{
						types.LabelMetaScrapeJob:      "set-all",
						types.LabelMetaScrapeInstance: testLocalhostPort,
					},
					AllowList: []string{"hello", "world"},
					DenyList:  []string{"test"},
					URL:       mustParse(testLocalhostMetricsURL),
				},
				{
					ExtraLabels: map[string]string{
						types.LabelMetaScrapeJob:      testOld,
						types.LabelMetaScrapeInstance: testOldPort,
					},
					URL: mustParse("http://old:9090/metrics"),
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Ignore warnings, they are already tested in the config package.
			config, _, _, err := Load(false, false, tt.cfgFilename)
			if err != nil {
				t.Fatalf("Failed to load config: %v", err)
			}

			got, warnings := PrometheusConfigToURLs(config.Metric.Prometheus.Targets)
			if warnings != nil {
				t.Fatalf("Failed to convert config to Prometheus target: %v", warnings)
			}

			if diff := cmp.Diff(tt.want, got, cmpopts.IgnoreUnexported(scrapper.Target{})); diff != "" {
				t.Errorf("prometheusConfigToURLs() != want: %v", diff)
			}
		})
	}
}
