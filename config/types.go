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
	"time"

	bbConf "github.com/prometheus/blackbox_exporter/config"
)

// Config is the structured configuration of the agent.
type Config struct {
	Agent                          Agent                `yaml:"agent"`
	Blackbox                       Blackbox             `yaml:"blackbox"`
	Bleemeo                        Bleemeo              `yaml:"bleemeo"`
	Container                      Container            `yaml:"container"`
	DF                             DF                   `yaml:"df"`
	DiskIgnore                     []string             `yaml:"disk_ignore"`
	DiskMonitor                    []string             `yaml:"disk_monitor"`
	IPMI                           IPMI                 `yaml:"ipmi"`
	JMX                            JMX                  `yaml:"jmx"`
	JMXTrans                       JMXTrans             `yaml:"jmxtrans"`
	Kubernetes                     Kubernetes           `yaml:"kubernetes"`
	Log                            Log                  `yaml:"log"`
	Logging                        Logging              `yaml:"logging"`
	Mdstat                         Mdstat               `yaml:"mdstat"`
	Metric                         Metric               `yaml:"metric"`
	MQTT                           OpenSourceMQTT       `yaml:"mqtt"`
	NetworkInterfaceDenylist       []string             `yaml:"network_interface_denylist"`
	NRPE                           NRPE                 `yaml:"nrpe"`
	NvidiaSMI                      NvidiaSMI            `yaml:"nvidia_smi"`
	OpenTelemetry                  OpenTelemetryConfig  `yaml:"opentelemetry"`
	Services                       []Service            `yaml:"service"`
	ServiceAbsentDeactivationDelay time.Duration        `yaml:"service_absent_deactivation_delay"`
	ServiceIgnore                  []NameInstance       `yaml:"service_ignore"`
	ServiceIgnoreMetrics           []NameInstance       `yaml:"service_ignore_metrics"`
	ServiceIgnoreCheck             []NameInstance       `yaml:"service_ignore_check"`
	Smart                          Smart                `yaml:"smart"`
	SSACLI                         SSACLI               `yaml:"ssacli"`
	Tags                           []string             `yaml:"tags"`
	Telegraf                       Telegraf             `yaml:"telegraf"`
	Thresholds                     map[string]Threshold `yaml:"thresholds"`
	VSphere                        []VSphere            `yaml:"vsphere"`
	Web                            Web                  `yaml:"web"`
	Zabbix                         Zabbix               `yaml:"zabbix"`
}

type Log struct {
	OpenTelemetry OpenTelemetry `yaml:"opentelemetry"`
	// MetricsRules are named, reusable libraries of metric definitions
	// (same shape as a receiver's own inline metrics: entries). An entry
	// does nothing on its own until a receiver includes it by name from its
	// own metrics: list (see OpenTelemetry.Receivers/LogReceiver).
	MetricsRules map[string][]LogMetricEntry `yaml:"metrics_rules"`
}

// OpenTelemetryConfig holds OpenTelemetry-related settings that aren't specific to any one signal
// (logs/metrics/traces), as opposed to Log.OpenTelemetry which is log-specific.
type OpenTelemetryConfig struct {
	NetworkListeners map[string]NetworkListener `yaml:"listeners"`
}

// NetworkListener mirrors otlpreceiver.Config: a protocol's presence enables
// it, nil disables it, no separate enable flag.
type NetworkListener struct {
	Protocols NetworkProtocols `yaml:"protocols"`
}

type NetworkProtocols struct {
	GRPC *NetworkEndpoint `yaml:"grpc"`
	HTTP *NetworkEndpoint `yaml:"http"`
}

// NetworkEndpoint mirrors OTel's single "host:port" endpoint string. Left
// blank, otlpreceiver's factory default applies (localhost: gRPC port 4317, HTTP port 4318).
type NetworkEndpoint struct {
	Endpoint string `yaml:"endpoint"`
}

