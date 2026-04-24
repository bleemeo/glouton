// Copyright 2015-2025 Bleemeo
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

package syncaccountconfig

import (
	"context"
	"net/url"
	"reflect"
	"strconv"

	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
)

type SyncAccountConfig struct {
	lastNotifiedConfig bleemeoTypes.GloutonAccountConfig
	suspendedMode      bool
}

func New() *SyncAccountConfig {
	return &SyncAccountConfig{}
}

func (s *SyncAccountConfig) Name() types.EntityName {
	return types.EntityAccountConfig
}

func (s *SyncAccountConfig) EnabledInMaintenance() bool {
	return true
}

func (s *SyncAccountConfig) EnabledInSuspendedMode() bool {
	return true
}

func (s *SyncAccountConfig) PrepareExecution(_ context.Context, execution types.SynchronizationExecution) (types.EntitySynchronizerExecution, error) {
	return &syncAccountConfigExecution{
		parent:    s,
		execution: execution,
	}, nil
}

type syncAccountConfigExecution struct {
	parent    *SyncAccountConfig
	execution types.SynchronizationExecution
}

func (e *syncAccountConfigExecution) NeedSynchronization(_ context.Context) (bool, error) {
	return true, nil
}

func (e *syncAccountConfigExecution) RefreshCache(ctx context.Context, syncType types.SyncType) error {
	if syncType != types.SyncTypeForceCacheRefresh {
		return nil
	}

	option := e.execution.Option()
	apiClient := e.execution.BleemeoAPIClient()
	cache := option.Cache

	agentTypes, err := apiClient.ListAgentTypes(ctx)
	if err != nil {
		return err
	}

	cache.SetAgentTypes(agentTypes)

	accountID := cache.AccountID()

	accountConfigs, err := apiClient.ListConfigs(ctx, url.Values{"account": {accountID}})
	if err != nil {
		return err
	}

	probeParams := url.Values{}
	for _, t := range []int{
		bleemeoTypes.ConfigTypeAgentMetricsResolution,
		bleemeoTypes.ConfigTypeAgentMetricsAllowlist,
		bleemeoTypes.ConfigTypeSuspended,
	} {
		probeParams.Add("type", strconv.Itoa(t))
	}

	probeConfigs, err := apiClient.ListConfigs(ctx, probeParams)
	if err != nil {
		return err
	}

	cache.SetConfigs(deduplicateConfigs(append(accountConfigs, probeConfigs...)))

	return nil
}

func (e *syncAccountConfigExecution) SyncRemoteAndLocal(_ context.Context, _ types.SyncType) error {
	option := e.execution.Option()
	cache := option.Cache

	newConfig, _ := cache.CurrentAccountConfig()

	if !reflect.DeepEqual(e.parent.lastNotifiedConfig, newConfig) {
		if option.UpdateConfigCallback != nil {
			option.UpdateConfigCallback(false)
		}

		e.parent.lastNotifiedConfig = newConfig
	}

	if e.parent.suspendedMode != newConfig.Suspended {
		if option.SetBleemeoInSuspendedMode != nil {
			option.SetBleemeoInSuspendedMode(newConfig.Suspended)
		}

		e.parent.suspendedMode = newConfig.Suspended
	}

	return nil
}

func (e *syncAccountConfigExecution) FinishExecution(_ context.Context) {}

func deduplicateConfigs(configs []bleemeoTypes.Config) []bleemeoTypes.Config {
	seen := make(map[string]bool, len(configs))
	result := make([]bleemeoTypes.Config, 0, len(configs))

	for _, c := range configs {
		if !seen[c.ID] {
			seen[c.ID] = true
			result = append(result, c)
		}
	}

	return result
}
