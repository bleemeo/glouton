package config2

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// TestMerge tests that config files are merged correctly.
// Merge should override existing values, merge maps and concatenate arrays.
func TestMerge(t *testing.T) {
	k, err := load(false, "testdata/merge1.conf", "testdata/merge2.conf")
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
				ID:           "service1",
				Port:         8080,
				Address:      "127.0.0.1",
				Interval:     60,
				CheckType:    "nagios",
				CheckCommand: "/path/to/bin --with-option",
			},
			{
				ID:             "service2",
				Port:           8443,
				CheckType:      "https",
				HTTPPath:       "/check/",
				HTTPStatusCode: 200,
			},
			{
				ID:           "service3",
				CheckType:    "process",
				MatchProcess: "/usr/bin/dockerd",
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

	config, _, err := Load(false, "testdata/full.conf")
	if err != nil {
		t.Fatalf("Failed to load config: %s", err)
	}

	if diff := cmp.Diff(expectedConfig, config); diff != "" {
		t.Fatalf("Unexpected config loaded:\n%s", diff)
	}
}

// Test the config can be modified with environment variable.
func TestConfigFromEnv(t *testing.T) {
	const cdnUrl = "/static2/"

	// Simple test
	t.Setenv("GLOUTON_WEB_ENABLE", "false")

	// More complex test, underscores can't be converted to YAML indentation directly.
	t.Setenv("GLOUTON_WEB_STATIC_CDN_URL", cdnUrl)

	config, _, err := Load(false)
	if err != nil {
		t.Fatalf("Failed to load config: %s", err)
	}

	if config.Web.Enable {
		t.Fatal("Expected web.enable=false, got true")
	}

	if config.Web.StaticCDNURL != cdnUrl {
		t.Fatalf("Expected web.static_web_url=%s, got %s", cdnUrl, config.Web.StaticCDNURL)
	}
}

// Test that config files can be passed with environment variables.
func TestConfigFilesFromEnv(t *testing.T) {
	t.Setenv("GLOUTON_CONFIG_FILES", "testdata/simple.conf")

	config, _, err := Load(false)
	if err != nil {
		t.Fatalf("Failed to load config: %s", err)
	}

	if config.Web.StaticCDNURL != "/simple" {
		t.Fatal("File given with GLOUTON_CONFIG_FILES not loaded")
	}
}

// Test that users are able to override default settings.
func TestOverrideDefault(t *testing.T) {
	config, _, err := Load(true, "testdata/override_default.conf")
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
	config, _, err := Load(true)
	if err != nil {
		t.Fatalf("Failed to load config: %s", err)
	}

	if diff := cmp.Diff(defaultConfig(), config, cmpopts.EquateEmpty()); diff != "" {
		t.Fatalf("Default value modified:\n%s", diff)
	}
}
