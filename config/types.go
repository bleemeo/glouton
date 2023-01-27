// Copyright 2015-2022 Bleemeo
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
	bbConf "github.com/prometheus/blackbox_exporter/config"
)

// Config is the structured configuration of the agent.
type Config struct {
	Agent                    Agent                `yaml:"agent"`
	Blackbox                 Blackbox             `yaml:"blackbox"`
	Bleemeo                  Bleemeo              `yaml:"bleemeo"`
	Container                Container            `yaml:"container"`
	DF                       DF                   `yaml:"df"`
	DiskIgnore               []string             `yaml:"disk_ignore"`
	DiskMonitor              []string             `yaml:"disk_monitor"`
	InfluxDB                 InfluxDB             `yaml:"influxdb"`
	JMX                      JMX                  `yaml:"jmx"`
	JMXTrans                 JMXTrans             `yaml:"jmxtrans"`
	Kubernetes               Kubernetes           `yaml:"kubernetes"`
	Log                      Log                  `yaml:"log"`
	Logging                  Logging              `yaml:"logging"`
	Metric                   Metric               `yaml:"metric"`
	MQTT                     OpenSourceMQTT       `yaml:"mqtt"`
	NetworkInterfaceDenylist []string             `yaml:"network_interface_denylist"`
	NRPE                     NRPE                 `yaml:"nrpe"`
	NvidiaSMI                NvidiaSMI            `yaml:"nvidia_smi"`
	Services                 []Service            `yaml:"service"`
	ServiceIgnoreMetrics     []NameInstance       `yaml:"service_ignore_metrics"`
	ServiceIgnoreCheck       []NameInstance       `yaml:"service_ignore_check"`
	Smart                    Smart                `yaml:"smart"`
	Stack                    string               `yaml:"stack"`
	Tags                     []string             `yaml:"tags"`
	Telegraf                 Telegraf             `yaml:"telegraf"`
	Thresholds               map[string]Threshold `yaml:"thresholds"`
	Web                      Web                  `yaml:"web"`
	Zabbix                   Zabbix               `yaml:"zabbix"`
}

type Log struct {
	FluentBitURL string     `yaml:"fluentbit_url"`
	Inputs       []LogInput `yaml:"inputs"`
}

type LogInput struct {
	Path          string        `yaml:"path"`
	ContainerName string        `yaml:"container_name"`
	Selectors     []LogSelector `yaml:"selectors"`
	Filters       []LogFilter   `yaml:"filters"`
}

type LogSelector struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

type LogFilter struct {
	Metric string `yaml:"metric"`
	Regex  string `yaml:"regex"`
}

type Smart struct {
	Enable       bool     `yaml:"enable"`
	PathSmartctl string   `yaml:"path_smartctl"`
	Devices      []string `yaml:"devices"`
	Excludes     []string `yaml:"excludes"`
}

type Zabbix struct {
	Enable  bool   `yaml:"enable"`
	Address string `yaml:"address"`
	Port    int    `yaml:"port"`
}

type Threshold struct {
	LowWarning   *float64 `yaml:"low_warning"`
	LowCritical  *float64 `yaml:"low_critical"`
	HighWarning  *float64 `yaml:"high_warning"`
	HighCritical *float64 `yaml:"high_critical"`
}

type Telegraf struct {
	DockerMetricsEnable bool   `yaml:"docker_metrics_enable"`
	StatsD              StatsD `yaml:"statsd"`
}

type StatsD struct {
	Enable  bool   `yaml:"enable"`
	Address string `yaml:"address"`
	Port    int    `yaml:"port"`
}

type NameInstance struct {
	Name     string `yaml:"name"`
	Instance string `yaml:"instance"`
}

type NvidiaSMI struct {
	Enable  bool   `yaml:"enable"`
	BinPath string `yaml:"bin_path"`
	Timeout int    `yaml:"timeout"`
}

type NRPE struct {
	Enable    bool     `yaml:"enable"`
	Address   string   `yaml:"address"`
	Port      int      `yaml:"port"`
	SSL       bool     `yaml:"ssl"`
	ConfPaths []string `yaml:"conf_paths"`
}

type OpenSourceMQTT struct {
	Enable      bool     `yaml:"enable"`
	Username    string   `yaml:"username"`
	Password    string   `yaml:"password"`
	CAFile      string   `yaml:"ca_file"`
	Hosts       []string `yaml:"hosts"`
	Port        int      `yaml:"port"`
	SSLInsecure bool     `yaml:"ssl_insecure"`
	SSL         bool     `yaml:"ssl"`
}

type Logging struct {
	Buffer        LoggingBuffer `yaml:"buffer"`
	Level         string        `yaml:"level"`
	Output        string        `yaml:"output"`
	FileName      string        `yaml:"filename"`
	PackageLevels string        `yaml:"package_levels"`
}

type LoggingBuffer struct {
	HeadSizeBytes int `yaml:"head_size_bytes"`
	TailSizeBytes int `yaml:"tail_size_bytes"`
}

