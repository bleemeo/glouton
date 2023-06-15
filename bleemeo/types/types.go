// Copyright 2015-2023 Bleemeo
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

package types

import (
	"context"
	"glouton/config"
	"glouton/discovery"
	"glouton/facts"
	"glouton/prometheus/exporter/snmp"
	"glouton/prometheus/rules"
	"glouton/threshold"
	"glouton/types"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
)

// GlobalOption are option user by most component of bleemeo.Connector.
type GlobalOption struct {
	Config                  config.Config
	ConfigItems             []config.Item
	State                   State
	Facts                   FactProvider
	Process                 ProcessProvider
	Docker                  DockerProvider
	SNMP                    []*snmp.Target
	SNMPOnlineTarget        func() int
	Store                   Store
	PushPoints              types.PointPusher
	Discovery               discovery.PersistentDiscoverer
	MonitorManager          MonitorManager
	MetricFormat            types.MetricFormat
	NotifyFirstRegistration func()
	NotifyLabelsUpdate      func()
	BlackboxScraperName     string
	ReloadState             BleemeoReloadState

	UpdateMetricResolution func(ctx context.Context, defaultResolution time.Duration, snmpResolution time.Duration)
	UpdateThresholds       func(ctx context.Context, thresholds map[string]threshold.Threshold, firstUpdate bool)
	UpdateUnits            func(units map[string]threshold.Unit)
	RebuildPromQLRules     func(promqlRules []rules.PromQLRule) error
	IsContainerEnabled     func(facts.Container) (bool, bool)
	// IsMetricAllowed returns whether a metric is allowed or not in the config files.
	IsMetricAllowed func(lbls map[string]string) bool
}

// MonitorManager is the interface used by Bleemeo to update the dynamic monitors list.
type MonitorManager interface {
	// UpdateDynamicTargets updates the list of dynamic monitors to watch.
	UpdateDynamicTargets(monitors []types.Monitor) error
}

// Config is the interface used by Bleemeo to access Config.
type Config interface {
	String(string) string
	StringList(string) []string
	Int(string) int
	DurationMap(string) map[string]time.Duration
	Bool(string) bool
}

// State is the interface used by Bleemeo to access State.
type State interface {
	Set(key string, object interface{}) error
	Get(key string, result interface{}) error
	BleemeoCredentials() (string, string)
	SetBleemeoCredentials(agentUUID string, password string) error
}

// FactProvider is the interface used by Bleemeo to access facts.
type FactProvider interface {
	Facts(ctx context.Context, maxAge time.Duration) (facts map[string]string, err error)
}

// ProcessProvider is the interface used by Bleemeo to access processes.
type ProcessProvider interface {
	Processes(ctx context.Context, maxAge time.Duration) (processes map[int]facts.Process, err error)
	TopInfo(ctx context.Context, maxAge time.Duration) (topinfo facts.TopInfo, err error)
}

// DockerProvider is the interface used by Bleemeo to access Docker containers.
type DockerProvider interface {
	Containers(ctx context.Context, maxAge time.Duration, includeIgnored bool) (containers []facts.Container, err error)
	ContainerLastKill(containerID string) time.Time
	LastUpdate() time.Time
}

// Store is the interface used by Bleemeo to access Metric Store.
type Store interface {
	Metrics(filters map[string]string) (result []types.Metric, err error)
	MetricsCount() int
	DropMetrics(labelsList []map[string]string)
	AddNotifiee(func([]types.MetricPoint)) int
	RemoveNotifiee(int)
}

// DisableReason is a list of status why Bleemeo connector may be (temporary) disabled.
type DisableReason int

// AgentID is an agent UUID.
// This type exists for the sole purpose of making type definitions clearer.
type AgentID string

// List of possible value for DisableReason.
const (
	NotDisabled DisableReason = iota
	DisableDuplicatedAgent
	DisableTooManyErrors
	DisableTooManyRequests
	DisableAgentTooOld
	DisableAuthenticationError
	DisableTimeDrift
)

func (r DisableReason) String() string {
	switch r {
	case DisableDuplicatedAgent:
		return "duplicated state.json"
	case DisableTooManyErrors:
		return "too many errors"
	case DisableTooManyRequests:
		return "too many requests - client is throttled"
	case DisableAgentTooOld:
		return "this agent is too old, and cannot be connected to our managed service"
	case DisableAuthenticationError:
		return "authentication error with Bleemeo API"
	case DisableTimeDrift:
		return "local time is too different from actual time"
	case NotDisabled:
		return "not disabled"
	default:
		return "unspecified reason"
	}
}

// DenyReason is the reason why a metric was denied.
type DenyReason int

const (
	NotDenied DenyReason = iota
	DenyNotAvailableInCurrentPlan
	DenyErrorOccurred
	DenyNoDockerIntegration
	DenyItemTooLong
	DenyMissingContainerID
)

func (d DenyReason) String() string {
	switch d {
	case DenyNotAvailableInCurrentPlan:
		return "not available in the current plan"
	case DenyErrorOccurred:
		return "an unexpected error occurred"
	case DenyNoDockerIntegration:
		return "docker integration is not available in the current plan"
	case DenyItemTooLong:
		return "item too long"
	case DenyMissingContainerID:
		return "temporarily denied, waiting to detect the associated container"
	case NotDenied:
		return "not denied"
	default:
		return "unspecified reason"
	}
}

type GloutonAccountConfig struct {
	ID                    string
	Name                  string
	LiveProcessResolution time.Duration
	LiveProcess           bool
	DockerIntegration     bool
	SNMPIntegration       bool
	Suspended             bool
	AgentConfigByName     map[string]GloutonAgentConfig
	AgentConfigByID       map[string]GloutonAgentConfig
	MaxCustomMetrics      int
}

type GloutonAgentConfig struct {
	MetricsAllowlist map[string]bool
	MetricResolution time.Duration
}

// BleemeoReloadState is used to keep some Bleemeo components alive during reloads.
type BleemeoReloadState interface {
	MQTTReloadState() MQTTReloadState
	SetMQTTReloadState(client MQTTReloadState)
	NextFullSync() time.Time
	SetNextFullSync(t time.Time)
	FullSyncCount() int
	SetFullSyncCount(count int)
	JWT() JWT
	SetJWT(jwt JWT)
	Close()
}

// MQTTReloadState allows changing some event handlers at runtime.
type MQTTReloadState interface {
	SetMQTT(mqtt MQTTClient)
	OnConnect(cli paho.Client)
	ConnectChannel() <-chan paho.Client
	OnNotification(cli paho.Client, msg paho.Message)
	NotificationChannel() <-chan paho.Message
	PopPendingPoints() []types.MetricPoint
	SetPendingPoints(points []types.MetricPoint)
	ClientState() types.MQTTReloadState
	Close()
}

type MQTTClient interface {
	Publish(topic string, payload []byte, retry bool)
	Run(ctx context.Context)
	IsConnectionOpen() bool
	DiagnosticArchive(ctx context.Context, archive types.ArchiveWriter) error
	LastReport() time.Time
	Disable(until time.Time)
	DisabledUntil() time.Time
	Disconnect(timeout time.Duration)
}

// JWT used to authenticate with the Bleemeo API.
type JWT struct {
	Token   string
	Refresh string
}
