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

// Package syncregistrationstatus builds the registration status Glouton reports on its Agent
// object, so that Bleemeo can tell a user why a service is or is not monitored.
package syncregistrationstatus

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
	// Metrics is roughly how many metric names of the service are meant to reach Bleemeo:
	// received lately, and dropped by neither Glouton's metric filter nor Bleemeo's. The
	// service check's status does not count: it is no metric of the service. A word rather
	// than a number, so that it is not taken for an exact count, kept coarse so that the value
	// stays stable.
	Metrics MetricsAmount `json:"metrics"`
}

// MetricsAmount is roughly how many metrics a service has.
type MetricsAmount string

const (
	MetricsZero MetricsAmount = "zero"
	MetricsOne  MetricsAmount = "one"
	MetricsSome MetricsAmount = "some"
)

// metricsAmount is the MetricsAmount of count metric names.
func metricsAmount(count int) MetricsAmount {
	switch {
	case count <= 0:
		return MetricsZero
	case count == 1:
		return MetricsOne
	default:
		return MetricsSome
	}
}

// msgNotRegistered is a service's error while the registry skips its input because the service
// has no Service object: its metrics are labelled with the service's UUID, so they are not
// gathered without one (see Connector.RelabelHook). It states the fact rather than a wait: the
// registration may be pending, failing, or impossible, such as for an instance too long.
const msgNotRegistered = "metrics are not gathered: the service is not registered on Bleemeo"

// FromDiscovery adds to each service the ID of its Service object, matched as the services
// synchronization does (name and instance, in the cache's ServiceLookupFromList), and its
// metrics, from metricNames. A skipped collection gets msgNotRegistered when the service has
// no Service object, and is pending otherwise: the registry retries the skip within minutes.
// The order of states is kept.
func FromDiscovery(
	states []discovery.ServiceState,
	registered map[common.ServiceNameInstance]bleemeoTypes.Service,
	metricNames map[discovery.NameInstance]int,
) []ServiceState {
	result := make([]ServiceState, 0, len(states))

	for _, state := range states {
		key := common.ServiceNameInstance{Name: state.Name, Instance: state.Instance}
		serviceUUID := registered[key].ID

		if state.CollectionSkipped {
			if serviceUUID == "" {
				state.Error = msgNotRegistered
			} else {
				state.CollectionPending = true
			}
		}

		result = append(result, ServiceState{
			ServiceState: state,
			ServiceUUID:  serviceUUID,
			Metrics:      metricsAmount(metricNames[discovery.NameInstance{Name: state.Name, Instance: state.Instance}]),
		})
	}

	return result
}

// ServiceMetricNames counts, per service, the names of the metrics of agentID that are meant to
// reach Bleemeo, the service check's status left out. metrics are those the synchronizer reads,
// already through Glouton's metric filter; isAllowed is Bleemeo's.
func ServiceMetricNames(
	metrics []gloutonTypes.Metric,
	agentID string,
	isAllowed func(gloutonTypes.Metric) bool,
	now time.Time,
) map[discovery.NameInstance]int {
	names := make(map[discovery.NameInstance]map[string]bool)

	for _, metric := range metrics {
		annotations := metric.Annotations()

		name := metric.Labels()[gloutonTypes.LabelName]

		// The status of the service check is no metric of the service: a service with no
		// input at all has one.
		if annotations.ServiceName == "" || name == gloutonTypes.MetricServiceStatus {
			continue
		}

		if !common.IsOwnMetric(annotations, agentID) {
			continue
		}

		if now.Sub(metric.LastPointReceivedAt()) > metricsFreshness || !isAllowed(metric) {
			continue
		}

		key := discovery.NameInstance{Name: annotations.ServiceName, Instance: annotations.ServiceInstance}
		if names[key] == nil {
			names[key] = make(map[string]bool)
		}

		names[key][name] = true
	}

	counts := make(map[discovery.NameInstance]int, len(names))

	for key, set := range names {
		counts[key] = len(set)
	}

	return counts
}
