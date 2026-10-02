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
)

const (
	// minSendInterval is how often, at most, a changed services state is sent, and how
	// long to wait after a failed attempt before trying again.
	//
	// Both lists move often -- a metric count changes every time one metric appears or
	// disappears -- and each change would otherwise be an API request. After a failure,
	// waiting as long keeps a value the API keeps refusing from being re-sent on every
	// synchronization.
	minSendInterval = 30 * time.Minute
	// minNewServiceInterval is how often, at most, a newly settled service sends the state
	// sooner than minSendInterval. Without it, a host starting short-lived containers would
	// send on every container start: a containerised service is identified by its container
	// name, so each new container is a new service.
	minNewServiceInterval = time.Minute
)

// Payload is the services state as sent to Bleemeo: both lists, limited in size.
type Payload struct {
	Services []ServiceState
	Metrics  []bleemeoTypes.MetricState
	// ServicesDropped and MetricsDropped are how many rows the limits left out.
	ServicesDropped int
	MetricsDropped  int

	hash string
}

// NewPayload builds the payload of the given lists, which must come sorted, as
// Discovery.ServicesState and Synchronizer.MetricsState return them.
func NewPayload(services []ServiceState, metrics []bleemeoTypes.MetricState) Payload {
	keptServices, servicesRows, servicesDropped := limitServices(services)
	keptMetrics, metricsRows, metricsDropped := limitMetrics(metrics)

	return Payload{
		Services:        keptServices,
		Metrics:         keptMetrics,
		ServicesDropped: servicesDropped,
		MetricsDropped:  metricsDropped,
		hash:            hashRows(servicesRows, metricsRows),
	}
}

// hashRows identifies a payload from the encoding of its rows, which limit already did:
// encoding the whole payload again on every synchronization would double that work. It
// depends on the rows only, so a nil list and an empty one are the same value.
func hashRows(services [][]byte, metrics [][]byte) string {
	h := sha256.New()

	for _, list := range [][][]byte{services, metrics} {
		for _, row := range list {
			// Rows are JSON objects, so a newline can only separate them, never be part
			// of one: two different lists cannot run into the same bytes.
			_, _ = h.Write(row)
			_, _ = h.Write([]byte{'\n'})
		}

		// Ends a list, so that a row moving from one list to the other changes the hash.
		_, _ = h.Write([]byte{0})
	}

	return hex.EncodeToString(h.Sum(nil))
}

// NeedSend tells whether payload must be sent, given what was last sent:
//   - never when it is what the API last accepted;
//   - right away when nothing was attempted yet;
//   - after a failed attempt, once minSendInterval has passed since it;
//   - when it lists a settled service the last accepted value did not, once
//     minNewServiceInterval has passed since the last attempt;
//   - otherwise, once minSendInterval has passed since the last attempt.
//
// A clock that went back before the last attempt counts as the interval having passed,
// rather than holding every change until the clock catches up.
func NeedSend(last bleemeoTypes.ServicesStateSent, payload Payload, now time.Time) bool {
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

// Sent is what to record once the API accepted payload, at now.
func Sent(payload Payload, now time.Time) bleemeoTypes.ServicesStateSent {
	services := make([]discovery.NameInstance, 0, len(payload.Services))

	for _, service := range payload.Services {
		if settled(service) {
			services = append(services, nameInstance(service))
		}
	}

	return bleemeoTypes.ServicesStateSent{Hash: payload.hash, Services: services, LastAttemptAt: now}
}

// SendFailed is what to record when sending failed or was refused, at now. What the API
// last accepted is kept: it still is what the API has.
func SendFailed(last bleemeoTypes.ServicesStateSent, now time.Time) bleemeoTypes.ServicesStateSent {
	last.LastAttemptAt = now
	last.LastAttemptFailed = true

	return last
}

// settled tells whether a service's state is worth sending sooner than minSendInterval:
// Bleemeo can link it to its Service object, and Glouton knows how its monitoring went --
// it has an error to report, produces metrics, or has no input to wait for.
//
// A service that just appeared is neither: sending it at once would tell Bleemeo nothing,
// and the state that matters, a minute later, would then wait the whole interval.
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
