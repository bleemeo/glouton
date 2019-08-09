package types

import (
	"agentgo/discovery"
	"agentgo/facts"
	"agentgo/types"
	"context"
	"time"
)

// GlobalOption are option user by most component of bleemeo.Connector
type GlobalOption struct {
	Config    Config
	State     State
	Facts     FactProvider
	Docker    DockerProvider
	Store     Store
	Discovery discovery.PersistentDiscoverer

	UpdateMetricResolution func(resolution time.Duration)
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

// DockerProvider is the interface used by Bleemeo to access Docker containers
type DockerProvider interface {
	Containers(ctx context.Context, maxAge time.Duration, includeIgnored bool) (containers []facts.Container, err error)
}

// Store is the interface used by Bleemeo to access Metric Store
type Store interface {
	Metrics(filters map[string]string) (result []types.Metric, err error)
	MetricsCount() int
	DropMetrics(labelsList []map[string]string)
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
