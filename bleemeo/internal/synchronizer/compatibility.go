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

package synchronizer

import (
	"sync"
	"time"
)

// Those state should be moved to each entity-synchronizer as needed.
type synchronizerState struct {
	l sync.Mutex

	currentConfigNotified string
	lastFactUpdatedAt     string
	lastSNMPcount         int
	lastVSphereUpdate     time.Time
	metricRetryAt         time.Time
	lastMetricCount       int
	delayedContainer      map[string]time.Time
	lastMetricActivation  time.Time

	onDemandDiagnostic synchronizerOnDemandDiagnostic

	// An edge case occurs when an agent is spawned while the maintenance mode is enabled on the backend:
	// the agent cannot register agent_status, thus the MQTT connector cannot start, and we cannot receive
	// notifications to tell us the backend is out of maintenance. So we resort to HTTP polling every 15
	// minutes to check whether we are still in maintenance of not.
	lastMaintenanceSync time.Time

	// configSyncDone is true when the config items were successfully synced.
	configSyncDone bool
}
