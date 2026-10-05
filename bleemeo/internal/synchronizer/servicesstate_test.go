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

	"github.com/bleemeo/glouton/discovery"
)

// TestSyncSendsServicesState checks the services state entity is part of the synchronization:
// not sent before a synchronization published the metrics state, then sent once, then not
// again while unchanged.
func TestSyncSendsServicesState(t *testing.T) {
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

	if sent := helper.s.option.Cache.ServicesStateSent(); !sent.LastAttemptAt.IsZero() {
		t.Errorf("sent before the metrics state was published: %+v", sent)
	}

	helper.AddTime(time.Minute)

	if err := helper.runOnce(t); err != nil {
		t.Fatal(err)
	}

	sent := helper.s.option.Cache.ServicesStateSent()
	if sent.AgentID != testAgent.ID || sent.Hash == "" || sent.LastAttemptFailed {
		t.Errorf("recorded %+v, want a value accepted for agent %s", sent, testAgent.ID)
	}

	// Past minSendInterval, and past the next full synchronization, which requests every entity:
	// a request alone must not send.
	helper.AddTime(2 * time.Hour)

	if err := helper.runOnce(t); err != nil {
		t.Fatal(err)
	}

	if again := helper.s.option.Cache.ServicesStateSent(); !again.LastAttemptAt.Equal(sent.LastAttemptAt) {
		t.Errorf("an unchanged services state was sent again, at %v", again.LastAttemptAt)
	}
}
