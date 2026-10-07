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

package synchronizer

import (
	"testing"
	"time"

	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/discovery"
)

// TestSyncSendsRegistrationStatus checks the registration status entity is part of the
// synchronization: not sent before a synchronization published the metrics state, then sent
// once, then not again while unchanged.
func TestSyncSendsRegistrationStatus(t *testing.T) {
	helper := newHelper(t)
	defer helper.Close()

	helper.preregisterAgent(t)
	helper.discovery.States = []discovery.ServiceState{{
		Name:           "apache",
		ServiceType:    discovery.ApacheService,
		Dynamic:        true,
		InputSupported: true,
	}}

	helper.initSynchronizer(t)

	// The metrics state is published at the end of this run, after the entity decided.
	if err := helper.runOnce(t); err != nil {
		t.Fatal(err)
	}

	if sent := helper.s.option.Cache.RegistrationStatusSent(); !sent.LastAttemptAt.IsZero() {
		t.Errorf("sent before the metrics state was published: %+v", sent)
	}

	helper.AddTime(time.Minute)

	if err := helper.runOnce(t); err != nil {
		t.Fatal(err)
	}

	// The mock API does not send glouton_features, as an API without the field: the synchronizer
	// sets the default ones on the cached Agent.
	if !helper.s.option.Cache.Agent().GloutonFeatures.Enabled(bleemeoTypes.FeatureSendRegistrationStatus) {
		t.Error("the cached Agent does not allow sending the registration status")
	}

	sent := helper.s.option.Cache.RegistrationStatusSent()
	if sent.AgentID != testAgent.ID || sent.Hash == "" || sent.LastAttemptFailed {
		t.Errorf("recorded %+v, want a value accepted for agent %s", sent, testAgent.ID)
	}

	// Past minSendInterval, and past the next full synchronization, which requests every entity:
	// a request alone must not send.
	helper.AddTime(2 * time.Hour)

	if err := helper.runOnce(t); err != nil {
		t.Fatal(err)
	}

	if again := helper.s.option.Cache.RegistrationStatusSent(); !again.LastAttemptAt.Equal(sent.LastAttemptAt) {
		t.Errorf("an unchanged registration status was sent again, at %v", again.LastAttemptAt)
	}
}

// TestSyncObeysAPIFeatures checks an API that has glouton_features decides alone: when it does
// not allow the feature, nothing is sent, even an empty object overriding the default ones.
func TestSyncObeysAPIFeatures(t *testing.T) {
	cases := []struct {
		name     string
		features bleemeoTypes.GloutonFeatures
	}{
		{name: "empty", features: bleemeoTypes.GloutonFeatures{}},
		{name: "disabled", features: bleemeoTypes.GloutonFeatures{bleemeoTypes.FeatureSendRegistrationStatus: false}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			helper := newHelper(t)
			defer helper.Close()

			helper.preregisterAgent(t)
			helper.wrapperClientMock.resources.agents.elems[0].GloutonFeatures = tc.features
			helper.discovery.States = []discovery.ServiceState{{Name: "apache", ServiceType: discovery.ApacheService, InputSupported: true}}

			helper.initSynchronizer(t)

			for range 2 {
				if err := helper.runOnce(t); err != nil {
					t.Fatal(err)
				}

				helper.AddTime(time.Minute)
			}

			if cached := helper.s.option.Cache.Agent().GloutonFeatures; cached.Enabled(bleemeoTypes.FeatureSendRegistrationStatus) {
				t.Errorf("the cached Agent allows the feature: %v", cached)
			}

			if sent := helper.s.option.Cache.RegistrationStatusSent(); !sent.LastAttemptAt.IsZero() {
				t.Errorf("the registration status was sent: %+v", sent)
			}
		})
	}
}
