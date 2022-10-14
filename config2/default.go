package config2

func defaultConfig() Config {
	return Config{
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
		Metric: Metric{
			Prometheus: Prometheus{Targets: []PrometheusTarget{}},
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
		Services: []Service{},
		Web: Web{
			Enable: true,
			Listener: Listener{
				Address: "127.0.0.1",
				Port:    8015,
			},
			StaticCDNURL: "/static/",
		},
	}
}
