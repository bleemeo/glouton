package synchronizer

import (
	"agentgo/bleemeo/types"
	"errors"
	"fmt"
	"net/http"
	"reflect"
)

func (s *Synchronizer) syncAgent() error {
	req, err := http.NewRequest("GET", fmt.Sprintf("v1/agent/%s/", s.option.State.AgentID()), nil)
	if err != nil {
		return err
	}
	var agent types.Agent
	_, err = s.client.Do(req, &agent)
	if err != nil {
		return err
	}
	s.option.Cache.SetAgent(agent)

	if agent.CurrentConfigID == "" {
		return errors.New("agent don't have any configuration on Bleemeo Cloud platform. Please contact support@bleemeo.com about this issue")
	}

	req, err = http.NewRequest("GET", fmt.Sprintf("v1/accountconfig/%s/", agent.CurrentConfigID), nil)
	if err != nil {
		return err
	}
	var config types.AccountConfig
	_, err = s.client.Do(req, &config)
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
