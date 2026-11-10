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
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/bleemeo/bleemeo-go"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
)

var errEntityFailed = errors.New("entity failed")

// TestEntityBackoff checks that a failed entity waits for its retry delay, which grows with
// its failures, without blocking the other entities.
func TestEntityBackoff(t *testing.T) {
	helper := newFullSyncHelper(t)
	defer helper.Close()

	helper.wrapperClientMock.monitorsErr = errMonitorsUnavailable

	if result := helper.runOnceWithResult(t); result.err == nil {
		t.Fatal("runOnce succeeded, want the monitors' error")
	}

	firstDelay := checkRetryDelay(t, helper, 15*time.Second)

	// The monitors wait for their retry delay, the other entities still synchronize.
	helper.s.UpdateInfo()

	result := helper.runOnceWithResult(t)
	if result.err != nil {
		t.Fatal(result.err)
	}

	if _, ok := result.syncPerEntity[types.EntityMonitor]; ok {
		t.Errorf("monitors synchronized during their retry delay: %v", result.syncPerEntity)
	}

	// The monitors would have made the metrics refresh: they don't while left out.
	if _, ok := result.syncPerEntity[types.EntityMetric]; ok {
		t.Errorf("metrics synchronized with the monitors left out: %v", result.syncPerEntity)
	}

	if _, ok := result.syncPerEntity[types.EntityInfo]; !ok {
		t.Errorf("info not synchronized during the monitors' retry delay: %v", result.syncPerEntity)
	}

	request := helper.s.forceSync[types.EntityMonitor]
	if request.Type != types.SyncTypeForceCacheRefresh || !request.Deadline.Equal(helper.s.entityRetryAt[types.EntityMonitor]) {
		t.Errorf("monitors request = %+v, want a cache refresh at their retry time %s", request, helper.s.entityRetryAt[types.EntityMonitor])
	}

	helper.AddTime(firstDelay)

	if result := helper.runOnceWithResult(t); result.err == nil {
		t.Fatal("runOnce succeeded, want the monitors' error")
	}

	if secondDelay := checkRetryDelay(t, helper, 15*time.Second*155/100); secondDelay <= firstDelay {
		t.Errorf("second retry delay %s, want it longer than the first one %s", secondDelay, firstDelay)
	}

	helper.wrapperClientMock.monitorsErr = nil

	helper.AddTime(time.Minute)

	result = helper.runOnceWithResult(t)
	if result.err != nil {
		t.Fatal(result.err)
	}

	if result.syncPerEntity[types.EntityMonitor] != types.SyncTypeForceCacheRefresh {
		t.Errorf("monitors synchronized with %s, want %s", result.syncPerEntity[types.EntityMonitor], types.SyncTypeForceCacheRefresh)
	}

	if retryAt, ok := helper.s.entityRetryAt[types.EntityMonitor]; ok {
		t.Errorf("monitors retry time %s kept after their success", retryAt)
	}
}

// checkRetryDelay checks the monitors' retry delay is want, give or take its jitter and rounding, and returns it.
func checkRetryDelay(t *testing.T, helper *syncTestHelper, want time.Duration) time.Duration {
	t.Helper()

	retryDelay := helper.s.entityRetryAt[types.EntityMonitor].Sub(helper.Now())
	if retryDelay < want*8/10 || retryDelay > want*12/10 {
		t.Errorf("monitors retry delay = %s, want about %s", retryDelay, want)
	}

	return retryDelay
}

// TestHandleExecutionResult checks when the failed executions disable the whole connector and
// mark the agent dysfunctional.
func TestHandleExecutionResult(t *testing.T) {
	authErr := fmt.Errorf("metric: %w", &bleemeo.AuthError{APIError: &bleemeo.APIError{StatusCode: 401}})

	tests := []struct {
		name string
		// synchronizedEvery is how often an execution also synchronizes an entity, zero for never.
		synchronizedEvery time.Duration
		errSync           error
		wantDysfunctional bool
		wantDisableReason bleemeoTypes.DisableReason
	}{
		{
			name:              "an entity failing, the others working",
			synchronizedEvery: time.Hour,
			errSync:           errEntityFailed,
		},
		{
			name:              "every entity failing",
			errSync:           errEntityFailed,
			wantDysfunctional: true,
			wantDisableReason: bleemeoTypes.DisableLongStandingError,
		},
		{
			name:              "authentication error",
			synchronizedEvery: time.Hour,
			errSync:           authErr,
			wantDisableReason: bleemeoTypes.DisableAuthenticationError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := newHelper(t)
			defer helper.Close()

			helper.preregisterAgent(t)
			helper.initSynchronizer(t)

			start := helper.Now()

			for helper.Now().Sub(start) < 37*time.Hour {
				execution := &Execution{entities: []EntityExecution{{syncType: types.SyncTypeNormal, err: tt.errSync}}}

				if tt.synchronizedEvery != 0 && helper.Now().Sub(start)%tt.synchronizedEvery == 0 {
					execution.entities = append(execution.entities, EntityExecution{syncType: types.SyncTypeNormal, synced: true})
				}

				helper.s.handleExecutionResult(execution, tt.errSync)

				if until, reason := helper.s.getDisabledUntil(); tt.wantDisableReason == bleemeoTypes.NotDisabled && until.After(helper.Now()) {
					t.Fatalf("connector disabled until %s (%s) after %s, want it enabled", until, reason, helper.Now().Sub(start))
				}

				helper.AddTime(15 * time.Minute)
			}

			if got := stateHasValue(agentDysfunctionalCacheKey, helper.state); got != tt.wantDysfunctional {
				t.Errorf("agent dysfunctional = %v, want %v", got, tt.wantDysfunctional)
			}

			if _, reason := helper.s.getDisabledUntil(); reason != tt.wantDisableReason {
				t.Errorf("connector disabled for %s, want %s", reason, tt.wantDisableReason)
			}
		})
	}
}
