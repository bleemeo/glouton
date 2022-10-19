package config2

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// TestMerge tests that config files are merged correctly.
// Merge should override existing values, merge maps and concatenate arrays.
func TestMerge(t *testing.T) {
	k, warnings, err := load(false, "testdata/merge1.conf", "testdata/merge2.conf")
	if warnings != nil {
		t.Fatalf("Warning while loading config: %s", err)
	}

	if err != nil {
		t.Error(err)
	}

	cases := []struct {
		Key  string
		Want string
	}{
		{Key: "d1", Want: "1"},
		{Key: "d2", Want: "2"},
		{Key: "replaced", Want: "2"},
		{Key: "dict.d1", Want: "1"},
		{Key: "dict.d2", Want: "2"},
		{Key: "dict.replaced", Want: "2"},
		{Key: "arr", Want: "[1 2 2 3]"},
	}
	for _, c := range cases {
		got := k.String(c.Key)
		if c.Want != got {
			t.Errorf("String(%#v) = %#v, want %#v", c.Key, got, c.Want)
		}
	}
}

func TestStructuredConfig(t *testing.T) {
	expectedConfig := Config{
		Container: Container{
			Filter: Filter{
				AllowByDefault: true,
				AllowList:      []string{"redis"},
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
			IgnoreFSType:   []string{"tmpfs"},
		},
		Metric: Metric{
			AllowMetrics:          []string{"allowed"},
			DenyMetrics:           []string{"denied"},
			IncludeDefaultMetrics: true,
			Prometheus: Prometheus{
				Targets: []PrometheusTarget{
					{
						URL:  "http://localhost:8080/metrics",
						Name: "my_app",
					},
				},
			},
			SoftStatusPeriodDefault: 100,
			SoftStatusPeriod: map[string]int{
				"system_pending_updates":          100,
				"system_pending_security_updates": 200,
			},
			SNMP: SNMP{
				ExporterAddress: "localhost",
				Targets: []SNMPTarget{
					{
						InitialName: "AP Wifi",
						Target:      "127.0.0.1",
					},
				},
			},
		},
		NetworkInterfaceBlacklist: []string{"lo", "veth"},
		Services: []Service{
			{
				ID:                      "service1",
				Instance:                "instance1",
				Port:                    8080,
				IgnorePorts:             []int{8081},
				Address:                 "127.0.0.1",
				Interval:                60,
				CheckType:               "nagios",
				HTTPPath:                "/check/",
				HTTPStatusCode:          200,
				HTTPHost:                "host",
				MatchProcess:            "/usr/bin/dockerd",
				CheckCommand:            "/path/to/bin --with-option",
				NagiosNRPEName:          "nagios",
				MetricsUnixSocket:       "/path/mysql.sock",
				Username:                "user",
				Password:                "password",
				StatsURL:                "http://nginx/stats",
				ManagementPort:          9090,
				CassandraDetailedTables: []string{"squirreldb.data"},
				JMXPort:                 1200,
				JMXUsername:             "jmx_user",
				JMXPassword:             "jmx_pass",
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
						TypeNames: []string{"name"},
					},
				},
			},
		},
		Web: Web{
			Enable: true,
			LocalUI: LocalUI{
				Enable: true,
			},
			Listener: Listener{
				Address: "192.168.0.1",
				Port:    8016,
			},
			StaticCDNURL: "/",
		},
	}

	config, warnings, err := Load(false, "testdata/full.conf")
	if warnings != nil {
		t.Fatalf("Warning while loading config: %s", warnings)
	}

	if err != nil {
		t.Fatalf("Failed to load config: %s", err)
	}

	if diff := cmp.Diff(expectedConfig, config); diff != "" {
		t.Fatalf("Unexpected config loaded:\n%s", diff)
	}
}

// Test that config files can be passed with environment variables.
func TestConfigFilesFromEnv(t *testing.T) {
	t.Setenv("GLOUTON_CONFIG_FILES", "testdata/simple.conf")

	config, warnings, err := Load(false)
	if warnings != nil {
		t.Fatalf("Warning while loading config: %s", err)
	}

	if err != nil {
		t.Fatalf("Failed to load config: %s", err)
	}

	if config.Web.StaticCDNURL != "/simple" {
		t.Fatal("File given with GLOUTON_CONFIG_FILES not loaded")
	}
}

