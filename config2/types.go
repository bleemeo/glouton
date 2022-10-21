package config2

import (
	bbConf "github.com/prometheus/blackbox_exporter/config"
)

// Config is the structured configuration of the agent.
type Config struct {
	Agent      Agent     `koanf:"agent"`
	Blackbox   Blackbox  `koanf:"blackbox"`
	Bleemeo    Bleemeo   `koanf:"bleemeo"`
	Container  Container `koanf:"container"`
	DF         DF        `koanf:"df"`
	DiskIgnore []string  `koanf:"disk_ignore"`
	// TODO: Not documented.
	DiskMonitor               []string             `koanf:"disk_monitor"`
	InfluxDB                  InfluxDB             `koanf:"influxdb"`
	JMX                       JMX                  `koanf:"jmx"`
	JMXTrans                  JMXTrans             `koanf:"jmxtrans"`
	Kubernetes                Kubernetes           `koanf:"kubernetes"`
	Logging                   Logging              `koanf:"logging"`
	Metric                    Metric               `koanf:"metric"`
	MQTT                      OpenSourceMQTT       `koanf:"mqtt"`
	NetworkInterfaceBlacklist []string             `koanf:"network_interface_blacklist"`
	NRPE                      NRPE                 `koanf:"nrpe"`
	NvidiaSMI                 NvidiaSMI            `koanf:"nvidia_smi"`
	Services                  []Service            `koanf:"service"`
	ServiceIgnoreMetrics      []NameInstance       `koanf:"service_ignore_metrics"`
	ServiceIgnoreCheck        []NameInstance       `koanf:"service_ignore_check"`
	Stack                     string               `koanf:"stack"`
	Tags                      []string             `koanf:"tags"`
	Telegraf                  Telegraf             `koanf:"telegraf"`
	Thresholds                map[string]Threshold `koanf:"thresholds"`
	Web                       Web                  `koanf:"web"`
	// TODO: Not documented.
	Zabbix Zabbix `koanf:"zabbix"`
}

type Zabbix struct {
	Enable  bool   `koanf:"enable"`
	Address string `koanf:"address"`
	Port    int    `koanf:"port"`
}

type Threshold struct {
	LowWarning   float64 `koanf:"low_warning"`
	LowCritical  float64 `koanf:"low_critical"`
	HighWarning  float64 `koanf:"high_warning"`
	HighCritical float64 `koanf:"high_critical"`
}

type Telegraf struct {
	DockerMetricsEnable bool   `koanf:"docker_metrics_enable"`
	StatsD              StatsD `koanf:"statsd"`
}

type StatsD struct {
	Enable  bool   `koanf:"enable"`
	Address string `koanf:"address"`
	Port    int    `koanf:"port"`
}

type NameInstance struct {
	Name     string `koanf:"name"`
	Instance string `koanf:"instance"`
}

type NvidiaSMI struct {
	Enable  bool   `koanf:"enable"`
	BinPath string `koanf:"bin_path"`
	Timeout int    `koanf:"timeout"`
}

type NRPE struct {
	Enable    bool     `koanf:"enable"`
	Address   string   `koanf:"address"`
	Port      int      `koanf:"port"`
	SSL       bool     `koanf:"ssl"`
	ConfPaths []string `koanf:"conf_paths"`
}

type OpenSourceMQTT struct {
	Enable      bool   `koanf:"enable"`
	Username    string `koanf:"username"`
	Password    string `koanf:"password"`
	CAFile      string `koanf:"ca_file"`
	Host        string `koanf:"host"`
	Port        int    `koanf:"port"`
	SSLInsecure bool   `koanf:"ssl_insecure"`
	SSL         bool   `koanf:"ssl"`
}

type Logging struct {
	Buffer        LoggingBuffer `koanf:"buffer"`
	Level         string        `koanf:"level"`
	Output        string        `koanf:"output"`
	FileName      string        `koanf:"filename"`
	PackageLevels string        `koanf:"package_levels"`
}

type LoggingBuffer struct {
	HeadSizeBytes int `koanf:"head_size_bytes"`
	TailSizeBytes int `koanf:"tail_size_bytes"`
}

type Kubernetes struct {
	Enable   bool   `koanf:"enable"`
	NodeName string `koanf:"nodename"`
	// TODO: Not documented.
	ClusterName string `koanf:"clustername"`
	KubeConfig  string `koanf:"kubeconfig"`
}

type JMXTrans struct {
	ConfigFile     string `koanf:"config_file"`
	FilePermission string `koanf:"file_permission"`
	GraphitePort   int    `koanf:"graphite_port"`
}

