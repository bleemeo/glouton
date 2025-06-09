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

package types

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/bleemeo/bleemeo-go"
	"github.com/bleemeo/glouton/threshold"
)

type NullTime time.Time //nolint: recvcheck

// MarshalJSON marshall the time.Time as usual BUT zero time is sent as "null".
func (t NullTime) MarshalJSON() ([]byte, error) {
	if time.Time(t).IsZero() {
		return []byte("null"), nil
	}

	return json.Marshal(time.Time(t))
}

// UnmarshalJSON the time.Time as usual BUT zero time is read as "null".
func (t *NullTime) UnmarshalJSON(b []byte) error {
	if bytes.Equal(b, []byte("null")) {
		*t = NullTime{}

		return nil
	}

	return json.Unmarshal(b, (*time.Time)(t))
}

func (t NullTime) Equal(b NullTime) bool {
	return time.Time(t).Equal(time.Time(b))
}

// AgentFact is an agent facts.
type AgentFact struct {
	ID      string `json:"id"`
	AgentID string `json:"agent"`
	Key     string `json:"key"`
	Value   string `json:"value"`
}

// Agent is an Agent object on Bleemeo API.
type Agent struct {
	ID                     string    `json:"id"`
	CreatedAt              time.Time `json:"created_at"`
	AccountID              string    `json:"account"`
	NextConfigAt           time.Time `json:"next_config_at"`
	CurrentAccountConfigID string    `json:"current_config"`
	Tags                   []Tag     `json:"tags"`
	AgentType              string    `json:"agent_type"`
	FQDN                   string    `json:"fqdn"`
	DisplayName            string    `json:"display_name"`
	// If the agent is running in Kubernetes, is he the current cluster leader?
	// Only the cluster leader gather global metrics for the cluster.
	IsClusterLeader bool `json:"is_cluster_leader"`
}

// AgentType is an AgentType object on Bleemeo API.
type AgentType struct {
	ID          string            `json:"id"`
	Name        bleemeo.AgentType `json:"name"`
	DisplayName string            `json:"display_name"`
}

// Tag is an Tag object on Bleemeo API.
type Tag struct {
	ID           string          `json:"id,omitempty"`
	Name         string          `json:"name"`
	IsAutomatic  bool            `json:"is_automatic,omitempty"`
	IsServiceTag bool            `json:"is_service_tag,omitempty"`
	TagType      bleemeo.TagType `json:"tag_type"`
}

// AccountConfig is a configuration of account.
type AccountConfig struct {
	ID                    string `json:"id"`
	Name                  string `json:"name"`
	MaxCustomMetrics      int    `json:"number_of_custom_metrics"`
	LiveProcessResolution int    `json:"live_process_resolution"`
	LiveProcess           bool   `json:"live_process"`
	DockerIntegration     bool   `json:"docker_integration"`
	SNMPIntegration       bool   `json:"snmp_integration"`
	VSphereIntegration    bool   `json:"vsphere_integration"`
	Suspended             bool   `json:"suspended"`
}

// AgentConfig is a configuration for one kind of agent.
type AgentConfig struct {
	ID               string `json:"id"`
	MetricsAllowlist string `json:"metrics_allowlist"`
	// MetricResolution is given in seconds.
	MetricResolution int    `json:"metrics_resolution"`
	AccountConfig    string `json:"account_config"`
	AgentType        string `json:"agent_type"`
}

// Application is a group of services.
type Application struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name"`
	Tag  string `json:"tag"`
}

// Service is a Service object on Bleemeo API.
type Service struct {
	ID              string `json:"id"`
	AccountConfig   string `json:"account_config"`
	Label           string `json:"label"`
	Instance        string `json:"instance"`
	ListenAddresses string `json:"listen_addresses"`
	ExePath         string `json:"exe_path"`
	Tags            []Tag  `json:"tags"`
	Active          bool   `json:"active"`
	CreationDate    string `json:"created_at"`
}

// Container is a Container object on Bleemeo API.
type Container struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	ContainerID      string    `json:"container_id"`
	ContainerInspect string    `json:"container_inspect"`
	Status           string    `json:"container_status"`
	CreatedAt        time.Time `json:"container_created_at"`
	DeletedAt        NullTime  `json:"deleted_at"`
	Runtime          string    `json:"container_runtime"`

	InspectHash          string    `json:",omitempty"`
	GloutonLastUpdatedAt time.Time `json:",omitempty"`
}

// Threshold is the threshold of a metrics. We use pointer to float to support
// null value in JSON.
type Threshold struct {
	LowWarning   *float64 `json:"threshold_low_warning"`
	LowCritical  *float64 `json:"threshold_low_critical"`
	HighWarning  *float64 `json:"threshold_high_warning"`
	HighCritical *float64 `json:"threshold_high_critical"`
}

