// Copyright 2015-2026 Bleemeo
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
	"github.com/bleemeo/glouton/discovery"
	"github.com/bleemeo/glouton/threshold"
	"github.com/bleemeo/glouton/types"
	"github.com/bleemeo/glouton/utils/metricutils"
)

type NullTime time.Time

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
	ID           string    `json:"id"`
	CreatedAt    time.Time `json:"created_at"`
	AccountID    string    `json:"account"`
	NextConfigAt time.Time `json:"next_config_at"`
	Tags         []Tag     `json:"tags"`
	AgentType    string    `json:"agent_type"`
	FQDN         string    `json:"fqdn"`
	DisplayName  string    `json:"display_name"`
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

// Config is a per-account configuration entry on the Bleemeo API.
type Config struct {
	ID        string `json:"id"`
	Type      int    `json:"type"`
	AgentType string `json:"agent_type"`
	Account   string `json:"account"`
	Value     any    `json:"value"`
}

const (
	ConfigTypeApplicationIntegration = 1003
	ConfigTypeDockerIntegration      = 1010
	ConfigTypeSNMPIntegration        = 1030
	ConfigTypeVSphereIntegration     = 1040
	ConfigTypeLiveProcess            = 1070
	ConfigTypeLiveProcessResolution  = 1071
	ConfigTypeCustomMetrics          = 1400
	// ConfigTypeGloutonSendRegistrationStatus allows sending the Agent's registration_status. It
	// lets Bleemeo turn the feature off, as on a Glouton version that misbehaves, without an upgrade.
	ConfigTypeGloutonSendRegistrationStatus = 2001
	ConfigTypeAgentMetricsAllowlist         = 10000
	ConfigTypeAgentMetricsResolution        = 11000
	ConfigTypeSuspended                     = 100000
)

// Application is a group of services.
type Application struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name"`
	Tag  string `json:"tag"`
}

// Service is a Service object on Bleemeo API.
type Service struct {
	ID              string `json:"id"`
	Account         string `json:"account"`
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
	GloutonLastUpdatedAt time.Time `json:",omitzero"`
}

// Threshold is the threshold of a metrics. We use pointer to float to support
// null value in JSON.
type Threshold struct {
	LowWarning   *float64 `json:"threshold_low_warning"`
	LowCritical  *float64 `json:"threshold_low_critical"`
	HighWarning  *float64 `json:"threshold_high_warning"`
	HighCritical *float64 `json:"threshold_high_critical"`
}

// Monitor is a Monitor object on Bleemeo API.
type Monitor struct {
	MonitorHTTPOptions

	ID           string `json:"id"`
	Account      string `json:"account"`
	AgentID      string `json:"agent"`
	URL          string `json:"url"`
	CreationDate string `json:"created_at"`
	Active       bool   `json:"active"`
}

// MonitorHTTPOptions groups all the possible options when the probe is targeting an HTTP or HTTPS service.
type MonitorHTTPOptions struct {
	ExpectedContent      string            `json:"expected_content,omitempty"`
	ExpectedResponseCode int               `json:"expected_response_code,omitempty"`
	ForbiddenContent     string            `json:"unexpected_content,omitempty"`
	CAFile               string            `json:"ca_file,omitempty"`
	Headers              map[string]string `json:"headers,omitempty"`
	HeadRequest          bool              `json:"head_request,omitempty"`
}

// Metric is a Metric object on Bleemeo API.
type Metric struct {
	Threshold
	threshold.Unit

	ID            string    `json:"id"`
	AgentID       string    `json:"agent,omitempty"`
	LabelsText    string    `json:"labels_text,omitempty"`
	ServiceID     string    `json:"service,omitempty"`
	ContainerID   string    `json:"container,omitempty"`
	StatusOf      string    `json:"status_of,omitempty"`
	DeactivatedAt time.Time `json:"deactivated_at,omitzero"`
	FirstSeenAt   time.Time `json:"first_seen_at"`

	lazyInitialized bool
	onlyHasItem     bool
}

