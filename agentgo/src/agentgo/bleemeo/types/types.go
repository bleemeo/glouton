package types

import (
	"context"
	"time"
)

// Config is the interface used by Bleemeo to access Config
type Config interface {
	String(string) string
	Int(string) int
	Bool(string) bool
}

// State is the interaface user by Bleemeo to access State
type State interface {
	AgentID() string
	AgentPassword() string
	SetAgentIDPassword(string, string)
	SetCache(key string, object interface{}) error
	Cache(key string, result interface{}) error
}

// FactProvider is the interface user by Bleemeo to access facts
type FactProvider interface {
	Facts(ctx context.Context, maxAge time.Duration) (facts map[string]string, err error)
}

// DisableReason is a list of status why Bleemeo connector may be (temporary) disabled
type DisableReason int

// List of possible value for DisableReason
const (
	NotDisabled DisableReason = iota
	DisableDuplicatedAgent
	DisableTooManyErrors
	DisableAgentTooOld
	DisableMaintaince
)
