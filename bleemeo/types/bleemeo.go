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
	LiveProcess             bool   `json:"live_process"`
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
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	ContainerID      string    `json:"container_id"`
	ContainerInspect string    `json:"container_inspect"`
	Status           string    `json:"container_status"`
	CreatedAt        time.Time `json:"container_created_at"`
	Runtime          string    `json:"container_runtime"`

	InspectHash          string    `json:",omitempty"`
	GloutonLastUpdatedAt time.Time `json:",omitempty"`
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
	ServiceID   string            `json:"service,omitempty"`
	ContainerID string            `json:"container,omitempty"`
	StatusOf    string            `json:"status_of,omitempty"`
	Threshold
	threshold.Unit
	DeactivatedAt time.Time `json:"deactivated_at,omitempty"`
}

// FailureKind is the kind of failure to register a metric. Used to know if
// we should (quickly) retry a failure.
type FailureKind int

// All possible value for FailureKind.
const (
	FailureUnknown FailureKind = iota
	FailureAllowList
	FailureTooManyMetric
)

// MetricRegistration contains information about a metric registration failure.
type MetricRegistration struct {
	LabelsText   string
	LastFailAt   time.Time
	FailCounter  int
	LastFailKind FailureKind
}

// IsPermanentFailure tells whether the error is permanent and there is no need to quickly retry.
func (kind FailureKind) IsPermanentFailure() bool {
	switch kind {
	case FailureAllowList, FailureTooManyMetric:
		return true
	default:
		return false
	}
}

func (kind FailureKind) String() string {
	switch kind {
	case FailureAllowList:
		return "not-allowed"
	case FailureTooManyMetric:
		return "too-many-metric"
	default:
		return "unknown"
	}
}

// RetryAfter return the time after which the retry of the registration may be retried.
func (mr MetricRegistration) RetryAfter() time.Time {
	factor := math.Pow(2, float64(mr.FailCounter))
	delay := 15 * time.Second * time.Duration(factor)

	if delay > 45*time.Minute {
		delay = 45 * time.Minute
	}

	return mr.LastFailAt.Add(delay)
}

// FillInspectHash fill the DockerInspectHash.
func (c *Container) FillInspectHash() {
	bin := sha256.Sum256([]byte(c.ContainerInspect))
	c.InspectHash = fmt.Sprintf("%x", bin)
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

type minimumSupportedVersions struct {
	Glouton string `json:"glouton_version"`
}

type globalInfoAgents struct {
	MinVersions minimumSupportedVersions `json:"minimum_versions"`
}

// GlobalInfo represents the bleemeo agent global information.
type GlobalInfo struct {
	MaintenanceEnabled bool             `json:"maintenance"`
	Agents             globalInfoAgents `json:"agents"`
	CurrentTime        float64          `json:"current_time"`
	MaxTimeDrift       float64          `json:"max_time_drift"`
	FetchedAt          time.Time        `json:"-"`
}

// BleemeoTime return the time according to Bleemeo API.
func (i GlobalInfo) BleemeoTime() time.Time {
	return time.Unix(int64(i.CurrentTime), int64(i.CurrentTime*1e9)%1e9)
}

// TimeDrift return the time difference between local clock and Bleemeo API.
func (i GlobalInfo) TimeDrift() time.Duration {
	if i.FetchedAt.IsZero() || i.CurrentTime == 0 {
		return 0
	}

	return i.FetchedAt.Sub(i.BleemeoTime())
}

// IsTimeDriftTooLarge returns whether the local time it too wrong.
func (i GlobalInfo) IsTimeDriftTooLarge() bool {
	if i.FetchedAt.IsZero() || i.CurrentTime == 0 {
		return false
	}

	return math.Abs(i.TimeDrift().Seconds()) >= i.MaxTimeDrift
}
