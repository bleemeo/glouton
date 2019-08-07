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

// InterfaceToAgentFact convert an interface{} to an AgentFact
//
// input should be either an AgentFact or a map[string]interface{} / map[string]string
//
// Return a zero AgentFact (e.g. AgentFact.ID == "") if unable to convert.
func InterfaceToAgentFact(input interface{}) AgentFact {
	switch input := input.(type) {
	case AgentFact:
		return input
	case map[string]string:
		return AgentFact{
			ID:    input["id"],
			Key:   input["key"],
			Value: input["value"],
		}
	case map[string]interface{}:
		id, ok := input["id"].(string)
		if !ok {
			break
		}
		key, ok := input["key"].(string)
		if !ok {
			break
		}
		value, ok := input["value"].(string)
		if !ok {
			break
		}
		return AgentFact{
			ID:    id,
			Key:   key,
			Value: value,
		}
	}
	return AgentFact{}
}
