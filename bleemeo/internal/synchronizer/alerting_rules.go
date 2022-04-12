package synchronizer

import (
	"encoding/json"
	"fmt"
	bleemeoTypes "glouton/bleemeo/types"
	"glouton/logger"
	"time"
)

// alertingRules returns the alerting rules from the API.
func (s *Synchronizer) alertingRules() (
	alertingRules []bleemeoTypes.AlertingRule,
	resolution time.Duration,
	err error,
) {
	agents := s.option.Cache.AgentsByUUID()
	configs := s.option.Cache.AccountConfigsByUUID()

	agent := agents[s.agentID]
	cfg := configs[agent.CurrentConfigID]
	resolution = cfg.AgentConfigByID[agent.AgentType].MetricResolution

	fmt.Printf("!!! resolution: %v\n", resolution)

	params := map[string]string{
		"active": "true",
	}

	// TODO: The client is always uninitialized the first time this function is called.
	result, err := s.client.Iter(s.ctx, "alertingrule", params)
	if err != nil {
		return nil, 0, fmt.Errorf("client iter: %w", err)
	}

	alertingRules = make([]bleemeoTypes.AlertingRule, 0, len(result))

	for _, jsonMessage := range result {
		var alertingRule bleemeoTypes.AlertingRule

		if err := json.Unmarshal(jsonMessage, &alertingRule); err != nil {
			logger.V(2).Printf("Failed to unmarshal alerting rule: %v", err)

			continue
		}

		alertingRules = append(alertingRules, alertingRule)
	}

	return alertingRules, resolution, nil
}
