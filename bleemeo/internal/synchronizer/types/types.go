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
	"context"
	"errors"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bleemeo/glouton/bleemeo/internal/cache"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/bleemeoapi"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/discovery"
	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/prometheus/model"
)

type EntityName string

const (
	EntityAccountConfig EntityName = "accountconfig"
	EntityAgent         EntityName = "agent"
	EntityApplication   EntityName = "application"
	EntityConfig        EntityName = "config"
	EntityContainer     EntityName = "container"
	EntityDiagnostics   EntityName = "diagnostics"
	EntityFact          EntityName = "facts"
	EntityInfo          EntityName = "info"
	EntityMetric        EntityName = "metric"
	EntityMonitor       EntityName = "monitor"
	EntityService       EntityName = "service"
	EntitySNMP          EntityName = "snmp"
	EntityVSphere       EntityName = "vsphere"
)

type SyncType int

const (
	SyncTypeNone              SyncType = 0
	SyncTypeNormal            SyncType = 1
	SyncTypeForceCacheRefresh SyncType = 2
)

func (t SyncType) String() string {
	switch t {
	case SyncTypeNone:
		return "sync-none"
	case SyncTypeNormal:
		return "sync-normal"
	case SyncTypeForceCacheRefresh:
		return "sync-forced"
	default:
		return strconv.Itoa(int(t))
	}
}

type APIFeature int

// There are currently no features in this list

func (t APIFeature) String() string {
	switch t { //nolint: gocritic // more cases may exist
	default:
		return strconv.Itoa(int(t))
	}
}

var ErrUnexpectedWorkflow = errors.New("unexpected synchronization workflow")

type EntitySynchronizer interface {
	Name() EntityName
	// EnabledInMaintenance return True if this entity should be synchronized even in maintenance mode
	EnabledInMaintenance() bool
	// EnabledInSuspendedMode return True if this entity should be synchronized even in suspended mode
	EnabledInSuspendedMode() bool

	PrepareExecution(ctx context.Context, execution SynchronizationExecution) (EntitySynchronizerExecution, error)
}

type EntitySynchronizerExecution interface {
	// NeedSynchronization returns True if the entity needs to be synchronized.
	// This could depend on various states of the Glouton.
	// Note that the synchronized could call the entity synchronizer even if the method return false,
	// mostly in case a RequestSynchronization() was called.
	// It could also NOT call it even if the method returns true, mostly in case an error occurred in another
	// synchronizer.
	NeedSynchronization(ctx context.Context) (bool, error)

	// RefreshCache update the cache with information from Bleemeo API. This could no nothing
	// if the cache is still valid.
	RefreshCache(ctx context.Context, syncType SyncType) error

	// SyncRemoteAndLocal update the Bleemeo API and/or the local entity list: create, update or delete entries.
	// RefreshCache normally updates the Bleemeo API *cache*, not the Glouton local entity list.
	// For example, for services entity, the cache is a view of Bleemeo API and the local list is the output of
	// discovery.
	SyncRemoteAndLocal(ctx context.Context, syncType SyncType) error

	// FinishExecution is called at the end of the execution of all entities synchronization (NeedSynchronization,
	// RefreshCache, and SyncRemoteAndLocal).
	// It's called unconditionally if PrepareExecution() returned non-nil value. Even if NeedSynchronization() returned false,
	// or a function had error.
	FinishExecution(ctx context.Context)
}

