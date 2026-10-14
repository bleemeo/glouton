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
	"slices"
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
	helper.SetAPIConfigs(withSendRegistrationStatus(true))
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

	if cfg, _ := helper.s.option.Cache.CurrentAccountConfig(); !cfg.SendRegistrationStatus {
		t.Error("the cached account configuration does not allow sending the registration status")
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

// TestSyncObeysAPIFeatures checks nothing is sent when the account's
// GLOUTON_SEND_REGISTRATION_STATUS config does not allow it: off, or absent, as on every account
// until the API creates it.
func TestSyncObeysAPIFeatures(t *testing.T) {
	cases := []struct {
		name    string
		configs []bleemeoTypes.Config
	}{
		{name: "absent", configs: defaultAccountConfigs},
		{name: "disabled", configs: withSendRegistrationStatus(false)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			helper := newHelper(t)
			defer helper.Close()

			helper.preregisterAgent(t)
			helper.SetAPIConfigs(tc.configs)
			helper.discovery.States = []discovery.ServiceState{{Name: "apache", ServiceType: discovery.ApacheService, InputSupported: true}}

			helper.initSynchronizer(t)

			for range 2 {
				if err := helper.runOnce(t); err != nil {
					t.Fatal(err)
				}

				helper.AddTime(time.Minute)
			}

			if cfg, _ := helper.s.option.Cache.CurrentAccountConfig(); cfg.SendRegistrationStatus {
				t.Error("the cached account configuration allows the feature")
			}

			if sent := helper.s.option.Cache.RegistrationStatusSent(); !sent.LastAttemptAt.IsZero() {
				t.Errorf("the registration status was sent: %+v", sent)
			}
		})
	}
}

// withSendRegistrationStatus is the default account configs with GLOUTON_SEND_REGISTRATION_STATUS
// set to value.
func withSendRegistrationStatus(value bool) []bleemeoTypes.Config {
	return append(slices.Clone(defaultAccountConfigs), bleemeoTypes.Config{
		ID:      "cfg-send-registration-status",
		Type:    bleemeoTypes.ConfigTypeGloutonSendRegistrationStatus,
		Account: accountID,
		Value:   value,
	})
}
