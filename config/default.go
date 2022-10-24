package config

import (
	"glouton/version"

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

func DefaultConfig() Config {
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
			HTTPDebug: HTTPDebug{
				Enable:      false,
				BindAddress: "localhost:6060",
			},
			InstallationFormat: "manual",
			ProcessExporter: ProcessExporter{
				Enable: true,
			},
			PublicIPIndicator: "https://myip.bleemeo.com",
			NetstatFile:       "netstat.out",
			StateFile:         "state.json",
			// By default ".cache" is added before the state file extension.
			StateCacheFile:      "",
			StateResetFile:      "state.reset",
			DeprecatedStateFile: "",
			UpgradeFile:         "upgrade",
			AutoUpgradeFile:     "auto_upgrade",
			MetricsFormat:       "Bleemeo",
			NodeExporter: NodeExporter{
				Enable:     true,
				Collectors: []string{"cpu", "diskstats", "filesystem", "loadavg", "meminfo", "netdev"},
			},
			WindowsExporter: NodeExporter{
				Enable:     true,
				Collectors: []string{"cpu", "cs", "logical_disk", "logon", "memory", "net", "os", "system", "tcp"},
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
			Enable:                            true,
			AccountID:                         "",
			APIBase:                           "https://api.bleemeo.com/",
			APISSLInsecure:                    false,
			ContainerRegistrationDelaySeconds: 30,
			InitialAgentName:                  "",
			InitialServerGroupName:            "",
			InitialServerGroupNameForSNMP:     "",
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
			Filter: Filter{
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
				"^(autofs|binfmt_misc|bpf|cgroup2?|configfs|debugfs|devpts|devtmpfs|fusectl|hugetlbfs|iso9660|mqueue|nsfs|overlay|proc|procfs|pstore|rpc_pipefs|securityfs|selinuxfs|squashfs|sysfs|tracefs|devfs|aufs)$",
				"tmpfs",
				"efivarfs",
				".*gvfs.*",
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
			},
		},
		DiskIgnore: []string{
			"^(ram|loop|fd|(h|s|v|xv)d[a-z]|nvme\\d+n\\d+p)\\d+$",
			"^dm-[0-9]+$",
			// Ignore partitions.
			"^(hd|sd|vd|xvd|fio|rssd)[a-z][0-9]+$",
			"^(mmcblk|nvme[0-9]n|drbd|rbd|skd|rsxx)[0-9]p[0-9]+$",
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
		InfluxDB: InfluxDB{
			Enable: false,
			DBName: "glouton",
			Host:   "localhost",
			Port:   8086,
			Tags:   map[string]string{},
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
			Enable:      false,
			NodeName:    "",
			ClusterName: "",
			KubeConfig:  "",
		},
		Logging: Logging{
			Buffer: LoggingBuffer{
				HeadSizeBytes: 500000,
				TailSizeBytes: 500000,
			},
			Level:         "INFO",
			Output:        "console",
			FileName:      "",
			PackageLevels: "",
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
			Host:        "127.0.0.1",
			Port:        1883,
			Username:    "",
			Password:    "",
			CAFile:      "",
			SSLInsecure: false,
			SSL:         false,
		},
		NetworkInterfaceBlacklist: []string{
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
		ServiceIgnoreCheck:   []NameInstance{},
		ServiceIgnoreMetrics: []NameInstance{},
		Services:             []Service{},
		Stack:                "",
		Tags:                 []string{},
		Telegraf: Telegraf{
			DockerMetricsEnable: true,
			StatsD: StatsD{
				Enable:  true,
				Address: "127.0.0.1",
				Port:    8125,
			},
		},
		Thresholds: map[string]Threshold{},
		Web: Web{
			Enable: true,
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
