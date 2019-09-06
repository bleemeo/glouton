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
	"strconv"
	"strings"
)

// ValueType is a list of known value type
type ValueType int

// Possible values for the ValueType enum.
const (
	TypeUnknown ValueType = iota
	TypeString
	TypeStringList
	TypeInteger
	TypeBoolean
)

func convertBoolean(value string) (bool, error) {
	value = strings.ToLower(value)
	result, err := strconv.ParseBool(value)
	if err != nil {
		// We also support "yes" and "no"
		if value == "yes" {
			result = true
			err = nil
		} else if value == "no" {
			result = false
			err = nil
		}
	}
	return result, err
}
