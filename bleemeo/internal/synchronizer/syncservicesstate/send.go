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

package syncservicesstate

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/discovery"
	"github.com/bleemeo/glouton/logger"
)

const (
	// minSendInterval is how often, at most, a changed state is sent, and how long to wait after
	// a failed attempt: counts change all the time, and a refused value must not be re-sent on
	// every synchronization.
	minSendInterval = 30 * time.Minute
	// minNewServiceInterval spaces the sends a newly settled service triggers: each new
	// container is a new service, so a host starting short-lived ones would send on each start.
	minNewServiceInterval = time.Minute
)

// Payload is the services state as sent to Bleemeo: both lists, limited in size. It is the
// value of the Agent's services_state field.
type Payload struct {
	Services []ServiceState             `json:"services"`
	Metrics  []bleemeoTypes.MetricState `json:"metrics"`

	// servicesDropped and metricsDropped are how many rows the limits left out. They are not
	// sent, only logged by Sent.
	servicesDropped int
	metricsDropped  int
	hash            string
}

// NewPayload builds the payload of the given lists, which must come sorted, as
// Discovery.ServicesState and Synchronizer.MetricsState return them.
func NewPayload(services []ServiceState, metrics []bleemeoTypes.MetricState) Payload {
	keptServices, servicesRows, servicesDropped := limitServices(services)
	keptMetrics, metricsRows, metricsDropped := limitMetrics(metrics)

	return Payload{
		Services:        keptServices,
		Metrics:         keptMetrics,
		servicesDropped: servicesDropped,
		metricsDropped:  metricsDropped,
		hash:            hashRows(servicesRows, metricsRows),
	}
}

// hashRows identifies a payload from the rows limit already encoded. The rows left out are not
// part of it: they are not sent, so a value that differs only by them is the same value to the
// API. A nil list and an empty one hash the same.
func hashRows(services [][]byte, metrics [][]byte) string {
	h := sha256.New()

	for _, list := range [][][]byte{services, metrics} {
		for _, row := range list {
			// A newline cannot be part of a JSON row, so it separates rows unambiguously.
			_, _ = h.Write(row)
			_, _ = h.Write([]byte{'\n'})
		}

		// Ends a list, so that a row moving to the other list changes the hash.
		_, _ = h.Write([]byte{0})
	}

	return hex.EncodeToString(h.Sum(nil))
}

// NeedSend tells whether payload must be sent, given what was last sent to agentID:
//   - never when it is what the API last accepted;
//   - right away when nothing was attempted yet, or the clock went back before the last attempt;
//   - minSendInterval after a failed attempt;
//   - minNewServiceInterval after the last attempt when it lists a newly settled service;
//   - minSendInterval after the last attempt otherwise.
func NeedSend(last bleemeoTypes.ServicesStateSent, payload Payload, agentID string, now time.Time) bool {
	last = forAgent(last, agentID)

	switch {
	case payload.hash == last.Hash:
		return false
	case last.LastAttemptAt.IsZero():
		return true
	case now.Before(last.LastAttemptAt):
		return true
	}

	since := now.Sub(last.LastAttemptAt)

	switch {
	case last.LastAttemptFailed:
		return since >= minSendInterval
	case hasNewSettledService(last.Services, payload.Services):
		return since >= minNewServiceInterval
	default:
		return since >= minSendInterval
	}
}

// MaySend tells whether NeedSend could be true for any payload at now, so that the caller can
// skip building one.
func MaySend(last bleemeoTypes.ServicesStateSent, agentID string, now time.Time) bool {
	last = forAgent(last, agentID)

	switch {
	case last.LastAttemptAt.IsZero(), now.Before(last.LastAttemptAt):
		return true
	case last.LastAttemptFailed:
		return now.Sub(last.LastAttemptAt) >= minSendInterval
	default:
		return now.Sub(last.LastAttemptAt) >= minNewServiceInterval
	}
}

// forAgent returns what was sent to agentID: nothing when last was sent to another agent.
func forAgent(last bleemeoTypes.ServicesStateSent, agentID string) bleemeoTypes.ServicesStateSent {
	if last.AgentID != agentID {
		return bleemeoTypes.ServicesStateSent{AgentID: agentID}
	}

	return last
}

// Sent is what to record once the API accepted payload for agentID, at now.
func Sent(payload Payload, agentID string, now time.Time) bleemeoTypes.ServicesStateSent {
	// Logged here rather than in NewPayload, which runs on every synchronization.
	if payload.servicesDropped > 0 || payload.metricsDropped > 0 {
		logger.V(1).Printf(
			"The services state sent to Bleemeo left out %d services and %d metrics: the lists exceed their size limits",
			payload.servicesDropped, payload.metricsDropped,
		)
	}

	services := make([]discovery.NameInstance, 0, len(payload.Services))

	for _, service := range payload.Services {
		if settled(service) {
			services = append(services, nameInstance(service))
		}
	}

	return bleemeoTypes.ServicesStateSent{AgentID: agentID, Hash: payload.hash, Services: services, LastAttemptAt: now}
}

// SendFailed is what to record when sending to agentID failed or was refused, at now. What
// the API last accepted is kept: it still is what the API has.
func SendFailed(last bleemeoTypes.ServicesStateSent, agentID string, now time.Time) bleemeoTypes.ServicesStateSent {
	last = forAgent(last, agentID)
	last.LastAttemptAt = now
	last.LastAttemptFailed = true

	return last
}

// settled tells whether a service's state is worth sending sooner than minSendInterval: it
// has a service UUID, and an error, metrics, or no input to wait for. A service that just
// appeared has neither, and its state a minute later is the one that matters.
func settled(service ServiceState) bool {
	if service.ServiceUUID == "" {
		return false
	}

	return service.Error != "" || service.MetricFamiliesBucket > 0 || !service.InputSupported
}

// hasNewSettledService tells whether services holds a settled one that previous, the
// settled services of the last accepted value, did not.
func hasNewSettledService(previous []discovery.NameInstance, services []ServiceState) bool {
	known := make(map[discovery.NameInstance]bool, len(previous))

	for _, service := range previous {
		known[service] = true
	}

	for _, service := range services {
		if settled(service) && !known[nameInstance(service)] {
			return true
		}
	}

	return false
}

func nameInstance(service ServiceState) discovery.NameInstance {
	return discovery.NameInstance{Name: service.Name, Instance: service.Instance}
}
