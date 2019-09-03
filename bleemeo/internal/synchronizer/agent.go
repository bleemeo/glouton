package synchronizer

import (
	"agentgo/bleemeo/types"
	"agentgo/logger"
	"errors"
	"fmt"
	"reflect"
)

const apiTagsLength = 100

func (s *Synchronizer) syncAgent(fullSync bool) error {
	var agent types.Agent
	params := map[string]string{
		"fields": "tags,id,created_at,account,next_config_at,current_config",
	}
	data := map[string][]types.Tag{
		"tags": make([]types.Tag, 0),
	}
	for _, t := range s.option.Config.StringList("tags") {
		if len(t) <= apiTagsLength && t != "" {
			data["tags"] = append(data["tags"], types.Tag{Name: t})
		}
	}
	_, err := s.client.Do("PATCH", fmt.Sprintf("v1/agent/%s/", s.agentID), params, data, &agent)
	if err != nil {
		return err
	}
	s.option.Cache.SetAgent(agent)

	if agent.CurrentConfigID == "" {
		return errors.New("agent don't have any configuration on Bleemeo Cloud platform. Please contact support@bleemeo.com about this issue")
	}

	s.option.Cache.SetAccountID(agent.AccountID)
	if agent.AccountID != s.option.Config.String("bleemeo.account_id") && !s.warnAccountMismatchDone {
		s.warnAccountMismatchDone = true
		logger.Printf(
			"Account ID in configuration file (%s) mismatch the current account ID (%s). The Account ID from configuration file will be ignored.",
			s.option.Config.String("bleemeo.account_id"),
			agent.AccountID,
		)
	}

	var config types.AccountConfig
	params = map[string]string{
		"fields": "id,name,metrics_agent_whitelist,metrics_agent_resolution,live_process_resolution,docker_integration",
	}
	_, err = s.client.Do("GET", fmt.Sprintf("v1/accountconfig/%s/", agent.CurrentConfigID), params, nil, &config)
	if err != nil {
		return err
	}

	currentConfig := s.option.Cache.AccountConfig()
	if !reflect.DeepEqual(currentConfig, config) {
		s.option.Cache.SetAccountConfig(config)
		if s.option.UpdateConfigCallback != nil {
			s.option.UpdateConfigCallback()
		}
	}
	return nil
}
