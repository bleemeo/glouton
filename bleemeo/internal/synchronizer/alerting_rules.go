package synchronizer

import (
	"context"
	"encoding/json"
	"fmt"
	bleemeoTypes "glouton/bleemeo/types"
	"glouton/logger"
	"glouton/prometheus/rules"
	"time"
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
	needConfigUpdate := false

	var promqlRules []rules.PromQLRule

	alertingRules := s.option.Cache.AlertingRules()
	for _, rule := range alertingRules {
		newPromQLRules, needConfigUpdateTmp := s.alertingRuleToPromQLRules(rule, agents, configs)
		promqlRules = append(promqlRules, newPromQLRules...)
		needConfigUpdate = needConfigUpdate || needConfigUpdateTmp
	}

	if needConfigUpdate {
		s.l.Lock()
		s.forceSync[syncMethodAccountConfig] = true
		s.l.Unlock()
	}

	if err := s.option.RebuildPromQLRules(promqlRules); err != nil {
		return fmt.Errorf("failed to rebuild PromQL rules: %w", err)
	}

	return nil
}

// alertingRuleToPromQLRule converts an AlertingRule to PromQLRules (one for each known agent).
func (s *Synchronizer) alertingRuleToPromQLRules(
	alertingRule bleemeoTypes.AlertingRule,
	agents map[string]bleemeoTypes.Agent,
	configs map[string]bleemeoTypes.GloutonAccountConfig,
) (promqlRules []rules.PromQLRule, needConfigUpdate bool) {
	agentIDs := s.filterAgents(alertingRule.Agents, agents)
	promqlRules = make([]rules.PromQLRule, 0, len(agentIDs))

	for _, agentID := range agentIDs {
		// Find the resolution of this agent from the config.
		agent := agents[agentID]

		cfg, ok := configs[agent.CurrentConfigID]
		if !ok {
			logger.V(1).Printf("Config for agent %s not found", agent.CurrentConfigID)

			needConfigUpdate = true

			continue
		}

		agentConfig, ok := cfg.AgentConfigByID[agent.AgentType]
		if !ok {
			logger.V(1).Printf("Agent config for agent %s and type %s not found", agent.CurrentConfigID, agent.AgentType)

			needConfigUpdate = true

			continue
		}

		if agentConfig.MetricResolution == 0 {
			logger.V(1).Printf(
				"Empty metric resolution for agent config of agent %s and type %s",
				agent.CurrentConfigID,
				agent.AgentType,
			)

			continue
		}

		promqlRule := rules.PromQLRule{
			Name:          alertingRule.Name,
			ID:            alertingRule.ID,
			InstanceID:    agentID,
			WarningQuery:  alertingRule.WarningQuery,
			WarningDelay:  time.Duration(alertingRule.WarningDelaySecond) * time.Second,
			CriticalQuery: alertingRule.CriticalQuery,
			CriticalDelay: time.Duration(alertingRule.CriticalDelaySecond) * time.Second,
			Resolution:    agentConfig.MetricResolution,
		}

		promqlRules = append(promqlRules, promqlRule)
	}

	return promqlRules, needConfigUpdate
}

// filterAgents removes the agents this Glouton doesn't manage.
func (s *Synchronizer) filterAgents(agents []string, knownAgentsByUUID map[string]bleemeoTypes.Agent) []string {
	var filteredAgents []string

	for _, agent := range agents {
		if _, ok := knownAgentsByUUID[agent]; ok {
			filteredAgents = append(filteredAgents, agent)
		}
	}

	return filteredAgents
}