type JMX struct {
	Enable bool `koanf:"enable"`
}

type InfluxDB struct {
	Enable bool              `koanf:"enable"`
	Host   string            `koanf:"host"`
	Port   int               `koanf:"port"`
	DBName string            `koanf:"db_name"`
	Tags   map[string]string `koanf:"tags"`
}

type Bleemeo struct {
	AccountID                         string      `koanf:"account_id"`
	APIBase                           string      `koanf:"api_base"`
	APISSLInsecure                    bool        `koanf:"api_ssl_insecure"`
	ContainerRegistrationDelaySeconds int         `koanf:"container_registration_delay_seconds"`
	Enable                            bool        `koanf:"enable"`
	InitialAgentName                  string      `koanf:"initial_agent_name"`
	InitialServerGroupName            string      `koanf:"initial_server_group_name"`
	InitialServerGroupNameForSNMP     string      `koanf:"initial_server_group_name_for_snmp"`
	MQTT                              BleemeoMQTT `koanf:"mqtt"`
	RegistrationKey                   string      `koanf:"registration_key"`
	// TODO: Not documented.
	Sentry Sentry `koanf:"sentry"`
}

type Sentry struct {
	DSN string `koanf:"dsn"`
}

type BleemeoMQTT struct {
	CAFile      string `koanf:"cafile"`
	Host        string `koanf:"host"`
	Port        int    `koanf:"port"`
	SSLInsecure bool   `koanf:"ssl_insecure"`
	SSL         bool   `koanf:"ssl"`
}

type Blackbox struct {
	Enable      bool   `koanf:"enable"`
	ScraperName string `koanf:"scraper_name"`
	// TODO: Not documented.
	ScraperSendUUID bool                     `koanf:"scraper_send_uuid"`
	UserAgent       string                   `koanf:"user_agent"`
	Targets         []BlackboxTarget         `koanf:"targets"`
	Modules         map[string]bbConf.Module `koanf:"modules"`
}

type BlackboxTarget struct {
	// TODO: Not documented.
	Name   string `koanf:"name"`
	URL    string `koanf:"url"`
	Module string `koanf:"module"`
}

type Agent struct {
	CloudImageCreationFile string    `koanf:"cloudimage_creation_file"`
	HTTPDebug              HTTPDebug `koanf:"http_debug"`
	InstallationFormat     string    `koanf:"installation_format"`
	FactsFile              string    `koanf:"facts_file"`
	NetstatFile            string    `koanf:"netstat_file"`
	StateFile              string    `koanf:"state_file"`
	// TODO: Not documented.
	StateCacheFile      string          `koanf:"state_cache_file"`
	StateResetFile      string          `koanf:"state_reset_file"`
	DeprecatedStateFile string          `koanf:"deprecated_state_file"`
	UpgradeFile         string          `koanf:"upgrade_file"`
	AutoUpgradeFile     string          `koanf:"auto_upgrade_file"`
	ProcessExporter     ProcessExporter `koanf:"process_exporter"`
	PublicIPIndicator   string          `koanf:"public_ip_indicator"`
	NodeExporter        NodeExporter    `koanf:"node_exporter"`
	WindowsExporter     NodeExporter    `koanf:"windows_exporter"`
	// TODO: Not documented.
	Telemetry     Telemetry `koanf:"telemetry"`
	MetricsFormat string    `koanf:"metrics_format"`
}

type Telemetry struct {
	Enable  bool   `koanf:"enable"`
	Address string `koanf:"address"`
}

type ProcessExporter struct {
	Enable bool `koanf:"enable"`
}

type NodeExporter struct {
	Enable     bool     `koanf:"enable"`
	Collectors []string `koanf:"collectors"`
}
type HTTPDebug struct {
	Enable      bool   `koanf:"enable"`
	BindAddress string `koanf:"bind_address"`
}

type Metric struct {
	AllowMetrics            []string       `koanf:"allow_metrics"`
	DenyMetrics             []string       `koanf:"deny_metrics"`
	IncludeDefaultMetrics   bool           `koanf:"include_default_metrics"`
	Prometheus              Prometheus     `koanf:"prometheus"`
	SoftStatusPeriodDefault int            `koanf:"softstatus_period_default"`
	SoftStatusPeriod        map[string]int `koanf:"softstatus_period"`
	SNMP                    SNMP           `koanf:"snmp"`
}

