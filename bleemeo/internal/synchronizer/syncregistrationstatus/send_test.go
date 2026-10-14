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
	"fmt"
	"testing"
	"time"

	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/discovery"
	"github.com/google/go-cmp/cmp"
)

var t0 = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC) //nolint:gochecknoglobals

const testAgent = "agent-uuid"

// settledService is a registered service whose input produces metrics.
func settledService(i int) ServiceState {
	state := service(i, "")
	state.ServiceUUID = fmt.Sprintf("uuid-%d", i)
	state.InputSupported = true
	state.Metrics = MetricsSome

	return state
}

// freshService is a service just discovered: not registered, input not run yet.
func freshService(i int) ServiceState {
	state := service(i, "")
	state.InputSupported = true
	state.Metrics = MetricsZero

	return state
}

func payloadOf(services ...ServiceState) Payload {
	return NewPayload(services, []bleemeoTypes.MetricState{
		{Name: "cpu_used", Reason: bleemeoTypes.ReasonPending, Count: 3},
	}, true)
}

// TestNeedSend covers when the registration status is sent.
func TestNeedSend(t *testing.T) {
	base := payloadOf(settledService(1), settledService(2))
	sentBase := Sent(base, testAgent, t0)

	changedCount := NewPayload(base.Services, []bleemeoTypes.MetricState{
		{Name: "cpu_used", Reason: bleemeoTypes.ReasonPending, Count: 4},
	}, true)
	newSettled := payloadOf(settledService(1), settledService(2), settledService(3))
	newFresh := payloadOf(settledService(1), settledService(2), freshService(3))
	removed := payloadOf(settledService(1))

	cases := []struct {
		name    string
		last    bleemeoTypes.RegistrationStatusSent
		payload Payload
		now     time.Time
		want    bool
	}{
		{"nothing attempted yet", bleemeoTypes.RegistrationStatusSent{}, base, t0, true},
		{"unchanged, soon after", sentBase, base, t0.Add(time.Minute), false},
		// Unchanged is sent again only once a day, so that generated_at tells it is current.
		{"unchanged, just before a day", sentBase, base, t0.Add(unchangedSendInterval - time.Second), false},
		{"unchanged, a day after", sentBase, base, t0.Add(unchangedSendInterval), true},
		{"unchanged, clock went back", sentBase, base, t0.Add(-time.Hour), true},
		{"changed count, soon after", sentBase, changedCount, t0.Add(10 * time.Minute), false},
		{"changed count, once the interval passed", sentBase, changedCount, t0.Add(minSendInterval), true},
		{"new settled service, a minute after", sentBase, newSettled, t0.Add(minNewServiceInterval), true},
		// The shortcut is spaced too, so a host starting containers in a loop is bounded.
		{"new settled service, right after", sentBase, newSettled, t0.Add(time.Second), false},
		// A service that just appeared tells nothing yet: it waits until it settles.
		{"new fresh service", sentBase, newFresh, t0.Add(10 * time.Minute), false},
		// A service going away is a change, not a new service.
		{"removed service", sentBase, removed, t0.Add(10 * time.Minute), false},
		{"clock went back", sentBase, changedCount, t0.Add(-time.Hour), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NeedSend(tc.last, tc.payload, testAgent, tc.now); got != tc.want {
				t.Errorf("NeedSend() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestNeedSendAfterFailure checks a failed attempt waits the whole interval before the next
// one, new services included: a value the API keeps refusing must not be re-sent on every
// synchronization.
func TestNeedSendAfterFailure(t *testing.T) {
	payload := payloadOf(settledService(1), settledService(2))

	// Never accepted: the very first attempt failed.
	failed := SendFailed(bleemeoTypes.RegistrationStatusSent{}, testAgent, t0)

	if NeedSend(failed, payload, testAgent, t0.Add(minNewServiceInterval)) {
		t.Error("retried a minute after a failure, with settled services the API never accepted")
	}

	if !NeedSend(failed, payload, testAgent, t0.Add(minSendInterval)) {
		t.Error("not retried once the interval passed")
	}
}

// TestSendFailedKeepsAccepted checks a failure does not forget what the API accepted
// before: that still is what it has, so an unchanged state must not be sent.
func TestSendFailedKeepsAccepted(t *testing.T) {
	payload := payloadOf(settledService(1))
	accepted := Sent(payload, testAgent, t0)

	failed := SendFailed(accepted, testAgent, t0.Add(time.Hour))

	if failed.Hash != accepted.Hash {
		t.Errorf("SendFailed() changed the accepted hash")
	}

	if NeedSend(failed, payload, testAgent, t0.Add(2*time.Hour)) {
		t.Error("the value the API accepted is sent again after an unrelated failure")
	}
}

// TestSentThenSettledSends checks a service sent before it settled is sent again soon once
// it does: the state that matters is its first result, not its first sighting.
func TestSentThenSettledSends(t *testing.T) {
	sent := Sent(payloadOf(settledService(1), freshService(2)), testAgent, t0)

	if !NeedSend(sent, payloadOf(settledService(1), settledService(2)), testAgent, t0.Add(minNewServiceInterval)) {
		t.Error("a service that settled after it was first sent waits the whole interval")
	}
}

// TestSentRecordsSettledServices checks only settled services are recorded, which is what
// lets a later settling count as new.
func TestSentRecordsSettledServices(t *testing.T) {
	sent := Sent(payloadOf(settledService(1), freshService(2)), testAgent, t0)

	want := []discovery.NameInstance{{Name: "svc00001"}}
	if len(sent.Services) != 1 || sent.Services[0] != want[0] {
		t.Errorf("Sent() recorded services %+v, want %+v", sent.Services, want)
	}

	if sent.Hash == "" || !sent.LastAttemptAt.Equal(t0) || sent.LastAttemptFailed {
		t.Errorf("Sent() recorded %+v, want a hash, the attempt time and no failure", sent)
	}
}

// TestSettled covers which services can send the state sooner.
func TestSettled(t *testing.T) {
	missingInfo := freshService(1)
	missingInfo.ServiceUUID = "uuid"
	missingInfo.Error = "failed to create the input: missing credentials or address"

	unsupported := service(1, "")
	unsupported.ServiceUUID = "uuid"

	pending := freshService(1)
	pending.ServiceUUID = "uuid"

	unregistered := settledService(1)
	unregistered.ServiceUUID = ""

	cases := []struct {
		name    string
		service ServiceState
		want    bool
	}{
		{"produces metrics", settledService(1), true},
		{"error to report", missingInfo, true},
		{"no input to wait for", unsupported, true},
		{"input not run yet", pending, false},
		{"not registered", unregistered, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := settled(tc.service); got != tc.want {
				t.Errorf("settled() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestHashSameValue checks the hash depends on content only: a nil list and an empty one
// are the same value, and two payloads built apart from the same state are equal.
func TestHashSameValue(t *testing.T) {
	if NewPayload(nil, nil, true).hash != NewPayload([]ServiceState{}, []bleemeoTypes.MetricState{}, true).hash {
		t.Error("a nil list and an empty one hash differently")
	}

	first, second := payloadOf(settledService(1)), payloadOf(settledService(1))
	if first.hash != second.hash {
		t.Error("the same state built twice hashes differently")
	}

	if payloadOf(settledService(1)).hash == payloadOf(settledService(2)).hash {
		t.Error("two different states hash the same")
	}
}

// TestHashSeparatesLists checks moving content from one list to the other changes the
// hash, so the boundary between the lists is part of it.
func TestHashSeparatesLists(t *testing.T) {
	row := []byte(`{"x":1}`)

	if hashRows("1.0", [][]byte{row}, nil, 1, 1, true) == hashRows("1.0", nil, [][]byte{row}, 1, 1, true) {
		t.Error("a row in the services list hashes like the same row in the metrics list")
	}
}

// TestHashCountsVersion checks the same lists from another Glouton version are another value:
// an upgraded Glouton sends again, in the format it knows.
func TestHashCountsVersion(t *testing.T) {
	row := []byte(`{"x":1}`)

	if hashRows("1.0", [][]byte{row}, nil, 1, 0, true) == hashRows("1.1", [][]byte{row}, nil, 1, 0, true) {
		t.Error("two versions hash the same")
	}
}

// TestHashCountsTotals checks a list cut by one more row is another value, although the rows
// kept are the same: the totals sent with them differ.
func TestHashCountsTotals(t *testing.T) {
	services := make([]ServiceState, 0, maxServices+2)
	for i := range maxServices + 2 {
		services = append(services, settledService(i))
	}

	oneDropped := NewPayload(services[:maxServices+1], nil, true)
	twoDropped := NewPayload(services, nil, true)

	if oneDropped.servicesDropped != 1 || twoDropped.servicesDropped != 2 {
		t.Fatalf("dropped %d and %d, want 1 and 2", oneDropped.servicesDropped, twoDropped.servicesDropped)
	}

	if diff := cmp.Diff(oneDropped.Services, twoDropped.Services); diff != "" {
		t.Fatalf("the rows kept differ (-one +two):\n%s", diff)
	}

	if oneDropped.ServicesTotal != maxServices+1 || twoDropped.ServicesTotal != maxServices+2 {
		t.Errorf("totals %d and %d, want %d and %d", oneDropped.ServicesTotal, twoDropped.ServicesTotal, maxServices+1, maxServices+2)
	}

	if oneDropped.hash == twoDropped.hash {
		t.Error("two values sending different totals hash the same")
	}

	metrics := []bleemeoTypes.MetricState{{Name: "cpu_used", Reason: bleemeoTypes.ReasonPending, Count: 1}}
	if NewPayload(nil, metrics, true).hash == NewPayload(nil, append(metrics, metrics...), true).hash {
		t.Error("two metrics totals hash the same")
	}
}

// TestNeedSendNewAgent checks what was sent to another agent counts as nothing sent: the
// cache survives registering the host again, and the new Agent object has nothing.
func TestNeedSendNewAgent(t *testing.T) {
	payload := payloadOf(settledService(1))
	sentToOld := Sent(payload, "old-agent", t0)

	if !NeedSend(sentToOld, payload, testAgent, t0.Add(time.Minute)) {
		t.Error("an unchanged state is not sent to a new agent")
	}

	// A failure for the new agent backs off, rather than looking like "never sent" forever.
	failed := SendFailed(sentToOld, testAgent, t0.Add(time.Minute))
	if failed.AgentID != testAgent || failed.Hash != "" {
		t.Errorf("SendFailed() kept what the old agent accepted: %+v", failed)
	}

	if NeedSend(failed, payload, testAgent, t0.Add(2*time.Minute)) {
		t.Error("retried right after a failure for the new agent")
	}
}

// TestMaySend checks MaySend is false only when NeedSend is false whatever the payload.
func TestMaySend(t *testing.T) {
	sent := Sent(payloadOf(settledService(1)), testAgent, t0)
	failed := SendFailed(sent, testAgent, t0)
	newSettled := payloadOf(settledService(1), settledService(2))

	cases := []struct {
		name string
		last bleemeoTypes.RegistrationStatusSent
		now  time.Time
		want bool
	}{
		{"nothing sent", bleemeoTypes.RegistrationStatusSent{}, t0, true},
		{"right after a send", sent, t0.Add(time.Second), false},
		{"a minute after a send", sent, t0.Add(minNewServiceInterval), true},
		{"a minute after a failure", failed, t0.Add(minNewServiceInterval), false},
		{"the interval after a failure", failed, t0.Add(minSendInterval), true},
		{"another agent", sent, t0.Add(time.Second), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			agentID := testAgent
			if tc.name == "another agent" {
				agentID = "new-agent"
			}

			got := MaySend(tc.last, agentID, tc.now)
			if got != tc.want {
				t.Errorf("MaySend() = %v, want %v", got, tc.want)
			}

			if !got && NeedSend(tc.last, newSettled, agentID, tc.now) {
				t.Error("MaySend() is false while NeedSend() would send")
			}
		})
	}
}
