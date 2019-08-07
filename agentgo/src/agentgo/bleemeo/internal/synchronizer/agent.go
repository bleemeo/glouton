package synchronizer

import (
	"agentgo/bleemeo/types"
	"agentgo/logger"
	"errors"
	"fmt"
	"reflect"
)

func (s *Synchronizer) syncAgent(fullSync bool) error {
	var agent types.Agent
	_, err := s.client.Do("GET", fmt.Sprintf("v1/agent/%s/", s.option.State.AgentID()), nil, &agent)
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
	_, err = s.client.Do("GET", fmt.Sprintf("v1/accountconfig/%s/", agent.CurrentConfigID), nil, &config)
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
