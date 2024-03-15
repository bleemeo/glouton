// Copyright 2015-2023 Bleemeo
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
	"encoding/json"
	"glouton/bleemeo/internal/cache"
	bleemeoTypes "glouton/bleemeo/types"
	"glouton/prometheus/model"
	"io"
	"time"
)

type EntityName string

const (
	EntityInfo          EntityName = "info"
	EntityAgent         EntityName = "agent"
	EntityAccountConfig EntityName = "accountconfig"
	EntityMonitor       EntityName = "monitor"
	EntitySNMP          EntityName = "snmp"
	EntityVSphere       EntityName = "vsphere"
	EntityFact          EntityName = "facts"
	EntityService       EntityName = "service"
	EntityContainer     EntityName = "container"
	EntityMetric        EntityName = "metric"
	EntityConfig        EntityName = "config"
	EntityDiagnostics   EntityName = "diagnostics"
)

type SyncType int

const (
	SyncTypeNone              SyncType = 0
	SyncTypeNormal            SyncType = 1
	SyncTypeForceCacheRefresh SyncType = 2
)

type Client interface {
	RawClient
}

// RawClient a client doing generic HTTP call to Bleemeo API.
// Ideally synchronizer of entity should move to higher level interface, which will make mocking easier (like ListActiveMetrics, CreateService...)
type RawClient interface {
	Do(ctx context.Context, method string, path string, params map[string]string, data interface{}, result interface{}) (statusCode int, err error)
	DoWithBody(ctx context.Context, path string, contentType string, body io.Reader) (statusCode int, err error)
	Iter(ctx context.Context, resource string, params map[string]string) ([]json.RawMessage, error)
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
	UpdateConfigCallback func(ctx context.Context, nameChanged bool)

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
