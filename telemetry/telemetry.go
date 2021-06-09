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

package telemetry

import (
	"glouton/logger"
)

const telemetryKey = "Telemetry"

// State allow to persite object.
type state interface {
	Get(key string, result interface{}) error
	Set(key string, object interface{}) error
}

type Telemetry struct {
	ID string
}

func FromState(state state) Telemetry {
	var result Telemetry

	if err := state.Get(telemetryKey, &result); err != nil {
		logger.V(1).Printf("Unable to load new telemetry, try using old format: %v", err)
	}

	return result
}

func (t Telemetry) SaveState(state state) {
	err := state.Set(telemetryKey, t)
	if err != nil {
		logger.V(1).Printf("Unable to persist discovered Telemetry id: %v", err)
	}
}
