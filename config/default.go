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
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/bleemeo/glouton/version"

	bbConf "github.com/prometheus/blackbox_exporter/config"
)

// Default configuration value constants, shared between production and test code.
const (
	DefaultFactsFile      = "facts.yaml"
	DefaultInstallFormat  = "manual"
	DefaultStateCacheFile = "state.cache.json"
	DefaultLogLevel       = "INFO"
	DefaultLocalhost      = "localhost"
	DefaultLoopback       = "127.0.0.1"

	// Common collector and filesystem type names shared between production and test code.
	collectorCPU = "cpu"
	fsDevtmpfs   = "devtmpfs"
	fsTmpfs      = "tmpfs"

	// metricSystemPendingUpdates is a commonly referenced metric name in soft status periods.
	metricSystemPendingUpdates = "system_pending_updates"

	// defaultHTTP is the default protocol used in several config values.
	defaultHTTP = "http"
)

// DefaultPaths returns the default paths used to search for config files.
func DefaultPaths() []string {
	return []string{
		"/etc/glouton/glouton.conf",
		"/etc/glouton/conf.d",
		"etc/glouton.conf",
		"etc/conf.d",
		"C:\\ProgramData\\glouton\\glouton.conf",
		"C:\\ProgramData\\glouton\\conf.d",
	}
}

// mapKeys returns the config keys that hold map values, i.e. the keys allKeys/isMapKey (loader.go) must
// treat as one nested map rather than as flat "key.sub.field" entries when merging file and default config.
// This must be updated when a map value is added to the config, including for every prefix key used by
// dynamicEnvVarList (config.go) -- see Test_dynamicEnvVarListKeysAreInMapKeys.
func mapKeys() []string {
	return []string{
		keyThresholds,
		"metric.softstatus_period",
		"opentelemetry.listeners",
		"log.opentelemetry.receivers",
		"log.opentelemetry.global_filters",
		"log.opentelemetry.known_log_filters",
		"log.opentelemetry.container_filter",
		"log.opentelemetry.container_format",
		"log.metrics_rules",
	}
}

// defaultDockerAddresses returns the default Docker socket candidates
// glouton tries when the user has not configured anything explicit.
// On Linux this is the historical pair (host /run + /var/run). On
// macOS we additionally probe the paths Docker Desktop uses, so that
// `glouton` launched on a developer Mac picks up the running
// containers without requiring GLOUTON_CONTAINER_RUNTIME_DOCKER_ADDRESSES.
// First entry is the empty string so the Docker SDK's own DOCKER_HOST
// resolution gets a chance to win.
func defaultDockerAddresses() []string {
	addrs := []string{
		"",
		"unix:///run/docker.sock",
		"unix:///var/run/docker.sock",
	}

	if runtime.GOOS == "darwin" {
		// Docker Desktop ≥ 4.13 sockets live in the user home; the
		// exact subdir depends on the version. We list the known
		// locations and let the runtime probe pick the first that
		// answers.
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			addrs = append(addrs,
				"unix://"+filepath.Join(home, ".docker", "run", "docker.sock"),
				"unix://"+filepath.Join(home, "Library", "Containers", "com.docker.docker", "Data", "docker.sock"),
				"unix://"+filepath.Join(home, ".colima", "default", "docker.sock"),
			)
		}
	}

	return addrs
}

