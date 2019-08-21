package types

import (
	"agentgo/discovery"
	"agentgo/facts"
	"agentgo/threshold"
	"agentgo/types"
	"context"
	"time"

	"github.com/influxdata/telegraf"
)

// GlobalOption are option user by most component of bleemeo.Connector
type GlobalOption struct {
	Config    Config
	State     State
	Facts     FactProvider
	Process   ProcessProvider
	Docker    DockerProvider
	Store     Store
	Acc       telegraf.Accumulator
	Discovery discovery.PersistentDiscoverer

	UpdateMetricResolution func(resolution time.Duration)
	UpdateThresholds       func(thresholds map[threshold.MetricNameItem]threshold.Threshold)
	UpdateUnits            func(units map[threshold.MetricNameItem]threshold.Unit)
}

// Config is the interface used by Bleemeo to access Config
type Config interface {
	String(string) string
	Int(string) int
	Bool(string) bool
}

// State is the interaface used by Bleemeo to access State
type State interface {
	AgentID() string
	AgentPassword() string
	SetAgentIDPassword(string, string)
	SetCache(key string, object interface{}) error
	Cache(key string, result interface{}) error
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
