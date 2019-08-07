package types

import (
	"time"
)

// AgentFact is an agent facts
type AgentFact struct {
	ID    string
	Key   string
	Value string
}

// Agent is an Agent object on Bleemeo API
type Agent struct {
	ID              string    `json:"id"`
	CreatedAt       time.Time `json:"created_at"`
	AccountID       string    `json:"account"`
	NextConfigAt    time.Time `json:"next_config_at"`
	CurrentConfigID string    `json:"current_config"`
}

// AccountConfig is the configuration used by this agent
type AccountConfig struct {
	ID                    string `json:"id"`
	Name                  string `json:"name"`
	MetricsAgentWhitelist string `json:"metrics_agent_whitelist"`
	MetricAgentResolution int    `json:"metrics_agent_resolution"`
	LiveProcessResolution int    `json:"live_process_resolution"`
	DockerIntegration     bool   `json:"docker_integration"`
}

