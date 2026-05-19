// Copyright 2015-2026 Bleemeo
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

	"github.com/bleemeo/bleemeo-go"
	"github.com/bleemeo/glouton/bleemeo/internal/cache"
	"github.com/bleemeo/glouton/bleemeo/internal/common"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/types"
)

var ErrConfigNotFound = errors.New("configuration not found")

type Filter struct {
	currentAccountConfig bleemeoTypes.GloutonAccountConfig
	probeConfigs         map[string]bleemeoTypes.GloutonProbeAccountConfig
	agents               map[string]bleemeoTypes.Agent
	monitors             map[bleemeoTypes.AgentID]bleemeoTypes.Monitor
}

func NewFilter(cache *cache.Cache) *Filter {
	currentConfig, _ := cache.CurrentAccountConfig()

	return &Filter{
		currentAccountConfig: currentConfig,
		probeConfigs:         cache.ProbeConfigByAccountID(),
		agents:               cache.AgentsByUUID(),
		monitors:             cache.MonitorsByAgentUUID(),
	}
}

// IsAllowed returns whether a metric is allowed or not depending on the current plan.
func (f *Filter) IsAllowed(lbls map[string]string, annotations types.MetricAnnotations) (bool, bleemeoTypes.DenyReason, error) {
	allowlist, err := allowListForMetric(f.currentAccountConfig, f.probeConfigs, annotations, f.monitors, f.agents)
	if err != nil {
		return false, bleemeoTypes.DenyErrorOccurred, err
	}

	// Deny metrics with an item too long for the API.
	if len(lbls[types.LabelItem]) > common.APIMetricItemLength ||
		annotations.ServiceName != "" && len(annotations.ServiceInstance) > common.APIServiceInstanceLength {
		return false, bleemeoTypes.DenyItemTooLong, nil
	}

	// Deny metrics associated to a container when Docker integration is disabled unless we will ignore container.
	if !f.currentAccountConfig.DockerIntegration && annotations.ContainerID != "" && !common.IgnoreContainer(f.currentAccountConfig, lbls) {
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
	currentAccountConfig bleemeoTypes.GloutonAccountConfig,
	probeConfigs map[string]bleemeoTypes.GloutonProbeAccountConfig,
	annotations types.MetricAnnotations,
	monitors map[bleemeoTypes.AgentID]bleemeoTypes.Monitor,
	agents map[string]bleemeoTypes.Agent,
) (map[string]bool, error) {
	if annotations.BleemeoAgentID != "" {
		monitor, present := monitors[bleemeoTypes.AgentID(annotations.BleemeoAgentID)]
		if present {
			probeCfg, present := probeConfigs[monitor.Account]
			if !present {
				return nil, fmt.Errorf("%w for monitor with account ID=%s", ErrConfigNotFound, monitor.Account)
			}

			return probeCfg.MetricsAllowlist, nil
		}

		agent, present := agents[annotations.BleemeoAgentID]
		if !present {
			return nil, fmt.Errorf("%w: missing agent ID=%s", ErrConfigNotFound, annotations.BleemeoAgentID)
		}

		ac, ok := currentAccountConfig.AgentConfigByID[agent.AgentType]
		if !ok {
			return nil, fmt.Errorf("%w: missing agent config for type ID=%s", ErrConfigNotFound, agent.AgentType)
		}

		return ac.MetricsAllowlist, nil
	}

	tmp, ok := currentAccountConfig.AgentConfigByName[bleemeo.AgentType_Agent]
	if !ok {
		return nil, fmt.Errorf("%w: missing agent config for type Name=%s", ErrConfigNotFound, bleemeo.AgentType_Agent)
	}

	return tmp.MetricsAllowlist, nil
}
