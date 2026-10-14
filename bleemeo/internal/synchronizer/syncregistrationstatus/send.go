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

package syncregistrationstatus

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/discovery"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/version"
)

const (
	// minSendInterval is how often, at most, a changed state is sent, and how long to wait after
	// a failed attempt: counts change all the time, and a refused value must not be re-sent on
	// every synchronization.
	minSendInterval = 30 * time.Minute
	// minNewServiceInterval spaces the sends a newly settled service triggers: each new
	// container is a new service, so a host starting short-lived ones would send on each start.
	minNewServiceInterval = time.Minute
	// unchangedSendInterval is how often an unchanged value is sent again, so that its
	// generated_at tells Bleemeo the value is still current, and an old one that this Glouton
	// stopped sending.
	unchangedSendInterval = 24 * time.Hour
)

// Payload is the registration status as sent to Bleemeo: both lists, limited in size, and how
// many rows each had before the limits. It is the value of the Agent's registration_status field.
type Payload struct {
	// GloutonVersion is the version of the Glouton that built the value, so that Bleemeo knows
	// which format it reads, sure that both come from the same Glouton.
	GloutonVersion string `json:"glouton_version"`
	// GeneratedAt is when the value was built, and LastDiscoveryAt when discovery last
	// completed before it, the services listed being at least that recent. Neither is part of
	// the hash, or each build would differ: since an unchanged value is only sent again after
	// unchangedSendInterval, GeneratedAt is at most that old while Glouton keeps sending.
	GeneratedAt     time.Time `json:"generated_at"`
	LastDiscoveryAt time.Time `json:"last_discovery_at"`

	Services []ServiceState `json:"services"`
	// ServicesTotal is how many services there are, those the limits left out included, so that
	// Bleemeo sees how far beyond the limits a host goes.
	ServicesTotal int `json:"services_total"`
	// Metrics is null when the metrics state is not known yet: see NewPayload.
	Metrics []bleemeoTypes.MetricState `json:"metrics"`
	// MetricsTotal is how many rows Metrics had before the limits, null with Metrics.
	MetricsTotal *int `json:"metrics_total"`

	// servicesDropped and metricsDropped are how many rows the limits left out, logged by Sent.
	servicesDropped int
	metricsDropped  int
	hash            string
}

// NewPayload builds the payload of the given lists, which must come sorted, as
// Discovery.ServicesState and Synchronizer.metricsState return them. metricsKnown is false
// while no synchronization published the metrics state: Metrics is then null rather than an
// empty list, which would claim that every metric is registered.
func NewPayload(services []ServiceState, metrics []bleemeoTypes.MetricState, metricsKnown bool) Payload {
	keptServices, servicesRows, servicesDropped := limitServices(services)
	payload := Payload{
		GloutonVersion:  version.Version,
		Services:        keptServices,
		ServicesTotal:   len(services),
		servicesDropped: servicesDropped,
	}

	var metricsRows [][]byte

	if metricsKnown {
		payload.Metrics, metricsRows, payload.metricsDropped = limitMetrics(metrics)
		payload.MetricsTotal = new(len(metrics))
	}

	payload.hash = hashRows(payload.GloutonVersion, servicesRows, metricsRows, len(services), len(metrics), metricsKnown)

	return payload
}

// hashRows identifies a payload from the version, the rows limit already encoded and the totals
// sent with them: an upgraded Glouton sends its value again, in the format it knows. The rows
// left out are not part of it: they are not sent, only counted in the totals. A nil list and an
// empty one hash the same; unknown metrics do not.
func hashRows(
	gloutonVersion string,
	services [][]byte,
	metrics [][]byte,
	servicesTotal int,
	metricsTotal int,
	metricsKnown bool,
) string {
	h := sha256.New()

	// %q, so that the version cannot be confused with what follows.
	_, _ = fmt.Fprintf(h, "version %q\x00", gloutonVersion)

	if metricsKnown {
		_, _ = fmt.Fprintf(h, "totals %d %d\x00", servicesTotal, metricsTotal)
	} else {
		_, _ = fmt.Fprintf(h, "totals %d\x00metrics unknown\x00", servicesTotal)
	}

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
//   - unchangedSendInterval after the last attempt when it is what the API last accepted;
//   - right away when nothing was attempted yet, or the clock went back before the last attempt;
//   - minSendInterval after a failed attempt;
//   - minNewServiceInterval after the last attempt when it lists a newly settled service;
//   - minSendInterval after the last attempt otherwise.
func NeedSend(last bleemeoTypes.RegistrationStatusSent, payload Payload, agentID string, now time.Time) bool {
	last = forAgent(last, agentID)

	switch {
	case last.LastAttemptAt.IsZero():
		return true
	case now.Before(last.LastAttemptAt):
		return true
	}

	since := now.Sub(last.LastAttemptAt)

	switch {
	case payload.hash == last.Hash:
		// Whether the last attempt failed or not: it may have been for another value, the API
		// still having this one.
		return since >= unchangedSendInterval
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
func MaySend(last bleemeoTypes.RegistrationStatusSent, agentID string, now time.Time) bool {
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
func forAgent(last bleemeoTypes.RegistrationStatusSent, agentID string) bleemeoTypes.RegistrationStatusSent {
	if last.AgentID != agentID {
		return bleemeoTypes.RegistrationStatusSent{AgentID: agentID}
	}

	return last
}

// Sent is what to record once the API accepted payload for agentID, at now.
func Sent(payload Payload, agentID string, now time.Time) bleemeoTypes.RegistrationStatusSent {
	// Logged here rather than in NewPayload, which runs on every synchronization.
	if payload.servicesDropped > 0 || payload.metricsDropped > 0 {
		logger.V(1).Printf(
			"The registration status sent to Bleemeo left out %d services and %d metrics: the lists exceed their size limits",
			payload.servicesDropped, payload.metricsDropped,
		)
	}

	services := make([]discovery.NameInstance, 0, len(payload.Services))

	for _, service := range payload.Services {
		if settled(service) {
			services = append(services, nameInstance(service))
		}
	}

	return bleemeoTypes.RegistrationStatusSent{AgentID: agentID, Hash: payload.hash, Services: services, LastAttemptAt: now}
}

// SendFailed is what to record when sending to agentID failed or was refused, at now. What
// the API last accepted is kept: it still is what the API has.
func SendFailed(last bleemeoTypes.RegistrationStatusSent, agentID string, now time.Time) bleemeoTypes.RegistrationStatusSent {
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

	hasMetrics := service.Metrics == MetricsOne || service.Metrics == MetricsSome

	return service.Error != "" || hasMetrics || !service.InputSupported
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