type SynchronizationExecution interface {
	BleemeoAPIClient() Client
	IsOnlyEssential() bool
	// RequestSynchronization ask for an execution of synchronization of the specified entity.
	// If this is called during calls to NeedSynchronization, it is tried to be run during
	// the current execution of synchronization (no guarantee, e.g., on error).
	// If called later, once SyncRemote starts being called, it will be run during *next* execution.
	RequestSynchronization(entityName EntityName, requestFull bool)
	// RequestLinkedSynchronization ask for an execution of synchronization of the specified entity,
	// if another entity will be synchronized. This is better than using IsSynchronizationRequested,
	// because if later something requests synchronization of the other entity, it will be updated.
	// For example, RequestSynchronization(EntityApplication, EntityService) will
	// cause the synchronization of application if services are synchronized. This doesn't imply the other way.
	// This function could only be called during NeedSynchronization.
	RequestLinkedSynchronization(targetEntityName EntityName, triggerEntityName EntityName) error
	// FailOtherEntity will cause execution of the synchronization of another entity to not
	// be run in this execution and fail with provided error. This is useful if one entity
	// must be created/updated before another entity synchronization runs.
	FailOtherEntity(entityName EntityName, reason error)
	// RequestSynchronizationForAll calls RequestSynchronization for all known entities.
	RequestSynchronizationForAll(requestFull bool)
	// IsSynchronizationRequested return whether a synchronization was requested for the
	// specific entity during the current execution.
	IsSynchronizationRequested(entityName EntityName) bool
	RequestUpdateThresholds()
	RequestNotifyLabelsUpdate()
	Option() Option
	// LastSync returns the time the last synchronization started at and completed without error on all entities.
	LastSync() time.Time
	// StartedAt returns the time this synchronization started at.
	StartedAt() time.Time
	GlobalState() SynchronizedGlobalState
}

type Client interface {
	RawClient
	MetaClient
	ApplicationClient
	AccountConfigClient
	AgentClient
	GloutonConfigItemClient
	ContainerClient
	DiagnosticClient
	FactClient
	MetricClient
	MonitorClient
	ServiceClient
	SNMPClient
	VSphereClient
}

// RawClient a client doing generic HTTP call to Bleemeo API.
// Ideally synchronizer of entity should move to higher level interface, which will make mocking easier (like ListActiveMetrics, CreateService...)
type RawClient interface {
	ThrottleDeadline() time.Time
}

type MetaClient interface {
	GetGlobalInfo(ctx context.Context) (bleemeoTypes.GlobalInfo, error)
	RegisterSelf(ctx context.Context, accountID, password, initialServerGroupName, name, fqdn, registrationKey string) (id string, err error)
}

type ApplicationClient interface {
	ListApplications(ctx context.Context) ([]bleemeoTypes.Application, error)
	CreateApplication(ctx context.Context, app bleemeoTypes.Application) (bleemeoTypes.Application, error)
}

type AccountConfigClient interface {
	ListAgentTypes(ctx context.Context) ([]bleemeoTypes.AgentType, error)
	ListAccountConfigs(ctx context.Context) ([]bleemeoTypes.AccountConfig, error)
	ListAgentConfigs(ctx context.Context) ([]bleemeoTypes.AgentConfig, error)
}

type AgentClient interface {
	ListAgents(ctx context.Context) ([]bleemeoTypes.Agent, error)
	UpdateAgent(ctx context.Context, id string, data any) (bleemeoTypes.Agent, error)
	UpdateAgentLastDuplicationDate(ctx context.Context, agentID string, lastDuplicationDate time.Time) error
}

type GloutonConfigItemClient interface {
	ListGloutonConfigItems(ctx context.Context, agentID string) ([]bleemeoTypes.GloutonConfigItem, error)
	RegisterGloutonConfigItems(ctx context.Context, items []bleemeoTypes.GloutonConfigItem) error
	DeleteGloutonConfigItem(ctx context.Context, id string) error
}

type ContainerClient interface {
	ListContainers(ctx context.Context, agentID string) ([]bleemeoTypes.Container, error)
	UpdateContainer(ctx context.Context, id string, payload any) (bleemeoapi.ContainerPayload, error)
	RegisterContainer(ctx context.Context, payload bleemeoapi.ContainerPayload) (bleemeoapi.ContainerPayload, error)
}

type DiagnosticClient interface {
	ListDiagnostics(ctx context.Context) ([]bleemeoapi.RemoteDiagnostic, error)
	UploadDiagnostic(ctx context.Context, contentType string, content io.Reader) error
}

type FactClient interface {
	ListFacts(ctx context.Context) ([]bleemeoTypes.AgentFact, error)
	RegisterFact(ctx context.Context, payload bleemeoTypes.AgentFact) (bleemeoTypes.AgentFact, error)
	DeleteFact(ctx context.Context, id string) error
}