type SNMP struct {
	// TODO: Not documented.
	ExporterAddress string       `koanf:"exporter_address"`
	Targets         []SNMPTarget `koanf:"targets"`
}

type SNMPTarget struct {
	InitialName string `koanf:"initial_name"`
	Target      string `koanf:"target"`
}

type Prometheus struct {
	Targets []PrometheusTarget `koanf:"targets"`
}

type PrometheusTarget struct {
	URL          string   `koanf:"url"`
	Name         string   `koanf:"name"`
	AllowMetrics []string `koanf:"allow_metrics"`
	DenyMetrics  []string `koanf:"deny_metrics"`
}

type DF struct {
	HostMountPoint string   `koanf:"host_mount_point"`
	PathIgnore     []string `koanf:"path_ignore"`
	IgnoreFSType   []string `koanf:"ignore_fs_type"`
}

type Web struct {
	Enable bool `koanf:"enable"`
	// TODO: Not documented.
	LocalUI      LocalUI  `koanf:"local_ui"`
	Listener     Listener `koanf:"listener"`
	StaticCDNURL string   `koanf:"static_cdn_url"`
}

type LocalUI struct {
	Enable bool `koanf:"enable"`
}

type Listener struct {
	Address string `koanf:"address"`
	Port    int    `koanf:"port"`
}

type Service struct {
	// The name of the service.
	ID string `koanf:"id"`
	// Instance of the service, used to differentiate between services with the same ID.
	Instance string `koanf:"instance"`
	// The port the service is running on.
	Port int `koanf:"port"`
	// Ports that should be ignored.
	IgnorePorts []int `koanf:"ignore_ports"`
	// The address of the service.
	Address string `koanf:"address"`
	// The delay between two consecutive checks in seconds.
	Interval int `koanf:"interval"`
	// Check type used for custom checks.
	CheckType string `koanf:"check_type"`
	// The path used for HTTP checks.
	HTTPPath string `koanf:"http_path"`
	// The expected status code for HTTP checks.
	HTTPStatusCode int `koanf:"http_status_code"`
	// Host header sent with HTTP checks.
	HTTPHost string `koanf:"http_host"`
	// Regex to match in a process check.
	MatchProcess string `koanf:"match_process"`
	// Command used for a Nagios check.
	CheckCommand string `koanf:"check_command"`
	// TODO: Not documented.
	NagiosNRPEName string `koanf:"nagios_nrpe_name"`
	// Unix socket to connect and gather metric from MySQL.
	MetricsUnixSocket string `koanf:"metrics_unix_socket"`
	// Credentials for services that require authentication.
	Username string `koanf:"username"`
	Password string `koanf:"password"`
	// HAProxy and PHP-FMP stats URL.
	StatsURL string `koanf:"stats_url"`
	// Port of RabbitMQ management interface.
	ManagementPort int `koanf:"mgmt_port"`
	// Detailed monitoring of specific Cassandra tables.
	CassandraDetailedTables []string `koanf:"cassandra_detailed_tables"`
	// JMX services.
	JMXPort     int         `koanf:"jmx_port"`
	JMXUsername string      `koanf:"jmx_username"`
	JMXPassword string      `koanf:"jmx_password"`
	JMXMetrics  []JmxMetric `koanf:"jmx_metrics"`
}

type JmxMetric struct {
	Name      string  `koanf:"name"`
	MBean     string  `koanf:"mbean"`
	Attribute string  `koanf:"attribute"`
	Path      string  `koanf:"path"`
	Scale     float64 `koanf:"scale"`
	Derive    bool    `koanf:"derive"`
	// TODO: Not documented.
	Sum       bool     `koanf:"sum"`
	TypeNames []string `koanf:"type_names"`
	Ratio     string   `koanf:"ratio"`
}

type Container struct {
	Filter           Filter           `koanf:"filter"`
	Type             string           `koanf:"type"`
	PIDNamespaceHost bool             `koanf:"pid_namespace_host"`
	Runtime          ContainerRuntime `koanf:"runtime"`
}

type Filter struct {
	AllowByDefault bool     `koanf:"allow_by_default"`
	AllowList      []string `koanf:"allow_list"`
	DenyList       []string `koanf:"deny_list"`
}

type ContainerRuntime struct {
	Docker     ContainerRuntimeAddresses `koanf:"docker"`
	ContainerD ContainerRuntimeAddresses `koanf:"containerd"`
}

type ContainerRuntimeAddresses struct {
	Addresses      []string `koanf:"addresses"`
	PrefixHostRoot bool     `koanf:"prefix_hostroot"`
}
