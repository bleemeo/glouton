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
	"glouton/threshold"
	"glouton/types"
	"time"

	"github.com/influxdata/telegraf"
)

// GlobalOption are option user by most component of bleemeo.Connector
type GlobalOption struct {
	Config                  Config
	State                   State
	Facts                   FactProvider
	Process                 ProcessProvider
	Docker                  DockerProvider
	Store                   Store
	Acc                     telegraf.Accumulator
	Discovery               discovery.PersistentDiscoverer
	MetricFormat            types.MetricFormat
	NotifyFirstRegistration func(ctx context.Context)

	UpdateMetricResolution func(resolution time.Duration)
	UpdateThresholds       func(thresholds map[threshold.MetricNameItem]threshold.Threshold, firstUpdate bool)
	UpdateUnits            func(units map[threshold.MetricNameItem]threshold.Unit)
}

// Config is the interface used by Bleemeo to access Config
type Config interface {
	String(string) string
	StringList(string) []string
	Int(string) int
	Bool(string) bool
}

// State is the interaface used by Bleemeo to access State
type State interface {
	Set(key string, object interface{}) error
	Get(key string, result interface{}) error
}

// FactProvider is the interface used by Bleemeo to access facts
type FactProvider interface {
	Facts(ctx context.Context, maxAge time.Duration) (facts map[string]string, err error)
}

// ProcessProvider is the interface used by Bleemeo to access processes
type ProcessProvider interface {
	Processes(ctx context.Context, maxAge time.Duration) (processes map[int]facts.Process, err error)
	TopInfo(ctx context.Context, maxAge time.Duration) (topinfo facts.TopInfo, err error)
}

// DockerProvider is the interface used by Bleemeo to access Docker containers
type DockerProvider interface {
	Containers(ctx context.Context, maxAge time.Duration, includeIgnored bool) (containers []facts.Container, err error)
	ContainerLastKill(containerID string) time.Time
}

// Store is the interface used by Bleemeo to access Metric Store
type Store interface {
	Metrics(filters map[string]string) (result []types.Metric, err error)
	MetricsCount() int
	DropMetrics(labelsList []map[string]string)
	AddNotifiee(func([]types.MetricPoint)) int
	RemoveNotifiee(int)
}

// DisableReason is a list of status why Bleemeo connector may be (temporary) disabled
type DisableReason int

// List of possible value for DisableReason
const (
	NotDisabled DisableReason = iota
	DisableDuplicatedAgent
	DisableTooManyErrors
	DisableAgentTooOld
	DisableMaintenance
)

func (r DisableReason) String() string {
	switch r {
	case DisableDuplicatedAgent:
		return "duplicated state.json"
	case DisableTooManyErrors:
		return "too many errors"
	case DisableAgentTooOld:
		return "this agent being too old"
	case DisableMaintenance:
		return "maintenance on Bleemeo API"
	default:
		return "unspecified reason"
	}
}
