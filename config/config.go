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
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Configuration hold the agent configuration are set of key/value
//
// value could be typed and a default could be provided.
type Configuration struct {
	rawValues map[string]interface{}

	lookupEnv func(key string) (string, bool)
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
		data, err := ioutil.ReadFile(filepath.Join(dirPath, f.Name()))
		if err != nil && firstError == nil {
			firstError = fmt.Errorf("%#v: %v", f, err)
		} else if err == nil {
			err = c.LoadByte(data)
			if err != nil && firstError == nil {
				firstError = fmt.Errorf("%#v: %v", f, err)
			}
		}
	}
	return firstError
}

// LoadByte will load given YAML data
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
	switch varType {
	case TypeString:
		c.Set(key, value)
	case TypeStringList:
		c.Set(key, strings.Split(value, ","))
	case TypeBoolean:
		value, err := convertBoolean(value)
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
	default:
		return false, fmt.Errorf("unknown variable type %v", varType)
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

// String return the given key as string.
//
// Return "" if the key does not exists or could not be converted to string
func (c Configuration) String(key string) string {
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
// Return nil if the key does not exists or could not be converted to []string
func (c Configuration) StringList(key string) []string {
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

// StringMap return the given key as a string map
//
// Return an empty map if the key does not existor could not be converted to a string map
func (c Configuration) StringMap(key string) map[string]string {
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
func (c Configuration) Int(key string) int {
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
func (c Configuration) Bool(key string) bool {
	rawValue, ok := c.Get(key)
	if !ok {
		return false
	}
	switch value := rawValue.(type) {
	case bool:
		return value
	case int:
		return value == 0
	case string:
		v, err := convertBoolean(value)
		if err != nil {
			return false
		}
		return v
	default:
		return false
	}
}

// Get return the given key as interface{}
func (c Configuration) Get(key string) (result interface{}, found bool) {
	keyPart := strings.Split(key, ".")
	return get(c.rawValues, keyPart, key)
}

func get(root interface{}, keyPart []string, key string) (result interface{}, found bool) {
	if len(keyPart) == 0 {
		return root, true
	}
	if m, ok := root.(map[string]interface{}); ok {
		if subRoot, ok := m[keyPart[0]]; ok {
			return get(subRoot, keyPart[1:], key)
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
