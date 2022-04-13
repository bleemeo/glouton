package synchronizer

import (
	"context"
	"encoding/json"
	"fmt"
	bleemeoTypes "glouton/bleemeo/types"
	"glouton/logger"
	"time"
)

func (s *Synchronizer) syncAlertingRules(ctx context.Context, fullSync bool, onlyEssential bool) error {
	if s.option.RebuildPromQLRules == nil {
		return nil
	}

	alertingRules, resolution, err := s.alertingRules(ctx)
	if err != nil {
		return fmt.Errorf("failed to get PromQL rules: %w", err)
	}

	s.option.Cache.SetAlertingRules(alertingRules)

	if err := s.option.RebuildPromQLRules(alertingRules, resolution); err != nil {
		return fmt.Errorf("failed to rebuild PromQL rules: %v", err)
	}

	return nil
}

// alertingRules returns the alerting rules from the API.
func (s *Synchronizer) alertingRules(ctx context.Context) (
	alertingRules []bleemeoTypes.AlertingRule,
	resolution time.Duration,
	err error,
) {
	agents := s.option.Cache.AgentsByUUID()
	configs := s.option.Cache.AccountConfigsByUUID()

	agent := agents[s.agentID]
	cfg := configs[agent.CurrentConfigID]
	resolution = cfg.AgentConfigByID[agent.AgentType].MetricResolution

	params := map[string]string{
		"active": "true",
	}

	result, err := s.client.Iter(ctx, "alertingrule", params)
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
