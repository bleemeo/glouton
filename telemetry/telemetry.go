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

package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/bleemeo/glouton/logger"
)

type information struct {
	ID                 string `json:"id"`
	BleemeoActive      bool   `json:"bleemeo_active"`
	CPUCores           string `json:"cpu_cores"`
	CPUModel           string `json:"cpu_model"`
	Country            string `json:"country"`
	InstallationFormat string `json:"installation_format"`
	KernelVersion      string `json:"kernel_version"`
	Memory             string `json:"memory"`
	Product            string `json:"product"`
	OSType             string `json:"os_type"`
	OSVersion          string `json:"os_version"`
	SystemArchitecture string `json:"system_architecture"`
	Version            string `json:"version"`
}

func PostInformation(ctx context.Context, telemetryID string, url string, agentid string, facts map[string]string) {
	var bleemeoActive bool

	if agentid == "" {
		bleemeoActive = false
	} else {
		bleemeoActive = true
	}

	information := information{
		ID:                 telemetryID,
		BleemeoActive:      bleemeoActive,
		CPUCores:           facts["cpu_cores"],
		CPUModel:           facts["cpu_model_name"],
		Country:            facts["timezone"],
		InstallationFormat: facts["installation_format"],
		KernelVersion:      facts["kernel_major_version"],
		Memory:             facts["memory"],
		Product:            "Glouton",
		OSType:             facts["os_name"],
		OSVersion:          facts["os_version"],
		SystemArchitecture: facts["architecture"],
		Version:            facts["glouton_version"],
	}

	body, _ := json.Marshal(information) //nolint:errchkjson // False positive.

	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewBuffer(body))

	req.Header.Set("Content-Type", "application/json")

	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	resp, err := http.DefaultClient.Do(req.WithContext(ctx2))
	if err != nil {
		logger.V(1).Printf("failed when we post on telemetry: %v", err)

		return
	}

	logger.V(1).Printf("telemetry response status: %s", resp.Status)

	defer func() {
		// Ensure we read the whole response to avoid "Connection reset by peer" on server
		// and ensure HTTP connection can be resused
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
}