// ContainerExcludeRule matches a container by exact name and/or
// label/annotation selector, to veto it from BOTH log shipping
// auto_discovery and metrics container-label detection (see
// OpenTelemetry.ContainerExclude) -- one shared list for both concerns.
type ContainerExcludeRule struct {
	ContainerName string            `yaml:"container_name"`
	Selectors     map[string]string `yaml:"selectors"`
}

// OTELOperator is raw YAML built into an operator.Config before use.
type OTELOperator = map[string]any

// OTELFilters is raw YAML decoded into a filterprocessor.LogFilters before use.
type OTELFilters = map[string]any

type OpenTelemetry struct {
	// ShippingEnable is the master switch for log SHIPPING only (renamed
	// from Enable): it no longer gates metrics, which run whenever a
	// receiver/metrics_rules/container label opts a source in, independent
	// of shipping.
	ShippingEnable bool `yaml:"shipping_enable"`
	// ReceiversDefaultSendLogs is the global default for whether a receiver ships its logs, applied
	// unless the receiver overrides it with its own send_logs field. Irrelevant with zero configured
	// receivers. Named distinctly from ShippingEnable (the master shipping switch) and from a
	// receiver's own send_logs (this default's per-receiver override) to keep the three apart.
	ReceiversDefaultSendLogs bool                      `yaml:"receivers_default_send_logs"`
	AutoDiscovery            AutoDiscovery             `yaml:"auto_discovery"`
	KnownLogFormats          map[string][]OTELOperator `yaml:"known_log_formats"`
	// Receivers are named log sources, for shipping and/or metrics (see
	// LogReceiver's doc comment for its Glouton-specific keys).
	Receivers map[string]LogReceiver `yaml:"receivers"`
	// map: container name -> format to apply
	ContainerFormat map[string]string      `yaml:"container_format"`
	GlobalFilters   OTELFilters            `yaml:"global_filters"`
	KnownLogFilters map[string]OTELFilters `yaml:"known_log_filters"`
	// map: container name -> filter to apply
	ContainerFilter map[string]string `yaml:"container_filter"`
	// ContainerExclude vetoes a matching container from both shipping
	// auto_discovery and metrics container-label detection at once.
	ContainerExclude []ContainerExcludeRule `yaml:"container_exclude"`
	// GRPC/HTTP are the deprecated pre-network-receivers listener shape, kept as real typed fields
	// purely so they can be migrated (see synthesizeLegacyNetworkListener) rather than consumed by
	// anything downstream: nothing outside that migration reads them, and it clears them once it has run.
	//
	// They must be real Config fields rather than raw keys deleted by a per-provider migration, for two
	// reasons this shape gets wrong otherwise. First, the new shape fuses address+port into one
	// "host:port" endpoint string, so a per-provider migration can only synthesize an endpoint from the
	// halves that one file happens to set -- a second conf.d file overriding just "port" has no complete
	// endpoint to contribute and its value was silently dropped. As plain sibling scalars these merge
	// per-leaf across providers exactly like any other config, and the fusion then happens once on the
	// merged result. Second, envToKeyFunc derives the accepted environment variables from this struct's
	// keys, so without these fields GLOUTON_LOG_OPENTELEMETRY_GRPC_ENABLE resolved to no key at all and
	// was dropped before any migration could see it -- silently, since the key never entered koanf.
	//
	// Deliberately absent from DefaultConfig(): the zero value is what "unset" has to look like here,
	// and defaults for address/port are applied by the migration itself.
	GRPC LegacyEnableListener `yaml:"grpc"`
	HTTP LegacyEnableListener `yaml:"http"`
}

// LegacyEnableListener is the deprecated log.opentelemetry.grpc/http {enable, address, port} shape,
// superseded by opentelemetry.listeners + a receiver's from_listeners. See OpenTelemetry.GRPC.
type LegacyEnableListener struct {
	Enable  bool   `yaml:"enable"`
	Address string `yaml:"address"`
	Port    int    `yaml:"port"`
}

