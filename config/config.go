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

package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"glouton/logger"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

var errUnknownVarType = errors.New("unknown variable type")

// Configuration hold the agent configuration are set of key/value
//
// value could be typed and a default could be provided.
type Configuration struct {
	rawValues map[string]interface{}

	lookupEnv func(key string) (string, bool)

	warnings []string
}

// MockLookupEnv could be used to fake environment lookup. Useful for testing.
// Use nil as lookup function to switch back to real implementation.
func (c *Configuration) MockLookupEnv(fun func(string) (string, bool)) {
	c.lookupEnv = fun
}

// LoadDirectory will read all *.conf file within given directory.
//
// File are read in lexicographic order (e.g. 00-initial.conf is read before 99-override.conf)
//
// If one file fail, error will be raised at the end after trying to load all other files.
func (c *Configuration) LoadDirectory(dirPath string) error {
	var firstError error

	files, err := ioutil.ReadDir(dirPath)
	if err != nil {
		return err
	}

	for _, f := range files {
		if !strings.HasSuffix(f.Name(), ".conf") {
			continue
		}

		path := filepath.Join(dirPath, f.Name())

		data, err := ioutil.ReadFile(path)
		if err != nil && firstError == nil {
			firstError = fmt.Errorf("%s: %w", path, err)
		} else if err == nil {
			err = c.LoadByte(data)
			if err != nil && firstError == nil {
				firstError = fmt.Errorf("%s: %w", path, err)
			}
		}
	}

	return firstError
}

// LoadByte will load given YAML data.
func (c *Configuration) LoadByte(data []byte) error {
	var newValue map[string]interface{}

	err := yaml.Unmarshal(data, &newValue)

	if c.rawValues == nil {
		c.rawValues = make(map[string]interface{})
	}

	merge(c.rawValues, newValue)

	return err
}

// LoadEnv will load given key from specified environment variable name.
func (c *Configuration) LoadEnv(key string, varType ValueType, envName string) (found bool, err error) {
	var value string

	if c.lookupEnv == nil {
		value, found = os.LookupEnv(envName)
	} else {
		value, found = c.lookupEnv(envName)
	}

	if !found {
		return
	}

	switch varType { //nolint:exhaustive
	case TypeString:
		c.Set(key, value)
	case TypeStringList:
		c.Set(key, strings.Split(value, ","))
	case TypeBoolean:
		value, err := ConvertBoolean(value)
		if err != nil {
			return false, err
		}

		c.Set(key, value)
	case TypeInteger:
		value, err := strconv.ParseInt(value, 10, 0)
		if err != nil {
			return false, err
		}

		c.Set(key, int(value))
	case TypeMap:
		mapValue, err := convertMap(value)
		if err != nil {
			return false, err
		}

		c.Set(key, mapValue)
	default:
		return false, fmt.Errorf("%w %#v", errUnknownVarType, varType)
	}

	return found, err
}

// Set define the default for given key.
func (c *Configuration) Set(key string, value interface{}) {
	if c.rawValues == nil {
		c.rawValues = make(map[string]interface{})
	}

	keyPart := strings.Split(key, ".")

	setValue(c.rawValues, keyPart, value)
}

// Delete delete a key.
func (c *Configuration) Delete(key string) {
	keyPart := strings.Split(key, ".")
	deleteCfg(c.rawValues, keyPart)
}

func deleteCfg(root map[string]interface{}, keyPart []string) {
	key := keyPart[0]

	if len(keyPart) == 1 {
		delete(root, key)

		return
	}

	newRoot, ok := root[key]
	if !ok {
		return
	}

	newMap, ok := newRoot.(map[string]interface{})
	if !ok {
		return
	}

	deleteCfg(newMap, keyPart[1:])
}

// String return the given key as string.
//
// Return "" if the key does not exists or could not be converted to string.
func (c *Configuration) String(key string) string {
	rawValue, ok := c.Get(key)
	if !ok {
		return ""
	}

	switch value := rawValue.(type) {
	case string:
		return value
	case fmt.Stringer:
		return value.String()
	case int:
		return strconv.FormatInt(int64(value), 10)
	default:
		return fmt.Sprintf("%v", rawValue)
	}
}

