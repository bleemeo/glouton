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

// Package syncservicesstate builds the services state Glouton reports on its Agent object,
// so that Bleemeo can tell a user why a service is or is not monitored.
package syncservicesstate

import (
	"time"

	"github.com/bleemeo/glouton/bleemeo/internal/common"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/discovery"
	gloutonTypes "github.com/bleemeo/glouton/types"
)

// metricsFreshness is how recent a metric's last point must be for the metric to count: the
// store keeps a metric for a while after its last point. It covers the slowest collections,
// such as a 5-minute resolution or jmxtrans' flushes.
const metricsFreshness = 15 * time.Minute

// ServiceState is one service as reported to Bleemeo: what discovery knows about it, plus
// what the synchronizer knows: the Service object it is registered as, and its metrics.
type ServiceState struct {
	discovery.ServiceState

	// ServiceUUID is the ID of the Service object registered for this service, empty when there
	// is none: pending, failed, or impossible (an instance too long for the API). Such a service
	// is still reported.
	ServiceUUID string `json:"service_uuid"`
	// Metrics is how many metric names of the service are meant to reach Bleemeo: received
	// lately, and dropped by neither Glouton's metric filter nor Bleemeo's. It is 0, 1 or 2
	// meaning "two or more", kept coarse so that the value stays stable.
	Metrics int `json:"metrics"`
}

// FromDiscovery adds to each service the ID of its Service object, matched as the services
// synchronization does (name and instance, in the cache's ServiceLookupFromList), and its
// metrics, from metricNames. The order of states is kept.
func FromDiscovery(
	states []discovery.ServiceState,
	registered map[common.ServiceNameInstance]bleemeoTypes.Service,
	metricNames map[discovery.NameInstance]int,
) []ServiceState {
	result := make([]ServiceState, 0, len(states))

	for _, state := range states {
		key := common.ServiceNameInstance{Name: state.Name, Instance: state.Instance}

		result = append(result, ServiceState{
			ServiceState: state,
			ServiceUUID:  registered[key].ID,
			Metrics:      min(metricNames[discovery.NameInstance{Name: state.Name, Instance: state.Instance}], 2),
		})
	}

	return result
}

// ServiceMetricNames counts, per service, the names of the metrics of agentID that are meant to
// reach Bleemeo. metrics are those the synchronizer reads, already through Glouton's metric
// filter; isAllowed is Bleemeo's.
func ServiceMetricNames(
	metrics []gloutonTypes.Metric,
	agentID string,
	isAllowed func(gloutonTypes.Metric) bool,
	now time.Time,
) map[discovery.NameInstance]int {
	names := make(map[discovery.NameInstance]map[string]bool)

	for _, metric := range metrics {
		annotations := metric.Annotations()

		if annotations.ServiceName == "" {
			continue
		}

		if annotations.BleemeoAgentID != "" && annotations.BleemeoAgentID != agentID {
			continue
		}

		if now.Sub(metric.LastPointReceivedAt()) > metricsFreshness || !isAllowed(metric) {
			continue
		}

		key := discovery.NameInstance{Name: annotations.ServiceName, Instance: annotations.ServiceInstance}
		if names[key] == nil {
			names[key] = make(map[string]bool)
		}

		names[key][metric.Labels()[gloutonTypes.LabelName]] = true
	}

	counts := make(map[discovery.NameInstance]int, len(names))

	for key, set := range names {
		counts[key] = len(set)
	}

	return counts
}
