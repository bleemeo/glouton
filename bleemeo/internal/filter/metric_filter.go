// Copyright 2015-2022 Bleemeo
//
// bleemeo.com an infrastructure monitoring solution in the Cloud
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package filter

import (
	"errors"
	"fmt"
	"glouton/bleemeo/internal/cache"
	"glouton/bleemeo/internal/common"
	bleemeoTypes "glouton/bleemeo/types"
	"glouton/types"
)

var ErrConfigNotFound = errors.New("configuration not found")

type Filter struct {
	defaultConfigID string
	accountConfigs  map[string]bleemeoTypes.GloutonAccountConfig
	agents          map[string]bleemeoTypes.Agent
	monitors        map[bleemeoTypes.AgentID]bleemeoTypes.Monitor
}

func NewFilter(cache *cache.Cache) *Filter {
	return &Filter{
		defaultConfigID: cache.Agent().CurrentConfigID,
		accountConfigs:  cache.AccountConfigsByUUID(),
		agents:          cache.AgentsByUUID(),
		monitors:        cache.MonitorsByAgentUUID(),
	}
}

// IsAllowed returns whether a metric is allowed or not depending on the current plan.
func (f *Filter) IsAllowed(lbls map[string]string, annotations types.MetricAnnotations) (bool, bleemeoTypes.DenyReason, error) {
	allowlist, err := allowListForMetric(f.accountConfigs, f.defaultConfigID, annotations, f.monitors, f.agents)
	if err != nil {
		return false, bleemeoTypes.DenyErrorOccurred, err
	}

	// Deny metrics with an item too long for the API.
	if len(annotations.BleemeoItem) > common.APIMetricItemLength ||
		annotations.ServiceName != "" && len(annotations.ServiceInstance) > common.APIServiceInstanceLength {
		return false, bleemeoTypes.DenyItemTooLong, nil
	}

	// Alerting rules metrics are always allowed.
	if annotations.AlertingRuleID != "" {
		return true, bleemeoTypes.NotDenied, nil
	}

	// Deny metrics associated to a container when Docker integration is disabled unless we will ignore container.
	if !f.accountConfigs[f.defaultConfigID].DockerIntegration && annotations.ContainerID != "" && !common.IgnoreContainer(f.accountConfigs[f.defaultConfigID], lbls) {
		return false, bleemeoTypes.DenyNoDockerIntegration, nil
	}

	// Wait for network metrics from virtual interfaces to be associated with a container.
	if annotations.ContainerID == types.MissingContainerID {
		return false, bleemeoTypes.DenyMissingContainerID, nil
	}

	if len(allowlist) == 0 {
		return true, bleemeoTypes.NotDenied, nil
	}

	if allowlist[lbls[types.LabelName]] {
		return true, bleemeoTypes.NotDenied, nil
	}

	return false, bleemeoTypes.DenyNotAvailableInCurrentPlan, nil
}

func allowListForMetric(
	configs map[string]bleemeoTypes.GloutonAccountConfig,
	defaultConfigID string,
	annotations types.MetricAnnotations,
	monitors map[bleemeoTypes.AgentID]bleemeoTypes.Monitor,
	agents map[string]bleemeoTypes.Agent,
) (map[string]bool, error) {
	// TODO: snmp metric should use the correct AgentConfig
	if annotations.BleemeoAgentID != "" {
		var allowlist map[string]bool

		monitor, present := monitors[bleemeoTypes.AgentID(annotations.BleemeoAgentID)]
		if present {
			accountConfig, present := configs[monitor.AccountConfig]
			if !present {
				return nil, fmt.Errorf("%w for monitor with config ID=%s", ErrConfigNotFound, monitor.AccountConfig)
			}

			allowlist = accountConfig.AgentConfigByName[bleemeoTypes.AgentTypeMonitor].MetricsAllowlist
		} else {
			agent, present := agents[annotations.BleemeoAgentID]
			if !present {
				return nil, fmt.Errorf("%w: missing agent ID=%s", ErrConfigNotFound, annotations.BleemeoAgentID)
			}

			accountConfig, present := configs[agent.CurrentConfigID]
			if !present {
				return nil, fmt.Errorf("%w for agent with config ID=%s", ErrConfigNotFound, agent.CurrentConfigID)
			}

			ac, ok := accountConfig.AgentConfigByID[agent.AgentType]
			if !ok {
				return nil, fmt.Errorf("%w: missing agent config for type ID=%s", ErrConfigNotFound, agent.AgentType)
			}

			allowlist = ac.MetricsAllowlist
		}

		return allowlist, nil
	}

	tmp, ok := configs[defaultConfigID].AgentConfigByName[bleemeoTypes.AgentTypeAgent]
	if !ok {
		return nil, fmt.Errorf("%w: missing agent config for type Name=%s", ErrConfigNotFound, bleemeoTypes.AgentTypeAgent)
	}

	return tmp.MetricsAllowlist, nil
}
