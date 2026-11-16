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

	"github.com/bleemeo/bleemeo-go"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
)

// TestRegistrationFailureBackoff checks that an agent failing to register waits longer and
// longer between its attempts: the registration happens before any entity, so no entity's
// retry delay holds it back.
func TestRegistrationFailureBackoff(t *testing.T) {
	tests := []struct {
		name  string
		setup func(helper *syncTestHelper)
	}{
		{
			name: "API error",
			setup: func(helper *syncTestHelper) {
				helper.wrapperClientMock.registerErr = &bleemeo.APIError{StatusCode: 500}
			},
		},
		{
			name: "fqdn not set",
			setup: func(helper *syncTestHelper) {
				helper.facts.SetFact("fqdn", "")
			},
		},
		{
			name: "registration key not set",
			setup: func(helper *syncTestHelper) {
				helper.cfg.Bleemeo.RegistrationKey = ""
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := newHelper(t)
			defer helper.Close()

			tt.setup(helper)
			helper.initSynchronizer(t)

			// 15 seconds growing by 55% up to 15 minutes: about a dozen attempts in the first
			// hour, where 15 seconds between attempts would make 240.
			if attempts := runRegistrationFailures(t, helper, time.Hour); attempts > 20 {
				t.Errorf("%d registration attempts in the first hour, want at most 20", attempts)
			}

			if _, reason := helper.s.getDisabledUntil(); reason != bleemeoTypes.DisableTooManyErrors {
				t.Errorf("connector disabled for %s, want %s", reason, bleemeoTypes.DisableTooManyErrors)
			}

			// Then 4 attempts an hour, until the registration has failed for 36 hours.
			if attempts := runRegistrationFailures(t, helper, 35*time.Hour); attempts < 35*4-5 || attempts > 35*4+5 {
				t.Errorf("%d registration attempts in the next 35 hours, want about %d", attempts, 35*4)
			}

			// Then 1 an hour.
			runRegistrationFailures(t, helper, time.Hour)

			if attempts := runRegistrationFailures(t, helper, 10*time.Hour); attempts < 9 || attempts > 12 {
				t.Errorf("%d registration attempts in 10 hours after 37 hours of failures, want about 10", attempts)
			}
		})
	}
}

// TestRegistrationFailureRecovery checks the agent registers once the API accepts it, and that
// the start of the registration failures is then forgotten.
func TestRegistrationFailureRecovery(t *testing.T) {
	helper := newHelper(t)
	defer helper.Close()

	helper.wrapperClientMock.registerErr = &bleemeo.APIError{StatusCode: 500}

	helper.initSynchronizer(t)
	runRegistrationFailures(t, helper, 40*time.Hour)

	if helper.s.firstRegistrationFailureAt.IsZero() {
		t.Fatal("first registration failure not recorded")
	}

	helper.wrapperClientMock.registerErr = nil

	until, _ := helper.s.getDisabledUntil()
	helper.AddTime(until.Sub(helper.Now()))

	execution, err := helper.s.runOnce(t.Context(), false)
	if err != nil {
		t.Fatalf("runOnce failed after the API accepted the registration: %v", err)
	}

	helper.s.handleExecutionResult(execution, err)

	if helper.s.agentID == "" {
		t.Fatal("agent not registered")
	}

	if !helper.s.firstRegistrationFailureAt.IsZero() {
		t.Errorf("first registration failure %s kept after the registration", helper.s.firstRegistrationFailureAt)
	}
}

// runRegistrationFailures runs the synchronization like Synchronizer.Run during d, each
// execution failing to register, and returns the number of executions.
func runRegistrationFailures(t *testing.T, helper *syncTestHelper, d time.Duration) int {
	t.Helper()

	start := helper.Now()
	attempts := 0
	registerCalls := helper.wrapperClientMock.registerCalls

	// Like Synchronizer.Run: wait for the connector to be enabled, and at least 15 seconds
	// between two executions.
	for helper.Now().Sub(start) < d {
		if until, _ := helper.s.getDisabledUntil(); until.After(helper.Now()) {
			helper.AddTime(until.Sub(helper.Now()))

			continue
		}

		attempts++

		execution, err := helper.s.runOnce(t.Context(), false)
		if err == nil {
			t.Fatal("runOnce succeeded, want a registration error")
		}

		helper.s.handleExecutionResult(execution, err)
		helper.AddTime(15 * time.Second)
	}

	if helper.wrapperClientMock.registerErr != nil && helper.wrapperClientMock.registerCalls-registerCalls != attempts {
		t.Errorf("RegisterSelf called %d times, want %d", helper.wrapperClientMock.registerCalls-registerCalls, attempts)
	}

	return attempts
}
