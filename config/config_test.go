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
	"math"
	"net/url"
	"strings"
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
			OpenTelemetry: OpenTelemetry{
				ShippingEnable:           true,
				ReceiversDefaultSendLogs: true,
				AutoDiscovery: AutoDiscovery{
					AllEnable:                 true,
					JournaldEnable:            true,
					SyslogEnable:              true,
					AuditdEnable:              true,
					ContainerAndServiceEnable: true,
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
				Receivers: map[string]LogReceiver{
					"filelog/recv": {
						"include": []any{"/var/log/apache/access.log", "/var/log/apache/error.log"},
						"operators": []any{
							map[string]any{
								keyType: "add",
								"field": "resource['service.name']",
								"value": "apache_server",
							},
						},
						"from_listeners": []any{"otlp"},
					},
					"apache_access": {
						"include": []any{"/var/log/apache/access.log"},
						"metrics": []any{
							map[string]any{
								"metric":     "apache_errors_count",
								"conditions": []any{`IsMatch(body, "\\[error\\]")`},
							},
						},
					},
					"redis": {
						"container_name": testRedis,
						"metrics": []any{
							map[string]any{
								"metric":     "redis_errors_count",
								"conditions": []any{`IsMatch(body, "` + testERROR + `")`},
							},
						},
					},
					"postgres": {
						"container_selectors": map[string]any{"app": "postgres"},
						"metrics": []any{
							map[string]any{
								"metric":     "postgres_errors_count",
								"conditions": []any{`IsMatch(body, "error")`},
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
		OpenTelemetry: OpenTelemetryConfig{
			NetworkListeners: map[string]NetworkListener{
				"otlp": {
					Protocols: NetworkProtocols{
						GRPC: &NetworkEndpoint{Endpoint: "localhost:4317"},
						HTTP: &NetworkEndpoint{Endpoint: "localhost:4318"},
					},
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

// TestMergeWithDefault tests that config files and environment variables are correctly merged with defaults.
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
	// Regression test: log.metrics_rules must be registered in mapKeys(), or file values get wiped by the empty default map.
	expectedConfig.Log.MetricsRules = map[string][]LogMetricEntry{
		"rule_a": {{"metric": "metric_a", "regex": "a"}},
		"rule_b": {{"metric": "metric_b", "regex": "b"}},
	}

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

func TestEffectiveAllowedLabelOverrides(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		container Container
		want      []string
	}{
		{
			name:      "empty allow-list with defaults",
			container: Container{AllowedLabelOverrides: nil, IncludeDefaultLabelOverrides: true},
			want:      DefaultAllowedLabelOverrides(),
		},
		{
			name:      "override replaces when defaults disabled",
			container: Container{AllowedLabelOverrides: []string{keyCheckCommand}, IncludeDefaultLabelOverrides: false},
			want:      []string{keyCheckCommand},
		},
		{
			name:      "override merged on top of defaults",
			container: Container{AllowedLabelOverrides: []string{keyCheckCommand}, IncludeDefaultLabelOverrides: true},
			want:      append([]string{keyCheckCommand}, DefaultAllowedLabelOverrides()...),
		},
		{
			name:      "empty allow-list without defaults",
			container: Container{AllowedLabelOverrides: nil, IncludeDefaultLabelOverrides: false},
			want:      []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.container.EffectiveAllowedLabelOverrides()
			if diff := cmp.Diff(tt.want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("EffectiveAllowedLabelOverrides() mismatch (-want +got)\n%s", diff)
			}
		})
	}
}

// legacyNetworkReceiverKey and legacyNetworkListenerKey expose migrateLegacyNetworkListeners' fixed
// generated names to test tables, mirroring legacyInputReceiverName's direct use elsewhere in this file.
func legacyNetworkReceiverKey() string {
	key, _ := legacyNetworkReceiverNames()

	return key
}

func legacyNetworkListenerKey() string {
	_, key := legacyNetworkReceiverNames()

	return key
}

// TestLoad tests loading the config and the warnings and errors returned.
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
				"testdata/bad_yaml.conf: invalid YAML: line 1, column 1: string was used where mapping is expected",
			},
		},
		{
			Name:  "invalid yaml multiple files",
			Files: []string{"testdata/invalid"},
			WantWarnings: []string{
				"testdata/invalid/10-invalid.conf: invalid YAML: line 2, column 1: found character '\t' that cannot start any token",
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
			Name:  "invalid yaml bad indentation",
			Files: []string{"testdata/bad_indentation.conf"},
			WantWarnings: []string{
				"testdata/bad_indentation.conf: invalid YAML: line 3, column 5: value is not allowed in this context. map key-value is pre-defined",
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
		// Guards against a regression where splitting one listener's protocols across two conf.d
		// files (file A sets grpc, file B sets http) lost file A's protocol: convertTypes' Config-struct
		// round trip fills in file B's unset "grpc" field as an explicit nil (same mechanism
		// dynamicEnvVarConfigKeys/pruneNilMapValues already guards for dynamic env vars, but pruning
		// used to be gated on provider == SourceEnv), so merge()'s fallback case (dst/src not both maps)
		// treated that invented nil as file B intentionally overwriting file A's grpc protocol --
		// dropping it, even though file B never mentioned grpc at all.
		{
			Name:  "network listener split across files survives merge",
			Files: []string{"testdata/split-listener-grpc.conf", "testdata/split-listener-http.conf"},
			WantConfig: Config{
				OpenTelemetry: OpenTelemetryConfig{
					NetworkListeners: map[string]NetworkListener{
						"otlp/my_custom": {
							Protocols: NetworkProtocols{
								GRPC: &NetworkEndpoint{Endpoint: "0.0.0.0:4317"},
								HTTP: &NetworkEndpoint{Endpoint: "0.0.0.0:4318"},
							},
						},
					},
				},
			},
		},
		// The legacy log.opentelemetry.grpc/http shape is a single flat scalar setting, just like in the
		// Fluent-Bit-era system it came from: it has no name of its own to key on, so
		// legacyNetworkReceiverNames always uses the same fixed names regardless of which file set it.
		// Two files each still using this shape therefore merge into one listener via the ordinary
		// multi-file config merge, last file wins per field -- there is no way, legacy or otherwise, to
		// end up with two independent listeners from it.
		{
			Name:  "legacy network listeners from two files merge into one, last file wins",
			Files: []string{"testdata/legacy-network-multifile-a.conf", "testdata/legacy-network-multifile-b.conf"},
			WantWarnings: []string{
				"testdata/legacy-network-multifile-a.conf: setting is deprecated: log.opentelemetry.grpc/http " +
					"{enable, address, port}, use opentelemetry.listeners + a log.opentelemetry.receivers entry's " +
					"from_listener field instead",
				"testdata/legacy-network-multifile-b.conf: setting is deprecated: log.opentelemetry.grpc/http " +
					"{enable, address, port}, use opentelemetry.listeners + a log.opentelemetry.receivers entry's " +
					"from_listener field instead",
			},
			WantConfig: Config{
				OpenTelemetry: OpenTelemetryConfig{
					NetworkListeners: map[string]NetworkListener{
						legacyNetworkListenerKey(): {
							Protocols: NetworkProtocols{
								GRPC: &NetworkEndpoint{Endpoint: "10.0.0.2:5002"},
							},
						},
					},
				},
				Log: Log{
					OpenTelemetry: OpenTelemetry{
						Receivers: map[string]LogReceiver{
							legacyNetworkReceiverKey(): {
								"from_listeners": []any{legacyNetworkListenerKey()},
								"send_logs":      true,
							},
						},
					},
				},
			},
		},
		// "grpc: null" enables grpc with the default endpoint, never disables it -- matches upstream OTel
		// collector's own otlpreceiver (see testdata/only_http_null.yaml). See
		// networkProtocolsNullMeansDefaultHookFunc.
		{
			Name:  "network listener grpc: null still enables grpc with the default endpoint",
			Files: []string{"testdata/network-listener-grpc-null-with-http.conf"},
			WantConfig: Config{
				OpenTelemetry: OpenTelemetryConfig{
					NetworkListeners: map[string]NetworkListener{
						"otlp/my_custom": {
							Protocols: NetworkProtocols{
								GRPC: &NetworkEndpoint{},
								HTTP: &NetworkEndpoint{Endpoint: "0.0.0.0:4318"},
							},
						},
					},
				},
			},
		},
		// Known gotcha: file A sets a real grpc endpoint, file B writes "grpc: null" for the same
		// listener hoping to disable it. Since null never disables (see above), it just resets the
		// endpoint to default via the ordinary last-file-wins merge rule -- grpc stays enabled. There's
		// no way to actually disable a protocol from a later file today; documented, not fixed.
		{
			Name:  "network listener grpc: null in a later file resets, not disables, an earlier file's endpoint",
			Files: []string{"testdata/network-listener-grpc-override-a.conf", "testdata/network-listener-grpc-override-b.conf"},
			WantConfig: Config{
				OpenTelemetry: OpenTelemetryConfig{
					NetworkListeners: map[string]NetworkListener{
						"otlp/my_custom": {
							Protocols: NetworkProtocols{
								GRPC: &NetworkEndpoint{},
								HTTP: &NetworkEndpoint{Endpoint: "0.0.0.0:4318"},
							},
						},
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

	// This subtest needs a slightly different setup than the other cases.
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
		// TODO: this should be true, or a warning should be raised.
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
		// Windows can't be tested from a unix Go runtime since path/filepath always uses /.
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

// TestCensorSecretItem tests per-item secret censoring, including blackbox secrets not named like a typical secret.
func TestCensorSecretItem(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		key   string
		value any
		want  any
	}{
		{
			name:  "blackbox bearer_token",
			key:   "blackbox.modules.mymod.http.bearer_token",
			value: "s3cr3t-token",
			want:  CensoredValue,
		},
		{
			name:  "blackbox bearer_token_file",
			key:   "blackbox.modules.mymod.http.bearer_token_file",
			value: "/etc/glouton/token",
			want:  CensoredValue,
		},
		{
			name:  "blackbox authorization credentials",
			key:   "blackbox.modules.mymod.http.authorization.credentials",
			value: "s3cr3t-creds",
			want:  CensoredValue,
		},
		{
			name:  "blackbox oauth2 client_secret",
			key:   "blackbox.modules.mymod.http.oauth2.client_secret",
			value: "s3cr3t",
			want:  CensoredValue,
		},
		{ //nolint:gosec
			name:  "proxy_url with credentials",
			key:   "blackbox.modules.mymod.http.proxy_url",
			value: "http://user:pass@proxy.example.com:3128",
			want:  "http://user:" + CensoredValue + "@proxy.example.com:3128",
		},
		{
			name:  "proxy_url without credentials is preserved",
			key:   "blackbox.modules.mymod.http.proxy_url",
			value: "http://proxy.example.com:3128",
			want:  "http://proxy.example.com:3128",
		},
		{
			name:  "empty secret is not censored",
			key:   "blackbox.modules.mymod.http.bearer_token",
			value: "",
			want:  "",
		},
		{
			name:  "non-secret string is preserved",
			key:   "blackbox.modules.mymod.http.method",
			value: "GET",
			want:  "GET",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := CensorSecretItem(tc.key, tc.value)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("CensorSecretItem(%q, %v): (-want +got)\n%s", tc.key, tc.value, diff)
			}
		})
	}
}

// TestCensorURLSecrets tests that URL userinfo credentials and secret-looking query parameters are redacted.
func TestCensorURLSecrets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		want  string
	}{
		{
			name:  "query secret key",
			value: "http://localhost:1234/path?key=s3cr3t&foo=bar",
			want:  "http://localhost:1234/path?key=" + CensoredValue + "&foo=bar",
		},
		{
			name:  "query secret token mixed case",
			value: "https://host/probe?Token=abc&q=1",
			want:  "https://host/probe?Token=" + CensoredValue + "&q=1",
		},
		{ //nolint:gosec
			name:  "userinfo and query secret combined",
			value: "http://user:pass@host:1234/p?api_key=xyz",
			want:  "http://user:" + CensoredValue + "@host:1234/p?api_key=" + CensoredValue,
		},
		{
			name:  "no secret query parameter is preserved",
			value: "http://localhost:1234/path?foo=bar&page=2",
			want:  "http://localhost:1234/path?foo=bar&page=2",
		},
		{
			name:  "non-url string is preserved",
			value: "not a url",
			want:  "not a url",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := CensorURLSecrets(tc.value); got != tc.want {
				t.Errorf("CensorURLSecrets(%q) = %q, want %q", tc.value, got, tc.want)
			}
		})
	}
}

// Test that two filters for the same metric within a single input don't trigger a false collision warning.
func TestMergeLegacyFiltersSameInputRepeatedMetric(t *testing.T) {
	t.Parallel()

	metricsByName := map[string]any{}

	filters := []any{
		map[string]any{"metric": "app_errors", "regex": "one"},
		map[string]any{"metric": "app_errors", "regex": "two"},
	}

	touched, warnings := mergeLegacyFilters(metricsByName, filters)
	if len(warnings) != 0 {
		t.Fatalf("Expected no warning for two filters of the same metric within one call, got %v", warnings)
	}

	if diff := cmp.Diff([]string{"app_errors"}, touched); diff != "" {
		t.Errorf("Unexpected touched metrics (-want +got):\n%s", diff)
	}

	entry := metricsByName["app_errors"].(map[string]any) //nolint:forcetypeassert

	wantConditions := []any{`IsMatch(body, "one")`, `IsMatch(body, "two")`}
	if diff := cmp.Diff(wantConditions, entry["conditions"]); diff != "" {
		t.Errorf("Unexpected conditions (-want +got):\n%s", diff)
	}

	if item, ok := entry["item"]; !ok || item != "" {
		t.Errorf("Expected item to always be explicitly set to \"\", got %v (present=%v)", item, ok)
	}

	if _, hasSources := entry["sources"]; hasSources {
		t.Errorf("Expected no \"sources\" key at all, got %v", entry["sources"])
	}
}

// Test that two separate inputs sharing a metric name get merged into one entry, warning once.
func TestMergeLegacyFiltersCrossInputCollisionMerges(t *testing.T) {
	t.Parallel()

	metricsByName := map[string]any{}

	touchedA, warningsA := mergeLegacyFilters(metricsByName, []any{map[string]any{"metric": "shared", "regex": "a"}})
	if len(warningsA) != 0 {
		t.Fatalf("Expected no warning for the first call to create the entry, got %v", warningsA)
	}

	if diff := cmp.Diff([]string{"shared"}, touchedA); diff != "" {
		t.Errorf("Unexpected touched metrics (-want +got):\n%s", diff)
	}

	touchedB, warningsB := mergeLegacyFilters(metricsByName, []any{map[string]any{"metric": "shared", "regex": "b"}})
	if len(warningsB) != 1 || !strings.Contains(warningsB[0].Error(), "merged into one shared") {
		t.Fatalf("Expected exactly one 'merged into one shared' warning, got %v", warningsB)
	}

	if diff := cmp.Diff([]string{"shared"}, touchedB); diff != "" {
		t.Errorf("Unexpected touched metrics (-want +got):\n%s", diff)
	}

	entry := metricsByName["shared"].(map[string]any) //nolint:forcetypeassert

	wantConditions := []any{`IsMatch(body, "a")`, `IsMatch(body, "b")`}
	if diff := cmp.Diff(wantConditions, entry["conditions"]); diff != "" {
		t.Errorf("Unexpected conditions (-want +got):\n%s", diff)
	}

	if item, ok := entry["item"]; !ok || item != "" {
		t.Errorf("Expected item to always be explicitly set to \"\", got %v (present=%v)", item, ok)
	}

	if _, hasSources := entry["sources"]; hasSources {
		t.Errorf("Expected no \"sources\" key at all, got %v", entry["sources"])
	}
}

func Test_migrate(t *testing.T) { //nolint:maintidx
	tests := []struct {
		Name                string
		ConfigFile          string
		WantConfig          Config
		WantWarning         bool
		WantWarningContains string
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
		{
			Name:       "legacy-log-inputs-path",
			ConfigFile: "testdata/legacy-log-inputs-path.conf",
			WantConfig: Config{
				Log: Log{
					OpenTelemetry: OpenTelemetry{
						Receivers: map[string]LogReceiver{
							legacyInputReceiverName("testdata/legacy-log-inputs-path.conf", 0): {
								"include":   []any{"/var/log/apache/access.log"},
								"send_logs": false,
								"metrics": []any{
									map[string]any{
										"metric":     "apache_errors_count",
										"item":       "",
										"conditions": []any{`IsMatch(body, "\\[error\\]")`},
									},
								},
							},
						},
					},
				},
			},
			WantWarning: true,
		},
		{
			Name:       "legacy-log-inputs-container-name",
			ConfigFile: "testdata/legacy-log-inputs-container-name.conf",
			WantConfig: Config{
				Log: Log{
					OpenTelemetry: OpenTelemetry{
						Receivers: map[string]LogReceiver{
							legacyInputReceiverName("testdata/legacy-log-inputs-container-name.conf", 0): {
								"container_name": testRedis,
								"send_logs":      false,
								"metrics": []any{
									map[string]any{
										"metric":     "redis_errors_count",
										"item":       "",
										"conditions": []any{`IsMatch(body, "ERROR")`},
									},
								},
							},
						},
					},
				},
			},
			WantWarning: true,
		},
		{
			Name:       "legacy-log-inputs-selectors",
			ConfigFile: "testdata/legacy-log-inputs-selectors.conf",
			WantConfig: Config{
				Log: Log{
					OpenTelemetry: OpenTelemetry{
						Receivers: map[string]LogReceiver{
							legacyInputReceiverName("testdata/legacy-log-inputs-selectors.conf", 0): {
								"container_selectors": map[string]any{"app": "postgres"},
								"send_logs":           false,
								"metrics": []any{
									map[string]any{
										"metric":     "postgres_errors_count",
										"item":       "",
										"conditions": []any{`IsMatch(body, "error")`},
									},
								},
							},
						},
					},
				},
			},
			WantWarning: true,
		},
		{
			Name:       "legacy-opentelemetry-network",
			ConfigFile: "testdata/legacy-opentelemetry-network.conf",
			WantConfig: Config{
				OpenTelemetry: OpenTelemetryConfig{
					NetworkListeners: map[string]NetworkListener{
						legacyNetworkListenerKey(): {
							Protocols: NetworkProtocols{
								GRPC: &NetworkEndpoint{Endpoint: "192.168.1.10:5000"},
							},
						},
					},
				},
				Log: Log{
					OpenTelemetry: OpenTelemetry{
						Receivers: map[string]LogReceiver{
							legacyNetworkReceiverKey(): {
								"from_listeners": []any{legacyNetworkListenerKey()},
								"send_logs":      true,
							},
						},
					},
				},
			},
			WantWarning: true,
		},
		{
			// Both protocols enabled at once, migrated into a single receiver.
			Name:       "legacy-network-both-protocols",
			ConfigFile: "testdata/legacy-network-both-protocols.conf",
			WantConfig: Config{
				OpenTelemetry: OpenTelemetryConfig{
					NetworkListeners: map[string]NetworkListener{
						legacyNetworkListenerKey(): {
							Protocols: NetworkProtocols{
								GRPC: &NetworkEndpoint{Endpoint: "10.0.0.5:9000"},
								HTTP: &NetworkEndpoint{Endpoint: "10.0.0.5:9001"},
							},
						},
					},
				},
				Log: Log{
					OpenTelemetry: OpenTelemetry{
						Receivers: map[string]LogReceiver{
							legacyNetworkReceiverKey(): {
								"from_listeners": []any{legacyNetworkListenerKey()},
								"send_logs":      true,
							},
						},
					},
				},
			},
			WantWarning: true,
		},
		{
			Name:        "legacy-network-disabled",
			ConfigFile:  "testdata/legacy-network-disabled.conf",
			WantConfig:  Config{},
			WantWarning: true,
		},
		{
			Name:       "legacy-log-inputs-unmatched",
			ConfigFile: "testdata/legacy-log-inputs-unmatched.conf",
			WantConfig: Config{
				Log: Log{
					OpenTelemetry: OpenTelemetry{
						Receivers: map[string]LogReceiver{
							legacyInputReceiverName("testdata/legacy-log-inputs-unmatched.conf", 0): {
								"include":   []any{"/var/log/apache/access.log"},
								"send_logs": false,
								"metrics": []any{
									map[string]any{
										"metric":     "apache_errors_count",
										"item":       "",
										"conditions": []any{`IsMatch(body, "\\[error\\]")`},
									},
								},
							},
						},
					},
				},
			},
			WantWarning:         true,
			WantWarningContains: "has filters but no path/container_name/container_selectors",
		},
		{
			Name:       "legacy-log-inputs-name-collision",
			ConfigFile: "testdata/legacy-log-inputs-name-collision.conf",
			WantConfig: Config{
				Log: Log{
					OpenTelemetry: OpenTelemetry{
						Receivers: map[string]LogReceiver{
							legacyInputReceiverName("testdata/legacy-log-inputs-name-collision.conf", 0): {
								"include":   []any{"/var/log/apache/access.log"},
								"send_logs": false,
								"metrics": []any{
									map[string]any{
										"metric": "shared_errors_count",
										"item":   "",
										"conditions": []any{
											`IsMatch(body, "\\[error\\]")`,
											`IsMatch(body, "ERROR")`,
										},
									},
								},
							},
							legacyInputReceiverName("testdata/legacy-log-inputs-name-collision.conf", 1): {
								"container_name": testRedis,
								"send_logs":      false,
								"metrics": []any{
									map[string]any{
										"metric": "shared_errors_count",
										"item":   "",
										"conditions": []any{
											`IsMatch(body, "\\[error\\]")`,
											`IsMatch(body, "ERROR")`,
										},
									},
								},
							},
						},
					},
				},
			},
			WantWarning:         true,
			WantWarningContains: "merged into one shared",
		},
		{
			Name:       "legacy-log-inputs-name-and-selectors",
			ConfigFile: "testdata/legacy-log-inputs-name-and-selectors.conf",
			WantConfig: Config{
				Log: Log{
					OpenTelemetry: OpenTelemetry{
						Receivers: map[string]LogReceiver{
							legacyInputReceiverName("testdata/legacy-log-inputs-name-and-selectors.conf", 0): {
								"container_name":      "postgres",
								"container_selectors": map[string]any{"env": "prod"},
								"send_logs":           false,
								"metrics": []any{
									map[string]any{
										"metric":     "postgres_errors_count",
										"item":       "",
										"conditions": []any{`IsMatch(body, "error")`},
									},
								},
							},
						},
					},
				},
			},
			WantWarning: true,
		},
		{
			// A hand-written log.metrics_rules entry that happens to share the (otherwise unused)
			// legacy-style name must be left untouched, and the migrated receiver must still get its
			// own inline metric built from its own filter -- not the hand-written rule's conditions.
			Name:       "legacy-log-inputs-metrics-rule-name-clash",
			ConfigFile: "testdata/legacy-log-inputs-metrics-rule-name-clash.conf",
			WantConfig: Config{
				Log: Log{
					OpenTelemetry: OpenTelemetry{
						Receivers: map[string]LogReceiver{
							legacyInputReceiverName("testdata/legacy-log-inputs-metrics-rule-name-clash.conf", 0): {
								"include":   []any{"/var/log/apache/access.log"},
								"send_logs": false,
								"metrics": []any{
									map[string]any{
										"metric":     "apache_errors_count",
										"item":       "",
										"conditions": []any{`IsMatch(body, "\\[error\\]")`},
									},
								},
							},
						},
					},
					MetricsRules: map[string][]LogMetricEntry{
						"legacy_log_inputs_metric_apache_errors_count": {
							{
								"metric": "apache_errors_count",
								"item":   "",
								"conditions": []any{
									`IsMatch(body, "totally unrelated pattern")`,
								},
							},
						},
					},
				},
			},
			WantWarning: true,
		},
		{
			// A malformed entry (not an object at all) is warned about and dropped, not kept around in
			// Config.Log -- there's nowhere left to put it now that Log.Inputs/LogInput no longer exist,
			// and nothing ever read that leftover data anyway.
			Name:                "legacy-log-inputs-malformed-entry",
			ConfigFile:          "testdata/legacy-log-inputs-malformed-entry.conf",
			WantConfig:          Config{},
			WantWarning:         true,
			WantWarningContains: "log.inputs[0] is not a valid entry",
		},
		{
			// An entry with no filters at all never did anything for log-to-metric, even before this
			// migration existed: warned about and dropped, same as the malformed-entry case above.
			Name:                "legacy-log-inputs-no-filters",
			ConfigFile:          "testdata/legacy-log-inputs-no-filters.conf",
			WantConfig:          Config{},
			WantWarning:         true,
			WantWarningContains: "log.inputs[0] has no filters",
		},
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			config, warnings, err := load(&configLoader{}, false, false, test.ConfigFile)
			if err != nil {
				t.Fatalf("Failed to load config: %s", err)
			}

			if test.WantWarning && warnings == nil {
				t.Fatal("Expected a deprecation warning, got none")
			}

			if test.WantWarningContains != "" && !strings.Contains(warnings.Error(), test.WantWarningContains) {
				t.Fatalf("Expected a warning containing %q, got: %s", test.WantWarningContains, warnings.Error())
			}

			if diff := compareConfig(test.WantConfig, config, cmpopts.EquateEmpty()); diff != "" {
				t.Fatalf("Unexpected config:\n%s", diff)
			}
		})
	}
}

// Test_loadWarnsNetworkListenerWithNoProtocol guards against an opentelemetry.listeners entry
// with an explicit empty "protocols: {}" (neither grpc nor http present at all) silently doing nothing
// once a receiver references it: Load() must warn about it up front, the same way it already warns about a
// log receiver with no selector. This must be a warning rather than a load error: Glouton should keep
// starting whenever possible, since misconfiguration is otherwise only visible from the Bleemeo panel. Note
// this is distinct from a "protocols:" block that DOES list grpc/http but with nothing under them (a bare
// key) -- see Test_loadBareProtocolKeyMeansEnabledWithDefaults: since networkProtocolsNullMeansDefaultHookFunc,
// that shape is enabled with default endpoints, not rejected.
func Test_loadWarnsNetworkListenerWithNoProtocol(t *testing.T) {
	t.Parallel()

	_, warnings, err := load(&configLoader{}, false, false, "testdata/network-listener-no-protocol.conf")
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if warnings == nil || !strings.Contains(warnings.Error(), errNetworkListenerNoProtocol.Error()) {
		t.Fatalf("Expected a warning containing %q, got: %v", errNetworkListenerNoProtocol, warnings)
	}
}

// Test_loadBareProtocolKeyMeansEnabledWithDefaults guards networkProtocolsNullMeansDefaultHookFunc: a bare
// "grpc:"/"http:" key (a YAML null value, the natural way to write "enable this with defaults" -- the same
// shorthand used throughout the rest of glouton.conf) must be enabled with the factory-default endpoint,
// matching how every upstream OTel collector receiver's own "protocols:" block already behaves. Without
// the hook, mapstructure treats "key present but null" the same as "key absent", so this would otherwise
// leave both *NetworkEndpoint fields nil and trip errNetworkListenerNoProtocol.
func Test_loadBareProtocolKeyMeansEnabledWithDefaults(t *testing.T) {
	t.Parallel()

	cfg, _, err := load(&configLoader{}, false, false, "testdata/network-listener-bare-protocol-keys.conf")
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	listener, ok := cfg.OpenTelemetry.NetworkListeners["otlp/my_custom"]
	if !ok {
		t.Fatal("Expected the otlp/my_custom listener to be present")
	}

	if listener.Protocols.GRPC == nil {
		t.Error("Expected a bare \"grpc:\" key to enable gRPC with the default endpoint, got nil")
	} else if listener.Protocols.GRPC.Endpoint != "" {
		t.Errorf("Expected an empty endpoint (factory default), got %q", listener.Protocols.GRPC.Endpoint)
	}

	if listener.Protocols.HTTP == nil {
		t.Error("Expected a bare \"http:\" key to enable HTTP with the default endpoint, got nil")
	} else if listener.Protocols.HTTP.Endpoint != "" {
		t.Errorf("Expected an empty endpoint (factory default), got %q", listener.Protocols.HTTP.Endpoint)
	}
}

// Test_loadWarnsEmptyContainerExcludeRule guards against a log.opentelemetry.container_exclude entry
// with neither container_name nor selectors set: MatchesContainerRule treats an unset field as a
// wildcard, so such an entry would otherwise silently veto every container from both log shipping and
// metrics container-label detection instead of the one container it was meant to match. This must be a
// warning rather than a load error: Glouton should keep starting whenever possible, since misconfiguration
// is otherwise only visible from the Bleemeo panel.
func Test_loadWarnsEmptyContainerExcludeRule(t *testing.T) {
	t.Parallel()

	_, warnings, err := load(&configLoader{}, false, false, "testdata/container-exclude-empty.conf")
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if warnings == nil || !strings.Contains(warnings.Error(), errContainerExcludeEmpty.Error()) {
		t.Fatalf("Expected a warning containing %q, got: %v", errContainerExcludeEmpty, warnings)
	}
}

// Test_loadWarnsDuplicateMetricEntryInReceiver guards against a receiver's metrics: list containing
// two verbatim-identical entries (same metric, conditions, and labels): since each metrics: entry gets
// its own countconnector (see otel/logmetrics's buildConnectors), two identical entries would both
// independently match and count every line, silently doubling the resulting series' value with no
// warning at all.
func Test_loadWarnsDuplicateMetricEntryInReceiver(t *testing.T) {
	t.Parallel()

	_, warnings, err := load(&configLoader{}, false, false, "testdata/log-metrics-duplicate-entry.conf")
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if warnings == nil || !strings.Contains(warnings.Error(), errDuplicateMetricEntry.Error()) {
		t.Fatalf("Expected a warning containing %q, got: %v", errDuplicateMetricEntry, warnings)
	}
}

// Test_loadWarnsDuplicateMetricEntryInMetricsRules is Test_loadWarnsDuplicateMetricEntryInReceiver's
// counterpart for a log.metrics_rules list, the other place a metrics: entry list can be declared.
func Test_loadWarnsDuplicateMetricEntryInMetricsRules(t *testing.T) {
	t.Parallel()

	_, warnings, err := load(&configLoader{}, false, false, "testdata/log-metrics-rules-duplicate-entry.conf")
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if warnings == nil || !strings.Contains(warnings.Error(), errDuplicateMetricEntry.Error()) {
		t.Fatalf("Expected a warning containing %q, got: %v", errDuplicateMetricEntry, warnings)
	}
}

// Test_migrateLoggingMigratesExplicitZero guards against a regression where migrateLogging used
// "k.Int(oldKey) == 0" to decide whether to migrate, which can't distinguish "key absent" from
// "explicitly set to 0" -- an explicit logging.buffer.tail_size/head_size: 0 was silently left unmigrated,
// leaking a confusing "invalid keys" warning instead of a clean deprecation notice, and never reaching
// tail_size_bytes/head_size_bytes.
func Test_migrateLoggingMigratesExplicitZero(t *testing.T) {
	t.Parallel()

	_, warnings, err := load(&configLoader{}, false, false, "testdata/old-logging-explicit-zero.conf")
	if err != nil {
		t.Fatalf("Failed to load config: %s", err)
	}

	if warnings == nil ||
		!strings.Contains(warnings.Error(), "logging.buffer.tail_size") ||
		!strings.Contains(warnings.Error(), "logging.buffer.head_size") {
		t.Fatalf("Expected deprecation warnings mentioning both tail_size and head_size, got: %v", warnings)
	}

	// The real signal that the explicit 0 was actually migrated (not just coincidentally left at its
	// zero value either way): the old key must be gone by the final decode, or ErrorUnused would leak an
	// "invalid keys" warning here exactly like the delete()-no-op bug does for the legacy grpc/http shape.
	if strings.Contains(warnings.Error(), "invalid keys") {
		t.Fatalf("Expected no leaked \"invalid keys\" warning (old key left behind unmigrated), got: %s", warnings.Error())
	}
}

// Test_migrateLegacyNetworkListenersNoInvalidKeysLeak guards against a regression where
// migrateLegacyNetworkListeners' delete() calls targeted "log.opentelemetry.grpc"/".http" -- keys that
// never exist in k.All()'s flat, dot-joined map (only their leaves .enable/.address/.port do) -- making
// the deletes no-ops. The leaked leaf keys then tripped the final decode's ErrorUnused check, producing a
// confusing "invalid keys" warning on every single legitimate use of this legacy shape, alongside the
// intended deprecation notice.
func Test_migrateLegacyNetworkListenersNoInvalidKeysLeak(t *testing.T) {
	t.Parallel()

	_, warnings, err := load(&configLoader{}, false, false, "testdata/legacy-opentelemetry-network.conf")
	if err != nil {
		t.Fatalf("Failed to load config: %s", err)
	}

	if warnings == nil {
		t.Fatal("Expected the deprecation warning, got none")
	}

	if strings.Contains(warnings.Error(), "invalid keys") {
		t.Fatalf("Expected no leaked \"invalid keys\" warning, got: %s", warnings.Error())
	}
}

// Test_migrateLegacyNetworkListenersFlatKeys guards against a regression where
// migrateLegacyNetworkListeners detected the legacy grpc/http shape via k.Get(path+".grpc").(map[string]any)
// -- a lookup for an intermediate tree node, which koanf only builds when the source YAML itself nests
// "grpc"/"http" (as in legacy-opentelemetry-network.conf). The equally valid, and more common, conf.d
// style of writing "log.opentelemetry.grpc.enable: true" as one flat, dot-joined key is stored by koanf
// as a single opaque key: k.Get on the parent path silently returned nil, so the migration never fired at
// all, and the legacy keys survived to trip the final decode's "invalid keys" warning with the listener
// never migrated. Uses the real, public Load() so the final decoded Config is checked, not just warnings.
func Test_migrateLegacyNetworkListenersFlatKeys(t *testing.T) {
	t.Parallel()

	cfg, _, warnings, err := Load(true, false, "testdata/legacy-opentelemetry-network-flat.conf")
	if err != nil {
		t.Fatalf("Load returned an error: %v", err)
	}

	if warnings == nil || !strings.Contains(warnings.Error(), errSettingsDeprecated.Error()) {
		t.Fatalf("Expected the deprecation warning, got: %v", warnings)
	}

	if strings.Contains(warnings.Error(), "invalid keys") {
		t.Fatalf("Expected no leaked \"invalid keys\" warning, got: %s", warnings.Error())
	}

	receiverKey, listenerKey := legacyNetworkReceiverNames()

	listener, ok := cfg.OpenTelemetry.NetworkListeners[listenerKey]
	if !ok {
		t.Fatalf("Expected a %q network listener, got %v", listenerKey, cfg.OpenTelemetry.NetworkListeners)
	}

	if listener.Protocols.GRPC == nil || listener.Protocols.GRPC.Endpoint != "192.168.1.10:5000" {
		t.Errorf("Expected the flat grpc.address/grpc.port to survive as the GRPC endpoint, got %+v", listener.Protocols)
	}

	if listener.Protocols.HTTP != nil {
		t.Errorf("Expected no HTTP protocol (http.enable: false), got %+v", listener.Protocols)
	}

	receiver, ok := cfg.Log.OpenTelemetry.Receivers[receiverKey]
	if !ok {
		t.Fatalf("Expected a %q log receiver, got %v", receiverKey, cfg.Log.OpenTelemetry.Receivers)
	}

	fromListeners, _ := receiver["from_listeners"].([]any)
	if len(fromListeners) != 1 || fromListeners[0] != listenerKey {
		t.Errorf("Expected from_listeners: [%q], got %v", listenerKey, receiver["from_listeners"])
	}
}

// Test_migrateKeepsFlatSiblingsOfSynthesizedEntries guards against a regression where a migration wrote
// its synthesized entry straight into config[parentKey] while the user's own entries for that same parent
// were still sitting in migrate()'s flat map as dotted leaves. Both then reached the final confmap load,
// whose maps.Unflatten walks the map in Go's randomized order: whichever landed last replaced the other's
// whole subtree, so the user's receiver/listener vanished on roughly 4 starts out of 5 and came back on
// the others. Loops because a single load could pass on map order alone.
func Test_migrateKeepsFlatSiblingsOfSynthesizedEntries(t *testing.T) {
	t.Parallel()

	receiverKey, listenerKey := legacyNetworkReceiverNames()

	for range 30 {
		cfg, _, _, err := Load(true, false, "testdata/legacy-network-flat-sibling-receiver.conf")
		if err != nil {
			t.Fatalf("Load returned an error: %v", err)
		}

		// The user's own flat-spelled entries.
		if _, ok := cfg.Log.OpenTelemetry.Receivers["myrecv"]; !ok {
			t.Fatalf("the user's flat-spelled receiver was dropped, got %v", cfg.Log.OpenTelemetry.Receivers)
		}

		if _, ok := cfg.OpenTelemetry.NetworkListeners["mine"]; !ok {
			t.Fatalf("the user's flat-spelled listener was dropped, got %v", cfg.OpenTelemetry.NetworkListeners)
		}

		// ... alongside, not instead of, what the migration synthesized.
		if _, ok := cfg.Log.OpenTelemetry.Receivers[receiverKey]; !ok {
			t.Fatalf("the synthesized receiver was dropped, got %v", cfg.Log.OpenTelemetry.Receivers)
		}

		if _, ok := cfg.OpenTelemetry.NetworkListeners[listenerKey]; !ok {
			t.Fatalf("the synthesized listener was dropped, got %v", cfg.OpenTelemetry.NetworkListeners)
		}
	}
}

// Test_loadNetworkListenerSurvivesDefaultMerge guards against a regression where
// "opentelemetry.listeners" was missing from default.go's mapKeys(), so DefaultConfig()'s empty
// map for that field and a real config file's nested entries landed as separate flat keys under the same
// prefix -- and koanf's tree-building let the shorter (default, empty) key silently clobber the deeper
// (file, populated) one once merged, wiping out any configured listeners entirely. Must use the
// real, public Load() (withDefault=true) to reproduce: the internal load() with withDefault=false doesn't
// merge in the default map at all, so it can't catch this class of bug.
func Test_loadNetworkListenerSurvivesDefaultMerge(t *testing.T) {
	t.Parallel()

	cfg, _, warnings, err := Load(true, false, "testdata/network-listener-survives-defaults.conf")
	if err != nil {
		t.Fatalf("Load returned an error: %v", err)
	}

	if warnings != nil {
		t.Fatalf("Expected no warnings, got: %v", warnings)
	}

	listener, ok := cfg.OpenTelemetry.NetworkListeners["otlp"]
	if !ok {
		t.Fatalf("Expected an %q network listener to survive loading with defaults, got %v", "otlp", cfg.OpenTelemetry.NetworkListeners)
	}

	if listener.Protocols.GRPC == nil || listener.Protocols.GRPC.Endpoint != "127.0.0.1:9999" {
		t.Errorf("Expected the configured GRPC endpoint to survive, got %+v", listener.Protocols)
	}
}

// Test_loadDynamicListenerEnv guards resolveDynamicEnvKey and its interaction with the loader's
// merge-priority logic: a GLOUTON_OPENTELEMETRY_LISTENERS_<name>_PROTOCOLS_GRPC/HTTP_ENDPOINT variable must
// only overwrite that single leaf, not wholesale-replace the whole opentelemetry.listeners map (which
// would silently drop every other listener, and every other field of the targeted listener, loaded from a
// config file), and must be able to create a listener that doesn't exist in any file.
func Test_loadDynamicListenerEnv(t *testing.T) {
	t.Setenv("GLOUTON_OPENTELEMETRY_LISTENERS_otlp_PROTOCOLS_GRPC_ENDPOINT", "0.0.0.0:1")
	t.Setenv("GLOUTON_OPENTELEMETRY_LISTENERS_foo_bar_PROTOCOLS_HTTP_ENDPOINT", "0.0.0.0:2")

	cfg, _, warnings, err := Load(true, true, "testdata/network-listener-dynamic-env.conf")
	if err != nil {
		t.Fatalf("Load returned an error: %v", err)
	}

	if warnings != nil {
		t.Fatalf("Expected no warnings, got: %v", warnings)
	}

	otlp, ok := cfg.OpenTelemetry.NetworkListeners["otlp"]
	if !ok {
		t.Fatalf("Expected the %q listener to still exist, got %v", "otlp", cfg.OpenTelemetry.NetworkListeners)
	}

	if otlp.Protocols.GRPC == nil || otlp.Protocols.GRPC.Endpoint != "0.0.0.0:1" {
		t.Errorf("Expected the environment variable to override otlp's GRPC endpoint, got %+v", otlp.Protocols)
	}

	if otlp.Protocols.HTTP == nil || otlp.Protocols.HTTP.Endpoint != "127.0.0.1:4318" {
		t.Errorf("Expected otlp's file-configured HTTP endpoint to survive untouched, got %+v", otlp.Protocols)
	}

	other, ok := cfg.OpenTelemetry.NetworkListeners["other"]
	if !ok {
		t.Fatalf("Expected the %q listener (untouched by any environment variable) to survive, got %v", "other", cfg.OpenTelemetry.NetworkListeners)
	}

	if other.Protocols.GRPC == nil || other.Protocols.GRPC.Endpoint != "127.0.0.1:9999" {
		t.Errorf("Expected other's file-configured GRPC endpoint to survive untouched, got %+v", other.Protocols)
	}

	fooBar, ok := cfg.OpenTelemetry.NetworkListeners["foo_bar"]
	if !ok {
		t.Fatalf("Expected a new %q listener to be created from the environment alone, got %v", "foo_bar", cfg.OpenTelemetry.NetworkListeners)
	}

	if fooBar.Protocols.GRPC != nil {
		t.Errorf("Expected foo_bar's GRPC endpoint to remain unset, got %+v", fooBar.Protocols)
	}

	if fooBar.Protocols.HTTP == nil || fooBar.Protocols.HTTP.Endpoint != "0.0.0.0:2" {
		t.Errorf("Expected the environment variable to set foo_bar's HTTP endpoint, got %+v", fooBar.Protocols)
	}
}

// Test_loadDynamicListenerEnvIgnoresMalformed guards resolveDynamicEnvKey against two inputs that look
// intentional (same GLOUTON_OPENTELEMETRY_LISTENERS_ prefix) but aren't valid: no listener name between the
// prefix and the suffix, and a suffix that isn't one of the two known protocol endpoints. Both must be
// silently ignored, consistent with how any other unrecognized GLOUTON_ variable is already treated.
func Test_loadDynamicListenerEnvIgnoresMalformed(t *testing.T) {
	t.Setenv("GLOUTON_OPENTELEMETRY_LISTENERS__PROTOCOLS_GRPC_ENDPOINT", "0.0.0.0:1")
	t.Setenv("GLOUTON_OPENTELEMETRY_LISTENERS_otlp_PROTOCOLS_UDP_ENDPOINT", "0.0.0.0:2")

	cfg, _, warnings, err := Load(true, true, "testdata/network-listener-survives-defaults.conf")
	if err != nil {
		t.Fatalf("Load returned an error: %v", err)
	}

	if warnings != nil {
		t.Fatalf("Expected no warnings, got: %v", warnings)
	}

	if got := len(cfg.OpenTelemetry.NetworkListeners); got != 1 {
		t.Fatalf("Expected only the file-configured listener to exist, got %d: %v", got, cfg.OpenTelemetry.NetworkListeners)
	}

	otlp := cfg.OpenTelemetry.NetworkListeners["otlp"]
	if otlp.Protocols.GRPC == nil || otlp.Protocols.GRPC.Endpoint != "127.0.0.1:9999" {
		t.Errorf("Expected the malformed environment variables to be ignored, got %+v", otlp.Protocols)
	}
}

// Test_loadDynamicThresholdEnv is the thresholds counterpart of Test_loadDynamicListenerEnv: a
// GLOUTON_THRESHOLDS_<metric>_LOW_WARNING/LOW_CRITICAL/HIGH_WARNING/HIGH_CRITICAL variable must only
// overwrite that single leaf, not wholesale-replace the whole thresholds map or the other fields of the
// targeted metric's entry, and must be able to create a threshold entry for a metric that doesn't exist in
// any file.
func Test_loadDynamicThresholdEnv(t *testing.T) {
	t.Setenv("GLOUTON_THRESHOLDS_cpu_used_HIGH_CRITICAL", "95")
	t.Setenv("GLOUTON_THRESHOLDS_mem_used_LOW_WARNING", "10")

	cfg, _, warnings, err := Load(true, true, "testdata/threshold-dynamic-env.conf")
	if err != nil {
		t.Fatalf("Load returned an error: %v", err)
	}

	if warnings != nil {
		t.Fatalf("Expected no warnings, got: %v", warnings)
	}

	cpuUsed, ok := cfg.Thresholds["cpu_used"]
	if !ok {
		t.Fatalf("Expected the %q threshold to still exist, got %v", "cpu_used", cfg.Thresholds)
	}

	if cpuUsed.HighCritical == nil || *cpuUsed.HighCritical != 95 {
		t.Errorf("Expected the environment variable to override cpu_used's high_critical, got %+v", cpuUsed)
	}

	if cpuUsed.LowWarning == nil || *cpuUsed.LowWarning != 2 ||
		cpuUsed.LowCritical == nil || *cpuUsed.LowCritical != 1.5 ||
		cpuUsed.HighWarning == nil || *cpuUsed.HighWarning != 80.2 {
		t.Errorf("Expected cpu_used's other file-configured fields to survive untouched, got %+v", cpuUsed)
	}

	diskUsed, ok := cfg.Thresholds["disk_used"]
	if !ok {
		t.Fatalf("Expected the %q threshold (untouched by any environment variable) to survive, got %v", "disk_used", cfg.Thresholds)
	}

	if diskUsed.LowCritical == nil || *diskUsed.LowCritical != 2 || diskUsed.HighWarning == nil || *diskUsed.HighWarning != 90.5 {
		t.Errorf("Expected disk_used's file-configured fields to survive untouched, got %+v", diskUsed)
	}

	memUsed, ok := cfg.Thresholds["mem_used"]
	if !ok {
		t.Fatalf("Expected a new %q threshold to be created from the environment alone, got %v", "mem_used", cfg.Thresholds)
	}

	if memUsed.LowWarning == nil || *memUsed.LowWarning != 10 {
		t.Errorf("Expected the environment variable to set mem_used's low_warning, got %+v", memUsed)
	}

	if memUsed.LowCritical != nil || memUsed.HighWarning != nil || memUsed.HighCritical != nil {
		t.Errorf("Expected mem_used's other fields to remain unset, got %+v", memUsed)
	}
}

// Test_loadDynamicThresholdEnvIgnoresMalformed is the thresholds counterpart of
// Test_loadDynamicListenerEnvIgnoresMalformed: an empty metric name and an unrecognized suffix must both
// be silently ignored.
func Test_loadDynamicThresholdEnvIgnoresMalformed(t *testing.T) {
	t.Setenv("GLOUTON_THRESHOLDS__HIGH_WARNING", "1")
	t.Setenv("GLOUTON_THRESHOLDS_cpu_used_MEDIUM_WARNING", "2")

	cfg, _, warnings, err := Load(true, true, "testdata/threshold-survives-defaults.conf")
	if err != nil {
		t.Fatalf("Load returned an error: %v", err)
	}

	if warnings != nil {
		t.Fatalf("Expected no warnings, got: %v", warnings)
	}

	if got := len(cfg.Thresholds); got != 1 {
		t.Fatalf("Expected only the file-configured threshold to exist, got %d: %v", got, cfg.Thresholds)
	}

	cpuUsed := cfg.Thresholds["cpu_used"]
	if cpuUsed.HighCritical == nil || *cpuUsed.HighCritical != 90 {
		t.Errorf("Expected the malformed environment variables to be ignored, got %+v", cpuUsed)
	}

	if cpuUsed.LowWarning != nil || cpuUsed.LowCritical != nil || cpuUsed.HighWarning != nil {
		t.Errorf("Expected the malformed environment variables not to add any field, got %+v", cpuUsed)
	}
}

// Test_resolveDynamicEnvKey unit-tests resolveDynamicEnvKey directly, without going through the whole
// Load() pipeline: correct key construction for both known suffixes, a name containing underscores, and
// rejection of an unrelated variable, an unknown suffix, and an empty listener name.
func Test_resolveDynamicEnvKey(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		env     string
		wantKey string
		wantOK  bool
	}{
		{"grpc endpoint", "GLOUTON_OPENTELEMETRY_LISTENERS_otlp_PROTOCOLS_GRPC_ENDPOINT", "opentelemetry.listeners.otlp.protocols.grpc.endpoint", true},
		{"http endpoint", "GLOUTON_OPENTELEMETRY_LISTENERS_otlp_PROTOCOLS_HTTP_ENDPOINT", "opentelemetry.listeners.otlp.protocols.http.endpoint", true},
		{"name with underscores", "GLOUTON_OPENTELEMETRY_LISTENERS_foo_bar_PROTOCOLS_GRPC_ENDPOINT", "opentelemetry.listeners.foo_bar.protocols.grpc.endpoint", true},
		{"unrelated variable", "GLOUTON_WEB_ENABLE", "", false},
		{"unknown suffix", "GLOUTON_OPENTELEMETRY_LISTENERS_otlp_PROTOCOLS_UDP_ENDPOINT", "", false},
		{"empty name", "GLOUTON_OPENTELEMETRY_LISTENERS__PROTOCOLS_GRPC_ENDPOINT", "", false},
		{"threshold low_warning", "GLOUTON_THRESHOLDS_cpu_used_LOW_WARNING", "thresholds.cpu_used.low_warning", true},
		{"threshold high_critical", "GLOUTON_THRESHOLDS_cpu_used_HIGH_CRITICAL", "thresholds.cpu_used.high_critical", true},
		{"threshold unknown suffix", "GLOUTON_THRESHOLDS_cpu_used_MEDIUM_WARNING", "", false},
		{"threshold empty name", "GLOUTON_THRESHOLDS__LOW_WARNING", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			key, ok := resolveDynamicEnvKey(tc.env)
			if ok != tc.wantOK {
				t.Fatalf("resolveDynamicEnvKey(%q) ok = %v, want %v", tc.env, ok, tc.wantOK)
			}

			if key != tc.wantKey {
				t.Errorf("resolveDynamicEnvKey(%q) = %q, want %q", tc.env, key, tc.wantKey)
			}
		})
	}
}

// Test_resolveDynamicEnvKeyTriesEveryEntry is a regression test for a bug introduced while generalizing
// resolveDynamicEnvKey from a single hardcoded listener case into a loop over dynamicEnvVarList: the loop
// used to `return "", false` as soon as ONE entry's prefix didn't match the input, instead of trying the
// next entry, which made every dynamicEnvVar registered after the first one silently unreachable.
// Temporarily appends a second, unrelated synthetic entry to dynamicEnvVarList and checks that a variable
// matching only that second entry (and not the first) still resolves -- this must keep passing however
// many entries dynamicEnvVarList grows to.
func Test_resolveDynamicEnvKeyTriesEveryEntry(t *testing.T) {
	original := dynamicEnvVarList
	dynamicEnvVarList = append(append([]dynamicEnvVar{}, original...), dynamicEnvVar{
		envPrefix:    "GLOUTON_SOME_OTHER_MAP_",
		configPrefix: "some.other.map.",
		suffixes: map[string]string{
			"_VALUE": "value",
		},
	})

	t.Cleanup(func() { dynamicEnvVarList = original })

	key, ok := resolveDynamicEnvKey("GLOUTON_SOME_OTHER_MAP_myentry_VALUE")
	if !ok {
		t.Fatal("Expected a variable matching the second dynamicEnvVar entry to resolve, got ok=false -- " +
			"the loop is probably bailing out on the first entry's prefix mismatch instead of trying the next one")
	}

	if want := "some.other.map.myentry.value"; key != want {
		t.Errorf("resolveDynamicEnvKey = %q, want %q", key, want)
	}
}

// Test_dynamicEnvVarConfigKeysCoversNewEntries guards the point of dynamicEnvVarConfigKeys existing at
// all: loader.go's merge-priority and nil-pruning special cases must apply to ANY dynamicEnvVarList entry,
// not just opentelemetry.listeners, without loader.go needing to be touched again when a new entry is
// added. Appends a synthetic second entry and checks that priority() grants its config key the
// map-merging priority (instead of the env-always-wins priority a plain scalar env var would get) purely
// because it's present in dynamicEnvVarList.
func Test_dynamicEnvVarConfigKeysCoversNewEntries(t *testing.T) {
	original := dynamicEnvVarList
	dynamicEnvVarList = append(append([]dynamicEnvVar{}, original...), dynamicEnvVar{
		envPrefix:    "GLOUTON_SOME_OTHER_MAP_",
		configPrefix: "some.other.map.",
		suffixes: map[string]string{
			"_VALUE": "value",
		},
	})

	t.Cleanup(func() { dynamicEnvVarList = original })

	dynamicKeys := dynamicEnvVarConfigKeys()
	if !dynamicKeys["some.other.map"] {
		t.Fatalf("Expected %q to be derived from the synthetic entry's configPrefix, got %v", "some.other.map", dynamicKeys)
	}

	// priorityMapAndArrayFile and priorityEnv are unexported consts local to priority() in loader.go
	// (1 and math.MaxInt32 respectively); mirrored here since they aren't reachable from the test.
	const priorityMapAndArrayFile = 1

	got := priority(SourceEnv, "some.other.map", map[string]any{"myentry": map[string]any{"value": "x"}}, 0, dynamicKeys)
	if got != priorityMapAndArrayFile {
		t.Errorf("priority(SourceEnv, %q, ...) = %d, want %d (priorityMapAndArrayFile) -- a new dynamicEnvVarList "+
			"entry's config key isn't getting the merge treatment automatically", "some.other.map", got, priorityMapAndArrayFile)
	}

	// A plain, unrelated env-sourced key must still get the ordinary env-always-wins priority.
	if got := priority(SourceEnv, "web.enable", true, 0, dynamicKeys); got != math.MaxInt32 {
		t.Errorf("priority(SourceEnv, %q, ...) = %d, want %d (priorityEnv)", "web.enable", got, math.MaxInt32)
	}
}

// Test_dynamicEnvVarListKeysAreInMapKeys guards the link between dynamicEnvVarList (config.go) and
// default.go's mapKeys(): a prefix key missing from mapKeys() stays a set of flat "key.sub.field" entries
// that dynamicEnvVarConfigKeys never matches, so its dynamic env vars stop merging correctly (the
// whole-map-replacement bug the mechanism exists to prevent) without failing anything else.
// Test_nilPrunedConfigKeys pins the set of map keys that get their invented nils pruned. It is derived
// from the Config types (every mapKeys() entry that is a map of structs), rather than hand-listed or
// borrowed from dynamicEnvVarConfigKeys(): the two coincide today, but one is about environment variables
// and this one is about a struct's unset pointer fields round-tripping as explicit nils. A new
// struct-valued map key must show up here on its own, or merge() would silently drop a sibling field an
// earlier-loaded file set. A map of scalars/slices/raw map[string]any has no such fields and must stay
// out, so an explicit null the user wrote there survives.
func Test_nilPrunedConfigKeys(t *testing.T) {
	t.Parallel()

	want := map[string]bool{
		keyThresholds:             true, // map[string]Threshold
		"opentelemetry.listeners": true, // map[string]NetworkListener
	}

	if diff := cmp.Diff(want, nilPrunedConfigKeys()); diff != "" {
		t.Errorf("Unexpected pruned key set (-want +got):\n%s\n"+
			"If you added a struct-valued map key to mapKeys(), add it here too. If a key unexpectedly "+
			"dropped out, pruning silently stopped protecting it.", diff)
	}

	// Every entry must resolve against the Config types, or it would be skipped silently.
	for _, key := range mapKeys() {
		if _, found := configFieldTypeByPath(key); !found {
			t.Errorf("mapKeys() entry %q does not resolve to a Config field: a typo here disables both "+
				"nil-pruning and any other Config-type-derived check for that key", key)
		}
	}
}

func Test_dynamicEnvVarListKeysAreInMapKeys(t *testing.T) {
	t.Parallel()

	known := make(map[string]bool, len(mapKeys()))
	for _, k := range mapKeys() {
		known[k] = true
	}

	for key := range dynamicEnvVarConfigKeys() {
		if !known[key] {
			t.Errorf("dynamicEnvVarList has an entry for %q, but %q is missing from default.go's mapKeys() -- "+
				"its dynamic env vars will silently fail to merge correctly", key, key)
		}
	}
}

// Test_migrateLogFluentBitURL guards against log.fluentbit_url (dropped by the OpenTelemetry log rewrite, but
// still set by upgrading installs via the bleemeo-agent-logs package override) failing config load as an
// unknown key instead of being silently deprecated like other removed settings.
func Test_migrateLogFluentBitURL(t *testing.T) {
	t.Parallel()

	config, warnings, err := load(&configLoader{}, false, false, "testdata/legacy-log-fluentbit-url.conf")
	if err != nil {
		t.Fatalf("Failed to load config: %s", err)
	}

	if warnings == nil || !strings.Contains(warnings.Error(), "log.fluentbit_url") {
		t.Fatalf("Expected a deprecation warning mentioning log.fluentbit_url, got: %v", warnings)
	}

	if diff := compareConfig(Config{}, config, cmpopts.EquateEmpty()); diff != "" {
		t.Fatalf("Expected fluentbit_url to be dropped with no other effect on config:\n%s", diff)
	}
}

// Test_migrateLogInputs_multiFileNoCollision guards against a regression where each config file's log.inputs
// migration restarted its "legacy_input_%d" naming from 0, so two conf.d files each declaring one log.inputs
// entry produced the same receiver key and the last-loaded one silently discarded the other's receiver.
func Test_migrateLogInputs_multiFileNoCollision(t *testing.T) {
	t.Parallel()

	fileA := "testdata/legacy-log-inputs-multifile-a.conf"
	fileB := "testdata/legacy-log-inputs-multifile-b.conf"

	config, _, err := load(&configLoader{}, false, false, fileA, fileB)
	if err != nil {
		t.Fatalf("Failed to load config: %s", err)
	}

	if got := len(config.Log.OpenTelemetry.Receivers); got != 2 {
		t.Fatalf("Expected 2 distinct receivers (one per file), got %d: %v", got, config.Log.OpenTelemetry.Receivers)
	}

	nameA := legacyInputReceiverName(fileA, 0)
	nameB := legacyInputReceiverName(fileB, 0)

	if nameA == nameB {
		t.Fatalf("Expected distinct receiver names for distinct files, both computed %q", nameA)
	}

	if _, ok := config.Log.OpenTelemetry.Receivers[nameA]; !ok {
		t.Errorf("Missing receiver %q from %s", nameA, fileA)
	}

	if _, ok := config.Log.OpenTelemetry.Receivers[nameB]; !ok {
		t.Errorf("Missing receiver %q from %s", nameB, fileB)
	}

	for name, wantMetric := range map[string]string{nameA: "redis_errors_count", nameB: "nginx_errors_count"} {
		receiver, ok := config.Log.OpenTelemetry.Receivers[name]
		if !ok {
			continue // already reported above
		}

		metrics, _ := receiver["metrics"].([]any)
		if len(metrics) != 1 {
			t.Fatalf("Expected exactly one inline metric for receiver %q, got %v", name, metrics)
		}

		entry, _ := metrics[0].(map[string]any)
		if entry["metric"] != wantMetric {
			t.Errorf("Expected receiver %q to define metric %q, got %v", name, wantMetric, entry)
		}
	}
}

// Test_migrateLogInputs_warnsEvenWhenNoEntryTranslates guards against a regression where
// migrateLogInputs discarded every warning already appended to its local slice (e.g. the
// "filters but no path/container_name/container_selectors" one) by returning a hardcoded nil
// whenever no log.inputs entry actually got converted into a receiver -- losing the warning
// whenever the untranslatable entry was the only one, instead of only when there was nothing to warn
// about at all.
func Test_migrateLogInputs_warnsEvenWhenNoEntryTranslates(t *testing.T) {
	t.Parallel()

	_, warnings, err := load(&configLoader{}, false, false, "testdata/legacy-log-inputs-no-target.conf")
	if err != nil {
		t.Fatalf("Failed to load config: %s", err)
	}

	if warnings == nil || !strings.Contains(warnings.Error(), "log.inputs[0] has filters but no path/container_name/container_selectors") {
		t.Fatalf("Expected a warning about the untranslatable log.inputs entry, got: %v", warnings)
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
