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

package syncconfig

import (
	"context"
	"net/url"
	"strconv"
	"time"

	"github.com/bleemeo/glouton/bleemeo/internal/cache"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/logger"
	"github.com/google/go-cmp/cmp"
)

type SyncConfig struct {
	lastNotifiedConfig bleemeoTypes.GloutonAccountConfig
	suspendedMode      bool
}

func New(c *cache.Cache) *SyncConfig {
	cfg, _ := c.CurrentAccountConfig()

	return &SyncConfig{
		lastNotifiedConfig: cfg,
	}
}

func (s *SyncConfig) Name() types.EntityName {
	return types.EntityConfig
}

func (s *SyncConfig) EnabledInMaintenance() bool {
	return true
}

func (s *SyncConfig) EnabledInSuspendedMode() bool {
	return true
}

func (s *SyncConfig) PrepareExecution(_ context.Context, execution types.SynchronizationExecution) (types.EntitySynchronizerExecution, error) {
	return &syncConfigExecution{
		parent:    s,
		execution: execution,
	}, nil
}

type syncConfigExecution struct {
	parent    *SyncConfig
	execution types.SynchronizationExecution
}

func (e *syncConfigExecution) NeedSynchronization(_ context.Context) (bool, error) {
	return false, nil
}

func (e *syncConfigExecution) RefreshCache(ctx context.Context, syncType types.SyncType) error {
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

func (e *syncConfigExecution) SyncRemoteAndLocal(_ context.Context, _ types.SyncType) error {
	return nil
}

func (e *syncConfigExecution) FinishExecution(_ context.Context) {
	option := e.execution.Option()
	cache := option.Cache

	newConfig, _ := cache.CurrentAccountConfig()

	if diff := cmp.Diff(e.parent.lastNotifiedConfig, newConfig); diff != "" {
		logger.V(1).Printf("Bleemeo config(s) changed: (-old +new):\n%s", diff)

		if option.UpdateConfigCallback != nil {
			option.UpdateConfigCallback()
		}

		e.parent.lastNotifiedConfig = newConfig

		// Use slightly delayed full synchronization:
		// * The full synchronization is needed because some features might be enabled/disabled and
		//   (especially with enabled) we don't want to wait for hours before object get created.
		// * But use a delay because multiple config hook might arrive in short time.
		e.execution.RequestLaterFullSynchronization(time.Minute)
	}

	if e.parent.suspendedMode != newConfig.Suspended {
		if option.SetBleemeoInSuspendedMode != nil {
			option.SetBleemeoInSuspendedMode(newConfig.Suspended)
		}

		e.parent.suspendedMode = newConfig.Suspended
	}
}

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