// Test that users are able to override default settings.
func TestOverrideDefault(t *testing.T) {
	config, warnings, err := Load(true, "testdata/override_default.conf")
	if warnings != nil {
		t.Fatalf("Warning while loading config: %s", err)
	}

	if err != nil {
		t.Fatalf("Failed to load config: %s", err)
	}

	if len(config.NetworkInterfaceBlacklist) != 1 || config.NetworkInterfaceBlacklist[0] != "override" {
		t.Fatalf("Expected [override], got %s", config.NetworkInterfaceBlacklist)
	}

	// Test override nested slice.
	if len(config.DF.PathIgnore) != 1 || config.DF.PathIgnore[0] != "/override" {
		t.Fatalf("Expected [/override], got %s", config.DF.PathIgnore)
	}

	// Test that default not set in the config file hasn't changed.
	if diff := cmp.Diff(defaultConfig().DF.IgnoreFSType, config.DF.IgnoreFSType); diff != "" {
		t.Fatalf("Default value modified:\n%s", diff)
	}
}

// Test that the config loaded with no config file has default values.
func TestDefaultNoFile(t *testing.T) {
	config, warnings, err := Load(true)
	if warnings != nil {
		t.Fatalf("Warning while loading config: %s", err)
	}

	if err != nil {
		t.Fatalf("Failed to load config: %s", err)
	}

	if diff := cmp.Diff(defaultConfig(), config, cmpopts.EquateEmpty()); diff != "" {
		t.Fatalf("Default value modified:\n%s", diff)
	}
}

// Test warnings and errors returned when loading the configuration.
func TestWarningsAndErrors(t *testing.T) {
	tests := []struct {
		Name         string
		Files        []string
		Environment  map[string]string
		WantConfig   Config
		WantWarnings []string
		WantError    error
	}{
		{
			Name:  "wrong-type",
			Files: []string{"testdata/bad_wrong_type.conf"},
			WantWarnings: []string{
				`cannot parse 'metric.softstatus_period_default' as int: strconv.ParseInt: parsing "string": invalid syntax`,
				`cannot parse 'metric.softstatus_period[1][system_pending_security_updates]' as int: strconv.ParseInt: parsing "bad": invalid syntax`,
			},
			WantConfig: Config{
				Metric: Metric{
					SoftStatusPeriod: map[string]int{"system_pending_updates": 100},
				},
			},
		},
		{
			Name:  "invalid-yaml",
			Files: []string{"testdata/bad_yaml.conf"},
			WantWarnings: []string{
				"line 1: cannot unmarshal !!str `bad:bad` into map[string]interface {}",
			},
		},
		{
			Name: "deprecated-env",
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
					// TODO Bleemeo.AccountID = "my-account"
					Enable: true,
				},
			},
		},
		{
			Name:  "deprecated-config",
			Files: []string{"testdata/deprecated.conf"},
			WantWarnings: []string{
				"setting is deprecated: web.enabled, use web.enable instead",
			},
			WantConfig: Config{
				Web: Web{
					Enable: true,
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			for k, v := range test.Environment {
				t.Setenv(k, v)
			}

			config, warnings, err := Load(false, test.Files...)
			if diff := cmp.Diff(test.WantError, err); diff != "" {
				t.Fatalf("Unexpected error for files %s\n%s", test.Files, diff)
			}

			var strWarnings []string

			for _, warning := range warnings {
				strWarnings = append(strWarnings, warning.Error())
			}

			if diff := cmp.Diff(test.WantWarnings, strWarnings); diff != "" {
				t.Fatalf("Unexpected warnings:\n%s", diff)
			}

			if diff := cmp.Diff(test.WantConfig, config); diff != "" {
				t.Fatalf("Unexpected config:\n%s", diff)
			}
		})
	}
}