type Kubernetes struct {
	Enable              bool   `yaml:"enable"`
	AllowClusterMetrics bool   `yaml:"allow_cluster_metrics"`
	NodeName            string `yaml:"nodename"`
	ClusterName         string `yaml:"clustername"`
	KubeConfig          string `yaml:"kubeconfig"`
}

type JMXTrans struct {
	ConfigFile     string `yaml:"config_file"`
	FilePermission string `yaml:"file_permission"`
	GraphitePort   int    `yaml:"graphite_port"`
}

type JMX struct {
	Enable bool `yaml:"enable"`
}

type InfluxDB struct {
	Enable bool              `yaml:"enable"`
	Host   string            `yaml:"host"`
	Port   int               `yaml:"port"`
	DBName string            `yaml:"db_name"`
	Tags   map[string]string `yaml:"tags"`
}

type Bleemeo struct {
	AccountID                         string      `yaml:"account_id"`
	APIBase                           string      `yaml:"api_base"`
	APISSLInsecure                    bool        `yaml:"api_ssl_insecure"`
	ContainerRegistrationDelaySeconds int         `yaml:"container_registration_delay_seconds"`
	Enable                            bool        `yaml:"enable"`
	InitialAgentName                  string      `yaml:"initial_agent_name"`
	InitialServerGroupName            string      `yaml:"initial_server_group_name"`
	InitialServerGroupNameForSNMP     string      `yaml:"initial_server_group_name_for_snmp"`
	MQTT                              BleemeoMQTT `yaml:"mqtt"`
	RegistrationKey                   string      `yaml:"registration_key"`
	Sentry                            Sentry      `yaml:"sentry"`
}

type Sentry struct {
	DSN string `yaml:"dsn"`
}

type BleemeoMQTT struct {
	CAFile      string `yaml:"cafile"`
	Host        string `yaml:"host"`
	Port        int    `yaml:"port"`
	SSLInsecure bool   `yaml:"ssl_insecure"`
	SSL         bool   `yaml:"ssl"`
}

type Blackbox struct {
	Enable          bool                     `yaml:"enable"`
	ScraperName     string                   `yaml:"scraper_name"`
	ScraperSendUUID bool                     `yaml:"scraper_send_uuid"`
	UserAgent       string                   `yaml:"user_agent"`
	Targets         []BlackboxTarget         `yaml:"targets"`
	Modules         map[string]bbConf.Module `yaml:"modules"`
}

type BlackboxTarget struct {
	Name   string `yaml:"name"`
	URL    string `yaml:"url"`
	Module string `yaml:"module"`
}

type Agent struct {
	CloudImageCreationFile           string          `yaml:"cloudimage_creation_file"`
	HTTPDebug                        HTTPDebug       `yaml:"http_debug"`
	InstallationFormat               string          `yaml:"installation_format"`
	FactsFile                        string          `yaml:"facts_file"`
	NetstatFile                      string          `yaml:"netstat_file"`
	StateFile                        string          `yaml:"state_file"`
	StateCacheFile                   string          `yaml:"state_cache_file"`
	StateResetFile                   string          `yaml:"state_reset_file"`
	DeprecatedStateFile              string          `yaml:"deprecated_state_file"`
	UpgradeFile                      string          `yaml:"upgrade_file"`
	AutoUpgradeFile                  string          `yaml:"auto_upgrade_file"`
	DisableLocalDuplicationDetection bool            `yaml:"disable_local_duplication_detection_by_process"`
	ProcessExporter                  ProcessExporter `yaml:"process_exporter"`
	PublicIPIndicator                string          `yaml:"public_ip_indicator"`
	NodeExporter                     NodeExporter    `yaml:"node_exporter"`
	WindowsExporter                  NodeExporter    `yaml:"windows_exporter"`
	Telemetry                        Telemetry       `yaml:"telemetry"`
	MetricsFormat                    string          `yaml:"metrics_format"`
}

type Telemetry struct {
	Enable  bool   `yaml:"enable"`
	Address string `yaml:"address"`
}

type ProcessExporter struct {
	Enable bool `yaml:"enable"`
}

type NodeExporter struct {
	Enable     bool     `yaml:"enable"`
	Collectors []string `yaml:"collectors"`
}
type HTTPDebug struct {
	Enable      bool   `yaml:"enable"`
	BindAddress string `yaml:"bind_address"`
}

type Metric struct {
	AllowMetrics            []string       `yaml:"allow_metrics"`
	DenyMetrics             []string       `yaml:"deny_metrics"`
	IncludeDefaultMetrics   bool           `yaml:"include_default_metrics"`
	Prometheus              Prometheus     `yaml:"prometheus"`
	SoftStatusPeriodDefault int            `yaml:"softstatus_period_default"`
	SoftStatusPeriod        map[string]int `yaml:"softstatus_period"`
	SNMP                    SNMP           `yaml:"snmp"`
}

type SNMP struct {
	ExporterAddress string       `yaml:"exporter_address"`
	Targets         []SNMPTarget `yaml:"targets"`
}

