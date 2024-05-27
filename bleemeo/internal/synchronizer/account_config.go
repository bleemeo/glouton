// Copyright 2015-2023 Bleemeo
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

package synchronizer

import (
	"context"
	"errors"
	"reflect"

	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	"github.com/bleemeo/glouton/logger"
)

func (s *Synchronizer) syncAccountConfig(ctx context.Context, syncType types.SyncType, execution types.SynchronizationExecution) (updateThresholds bool, err error) {
	if syncType == types.SyncTypeForceCacheRefresh {
		currentConfig, _ := s.option.Cache.CurrentAccountConfig()
		apiClient := execution.BleemeoAPIClient()

		if err := s.agentTypesUpdateList(ctx, apiClient); err != nil {
			return false, err
		}

		if err := s.accountConfigUpdateList(ctx, apiClient); err != nil {
			return false, err
		}

		if err := s.agentConfigUpdateList(ctx, apiClient); err != nil {
			return false, err
		}

		newConfig, ok := s.option.Cache.CurrentAccountConfig()
		if ok && s.option.UpdateConfigCallback != nil {
			hasChanged := !reflect.DeepEqual(currentConfig, newConfig)
			nameHasChanged := currentConfig.Name != newConfig.Name

			if s.state.currentConfigNotified != newConfig.ID {
				hasChanged = true
				nameHasChanged = true
			}

			if hasChanged {
				s.option.UpdateConfigCallback(ctx, nameHasChanged)
			}
		}

		s.state.l.Lock()
		s.state.currentConfigNotified = newConfig.ID
		s.state.l.Unlock()

		// Set suspended mode if it changed.
		if s.suspendedMode != newConfig.Suspended {
			s.option.SetBleemeoInSuspendedMode(newConfig.Suspended)
			s.suspendedMode = newConfig.Suspended
		}
	}

	return false, nil
}

func (s *Synchronizer) agentTypesUpdateList(ctx context.Context, apiClient types.RawClient) error {
	agentTypes, err := apiClient.listAgentTypes(ctx)
	if err != nil {
		return err
	}

	s.option.Cache.SetAgentTypes(agentTypes)

	return nil
}

func (s *Synchronizer) accountConfigUpdateList(ctx context.Context, apiClient types.RawClient) error {
	configs, err := apiClient.listAccountConfigs(ctx)
	if err != nil {
		return err
	}

	s.option.Cache.SetAccountConfigs(configs)

	return nil
}

func (s *Synchronizer) agentConfigUpdateList(ctx context.Context, apiClient types.RawClient) error {
	configs, err := apiClient.listAgentConfigs(ctx)
	if err != nil {
		if errors.Is(err, errAgentConfigNotSupported) {
			logger.V(2).Printf(err.Error())
			s.option.Cache.SetAgentConfigs(nil)

			return nil
		}

		return err
	}

	s.option.Cache.SetAgentConfigs(configs)

	return nil
}
