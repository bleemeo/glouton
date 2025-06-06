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
	"time"

	"github.com/bleemeo/glouton/version"

	bbConf "github.com/prometheus/blackbox_exporter/config"
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

// mapKeys returns the config keys that hold map values.
// This must be updated when a map value is added to the config.
func mapKeys() []string {
	return []string{
		"thresholds",
		"metric.softstatus_period",
		"log.opentelemetry.receivers",
		"log.opentelemetry.global_filters",
		"log.opentelemetry.known_log_filters",
		"log.opentelemetry.container_filter",
	}
}

func DefaultConfig() Config { //nolint:maintidx
	defaultBlackboxModule := bbConf.DefaultModule
	defaultBlackboxModule.Prober = "http"
	// We default to IPv4 as the ip_protocol_fallback option does not retry a request
	// with a different IP version, but only has an effect when resolving the target.
	defaultBlackboxModule.HTTP.IPProtocol = "ip4"
	// DNS query name is unused, but we need to set it to avoid the error "query name
	// must be set for DNS module" returned by the DNSProbe UnmarshalYAML method.
	defaultBlackboxModule.DNS.QueryName = "default"

	return Config{
		Agent: Agent{
			CloudImageCreationFile: "cloudimage_creation",
			FactsFile:              "facts.yaml",
			InstallationFormat:     "manual",
			ProcessExporter: ProcessExporter{
				Enable: true,
			},
			PublicIPIndicator:    "https://myip.bleemeo.com",
			NetstatFile:          "netstat.out",
			StateDirectory:       "",
			StateFile:            "state.json",
			StateCacheFile:       "state.cache.json",
			StateResetFile:       "state.reset",
			DeprecatedStateFile:  "",
			EnableCrashReporting: true,
			MaxCrashReportsCount: 2,
			UpgradeFile:          "upgrade",
			AutoUpgradeFile:      "auto_upgrade",
			NodeExporter: NodeExporter{
				Enable:     true,
				Collectors: []string{"cpu", "diskstats", "filesystem", "loadavg", "meminfo", "netdev", "pressure", "stat", "time", "uname"},
			},
			WindowsExporter: NodeExporter{
				Enable:     true,
				Collectors: []string{"cpu", "cs", "logical_disk", "logon", "memory", "net", "os", "system", "tcp", "diskdrive"},
			},
			Telemetry: Telemetry{
				Enable:  true,
				Address: "https://telemetry.bleemeo.com/v1/telemetry/",
			},
		},
		Blackbox: Blackbox{
			Enable:          true,
			ScraperName:     "",
			ScraperSendUUID: true,
			UserAgent:       version.UserAgent(),
			Targets:         []BlackboxTarget{},
			Modules: map[string]bbConf.Module{
				"http": defaultBlackboxModule,
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
			Runtime: ContainerRuntime{
				Docker: ContainerRuntimeAddresses{
					Addresses: []string{
						"",
						"unix:///run/docker.sock",
						"unix:///var/run/docker.sock",
					},
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
				"devtmpfs",
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
				"tmpfs",
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
			// bleemeo-agent-logs overrides the URL and set an empty host root prefix.
			// We don't set an empty host root by default and change it in the Glouton docker image to
			// support the case where Glouton is installed as a package and Fluent Bit is in a container.
			FluentBitURL:   "",
			HostRootPrefix: "/hostroot",
			Inputs:         []LogInput{},
			OpenTelemetry: OpenTelemetry{
				Enable:        true,
				AutoDiscovery: false,
				GRPC: EnableListener{
					Enable:  false,
					Address: "localhost",
					Port:    4317,
				},
				HTTP: EnableListener{
					Enable:  false,
					Address: "localhost",
					Port:    4318,
				},
				KnownLogFormats: DefaultKnownLogFormats(),
				Receivers:       map[string]OTLPReceiver{},
				ContainerFormat: map[string]string{},
				GlobalFilters:   OTELFilters{},
				KnownLogFilters: map[string]OTELFilters{},
				ContainerFilter: map[string]string{},
			},
		},
		Logging: Logging{
			Buffer: LoggingBuffer{
				HeadSizeBytes: 500000,
				TailSizeBytes: 5000000,
			},
			Level:         "INFO",
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
				"system_pending_updates":          86400,
				"system_pending_security_updates": 86400,
				"time_elapsed_since_last_data":    0,
				"time_drift":                      0,
			},
		},
		MQTT: OpenSourceMQTT{
			Enable:      false,
			Hosts:       []string{"127.0.0.1"},
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
		Tags: []string{},
		Telegraf: Telegraf{
			DockerMetricsEnable: true,
			StatsD: StatsD{
				Enable:  true,
				Address: "127.0.0.1",
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
				Address: "127.0.0.1",
				Port:    8015,
			},
			LocalUI: LocalUI{
				Enable: true,
			},
			StaticCDNURL: "/static/",
		},
		Zabbix: Zabbix{
			Enable:  false,
			Address: "127.0.0.1",
			Port:    10050,
		},
	}
}
