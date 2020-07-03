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

package types

import (
	"crypto/sha256"
	"fmt"
	"glouton/threshold"
	"math"
	"strings"
	"time"
)

// AgentFact is an agent facts.
type AgentFact struct {
	ID    string
	Key   string
	Value string
}

// Agent is an Agent object on Bleemeo API.
type Agent struct {
	ID              string    `json:"id"`
	CreatedAt       time.Time `json:"created_at"`
	AccountID       string    `json:"account"`
	NextConfigAt    time.Time `json:"next_config_at"`
	CurrentConfigID string    `json:"current_config"`
	Tags            []Tag     `json:"tags"`
}

// Tag is an Tag object on Bleemeo API.
type Tag struct {
	ID           string `json:"id,omitempty"`
	Name         string `json:"name"`
	IsAutomatic  bool   `json:"is_automatic,omitempty"`
	IsServiceTag bool   `json:"is_service_tag,omitempty"`
}

// AccountConfig is the configuration used by this agent.
type AccountConfig struct {
	ID                      string `json:"id"`
	Name                    string `json:"name"`
	MetricsAgentWhitelist   string `json:"metrics_agent_whitelist"`
	MetricAgentResolution   int    `json:"metrics_agent_resolution"`
	MetricMonitorResolution int    `json:"metrics_monitor_resolution"`
	LiveProcessResolution   int    `json:"live_process_resolution"`
	DockerIntegration       bool   `json:"docker_integration"`
}

// Service is a Service object on Bleemeo API.
type Service struct {
	ID              string `json:"id"`
	AccountConfig   string `json:"account_config"`
	Label           string `json:"label"`
	Instance        string `json:"instance"`
	ListenAddresses string `json:"listen_addresses"`
	ExePath         string `json:"exe_path"`
	Stack           string `json:"stack"`
	Active          bool   `json:"active"`
	CreationDate    string `json:"created_at"`
}

// Container is a Contaier object on Bleemeo API.
type Container struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	DockerID          string `json:"docker_id"`
	DockerInspect     string `json:"docker_inspect"`
	DockerInspectHash string `json:",omitempty"`
}

// Threshold is the threshold of a metrics. We use pointer to float to support
// null value in JSON.
type Threshold struct {
	LowWarning    *float64 `json:"threshold_low_warning"`
	LowCrictical  *float64 `json:"threshold_low_critical"`
	HighWarning   *float64 `json:"threshold_high_warning"`
	HighCrictical *float64 `json:"threshold_high_critical"`
}

// Monitor groups all the informations required to write metrics to a monitor.
type Monitor struct {
	Service
	URL     string `json:"monitor_url"`
	AgentID string `json:"agent"`
	MonitorHTTPOptions
}

// MonitorHTTPOptions groups all the possible options when the probe is targeting an HTTP or HTTPS service.
type MonitorHTTPOptions struct {
	ExpectedContent      string `json:"monitor_expected_content,omitempty"`
	ExpectedResponseCode int    `json:"monitor_expected_response_code,omitempty"`
	ForbiddenContent     string `json:"monitor_unexpected_content,omitempty"`
}

// Metric is a Metric object on Bleemeo API.
type Metric struct {
	ID          string            `json:"id"`
	LabelsText  string            `json:"labels_text,omitempty"`
	Labels      map[string]string `json:"-"`
	Item        string            `json:"item,omitempty"`
	ServiceID   string            `json:"service,omitempty"`
	ContainerID string            `json:"container,omitempty"`
	StatusOf    string            `json:"status_of,omitempty"`
	Threshold
	threshold.Unit
	DeactivatedAt time.Time `json:"deactivated_at,omitempty"`
}

// FillInspectHash fill the DockerInspectHash.
func (c *Container) FillInspectHash() {
	bin := sha256.Sum256([]byte(c.DockerInspect))
	c.DockerInspectHash = fmt.Sprintf("%x", bin)
}

// MetricsAgentWhitelistMap return a map with all whitelisted agent metrics.
func (ac AccountConfig) MetricsAgentWhitelistMap() map[string]bool {
	result := make(map[string]bool)

	if len(ac.MetricsAgentWhitelist) == 0 {
		return nil
	}

	for _, n := range strings.Split(ac.MetricsAgentWhitelist, ",") {
		result[strings.Trim(n, " \t\n")] = true
	}

	return result
}

// ToInternalThreshold convert to a threshold.Threshold (use NaN instead of null pointer for unset threshold).
func (t Threshold) ToInternalThreshold() (result threshold.Threshold) {
	if t.LowWarning != nil {
		result.LowWarning = *t.LowWarning
	} else {
		result.LowWarning = math.NaN()
	}

	if t.LowCrictical != nil {
		result.LowCritical = *t.LowCrictical
	} else {
		result.LowCritical = math.NaN()
	}

	if t.HighWarning != nil {
		result.HighWarning = *t.HighWarning
	} else {
		result.HighWarning = math.NaN()
	}

	if t.HighCrictical != nil {
		result.HighCritical = *t.HighCrictical
	} else {
		result.HighCritical = math.NaN()
	}

	return result
}