// Monitor groups all the information required to write metrics to a monitor.
type Monitor struct {
	Service
	URL     string `json:"monitor_url"`
	AgentID string `json:"agent"`
	MonitorHTTPOptions
}

// MonitorHTTPOptions groups all the possible options when the probe is targeting an HTTP or HTTPS service.
type MonitorHTTPOptions struct {
	ExpectedContent      string            `json:"monitor_expected_content,omitempty"`
	ExpectedResponseCode int               `json:"monitor_expected_response_code,omitempty"`
	ForbiddenContent     string            `json:"monitor_unexpected_content,omitempty"`
	CAFile               string            `json:"monitor_ca_file,omitempty"`
	Headers              map[string]string `json:"monitor_headers,omitempty"`
}

// Metric is a Metric object on Bleemeo API.
type Metric struct {
	ID          string            `json:"id"`
	AgentID     string            `json:"agent,omitempty"`
	LabelsText  string            `json:"labels_text,omitempty"`
	Labels      map[string]string `json:"-"`
	ServiceID   string            `json:"service,omitempty"`
	ContainerID string            `json:"container,omitempty"`
	StatusOf    string            `json:"status_of,omitempty"`
	Threshold
	threshold.Unit
	DeactivatedAt time.Time `json:"deactivated_at,omitempty"`
	FirstSeenAt   time.Time `json:"first_seen_at"`
}

// FailureKind is the kind of failure to register a metric. Used to know if
// we should (quickly) retry a failure.
type FailureKind int

// All possible value for FailureKind.
const (
	FailureUnknown FailureKind = iota
	FailureAllowList
	FailureTooManyCustomMetrics
	FailureTooManyStandardMetrics
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
	case FailureAllowList, FailureTooManyStandardMetrics, FailureTooManyCustomMetrics:
		return true
	case FailureUnknown:
		return false
	default:
		return false
	}
}

func (kind FailureKind) String() string {
	switch kind {
	case FailureAllowList:
		return "not-allowed"
	case FailureTooManyStandardMetrics:
		return "too-many-standard-metrics"
	case FailureTooManyCustomMetrics:
		return "too-many-custom-metrics"
	case FailureUnknown:
		return "unknown" //nolint:goconst
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
	c.InspectHash = hex.EncodeToString(bin[:])
}

// ToInternalThreshold convert to a threshold.Threshold (use NaN instead of null pointer for unset thresholds).
func (t Threshold) ToInternalThreshold() (result threshold.Threshold) {
	if t.LowWarning != nil {
		result.LowWarning = *t.LowWarning
	} else {
		result.LowWarning = math.NaN()
	}

	if t.LowCritical != nil {
		result.LowCritical = *t.LowCritical
	} else {
		result.LowCritical = math.NaN()
	}

	if t.HighWarning != nil {
		result.HighWarning = *t.HighWarning
	} else {
		result.HighWarning = math.NaN()
	}

	if t.HighCritical != nil {
		result.HighCritical = *t.HighCritical
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

// GloutonConfigItem object on the Bleemeo API.
type GloutonConfigItem struct {
	ID       string                   `json:"id"`
	Agent    string                   `json:"agent"`
	Key      string                   `json:"key"`
	Value    any                      `json:"value"`
	Priority int                      `json:"priority"`
	Source   bleemeo.ConfigItemSource `json:"source"`
	Path     string                   `json:"path"`
	Type     bleemeo.ConfigItemType   `json:"type"`
}

func FormatConfigItemSource(c bleemeo.ConfigItemSource) string {
	switch c {
	case bleemeo.ConfigItemSource_File:
		return "file"
	case bleemeo.ConfigItemSource_Env:
		return "env"
	case bleemeo.ConfigItemSource_Default:
		return "default"
	case bleemeo.ConfigItemSource_API:
		return "api"
	case bleemeo.ConfigItemSource_Unknown:
		return "unknown"
	default:
		return "unknown"
	}
}

type LogsAvailability int

const (
	LogsAvailabilityOk LogsAvailability = iota + 1
	LogsAvailabilityShouldBuffer
	LogsAvailabilityShouldDiscard
)

func (la LogsAvailability) String() string {
	switch la {
	case LogsAvailabilityOk:
		return "ok"
	case LogsAvailabilityShouldBuffer:
		return "buffer"
	case LogsAvailabilityShouldDiscard:
		return "discard"
	default:
		return fmt.Sprintf("unknown (%d)", int(la))
	}
}