type AutoDiscovery struct {
	AllEnable                 bool `yaml:"all_enable"`
	JournaldEnable            bool `yaml:"journald_enable"`
	SyslogEnable              bool `yaml:"syslog_enable"`
	AuditdEnable              bool `yaml:"auditd_enable"`
	ContainerAndServiceEnable bool `yaml:"container_and_service_enable"`
}

// LogReceiver is raw YAML: real filelogreceiver/fileconsumer fields
// (include, exclude, start_at, encoding, ...) paste in verbatim (see
// logsource.SetupLogReceiverFactories), alongside Glouton's own additions:
//
//   - container_name / container_selectors: watch matching containers'
//     logs, in addition to (or instead of) include. Both can be combined,
//     and combined with include, on the same receiver -- every match feeds
//     this one receiver's shipping/metrics as a single unit.
//   - from_listeners: []string, pull logs from one or more
//     opentelemetry.listeners entries by name ({from_listeners: [name,...]}).
//     Every named entry must already exist under opentelemetry.listeners --
//     there's no implicit/default listener, so unrelated config elsewhere can never
//     change what this receiver participates in. A typo/missing entry is reported at load
//     time by validateLogReceivers (errReceiverNetworkListenerUndefined), and again at
//     wiring time by PlanSharedNetworkListeners (errUndefinedNetworkListener).
//   - send_logs: override the global OpenTelemetry.ReceiversDefaultSendLogs
//     default for this receiver.
//   - log_format / operators: parse each line into attributes before
//     shipping and before any metrics condition runs, applying to both.
//   - filters: OTELFilters for SHIPPING only (drop/keep, not counting).
//   - metrics: []LogMetricEntry, this receiver's log-to-metric definitions.
//
// A receiver needs at least one of include/container_name/
// container_selectors/from_listeners -- enforced by validateLogReceivers.
type LogReceiver = map[string]any

// LogMetricEntry is one item of a receiver's metrics: list, or of a
// Log.MetricsRules[name] list. Either:
//
//   - {include: <metrics_rules-name>}, pulling in every metric defined
//     under that Log.MetricsRules entry, or
//   - an inline definition decoding into countconnector.MetricInfo
//     (conditions, attributes), plus Glouton's own additions: regex
//     (sugar for a single 'IsMatch(body, "...")' condition), labels (a
//     static map[string]string stamped on every sample, zero cardinality
//     risk -- distinct from attributes' dynamic per-value grouping), and
//     item (explicit override of the auto-derived item -- see the item
//     derivation rules in otel/logmetrics).
//
// item, labels and attributes can all three produce a value for the same label key (most notably
// "item" itself, since nothing stops an attributes entry from using that key, or a labels entry from
// setting "item"). A top-level item: and a labels: {item: ...} entry are two equally valid spellings
// of the same override -- item: wins if both are set, otherwise labels: {item: ...} is used exactly as
// if it had been written as item: (see extractItem in otel/logmetrics/metrics.go). Every other
// labels: key, and "item"/"__name__" set via attributes, follow the usual precedence: item > labels >
// attributes -- the auto-derived/explicit item and every labels: entry are reserved-key-safe, static,
// and operator-declared, so they always win; an attribute (extracted from the log line's own content
// at match time, so effectively untrusted/dynamic) only fills in a label key nothing else has already
// claimed -- it is dropped, never promoted, on collision. See otel/logmetrics/registry.go's
// resolve()/resolveAttrCounterLocked for the implementation.
//
// Kept raw (not a struct) for the same verbatim-passthrough reason as
// LogReceiver, and so "item" stays distinguishable as absent (derive it)
// vs. explicitly set to "" (used by legacy log.inputs migration to
// preserve today's empty item on already-live metrics).
type LogMetricEntry = map[string]any