type MetricClient interface {
	UpdateMetric(ctx context.Context, id string, payload any, fields string) error
	ListActiveMetrics(ctx context.Context) ([]bleemeoapi.MetricPayload, error)
	ListInactiveMetrics(ctx context.Context, stopSearchingPredicate func(labelsText string) bool) ([]bleemeoapi.MetricPayload, error)
	CountInactiveMetrics(ctx context.Context) (int, error)
	ListMetricsBy(ctx context.Context, params url.Values) (map[string]bleemeoTypes.Metric, error)
	GetMetricByID(ctx context.Context, id string) (bleemeoapi.MetricPayload, error)
	RegisterMetric(ctx context.Context, payload bleemeoapi.MetricPayload) (bleemeoapi.MetricPayload, error)
	DeleteMetric(ctx context.Context, id string) error
	SetMetricActive(ctx context.Context, id string, active bool) error
}

type MonitorClient interface {
	ListMonitors(ctx context.Context) ([]bleemeoTypes.Monitor, error)
	GetMonitorByID(ctx context.Context, id string) (bleemeoTypes.Monitor, error)
}

type ServiceClient interface {
	ListServices(ctx context.Context, agentID string, fields string) ([]bleemeoTypes.Service, error)
	UpdateService(ctx context.Context, id string, payload bleemeoapi.ServicePayload, fields string) (bleemeoTypes.Service, error)
	RegisterService(ctx context.Context, payload bleemeoapi.ServicePayload) (bleemeoTypes.Service, error)
}

type SNMPClient interface {
	RegisterSNMPAgent(ctx context.Context, payload bleemeoapi.AgentPayload) (bleemeoTypes.Agent, error)
}

type VSphereClient interface {
	RegisterVSphereAgent(ctx context.Context, payload bleemeoapi.AgentPayload) (bleemeoTypes.Agent, error)
	DeleteAgent(ctx context.Context, id string) error
}

type SynchronizedGlobalState interface {
	IsMaintenance() bool
	DelayedContainers() (delayedByID map[string]time.Time, minDelayed time.Time)
	SuccessiveErrors() int
	UpdateDelayedContainers(localContainers []facts.Container)
	// Test if API had or not some feature. The default until a call to SetAPIHasFeature() is to assume
	// feature is NOT available.
	APIHasFeature(feature APIFeature) bool
	SetAPIHasFeature(feature APIFeature, has bool)
}

// Option are parameters for the synchronizer.
type Option struct {
	bleemeoTypes.GlobalOption

	Cache        *cache.Cache
	PushAppender *model.BufferAppender

	// DisableCallback is a function called when Synchronizer request Bleemeo connector to be disabled
	// reason state why it's disabled and until set for how long it should be disabled.
	DisableCallback func(reason bleemeoTypes.DisableReason, until time.Time)

	// UpdateConfigCallback is a function called when Synchronizer detected a AccountConfiguration change
	UpdateConfigCallback func(nameChanged bool)

	// ProvideClient may be used to provide an alternative Bleemeo API client.
	ProvideClient func() Client

	// SetInitialized tells the bleemeo connector that the MQTT module can be started
	SetInitialized func()

	// IsMqttConnected returns whether the MQTT connector is operating nominally, and specifically
	// that it can receive mqtt notifications. It is useful for the fallback on http polling
	// described above Synchronizer.lastMaintenanceSync definition.
	// Note: returns false when the mqtt connector is not enabled.
	IsMqttConnected func() bool

	// SetBleemeoInMaintenanceMode makes the bleemeo connector wait a day before checking again for maintenance.
	SetBleemeoInMaintenanceMode func(ctx context.Context, maintenance bool)

	// SetBleemeoInSuspendedMode sets the suspended mode. While Bleemeo is suspended the agent doesn't
	// create or update objects on the API and stops sending points on MQTT. The suspended mode differs
	// from the maintenance mode because we stop buffering points to send on MQTT and just drop them.
	SetBleemeoInSuspendedMode func(suspended bool)
}

// AutomaticApplicationName return the application name and the tag name from a local Application.
func AutomaticApplicationName(localApp discovery.Application) (string, string) {
	var name string

	switch localApp.Type { //nolint:exhaustive
	case discovery.ApplicationDockerCompose:
		name = "Docker compose " + localApp.Name
	default:
		name = localApp.Name
	}

	return name, taggify(name)
}

var nonAlphanumericRegex = regexp.MustCompile(`[^a-zA-Z0-9-]+`)

func taggify(name string) string {
	tag := strings.ToLower(name)

	return nonAlphanumericRegex.ReplaceAllString(tag, "-")
}
