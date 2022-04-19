package synchronizer

import (
	"context"
	"encoding/json"
	"fmt"
	"glouton/bleemeo/client"
	bleemeoTypes "glouton/bleemeo/types"
	"glouton/logger"
	"glouton/prometheus/rules"
	"time"
)

func (s *Synchronizer) syncAlertingRules(ctx context.Context, fullSync bool, onlyEssential bool) (updateThresholds bool, err error) {
	if s.option.RebuildPromQLRules == nil {
		return false, nil
	}

	previousAlertingRules := s.option.Cache.AlertingRules()
	pendingUpdates := s.popPendingAlertingRulesUpdate()

	if len(pendingUpdates) > len(previousAlertingRules)*3/100 {
		// If more than 3% of known alerting rules needs update, do a full
		// update. 3% is arbitrarily chosen, based on assumption request for
		// one page is cheaper than 3 request for one metric.
		fullSync = true
	}

	if fullSync {
		alertingRules, err := s.fetchAllAlertingRules(ctx)
		if err != nil {
			return false, fmt.Errorf("failed to fetch alerting rules: %w", err)
		}

		s.option.Cache.SetAlertingRules(alertingRules)
	} else {
		// Use a map to remove the previous alerting rules that are updated.
		alertingRulesByID := make(map[string]bleemeoTypes.AlertingRule)
		for _, previousRule := range previousAlertingRules {
			alertingRulesByID[previousRule.ID] = previousRule
		}

		for _, alertingRuleID := range pendingUpdates {
			alertingRule, err := s.fetchAlertingRule(ctx, alertingRuleID)
			if err != nil {
				// Delete the alerting rule if it's no longer present on the API.
				if client.IsNotFound(err) {
					delete(alertingRulesByID, alertingRuleID)

					continue
				}

				// Add the alerting rule to the pending update list to retry it later.
				s.UpdateAlertingRule(alertingRuleID)
				logger.V(1).Printf("Failed to fetch alerting rule %s", alertingRuleID)

				continue
			}

			alertingRulesByID[alertingRule.ID] = alertingRule
		}

		alertingRules := make([]bleemeoTypes.AlertingRule, 0, len(alertingRulesByID))
		for _, rule := range alertingRulesByID {
			alertingRules = append(alertingRules, rule)
		}

		s.option.Cache.SetAlertingRules(alertingRules)
	}

	err = s.UpdateAlertingRules()

	return true, err
}

// fetchAlertingRule fetches a single alerting rule from the API.
func (s *Synchronizer) fetchAlertingRule(ctx context.Context, id string) (alertingRule bleemeoTypes.AlertingRule, err error) {
	_, err = s.client.Do(ctx, "GET", fmt.Sprintf("v1/alertingrule/%s/", id), nil, nil, &alertingRule)
	if err != nil {
		return bleemeoTypes.AlertingRule{}, fmt.Errorf("client do: %w", err)
	}

	return alertingRule, nil
}

// fetchAllAlertingRules fetches all the alerting rules from the API.
func (s *Synchronizer) fetchAllAlertingRules(ctx context.Context) (alertingRules []bleemeoTypes.AlertingRule, err error) {
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

// UpdateAlertingRule requests an update for the given alerting rule UUID.
func (s *Synchronizer) UpdateAlertingRule(alertingRuleID string) {
	s.l.Lock()
	defer s.l.Unlock()

	s.pendingAlertingRulesUpdate = append(s.pendingAlertingRulesUpdate, alertingRuleID)
	s.forceSync[syncMethodAlertingRules] = false
}

func (s *Synchronizer) popPendingAlertingRulesUpdate() []string {
	s.l.Lock()
	defer s.l.Unlock()

	set := make(map[string]bool, len(s.pendingAlertingRulesUpdate))

	for _, id := range s.pendingAlertingRulesUpdate {
		set[id] = true
	}

	s.pendingAlertingRulesUpdate = nil
	result := make([]string, 0, len(set))

	for id := range set {
		result = append(result, id)
	}

	return result
}