type Smart struct {
	Enable         bool     `yaml:"enable"`
	PathSmartctl   string   `yaml:"path_smartctl"`
	Devices        []string `yaml:"devices"`
	Excludes       []string `yaml:"excludes"`
	MaxConcurrency int      `yaml:"max_concurrency"`
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

type IPMI struct {
	Enable           bool   `yaml:"enable"`
	BinarySearchPath string `yaml:"bin_search_path"`
	UseSudo          bool   `yaml:"use_sudo"`
	Timeout          int    `yaml:"timeout"`
}

type SSACLI struct {
	Enable           bool   `yaml:"enable"`
	BinarySearchPath string `yaml:"bin_search_path"`
	UseSudo          bool   `yaml:"use_sudo"`
	Timeout          int    `yaml:"timeout"`
}

type Bleemeo struct {
	AccountID                         string       `yaml:"account_id"`
	APIBase                           string       `yaml:"api_base"`
	APISSLInsecure                    bool         `yaml:"api_ssl_insecure"`
	Cache                             BleemeoCache `yaml:"cache"`
	ContainerRegistrationDelaySeconds int          `yaml:"container_registration_delay_seconds"`
	Enable                            bool         `yaml:"enable"`
	InitialAgentName                  string       `yaml:"initial_agent_name"`
	InitialServerGroupName            string       `yaml:"initial_server_group_name"`
	InitialServerGroupNameForSNMP     string       `yaml:"initial_server_group_name_for_snmp"`
	InitialServerGroupNameForVSphere  string       `yaml:"initial_server_group_name_for_vsphere"`
	MQTT                              BleemeoMQTT  `yaml:"mqtt"`
	RegistrationKey                   string       `yaml:"registration_key"`
	Sentry                            Sentry       `yaml:"sentry"`
}

type BleemeoCache struct {
	DeactivatedMetricsExpirationDays int `yaml:"deactivated_metrics_expiration_days"`
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
	Enable             bool                     `yaml:"enable"`
	ScraperName        string                   `yaml:"scraper_name"`
	ScraperSendUUID    bool                     `yaml:"scraper_send_uuid"`
	UserAgent          string                   `yaml:"user_agent"`
	DefaultDNSResolver string                   `yaml:"default_dns_resolver"`
	Targets            []BlackboxTarget         `yaml:"targets"`
	Modules            map[string]bbConf.Module `yaml:"modules"`
}

type BlackboxTarget struct {
	Name   string `yaml:"name"`
	URL    string `yaml:"url"`
	Module string `yaml:"module"`
}

type Agent struct {
	CloudImageCreationFile string          `yaml:"cloudimage_creation_file"`
	InstallationFormat     string          `yaml:"installation_format"`
	FactsFile              string          `yaml:"facts_file"`
	NetstatFile            string          `yaml:"netstat_file"`
	StateFile              string          `yaml:"state_file"`
	StateCacheFile         string          `yaml:"state_cache_file"`
	StateResetFile         string          `yaml:"state_reset_file"`
	DeprecatedStateFile    string          `yaml:"deprecated_state_file"`
	StateDirectory         string          `yaml:"state_directory"`
	EnableCrashReporting   bool            `yaml:"enable_crash_reporting"`
	MaxCrashReportsCount   int             `yaml:"max_crash_reports_count"`
	UpgradeFile            string          `yaml:"upgrade_file"`
	AutoUpgradeFile        string          `yaml:"auto_upgrade_file"`
	ProcessExporter        ProcessExporter `yaml:"process_exporter"`
	PublicIPIndicator      string          `yaml:"public_ip_indicator"`
	NodeExporter           NodeExporter    `yaml:"node_exporter"`
	WindowsExporter        NodeExporter    `yaml:"windows_exporter"`
	Telemetry              Telemetry       `yaml:"telemetry"`
	OverrideHostname       string          `yaml:"override_hostname"`
	LocalStore             LocalStore      `yaml:"local_store"`
}

type LocalStore struct {
	// Enable is a tri-state: nil means "auto" (on iff bleemeo.enable is
	// false), true forces on, false forces off.
	Enable    *bool         `yaml:"enable"`
	Path      string        `yaml:"path"`
	Retention time.Duration `yaml:"retention"`
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
	Enable       bool         `yaml:"enable"`
	Endpoints    WebEndpoints `yaml:"endpoints"`
	LocalUI      LocalUI      `yaml:"local_ui"`
	Listener     Listener     `yaml:"listener"`
	StaticCDNURL string       `yaml:"static_cdn_url"`
}

type WebEndpoints struct {
	DebugEnable bool `yaml:"debug_enable"`
}

type LocalUI struct {
	Enable bool `yaml:"enable"`
}

type Listener struct {
	Address string `yaml:"address"`
	Port    int    `yaml:"port"`
}

type Service struct {
	// The name of the service type, like "apache", "nginx". For custom service, it could be any value.
	Type string `yaml:"type"`
	// Instance of the service, used to differentiate between two same services (like two apaches)
	Instance string `yaml:"instance"`
	// The port the service is running on.
	Port int `yaml:"port"`
	// Ports that should be ignored.
	IgnorePorts []int `yaml:"ignore_ports"`
	// The address of the service.
	Address string `yaml:"address"`
	// Tags associated with this service.
	Tags []string `yaml:"tags"`
	// The delay between two consecutive checks in seconds.
	Interval int `yaml:"interval"`
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
	// IncludedItems or exclude specific items (for instance Jenkins jobs).
	IncludedItems []string `yaml:"included_items"`
	ExcludedItems []string `yaml:"excluded_items"`
	// Log processing config.
	LogFiles  []ServiceLogFile `yaml:"log_files"`
	LogFormat string           `yaml:"log_format"`
	LogFilter string           `yaml:"log_filter"`
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

type ServiceLogFile struct {
	FilePath  string `yaml:"file_path"`
	LogFormat string `yaml:"log_format"`
	LogFilter string `yaml:"log_filter"`
}

type Container struct {
	Filter           ContainerFilter  `yaml:"filter"`
	Type             string           `yaml:"type"`
	PIDNamespaceHost bool             `yaml:"pid_namespace_host"`
	Runtime          ContainerRuntime `yaml:"runtime"`
	// AllowedLabelOverrides is the list of service-configuration fields (by
	// their YAML name) that may be overridden through "glouton.*" container
	// labels and Kubernetes annotations. Fields that allow running arbitrary
	// commands (like check_command) are excluded from the built-in defaults and
	// must be added here explicitly: since any user able to start a container or
	// a Kubernetes pod can set these labels/annotations, honoring them would
	// otherwise let that user run commands as Glouton (usually root). Only enable
	// the dangerous fields when container/pod creation is restricted to trusted
	// users.
	//
	// This list is empty by default and a user-provided value replaces it. The
	// built-in safe defaults (DefaultAllowedLabelOverrides) are added on top only
	// when IncludeDefaultLabelOverrides is true.
	AllowedLabelOverrides []string `yaml:"allowed_label_overrides"`
	// IncludeDefaultLabelOverrides controls whether the built-in safe defaults
	// (DefaultAllowedLabelOverrides) are added to AllowedLabelOverrides. It is
	// true by default.
	IncludeDefaultLabelOverrides bool `yaml:"include_default_label_overrides"`
}

// EffectiveAllowedLabelOverrides returns the list of service-configuration
// fields that may be overridden through container labels/annotations: the
// explicit AllowedLabelOverrides plus, when IncludeDefaultLabelOverrides is
// true, the built-in safe defaults.
func (c Container) EffectiveAllowedLabelOverrides() []string {
	allowed := append([]string{}, c.AllowedLabelOverrides...)

	if c.IncludeDefaultLabelOverrides {
		allowed = append(allowed, DefaultAllowedLabelOverrides()...)
	}

	return allowed
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

type VSphere struct {
	URL                string `yaml:"url"`
	Username           string `yaml:"username"`
	Password           string `yaml:"password"`
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify"`
	SkipMonitorVMs     bool   `yaml:"skip_monitor_vms"`
}

type Mdstat struct {
	Enable    bool   `yaml:"enable"`
	PathMdadm string `yaml:"path_mdadm"`
	UseSudo   bool   `yaml:"use_sudo"`
}
