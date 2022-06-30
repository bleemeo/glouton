// Copyright 2015-2019 Bleemeo
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

package common

import (
	"errors"
	"fmt"
	bleemeoTypes "glouton/bleemeo/types"
	"glouton/types"
	"strings"
)

var ErrConfigNotFound = errors.New("configuration not found")

// AllowMetric return True if current configuration allow this metrics.
func AllowMetric(
	labels map[string]string,
	annotations types.MetricAnnotations,
	whitelist map[string]bool,
	hasDockerIntegration bool,
) bool {
	if len(whitelist) == 0 {
		return true
	}

	// Service status and alerting rules metrics are always allowed.
	if IsServiceCheckMetric(labels, annotations) || annotations.AlertingRuleID != "" {
		return true
	}

	// Deny metrics associated to a container if the docker integration is disabled.
	if !hasDockerIntegration && annotations.ContainerID != "" {
		return false
	}

	return whitelist[labels[types.LabelName]]
}

// IsServiceCheckMetric returns whether this metric is a service check and should always be allowed.
func IsServiceCheckMetric(labels map[string]string, annotations types.MetricAnnotations) bool {
	return annotations.ServiceName != "" && strings.HasSuffix(labels[types.LabelName], "_status")
}

func AllowListForMetric(configs map[string]bleemeoTypes.GloutonAccountConfig, defaultConfigID string, annotations types.MetricAnnotations, monitors map[bleemeoTypes.AgentID]bleemeoTypes.Monitor, agents map[string]bleemeoTypes.Agent) (map[string]bool, error) {
	// TODO: snmp metric should use the correct AgentConfig
	if annotations.BleemeoAgentID != "" {
		var whitelist map[string]bool

		monitor, present := monitors[bleemeoTypes.AgentID(annotations.BleemeoAgentID)]
		if present {
			accountConfig, present := configs[monitor.AccountConfig]
			if !present {
				return nil, fmt.Errorf("%w for monitor with config ID=%s", ErrConfigNotFound, monitor.AccountConfig)
			}

			whitelist = accountConfig.AgentConfigByName[bleemeoTypes.AgentTypeMonitor].MetricsAllowlist
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

			whitelist = ac.MetricsAllowlist
		}

		return whitelist, nil
	}

	tmp, ok := configs[defaultConfigID].AgentConfigByName[bleemeoTypes.AgentTypeAgent]
	if !ok {
		return nil, fmt.Errorf("%w: missing agent config for type Name=%s", ErrConfigNotFound, bleemeoTypes.AgentTypeAgent)
	}

	return tmp.MetricsAllowlist, nil
}
