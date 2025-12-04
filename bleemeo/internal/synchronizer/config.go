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

package synchronizer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/bleemeo/bleemeo-go"
	"github.com/bleemeo/glouton/bleemeo/internal/common"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/logger"
)

// The number of item Glouton can register in a single request.
const gloutonConfigItemBatchSize = 100

var errRegisterConfigItem = errors.New("can't register the following item")

// comparableConfigItem is a modified GloutonConfigItem without the
// `any` value to make it comparable.
type comparableConfigItem struct {
	Key      string
	Priority int
	Source   bleemeo.ConfigItemSource
	Path     string
	Type     bleemeo.ConfigItemType
}

type configItemValue struct {
	ID    string
	Value any
}

func configItemsToMap(items []bleemeoTypes.GloutonConfigItem) map[comparableConfigItem]configItemValue {
	itemsMap := make(map[comparableConfigItem]configItemValue, len(items))

	for _, item := range items {
		key := comparableConfigItem{
			Key:      item.Key,
			Priority: item.Priority,
			Source:   item.Source,
			Path:     item.Path,
			Type:     item.Type,
		}

		itemsMap[key] = configItemValue{
			ID:    item.ID,
			Value: item.Value,
		}
	}

	return itemsMap
}

func (s *Synchronizer) syncConfig(
	ctx context.Context,
	syncType types.SyncType,
	execution types.SynchronizationExecution,
) (updateThresholds bool, err error) {
	// The config is not essential and can be registered later.
	if execution.IsOnlyEssential() || syncType != types.SyncTypeForceCacheRefresh {
		return false, nil
	}

	apiClient := execution.BleemeoAPIClient()

	remoteConfigItemsList, err := apiClient.ListGloutonConfigItems(ctx, s.agentID)
	if err != nil {
		return false, fmt.Errorf("failed to fetch config items: %w", err)
	}

	remoteConfigItems := configItemsToMap(remoteConfigItemsList)
	localConfigItems := s.localConfigItems()

	// Registers local config items not present on the API.
	err = s.registerLocalConfigItems(ctx, apiClient, localConfigItems, remoteConfigItems)
	if err != nil {
		return false, err
	}

	// Remove remote config items not present locally.
	err = s.removeRemoteConfigItems(ctx, apiClient, localConfigItems, remoteConfigItems)
	if err != nil {
		return false, err
	}

	s.state.l.Lock()
	s.state.configSyncDone = true
	s.state.l.Unlock()

	return false, nil
}

// localConfigItems returns the local config items in a map of config value by comparableConfigItem.
func (s *Synchronizer) localConfigItems() map[comparableConfigItem]any {
	items := make(map[comparableConfigItem]any, len(s.option.ConfigItems))

	for _, item := range s.option.ConfigItems {
		// Ignore items with key or path too long because we won't be able to register them.
		if len(item.Key) > common.APIConfigItemKeyLength ||
			len(item.Path) > common.APIConfigItemPathLength {
			continue
		}

		// Censor secrets and passwords on the API.
		item.Value = config.CensorSecretItem(item.Key, item.Value)

		key := comparableConfigItem{
			Key:      item.Key,
			Priority: item.Priority,
			Source:   bleemeoItemSourceFromConfigSource(item.Source),
			Path:     item.Path,
			Type:     bleemeoItemTypeFromConfigType(item.Type),
		}

		items[key] = item.Value
	}

	return items
}

// Convert a config item source to a Bleemeo config item source.
func bleemeoItemSourceFromConfigSource(source config.ItemSource) bleemeo.ConfigItemSource {
	switch source {
	case config.SourceFile:
		return bleemeo.ConfigItemSource_File
	case config.SourceEnv:
		return bleemeo.ConfigItemSource_Env
	case config.SourceDefault:
		return bleemeo.ConfigItemSource_Default
	default:
		return bleemeo.ConfigItemSource_Unknown
	}
}

// Convert a config item type to a Bleemeo config item type.
// Both enums are very similar, but they are duplicated to not
// put any Bleemeo API specific types in the config.
func bleemeoItemTypeFromConfigType(source config.ItemType) bleemeo.ConfigItemType {
	switch source {
	case config.TypeBool:
		return bleemeo.ConfigItemType_Bool
	case config.TypeFloat:
		return bleemeo.ConfigItemType_Float
	case config.TypeInt:
		return bleemeo.ConfigItemType_Int
	case config.TypeString:
		return bleemeo.ConfigItemType_String
	case config.TypeListString:
		return bleemeo.ConfigItemType_ListString
	case config.TypeListInt:
		return bleemeo.ConfigItemType_ListInt
	case config.TypeMapStrStr:
		return bleemeo.ConfigItemType_MapStrStr
	case config.TypeMapStrInt:
		return bleemeo.ConfigItemType_MapStrInt
	case config.TypeThresholds:
		return bleemeo.ConfigItemType_Thresholds
	case config.TypeServices:
		return bleemeo.ConfigItemType_Services
	case config.TypeNameInstances:
		return bleemeo.ConfigItemType_NameInstances
	case config.TypeBlackboxTargets:
		return bleemeo.ConfigItemType_BlackboxTargets
	case config.TypePrometheusTargets:
		return bleemeo.ConfigItemType_PrometheusTargets
	case config.TypeSNMPTargets:
		return bleemeo.ConfigItemType_SNMPTargets
	case config.TypeLogInputs:
		return bleemeo.ConfigItemType_LogInputs
	case config.TypeAny:
		return bleemeo.ConfigItemType_Any
	default:
		return bleemeo.ConfigItemType_Any
	}
}

