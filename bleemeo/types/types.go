// Copyright 2015-2019 Bleemeo
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
	Config                  Config
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
	NotifyFirstRegistration func(ctx context.Context)
	NotifyLabelsUpdate      func(ctx context.Context)
	BlackboxScraperName     string

	UpdateMetricResolution func(defaultResolution time.Duration, snmpResolution time.Duration)
	UpdateThresholds       func(thresholds map[threshold.MetricNameItem]threshold.Threshold, firstUpdate bool)
	UpdateUnits            func(units map[threshold.MetricNameItem]threshold.Unit)
	RebuildAlertingRules   func(metrics []rules.MetricAlertRule) error
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
	Bool(string) bool
}

// State is the interface used by Bleemeo to access State.
type State interface {
	Set(key string, object interface{}) error
	Get(key string, result interface{}) error
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

type GloutonAccountConfig struct {
	ID                    string
	Name                  string
	LiveProcessResolution time.Duration
	LiveProcess           bool
	DockerIntegration     bool
	SNMPIntergration      bool
	AgentConfigByName     map[string]GloutonAgentConfig
	AgentConfigByID       map[string]GloutonAgentConfig
}

type GloutonAgentConfig struct {
	MetricsAllowlist map[string]bool
	MetricResolution time.Duration
}

// BleemeoReloadState is used to keep some Bleemeo components alive during reloads.
type BleemeoReloadState interface {
	PahoWrapper() PahoWrapper
	SetPahoWrapper(client PahoWrapper)
	Close()
}

// PahoWrapper allows changing some event handlers at runtime.
type PahoWrapper interface {
	Client() paho.Client
	SetClient(cli paho.Client)
	OnConnectionLost(cli paho.Client, err error)
	SetOnConnectionLost(f paho.ConnectionLostHandler)
	OnConnect(cli paho.Client)
	SetOnConnect(f paho.OnConnectHandler)
	OnNotification(cli paho.Client, msg paho.Message)
	SetOnNotification(f paho.MessageHandler)
	PendingPoints() []types.MetricPoint
	SetPendingPoints(points []types.MetricPoint)
	Close()
}
