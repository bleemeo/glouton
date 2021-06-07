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
	"bytes"
	"encoding/json"
	"glouton/logger"
	"net/http"

	"github.com/google/uuid"
)

const telemetryKey = "Telemetry"

// State allow to persite object.
type State interface {
	Get(key string, result interface{}) error
	Set(key string, object interface{}) error
}

type Telemetry struct {
	ID string
}

func telemetryFromState(state State) Telemetry {
	var result Telemetry

	if err := state.Get(telemetryKey, &result); err != nil {
		logger.V(1).Printf("Unable to load new telemetry, try using old format: %v", err)
	}

	return result
}

func saveState(state State, telemetry Telemetry) {

	err := state.Set(telemetryKey, telemetry)
	if err != nil {
		logger.V(1).Printf("Unable to persist discovered Telemetry id: %v", err)
	}
}

func SendInformationToTelemetry(state State, facts map[string]string) {
	tlm := telemetryFromState(state)

	if tlm.ID == "" {
		var t Telemetry
		t.ID = uuid.New().String()
		saveState(state, t)
		tlm = t
	}
	body, _ := json.Marshal(map[string]string{
		"id":                  tlm.ID,
		"cpu_cores":           facts["cpu_cores"],
		"country":             facts["timezone"],
		"installation_format": facts["installation_format"],
		"kernel_version":      facts["kernel_major_version"],
		"memory":              facts["memory_used"],
		"product":             "Glouton",
		"os_type":             facts["os_name"],
		"os_version":          facts["os_version"],
		"system_architecture": facts["architecture"],
		"version":             facts["glouton_version"],
	})
	req, err := http.NewRequest("POST", "https://telemetry.bleemeo.com/telemetry/", bytes.NewBuffer(body))
	req.Header.Set("X-Custom-Header", "telemetry tool")
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		logger.V(1).Printf("%v", err)
	}
	logger.V(2).Println("telemetry response Satus", resp.Status)
}