func DefaultConfig() Config { //nolint:maintidx
	defaultBlackboxModule := bbConf.DefaultModule
	defaultBlackboxModule.Prober = defaultHTTP
	// We default to IPv4 as the ip_protocol_fallback option does not retry a request
	// with a different IP version, but only has an effect when resolving the target.
	defaultBlackboxModule.HTTP.IPProtocol = "ip4"
	// DNS query name is unused, but we need to set it to avoid the error "query name
	// must be set for DNS module" returned by the DNSProbe UnmarshalYAML method.
	defaultBlackboxModule.DNS.QueryName = "default"

	return Config{
		Agent: Agent{
			CloudImageCreationFile: "cloudimage_creation",
			FactsFile:              DefaultFactsFile,
			InstallationFormat:     DefaultInstallFormat,
			ProcessExporter: ProcessExporter{
				Enable: true,
			},
			PublicIPIndicator:    "https://myip.bleemeo.com",
			NetstatFile:          "netstat.out",
			StateDirectory:       "",
			StateFile:            "state.json",
			StateCacheFile:       DefaultStateCacheFile,
			StateResetFile:       "state.reset",
			DeprecatedStateFile:  "",
			EnableCrashReporting: true,
			MaxCrashReportsCount: 2,
			UpgradeFile:          "upgrade",
			AutoUpgradeFile:      "auto_upgrade",
			NodeExporter: NodeExporter{
				Enable:     true,
				Collectors: []string{collectorCPU, "diskstats", "filesystem", "loadavg", "meminfo", "netdev", "pressure", "stat", "time", "uname"},
			},
			WindowsExporter: NodeExporter{
				Enable:     true,
				Collectors: []string{collectorCPU, "cs", "logical_disk", "logon", "memory", "net", "os", "system", "tcp", "diskdrive"},
			},
			Telemetry: Telemetry{
				Enable:  true,
				Address: "https://telemetry.bleemeo.com/v1/telemetry/",
			},
			OverrideHostname: "",
			LocalStore: LocalStore{
				Enable:    nil, // auto: on iff bleemeo.enable is false
				Path:      "",
				Retention: 15 * 24 * time.Hour,
			},
		},
		Blackbox: Blackbox{
			Enable:             true,
			ScraperName:        "",
			ScraperSendUUID:    true,
			UserAgent:          version.UserAgent(),
			DefaultDNSResolver: "", // means try to guess the system default DNS resolver
			Targets:            []BlackboxTarget{},
			Modules: map[string]bbConf.Module{
				defaultHTTP: defaultBlackboxModule,
			},
		},
		Bleemeo: Bleemeo{
			Enable:         true,
			AccountID:      "",
			APIBase:        "https://api.bleemeo.com",
			APISSLInsecure: false,
			Cache: BleemeoCache{
				DeactivatedMetricsExpirationDays: 200,
			},
			ContainerRegistrationDelaySeconds: 30,
			InitialAgentName:                  "",
			InitialServerGroupName:            "",
			InitialServerGroupNameForSNMP:     "",
			InitialServerGroupNameForVSphere:  "",
			MQTT: BleemeoMQTT{
				CAFile:      "",
				Host:        "mqtt.bleemeo.com",
				Port:        8883,
				SSLInsecure: false,
				SSL:         true,
			},
			RegistrationKey: "",
			Sentry: Sentry{
				DSN: "https://55b4938036a1488ca0362792a77ac3e2@errors.bleemeo.work/4",
			},
		},
		Container: Container{
			PIDNamespaceHost: false,
			Type:             "",
			Filter: ContainerFilter{
				AllowByDefault: true,
				AllowList:      []string{},
				DenyList:       []string{},
			},
			// AllowedLabelOverrides is empty by default and behaves as an exact
			// allow-list: a user-provided value replaces it. The built-in safe
			// defaults are added on top only when IncludeDefaultLabelOverrides is
			// true (see DefaultAllowedLabelOverrides).
			AllowedLabelOverrides:        []string{},
			IncludeDefaultLabelOverrides: true,
			Runtime: ContainerRuntime{
				Docker: ContainerRuntimeAddresses{
					Addresses:      defaultDockerAddresses(),
					PrefixHostRoot: true,
				},
				ContainerD: ContainerRuntimeAddresses{
					Addresses: []string{
						"/run/containerd/containerd.sock",
						"/run/k3s/containerd/containerd.sock",
					},
					PrefixHostRoot: true,
				},
			},
		},
		DF: DF{
			HostMountPoint: "",
			IgnoreFSType: []string{
				"aufs",
				"autofs",
				"binfmt_misc",
				"bpf",
				"cgroup",
				"cgroup2",
				"configfs",
				"debugfs",
				"devfs",
				"devpts",
				fsDevtmpfs,
				"efivarfs",
				"fdescfs",
				"fusectl",
				"hugetlbfs",
				"iso9660",
				"linprocfs",
				"linsysfs",
				"mqueue",
				"nfs",
				"nfs4",
				"nsfs",
				"nullfs",
				"overlay",
				"proc",
				"procfs",
				"pstore",
				"rpc_pipefs",
				"securityfs",
				"selinuxfs",
				"squashfs",
				"sysfs",
				fsTmpfs,
				"tracefs",
				"zfs",
			},
			PathIgnore: []string{
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
				"/dev",
			},
		},
		DiskIgnore: []string{
			// Ignore some devices
			"^(bcache|cd|dm-|fd|loop|pass|ram|sr|zd|zram)\\d+$",
			// Ignore partition (sda1 like, not pN)
			"^((h|rss|s|v|xv)d[a-z]+|fio[a-z]+)\\d+$",
			// Ignore partition (pN like)
			"^(drbd|md|mmcblk|nbd|nvme\\d+n|rbd|rsxx|skd)\\d+p\\d+$",
		},
		DiskMonitor: []string{
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
		IPMI: IPMI{
			Enable:           true,
			UseSudo:          true,
			BinarySearchPath: "", // means default $PATH
			Timeout:          10,
		},
		JMX: JMX{
			Enable: true,
		},
		JMXTrans: JMXTrans{
			ConfigFile:     "/var/lib/jmxtrans/glouton-generated.json",
			FilePermission: "0640",
			GraphitePort:   2004,
		},
		Kubernetes: Kubernetes{
			Enable:              false,
			AllowClusterMetrics: false,
			NodeName:            "",
			ClusterName:         "",
			KubeConfig:          "",
		},
		Log: Log{
			OpenTelemetry: OpenTelemetry{
				ShippingEnable:           true,
				ReceiversDefaultSendLogs: true,
				AutoDiscovery: AutoDiscovery{
					AllEnable:                 false,
					JournaldEnable:            false,
					SyslogEnable:              false,
					AuditdEnable:              false,
					ContainerAndServiceEnable: false,
				},
				KnownLogFormats:  DefaultKnownLogFormats(),
				Receivers:        map[string]LogReceiver{},
				ContainerFormat:  map[string]string{},
				GlobalFilters:    OTELFilters{},
				KnownLogFilters:  map[string]OTELFilters{},
				ContainerFilter:  map[string]string{},
				ContainerExclude: []ContainerExcludeRule{},
			},
			MetricsRules: map[string][]LogMetricEntry{},
		},
		Logging: Logging{
			Buffer: LoggingBuffer{
				HeadSizeBytes: 500000,
				TailSizeBytes: 5000000,
			},
			Level:         DefaultLogLevel,
			Output:        "console",
			FileName:      "",
			PackageLevels: "",
		},
		Mdstat: Mdstat{
			Enable:    true,
			PathMdadm: "mdadm",
			UseSudo:   true,
		},
		Metric: Metric{
			Prometheus: Prometheus{
				Targets: []PrometheusTarget{},
			},
			SNMP: SNMP{
				ExporterAddress: "http://localhost:9116",
				Targets:         []SNMPTarget{},
			},
			IncludeDefaultMetrics:   true,
			AllowMetrics:            []string{},
			DenyMetrics:             []string{},
			SoftStatusPeriodDefault: 5 * 60,
			SoftStatusPeriod: map[string]int{
				metricSystemPendingUpdates:        86400,
				"system_pending_security_updates": 86400,
				"time_elapsed_since_last_data":    0,
				"time_drift":                      0,
			},
		},
		MQTT: OpenSourceMQTT{
			Enable:      false,
			Hosts:       []string{DefaultLoopback},
			Port:        1883,
			Username:    "",
			Password:    "",
			CAFile:      "",
			SSLInsecure: false,
			SSL:         false,
		},
		NetworkInterfaceDenylist: []string{
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
		NRPE: NRPE{
			Enable:    false,
			Address:   "0.0.0.0",
			Port:      5666,
			SSL:       true,
			ConfPaths: []string{"/etc/nagios/nrpe.cfg"},
		},
		NvidiaSMI: NvidiaSMI{
			Enable:  false,
			BinPath: "/usr/bin/nvidia-smi",
			Timeout: 5,
		},
		// No network receiver is pre-declared; nothing binds until the user
		// adds one under the opentelemetry.listeners config key (this field's yaml name).
		OpenTelemetry: OpenTelemetryConfig{
			NetworkListeners: map[string]NetworkListener{},
		},
		ServiceAbsentDeactivationDelay: 7 * 24 * time.Hour,
		ServiceIgnore:                  []NameInstance{},
		ServiceIgnoreCheck:             []NameInstance{},
		ServiceIgnoreMetrics:           []NameInstance{},
		Services:                       []Service{},
		Smart: Smart{
			Enable:       true,
			PathSmartctl: "smartctl",
			Devices:      []string{},
			Excludes: []string{
				"/dev/cd0", // we assume there isn't more than one CDROM on TrueNAS.
			},
			MaxConcurrency: 4,
		},
		SSACLI: SSACLI{
			Enable:           true,
			UseSudo:          true,
			BinarySearchPath: "", // means default $PATH
			Timeout:          10,
		},
		Tags: []string{},
		Telegraf: Telegraf{
			DockerMetricsEnable: true,
			StatsD: StatsD{
				Enable:  true,
				Address: DefaultLoopback,
				Port:    8125,
			},
		},
		Thresholds: map[string]Threshold{},
		VSphere:    []VSphere{},
		Web: Web{
			Enable: true,
			Endpoints: WebEndpoints{
				DebugEnable: false,
			},
			Listener: Listener{
				Address: DefaultLoopback,
				Port:    8015,
			},
			LocalUI: LocalUI{
				Enable: true,
			},
			StaticCDNURL: "/assets/panel-glouton-main.js",
		},
		Zabbix: Zabbix{
			Enable:  false,
			Address: DefaultLoopback,
			Port:    10050,
		},
	}
}

// DefaultAllowedLabelOverrides returns the built-in safe service-configuration
// fields that may be overridden through "glouton.*" container
// labels/annotations. They are added to Container.AllowedLabelOverrides when
// Container.IncludeDefaultLabelOverrides is true.
//
// Only the fields that would grant a capability *beyond* the container sandbox
// are excluded by design (add them explicitly through AllowedLabelOverrides
// only when container/pod creation is restricted to trusted users):
//   - command execution on the host as Glouton (root): check_type,
//     check_command, nagios_nrpe_name
//   - arbitrary host file read (content exfiltrated as logs): log_files
//
// Fields that merely redirect a check to an attacker-chosen endpoint (address,
// stats_url, metrics_unix_socket) are intentionally allowed: making network
// requests is already possible from inside the attacker's container, and the
// response is not reflected back (blind SSRF), so the marginal risk is low and
// does not justify breaking mandatory options (e.g. stats_url is required to
// monitor services like Jenkins through "docker run" labels).
func DefaultAllowedLabelOverrides() []string {
	return []string{
		"type",
		"instance",
		"port",
		"ignore_ports",
		"address",
		"tags",
		"interval",
		"http_path",
		"http_status_code",
		"http_host",
		"match_process",
		"metrics_unix_socket",
		"username",
		"password",
		"stats_url",
		keyStatsPort,
		"stats_protocol",
		keyDetailedItems,
		"jmx_port",
		"jmx_username",
		"jmx_password",
		"jmx_metrics",
		"ssl",
		"ssl_insecure",
		"starttls",
		"ca_file",
		"cert_file",
		"key_file",
		"included_items",
		"excluded_items",
		"log_format",
		"log_filter",
	}
}
