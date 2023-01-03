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

package synchronizer

import (
	"context"
	"encoding/json"
	"fmt"
	"glouton/bleemeo/client"
	"glouton/bleemeo/internal/common"
	bleemeoTypes "glouton/bleemeo/types"
	"glouton/config"
	"glouton/logger"
	"reflect"
)

// comparableConfigItem is a modified GloutonConfigItem without the
// interface{} value to make it comparable.
type comparableConfigItem struct {
	Key      string
	Priority int
	Source   bleemeoTypes.ConfigItemSource
	Path     string
	Type     bleemeoTypes.ConfigItemType
}

type configItemValue struct {
	ID    string
	Value interface{}
}

func (s *Synchronizer) syncConfig(
	ctx context.Context,
	fullSync bool,
	onlyEssential bool,
) (updateThresholds bool, err error) {
	// The config is not essential and can be registered later.
	if onlyEssential || !fullSync {
		return false, nil
	}

	remoteConfigItems, err := s.fetchAllConfigItems(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to fetch config items: %w", err)
	}

	localConfigItems := s.localConfigItems()

	// Registers local config items not present on the API.
	err = s.registerLocalConfigItems(ctx, localConfigItems, remoteConfigItems)
	if err != nil {
		return false, err
	}

	// Remove remote config items not present locally.
	err = s.removeRemoteConfigItems(ctx, localConfigItems, remoteConfigItems)
	if err != nil {
		return false, err
	}

	s.configSyncDone = true

	return false, nil
}

// fetchAllConfigItems returns the remote config items in a map of config value by comparableConfigItem.
func (s *Synchronizer) fetchAllConfigItems(ctx context.Context) (map[comparableConfigItem]configItemValue, error) {
	params := map[string]string{
		"fields": "id,agent,key,value,priority,source,path,type",
		"agent":  s.agentID,
	}

	result, err := s.client.Iter(ctx, "gloutonconfigitem", params)
	if err != nil {
		return nil, fmt.Errorf("client iter: %w", err)
	}

	items := make(map[comparableConfigItem]configItemValue, len(result))

	for _, jsonMessage := range result {
		var item bleemeoTypes.GloutonConfigItem

		if err := json.Unmarshal(jsonMessage, &item); err != nil {
			logger.V(2).Printf("Failed to unmarshal config item: %v", err)

			continue
		}

		key := comparableConfigItem{
			Key:      item.Key,
			Priority: item.Priority,
			Source:   item.Source,
			Path:     item.Path,
			Type:     item.Type,
		}

		items[key] = configItemValue{
			ID:    item.ID,
			Value: item.Value,
		}
	}

	return items, nil
}

// localConfigItems returns the local config items in a map of config value by comparableConfigItem.
func (s *Synchronizer) localConfigItems() map[comparableConfigItem]interface{} {
	items := make(map[comparableConfigItem]interface{}, len(s.option.ConfigItems))

	for _, item := range s.option.ConfigItems {
		// Ignore items with key or path too long because we won't be able to register them.
		if len(item.Key) > common.APIConfigItemKeyLength ||
			len(item.Path) > common.APIConfigItemPathLength {
			continue
		}

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
func bleemeoItemSourceFromConfigSource(source config.ItemSource) bleemeoTypes.ConfigItemSource {
	switch source {
	case config.SourceFile:
		return bleemeoTypes.SourceFile
	case config.SourceEnv:
		return bleemeoTypes.SourceEnv
	case config.SourceDefault:
		return bleemeoTypes.SourceDefault
	default:
		return bleemeoTypes.SourceUnknown
	}
}

// Convert a config item type to a Bleemeo config item type.
// Both enums are very similar, but they are duplicated to not
// put any Bleemeo API specific types in the config.
func bleemeoItemTypeFromConfigType(source config.ItemType) bleemeoTypes.ConfigItemType {
	switch source {
	case config.TypeBool:
		return bleemeoTypes.TypeBool
	case config.TypeFloat:
		return bleemeoTypes.TypeFloat
	case config.TypeInt:
		return bleemeoTypes.TypeInt
	case config.TypeString:
		return bleemeoTypes.TypeString
	case config.TypeListString:
		return bleemeoTypes.TypeListString
	case config.TypeListInt:
		return bleemeoTypes.TypeListInt
	case config.TypeListUnknown:
		return bleemeoTypes.TypeListUnknown
	case config.TypeMapStrStr:
		return bleemeoTypes.TypeMapStrStr
	case config.TypeMapStrInt:
		return bleemeoTypes.TypeMapStrInt
	case config.TypeMapStrUnknown:
		return bleemeoTypes.TypeMapStrUnknown
	case config.TypeUnknown:
		return bleemeoTypes.TypeUnknown
	default:
		return bleemeoTypes.TypeUnknown
	}
}

// registerLocalConfigItems registers local config items not present on the API.
func (s *Synchronizer) registerLocalConfigItems(
	ctx context.Context,
	localConfigItems map[comparableConfigItem]interface{},
	remoteConfigItems map[comparableConfigItem]configItemValue,
) error {
	// Find and register local items that are not present on the API.
	for localItem, localValue := range localConfigItems {
		remoteItem, ok := remoteConfigItems[localItem]

		// Skip items that already exist on the API.
		if ok && reflect.DeepEqual(localValue, remoteItem.Value) {
			continue
		}

		// Register the new item.
		item := bleemeoTypes.GloutonConfigItem{
			Agent:    s.agentID,
			Key:      localItem.Key,
			Value:    localValue,
			Priority: localItem.Priority,
			Source:   localItem.Source,
			Path:     localItem.Path,
			Type:     localItem.Type,
		}

		_, err := s.client.Do(ctx, "POST", "v1/gloutonconfigitem/", nil, item, nil)
		if err != nil {
			return err
		}

		logger.V(2).Printf(`Config item "%s" from %s %s registered`, item.Key, item.Source, item.Path)
	}

	return nil
}

// removeRemoteConfigItems removes remote config items not present locally.
func (s *Synchronizer) removeRemoteConfigItems(
	ctx context.Context,
	localConfigItems map[comparableConfigItem]interface{},
	remoteConfigItems map[comparableConfigItem]configItemValue,
) error {
	// Find and remove remote items that are not present locally.
	for remoteKey, remoteItem := range remoteConfigItems {
		// Skip API source, these items are managed by the API itself.
		if remoteKey.Source == bleemeoTypes.SourceAPI {
			continue
		}

		localValue, ok := localConfigItems[remoteKey]

		// Skip items that already exist locally.
		if ok && reflect.DeepEqual(localValue, remoteItem.Value) {
			continue
		}

		_, err := s.client.Do(ctx, "DELETE", fmt.Sprintf("v1/gloutonconfigitem/%s/", remoteItem.ID), nil, nil, nil)
		if err != nil {
			// Ignore the error if the item has already been deleted.
			if client.IsNotFound(err) {
				continue
			}

			return err
		}

		logger.V(2).Printf(
			`Config item %s ("%s" from %s %s) deleted`,
			remoteItem.ID, remoteKey.Key, remoteKey.Source, remoteKey.Path,
		)
	}

	return nil
}