// StringList return the given key as []string.
//
// Return nil if the key does not exists or could not be converted to []string.
func (c *Configuration) StringList(key string) []string {
	rawValue, ok := c.Get(key)
	if !ok {
		return nil
	}

	switch value := rawValue.(type) {
	case []string:
		return value
	case []int:
		result := make([]string, len(value))

		for i, v := range value {
			result[i] = strconv.FormatInt(int64(v), 10)
		}

		return result
	case []interface{}:
		result := make([]string, len(value))

		for i, v := range value {
			result[i] = fmt.Sprintf("%v", v)
		}

		return result
	default:
		return nil
	}
}

// StringMap return the given key as a string map.
//
// Return an empty map if the key does not exist or could not be converted to a string map.
func (c *Configuration) StringMap(key string) map[string]string {
	rawValue, ok := c.Get(key)
	if !ok {
		return make(map[string]string)
	}

	switch value := rawValue.(type) {
	case map[string]string:
		return value
	case map[string]interface{}:
		finalMap := make(map[string]string)

		for k, v := range value {
			finalMap[k] = fmt.Sprintf("%v", v)
		}

		return finalMap
	default:
		return make(map[string]string)
	}
}

// Int return the given key as int.
//
// Return 0 if the key does not exist or could not be converted to int.
// Use Get() if you need to known if the key exists or not.
func (c *Configuration) Int(key string) int {
	rawValue, ok := c.Get(key)
	if !ok {
		return 0
	}

	switch value := rawValue.(type) {
	case int:
		return value
	case string:
		v, err := strconv.ParseInt(value, 10, 0)
		if err != nil {
			return 0
		}

		return int(v)
	default:
		return 0
	}
}

// Bool return the given key as bool.
//
// Return false if the key does not exists or could not be converted to bool.
// Use Get() if you need to known if the key exists or not.
func (c *Configuration) Bool(key string) bool {
	rawValue, ok := c.Get(key)
	if !ok {
		return false
	}

	switch value := rawValue.(type) {
	case bool:
		return value
	case int:
		return value != 0
	case string:
		v, err := ConvertBoolean(value)
		if err != nil {
			return false
		}

		return v
	default:
		return false
	}
}

// Get return the given key as interface{}.
func (c *Configuration) Get(key string) (result interface{}, found bool) {
	keyPart := strings.Split(key, ".")

	return get(c.rawValues, keyPart)
}

// DurationMap returns the given key as a duration map, it assumes values are given in seconds.
// Supports durations as int, float, and string.
func (c *Configuration) DurationMap(key string) map[string]time.Duration {
	input, ok := c.Get(key)
	if !ok {
		return make(map[string]time.Duration)
	}

	inputMap, ok := ConvertToMap(input)
	if !ok {
		logger.Printf("Could not convert config key %s to map", key)

		return make(map[string]time.Duration)
	}

	durationMap := make(map[string]time.Duration, len(inputMap))

	for k, rawValue := range inputMap {
		var duration time.Duration
		switch value := rawValue.(type) {
		case int:
			duration = time.Duration(value) * time.Second
		case float64:
			duration = time.Duration(value) * time.Second
		case string:
			var err error

			duration, err = time.ParseDuration(value)
			if err != nil {
				continue
			}
		default:
			continue
		}

		durationMap[k] = duration
	}

	return durationMap
}

func ConvertToMap(input interface{}) (result map[string]interface{}, ok bool) {
	result, ok = input.(map[string]interface{})
	if ok {
		return
	}

	tmp, ok := input.(map[interface{}]interface{})
	if !ok {
		return nil, false
	}

	result = make(map[string]interface{}, len(tmp))

	for k, v := range tmp {
		result[ConvertToString(k)] = v
	}

	return result, true
}