type SNMPTarget struct {
	InitialName string `yaml:"initial_name"`
	Target      string `yaml:"target"`
}

type Prometheus struct {
	Targets []PrometheusTarget `yaml:"targets"`
}

type PrometheusTarget struct {
	URL          string   `yaml:"url"`
	Name         string   `yaml:"name"`
	AllowMetrics []string `yaml:"allow_metrics"`
	DenyMetrics  []string `yaml:"deny_metrics"`
}

type DF struct {
	HostMountPoint string   `yaml:"host_mount_point"`
	PathIgnore     []string `yaml:"path_ignore"`
	IgnoreFSType   []string `yaml:"ignore_fs_type"`
}

type Web struct {
	Enable       bool     `yaml:"enable"`
	LocalUI      LocalUI  `yaml:"local_ui"`
	Listener     Listener `yaml:"listener"`
	StaticCDNURL string   `yaml:"static_cdn_url"`
}

type LocalUI struct {
	Enable bool `yaml:"enable"`
}

type Listener struct {
	Address string `yaml:"address"`
	Port    int    `yaml:"port"`
}

type Service struct {
	// The name of the service.
	ID string `yaml:"id"`
	// Instance of the service, used to differentiate between services with the same ID.
	Instance string `yaml:"instance"`
	// The port the service is running on.
	Port int `yaml:"port"`
	// Ports that should be ignored.
	IgnorePorts []int `yaml:"ignore_ports"`
	// The address of the service.
	Address string `yaml:"address"`
	// The delay between two consecutive checks in seconds.
	Interval int `yaml:"interval"`
	// Stack dashboard this service belongs to.
	Stack string `yaml:"stack"`
	// Check type used for custom checks.
	CheckType string `yaml:"check_type"`
	// The path used for HTTP checks.
	HTTPPath string `yaml:"http_path"`
	// The expected status code for HTTP checks.
	HTTPStatusCode int `yaml:"http_status_code"`
	// Host header sent with HTTP checks.
	HTTPHost string `yaml:"http_host"`
	// Regex to match in a process check.
	MatchProcess string `yaml:"match_process"`
	// Command used for a Nagios check.
	CheckCommand   string `yaml:"check_command"`
	NagiosNRPEName string `yaml:"nagios_nrpe_name"`
	// Unix socket to connect and gather metric from MySQL.
	MetricsUnixSocket string `yaml:"metrics_unix_socket"`
	// Credentials for services that require authentication.
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	// URL used to retrieve metrics (used for instance by HAProxy and PHP-FMP).
	StatsURL string `yaml:"stats_url"`
	// Port used to get statistics for a service.
	StatsPort int `yaml:"stats_port"`
	// Protocol used to get statistics (TCP, HTTP).
	StatsProtocol string `yaml:"stats_protocol"`
	// Detailed monitoring of specific items (Cassandra tables, Postgres databases or Kafka topics).
	DetailedItems []string `yaml:"detailed_items"`
	// JMX services.
	JMXPort     int         `yaml:"jmx_port"`
	JMXUsername string      `yaml:"jmx_username"`
	JMXPassword string      `yaml:"jmx_password"`
	JMXMetrics  []JmxMetric `yaml:"jmx_metrics"`
	// TLS config.
	SSL         bool   `yaml:"ssl"`
	SSLInsecure bool   `yaml:"ssl_insecure"`
	StartTLS    bool   `yaml:"starttls"`
	CAFile      string `yaml:"ca_file"`
	CertFile    string `yaml:"cert_file"`
	KeyFile     string `yaml:"key_file"`
	// IncludedItems or exlude specific items (for instance Jenkins jobs).
	IncludedItems []string `yaml:"included_items"`
	ExcludedItems []string `yaml:"excluded_items"`
}

type JmxMetric struct {
	Name      string   `yaml:"name"`
	MBean     string   `yaml:"mbean"`
	Attribute string   `yaml:"attribute"`
	Path      string   `yaml:"path"`
	Scale     float64  `yaml:"scale"`
	Derive    bool     `yaml:"derive"`
	Sum       bool     `yaml:"sum"`
	TypeNames []string `yaml:"type_names"`
	Ratio     string   `yaml:"ratio"`
}

type Container struct {
	Filter           ContainerFilter  `yaml:"filter"`
	Type             string           `yaml:"type"`
	PIDNamespaceHost bool             `yaml:"pid_namespace_host"`
	Runtime          ContainerRuntime `yaml:"runtime"`
}

type ContainerFilter struct {
	AllowByDefault bool     `yaml:"allow_by_default"`
	AllowList      []string `yaml:"allow_list"`
	DenyList       []string `yaml:"deny_list"`
}

type ContainerRuntime struct {
	Docker     ContainerRuntimeAddresses `yaml:"docker"`
	ContainerD ContainerRuntimeAddresses `yaml:"containerd"`
}

type ContainerRuntimeAddresses struct {
	Addresses      []string `yaml:"addresses"`
	PrefixHostRoot bool     `yaml:"prefix_hostroot"`
}
