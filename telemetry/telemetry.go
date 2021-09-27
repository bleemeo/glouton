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
	"context"
	"encoding/json"
	"glouton/logger"
	"io"
	"io/ioutil"
	"net/http"
	"time"
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

func (t Telemetry) PostInformation(ctx context.Context, url string, agentid string, facts map[string]string) {
	var bleemeoActive bool

	if agentid == "" {
		bleemeoActive = false
	} else {
		bleemeoActive = true
	}

	body, _ := json.Marshal(map[string]interface{}{
		"id":                  t.ID,
		"bleemeo_active":      bleemeoActive,
		"cpu_cores":           facts["cpu_cores"],
		"cpu_model":           facts["cpu_model_name"],
		"country":             facts["timezone"],
		"installation_format": facts["installation_format"],
		"kernel_version":      facts["kernel_major_version"],
		"memory":              facts["memory"],
		"product":             "Glouton",
		"os_type":             facts["os_name"],
		"os_version":          facts["os_version"],
		"system_architecture": facts["architecture"],
		"version":             facts["glouton_version"],
	})

	req, _ := http.NewRequest("POST", url, bytes.NewBuffer(body))

	req.Header.Set("Content-Type", "application/json")

	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	resp, err := http.DefaultClient.Do(req.WithContext(ctx2))
	if err != nil {
		logger.V(1).Printf("failed when we post on telemetry: %v", err)

		return
	}

	logger.V(1).Printf("telemetry response Satus: %s", resp.Status)

	defer func() {
		// Ensure we read the whole response to avoid "Connection reset by peer" on server
		// and ensure HTTP connection can be resused
		_, _ = io.Copy(ioutil.Discard, resp.Body)
		resp.Body.Close()
	}()
}