func ConvertToString(rawValue interface{}) string {
	switch value := rawValue.(type) {
	case string:
		return value
	case fmt.Stringer:
		return value.String()
	case int:
		return strconv.FormatInt(int64(value), 10)
	case []interface{}, []string, map[string]interface{}, map[interface{}]interface{}, []map[string]interface{}:
		b, err := json.Marshal(rawValue)
		if err != nil {
			logger.V(1).Printf("Failed to marshal raw value: %v", err)

			return ""
		}

		return string(b)
	default:
		return fmt.Sprintf("%v", rawValue)
	}
}

// Dump return a copy of the whole configuration, with "secret" retracted.
// secret is any key containing "key", "secret", "password" or "passwd".
func (c *Configuration) Dump() (result map[string]interface{}) {
	return dump(c.rawValues)
}

func dump(root map[string]interface{}) map[string]interface{} {
	secretKey := []string{"key", "secret", "password", "passwd"}
	result := make(map[string]interface{}, len(root))

	for k, v := range root {
		isSecret := false

		for _, name := range secretKey {
			if strings.Contains(k, name) {
				isSecret = true

				break
			}
		}

		if isSecret {
			result[k] = "*****"

			continue
		}

		switch v := v.(type) {
		case map[string]interface{}:
			result[k] = dump(v)
		case []interface{}:
			result[k] = dumpList(v)
		default:
			result[k] = v
		}
	}

	return result
}

func dumpList(root []interface{}) []interface{} {
	result := make([]interface{}, len(root))

	for i, v := range root {
		switch v := v.(type) {
		case map[string]interface{}:
			result[i] = dump(v)
		case []interface{}:
			result[i] = dumpList(v)
		default:
			result[i] = v
		}
	}

	return result
}

func get(root interface{}, keyPart []string) (result interface{}, found bool) {
	if len(keyPart) == 0 {
		return root, true
	}

	if m, ok := root.(map[string]interface{}); ok {
		if subRoot, ok := m[keyPart[0]]; ok {
			return get(subRoot, keyPart[1:])
		}
	}

	return nil, false
}

func merge(root map[string]interface{}, newValue map[string]interface{}) {
	for k, v := range newValue {
		if newMap, ok := v.(map[interface{}]interface{}); ok {
			v = convertToStringMap(newMap)
		}
		// if newMap is a map, force doing a merge to ensure all map[interface{}]interface{} are converted to map[string]interface{}
		if newMap, ok := v.(map[string]interface{}); ok {
			if oldV, ok := root[k]; ok {
				if oldMap, ok := oldV.(map[string]interface{}); ok {
					merge(oldMap, newMap)

					continue
				}
			}

			oldMap := make(map[string]interface{})
			root[k] = oldMap

			merge(oldMap, newMap)

			continue
		}

		if oldV, ok := root[k]; ok {
			if newList, ok := v.([]interface{}); ok {
				if oldList, ok := oldV.([]interface{}); ok {
					oldList = append(oldList, newList...)
					root[k] = oldList

					continue
				}
			}
		}

		root[k] = v
	}
}

func convertToStringMap(in map[interface{}]interface{}) map[string]interface{} {
	result := make(map[string]interface{})

	for k, v := range in {
		keyString := fmt.Sprintf("%v", k)
		result[keyString] = v
	}

	return result
}

func setValue(root map[string]interface{}, keyPart []string, value interface{}) {
	key := keyPart[0]

	if len(keyPart) == 1 {
		root[key] = value

		return
	}

	newRoot, ok := root[key]
	if !ok {
		newRoot = make(map[string]interface{})
		root[key] = newRoot
	}

	newMap, ok := newRoot.(map[string]interface{})
	if !ok {
		newMap = make(map[string]interface{})
		root[key] = newMap
	}

	setValue(newMap, keyPart[1:], value)
}

// GetWarnings returns the list of warnings generated by the configuration during parsing.
func (c *Configuration) GetWarnings() []string {
	return c.warnings
}

func (c *Configuration) AddWarning(desc string) {
	c.warnings = append(c.warnings, desc)
}