// registerLocalConfigItems registers local config items not present on the API.
func (s *Synchronizer) registerLocalConfigItems(
	ctx context.Context,
	apiClient types.GloutonConfigItemClient,
	localConfigItems map[comparableConfigItem]any,
	remoteConfigItems map[comparableConfigItem]configItemValue,
) error {
	var itemsToRegister []bleemeoTypes.GloutonConfigItem //nolint:prealloc

	// Find and register local items that are not present on the API.
	for localItem, localValue := range localConfigItems {
		remoteItem, ok := remoteConfigItems[localItem]

		// Skip items that already exist on the API.
		if ok && reflect.DeepEqual(localValue, remoteItem.Value) {
			continue
		}

		itemsToRegister = append(itemsToRegister,
			bleemeoTypes.GloutonConfigItem{
				Agent:    s.agentID,
				Key:      localItem.Key,
				Value:    localValue,
				Priority: localItem.Priority,
				Source:   localItem.Source,
				Path:     localItem.Path,
				Type:     localItem.Type,
			},
		)

		logger.V(2).Printf(`Registering config item "%s" from %s %s`, localItem.Key, bleemeoTypes.FormatConfigItemSource(localItem.Source), localItem.Path)
	}

	for start := 0; start < len(itemsToRegister); start += gloutonConfigItemBatchSize {
		end := min(start+gloutonConfigItemBatchSize, len(itemsToRegister))

		err := apiClient.RegisterGloutonConfigItems(ctx, itemsToRegister[start:end])
		if err != nil {
			return tryImproveRegisterError(err, itemsToRegister[start:end])
		}
	}

	return nil
}

// removeRemoteConfigItems removes remote config items not present locally.
func (s *Synchronizer) removeRemoteConfigItems(
	ctx context.Context,
	apiClient types.GloutonConfigItemClient,
	localConfigItems map[comparableConfigItem]any,
	remoteConfigItems map[comparableConfigItem]configItemValue,
) error {
	// Find and remove remote items that are not present locally.
	for remoteKey, remoteItem := range remoteConfigItems {
		// Skip API source, these items are managed by the API itself.
		if remoteKey.Source == bleemeo.ConfigItemSource_API {
			continue
		}

		localValue, ok := localConfigItems[remoteKey]

		// Skip items that already exist locally.
		if ok && reflect.DeepEqual(localValue, remoteItem.Value) {
			continue
		}

		err := apiClient.DeleteGloutonConfigItem(ctx, remoteItem.ID)
		if err != nil {
			// Ignore the error if the item has already been deleted.
			if IsNotFound(err) {
				continue
			}

			return err
		}

		logger.V(2).Printf(
			`Config item %s ("%s" from %s %s) deleted`,
			remoteItem.ID, remoteKey.Key, bleemeoTypes.FormatConfigItemSource(remoteKey.Source), remoteKey.Path,
		)
	}

	return nil
}

func tryImproveRegisterError(err error, configItems []bleemeoTypes.GloutonConfigItem) error {
	apiErr := new(bleemeo.APIError)
	if !errors.As(err, &apiErr) {
		return err // can't do anything with this
	}

	var messages []struct {
		Value []string `json:"value"`
	}

	if jsonErr := json.Unmarshal(apiErr.Response, &messages); jsonErr == nil {
		errorItems := make([]string, 0, len(messages)/2) // guesstimate

		for i, message := range messages {
			if len(message.Value) != 0 {
				errMsg := fmt.Sprintf("- %s: %s", configItems[i].Key, strings.Join(message.Value, " / "))
				errorItems = append(errorItems, errMsg)
			}
		}

		var s string

		if len(errorItems) > 1 {
			s = "s"
		}

		return fmt.Errorf("%w%s:\n%s", errRegisterConfigItem, s, strings.Join(errorItems, "\n"))
	}

	return fmt.Errorf("%w: <%s>", errRegisterConfigItem, apiErr.Response)
}