func (m *Metric) OnlyHasItem() bool {
	if !m.lazyInitialized {
		m.onlyHasItem = metricutils.MetricOnlyHasItem(types.TextToLabels(m.LabelsText), m.AgentID)
		m.lazyInitialized = true
	}

	return m.onlyHasItem
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

// MetricStateReason is why a metric Glouton has is not registered on Bleemeo. It unifies
// FailureKind (refused by the API), DenyReason (dropped before registration) and the
// dependency waits, which had no name, so that Bleemeo gets one vocabulary.
type MetricStateReason string

// All possible values of MetricStateReason.
const (
	// ReasonPending means the metric is simply waiting to be registered. Nothing is wrong.
	ReasonPending MetricStateReason = "pending"
	// ReasonUnknown means the registration failed for a reason Glouton could not classify.
	ReasonUnknown MetricStateReason = "unknown"

	// ReasonNotAllowed and the two following come from FailureKind: registration was
	// attempted by Glouton and refused by the API.
	ReasonNotAllowed             MetricStateReason = "not-allowed"
	ReasonTooManyStandardMetrics MetricStateReason = "too-many-standard-metrics"
	ReasonTooManyCustomMetrics   MetricStateReason = "too-many-custom-metrics"

	// ReasonNotAvailableInPlan and the four following come from DenyReason: the metric
	// was dropped before registration was even attempted.
	ReasonNotAvailableInPlan  MetricStateReason = "not-available-in-plan"
	ReasonErrorOccurred       MetricStateReason = "error-occurred"
	ReasonNoDockerIntegration MetricStateReason = "no-docker-integration"
	ReasonItemTooLong         MetricStateReason = "item-too-long"
	ReasonMissingContainerID  MetricStateReason = "missing-container-id"

	// ReasonWaitingContainer and ReasonWaitingService mean the metric depends on an
	// object that has to be registered first.
	ReasonWaitingContainer MetricStateReason = "waiting-container"
	ReasonWaitingService   MetricStateReason = "waiting-service"
	// ReasonWaitingAccountConfig means Glouton has not yet received the account
	// configuration, without which it cannot decide what to register.
	ReasonWaitingAccountConfig MetricStateReason = "waiting-account-config"
)

// MetricStateReason converts a registration failure to the unified vocabulary.
func (kind FailureKind) MetricStateReason() MetricStateReason {
	switch kind {
	case FailureAllowList:
		return ReasonNotAllowed
	case FailureTooManyStandardMetrics:
		return ReasonTooManyStandardMetrics
	case FailureTooManyCustomMetrics:
		return ReasonTooManyCustomMetrics
	case FailureUnknown:
		return ReasonUnknown
	default:
		return ReasonUnknown
	}
}

// MetricStateReason converts a deny reason to the unified vocabulary.
//
// NotDenied has no reason of its own: a metric that was not denied is not in the list.
func (d DenyReason) MetricStateReason() MetricStateReason {
	switch d {
	case DenyNotAvailableInCurrentPlan:
		return ReasonNotAvailableInPlan
	case DenyErrorOccurred:
		return ReasonErrorOccurred
	case DenyNoDockerIntegration:
		return ReasonNoDockerIntegration
	case DenyItemTooLong:
		return ReasonItemTooLong
	case DenyMissingContainerID:
		return ReasonMissingContainerID
	case NotDenied:
		return ReasonUnknown
	default:
		return ReasonUnknown
	}
}

// MetricState is how many metrics sharing a name are not registered, and why. Name is the
// metric name only, such as "disk_used_perc", without item or labels.
type MetricState struct {
	Name   string            `json:"name"`
	Reason MetricStateReason `json:"reason"`
	Count  int               `json:"count"`
}

// RegistrationStatusSent is what Glouton remembers of the last registration status it sent,
// kept in the cache so that a restart neither re-sends an unchanged value before its day nor
// reads every service as new.
type RegistrationStatusSent struct {
	// AgentID is the agent the value was sent to: the cache, in state.cache.json, survives
	// deleting state.json to register again.
	AgentID string
	// Hash identifies the value last accepted by the API. Empty when none was.
	Hash string
	// Services are the services that value listed as settled. See syncregistrationstatus.NeedSend.
	Services []discovery.NameInstance
	// AcceptedAt is when the API accepted that value, the generated_at it holds.
	AcceptedAt time.Time
	// LastAttemptAt is when a send was last attempted, accepted or not.
	LastAttemptAt time.Time
	// LastAttemptFailed tells whether that attempt was refused or failed.
	LastAttemptFailed bool
}

// RetryAfter return the time after which the retry of the registration may be retried.
func (mr MetricRegistration) RetryAfter() time.Time {
	factor := math.Pow(2, float64(mr.FailCounter))
	delay := min(15*time.Second*time.Duration(factor), 45*time.Minute)

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

// LogsBackPressureDiagnostic explains the LogsAvailability currently applied to log shipping.
type LogsBackPressureDiagnostic struct {
	// Status is LogsAvailability.String().
	Status string
	// Causes explains a Status other than "ok".
	Causes          []string `json:",omitempty"`
	MQTTConnected   bool
	LastAck         time.Time
	PendingMessages int
	// APIStatus is the logs stream availability the Bleemeo API sent in its last ack.
	APIStatus string
}

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
