package synchronizer

import (
	"context"
	"encoding/json"
	"fmt"
	bleemeoTypes "glouton/bleemeo/types"
	"glouton/logger"
	"glouton/prometheus/rules"
)

func (s *Synchronizer) syncAlertingRules(ctx context.Context, fullSync bool, onlyEssential bool) (updateThresholds bool, err error) {
	if s.option.RebuildPromQLRules == nil {
		return false, nil
	}

	alertingRules, err := s.alertingRules(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to get PromQL rules: %w", err)
	}

	s.option.Cache.SetAlertingRules(alertingRules)

	err = s.UpdateAlertingRules()

	return true, err
}

// alertingRules returns the alerting rules from the API.
func (s *Synchronizer) alertingRules(ctx context.Context) (alertingRules []bleemeoTypes.AlertingRule, err error) {
	params := map[string]string{
		"active": "true",
	}

	result, err := s.client.Iter(ctx, "alertingrule", params)
	if err != nil {
		return nil, fmt.Errorf("client iter: %w", err)
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

	return alertingRules, nil
}

// UpdateAlertingRules updates the alerting rules from the cache.
func (s *Synchronizer) UpdateAlertingRules() error {
	agents := s.option.Cache.AgentsByUUID()
	configs := s.option.Cache.AccountConfigsByUUID()

	alertingRules := s.option.Cache.AlertingRules()

	var promqlRules []rules.PromQLRule
	for _, rule := range alertingRules {
		newPromQLRules := s.alertingRuleToPromQLRules(rule, agents, configs)
		promqlRules = append(promqlRules, newPromQLRules...)
	}

	if err := s.option.RebuildPromQLRules(promqlRules); err != nil {
		return fmt.Errorf("failed to rebuild PromQL rules: %v", err)
	}

	return nil
}

// alertingRuleToPromQLRule converts an AlertingRule to PromQLRules (one for each known agent).
func (s *Synchronizer) alertingRuleToPromQLRules(
	alertingRule bleemeoTypes.AlertingRule,
	agents map[string]bleemeoTypes.Agent,
	configs map[string]bleemeoTypes.GloutonAccountConfig,
) []rules.PromQLRule {
	agentIDs := s.filterAgents(alertingRule.Agents, agents)
	promqlRules := make([]rules.PromQLRule, 0, len(agentIDs))

	for _, agentID := range agentIDs {
		// Find the resolution of this agent from the config.
		agent := agents[agentID]
		cfg := configs[agent.CurrentConfigID]
		resolution := cfg.AgentConfigByID[agent.AgentType].MetricResolution

		promqlRule := rules.PromQLRule{
			Name:                alertingRule.Name,
			ID:                  alertingRule.ID,
			InstanceID:          agentID,
			WarningQuery:        alertingRule.WarningQuery,
			WarningDelaySecond:  alertingRule.WarningDelaySecond,
			CriticalQuery:       alertingRule.CriticalQuery,
			CriticalDelaySecond: alertingRule.CriticalDelaySecond,
			Resolution:          resolution,
		}

		promqlRules = append(promqlRules, promqlRule)
	}

	return promqlRules
}

// filterAgents removes the agents this Glouton doesn't manage.
func (s *Synchronizer) filterAgents(agents []string, knownAgentsByUUID map[string]bleemeoTypes.Agent) []string {
	var filteredAgents []string

	for _, agent := range agents {
		for knownAgentID := range knownAgentsByUUID {
			if agent == knownAgentID {
				filteredAgents = append(filteredAgents, agent)
			}
		}
	}

	return filteredAgents
}
