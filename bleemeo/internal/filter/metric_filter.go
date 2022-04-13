package filter

import (
	"glouton/bleemeo/internal/cache"
	"glouton/bleemeo/internal/common"
	bleemeoTypes "glouton/bleemeo/types"
	"glouton/types"
)

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

func (f Filter) IsAllowed(lbls map[string]string, annoation types.MetricAnnotations) (bool, error) {
	allowlist, err := common.AllowListForMetric(f.accountConfigs, f.defaultConfigID, annoation, f.monitors, f.agents)
	if err != nil {
		return false, err
	}

	return common.AllowMetric(lbls, annoation, allowlist), nil
}
