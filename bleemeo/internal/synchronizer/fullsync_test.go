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
	"context"
	"testing"
	"time"

	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	gloutonTypes "github.com/bleemeo/glouton/types"

	"github.com/google/go-cmp/cmp"
	"github.com/prometheus/prometheus/model/labels"
)

// newFullSyncHelper returns a helper whose agent ran its first full synchronization, with a
// monitor on the API, and whose clock is at its next full synchronization.
func newFullSyncHelper(t *testing.T) *syncTestHelper {
	t.Helper()

	helper := newHelper(t)

	helper.preregisterAgent(t)
	helper.addMonitorOnAPI(t)
	helper.initSynchronizer(t)

	helper.pushPoints(t, []labels.Labels{
		labels.New(labels.Label{Name: gloutonTypes.LabelName, Value: metricCPUUsed}),
	})

	if err := helper.runOnce(t); err != nil {
		t.Fatal(err)
	}

	helper.SetTimeToNextFullSync()

	// Without a recent point, the metric would be deactivated after the clock moved by hours.
	helper.pushPoints(t, []labels.Labels{
		labels.New(labels.Label{Name: gloutonTypes.LabelName, Value: metricCPUUsed}),
	})

	return helper
}

// TestFullSynchronizationPartialFailure checks a full synchronization where some entities fail:
// it is done for the other entities, and only the failed ones are refreshed again.
func TestFullSynchronizationPartialFailure(t *testing.T) {
	t.Run("one entity failing", func(t *testing.T) {
		helper := newFullSyncHelper(t)
		defer helper.Close()

		fullSyncCount := helper.s.fullSyncCount

		helper.wrapperClientMock.monitorsErr = errMonitorsUnavailable

		if result := helper.runOnceWithResult(t); result.err == nil {
			t.Fatal("runOnce succeeded, want the monitors' error")
		}

		if helper.s.fullSyncCount != fullSyncCount+1 {
			t.Errorf("fullSyncCount = %d, want %d", helper.s.fullSyncCount, fullSyncCount+1)
		}

		if !helper.s.nextFullSync.After(helper.Now()) {
			t.Errorf("nextFullSync = %s, want it after now (%s)", helper.s.nextFullSync, helper.Now())
		}

		wantRequests := map[types.EntityName]types.SyncType{types.EntityMonitor: types.SyncTypeForceCacheRefresh}
		if diff := cmp.Diff(wantRequests, syncTypeByEntity(helper.s.forceSync)); diff != "" {
			t.Errorf("requested synchronizations mismatch (-want +got)\n%s", diff)
		}

		helper.wrapperClientMock.monitorsErr = nil

		helper.AddTime(time.Minute)

		result := helper.runOnceWithResult(t)
		if result.err != nil {
			t.Fatal(result.err)
		}

		// No metric waits for the monitors: the metrics aren't refreshed with them.
		wantSync := map[types.EntityName]types.SyncType{types.EntityMonitor: types.SyncTypeForceCacheRefresh}
		if diff := cmp.Diff(wantSync, result.syncPerEntity); diff != "" {
			t.Errorf("synchronized entities mismatch (-want +got)\n%s", diff)
		}
	})

	t.Run("cache refresh failing twice", func(t *testing.T) {
		helper := newFullSyncHelper(t)
		defer helper.Close()

		helper.wrapperClientMock.servicesErr = errServicesUnavailable

		if result := helper.runOnceWithResult(t); result.err == nil {
			t.Fatal("runOnce succeeded, want the services' error")
		}

		// The retry of the cache refresh fails too.
		helper.AddTime(time.Minute)

		result := helper.runOnceWithResult(t)
		if result.err == nil {
			t.Fatal("runOnce succeeded, want the services' error")
		}

		if result.syncPerEntity[types.EntityService] != types.SyncTypeForceCacheRefresh {
			t.Fatalf("services synchronized with %s, want %s", result.syncPerEntity[types.EntityService], types.SyncTypeForceCacheRefresh)
		}

		// A normal synchronization of the services is requested meanwhile: it mustn't replace
		// the cache refresh.
		helper.wrapperClientMock.servicesErr = nil

		helper.s.l.Lock()
		helper.s.requestSynchronizationLocked(types.EntityService, false)
		helper.s.l.Unlock()

		helper.AddTime(2 * time.Minute)

		result = helper.runOnceWithResult(t)
		if result.err != nil {
			t.Fatal(result.err)
		}

		if result.syncPerEntity[types.EntityService] != types.SyncTypeForceCacheRefresh {
			t.Errorf("services synchronized with %s, want %s", result.syncPerEntity[types.EntityService], types.SyncTypeForceCacheRefresh)
		}
	})

	t.Run("every entity failing", func(t *testing.T) {
		helper := newFullSyncHelper(t)
		defer helper.Close()

		// The connector is disabled: every entity fails.
		helper.s.Disable(helper.Now().Add(time.Minute), bleemeoTypes.DisableTooManyErrors)

		if result := helper.runOnceWithResult(t); result.err == nil {
			t.Fatal("runOnce succeeded, want the connector disabled")
		}

		helper.AddTime(2 * time.Minute)

		result := helper.runOnceWithResult(t)
		if result.err != nil {
			t.Fatal(result.err)
		}

		// Every entity is refreshed again: that's a full synchronization again.
		if len(result.syncPerEntity) != len(helper.s.synchronizers) {
			t.Errorf("synchronized %d entities, want all %d: %v", len(result.syncPerEntity), len(helper.s.synchronizers), result.syncPerEntity)
		}

		for name, syncType := range result.syncPerEntity {
			if syncType != types.SyncTypeForceCacheRefresh {
				t.Errorf("entity %s synchronized with %s, want %s", name, syncType, types.SyncTypeForceCacheRefresh)
			}
		}
	})

	t.Run("context cancelled", func(t *testing.T) {
		helper := newFullSyncHelper(t)
		defer helper.Close()

		fullSyncCount := helper.s.fullSyncCount
		nextFullSync := helper.s.nextFullSync

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		if _, err := helper.s.runOnce(ctx, false); err == nil {
			t.Fatal("runOnce succeeded, want the context's error")
		}

		// The execution stopped early: the full synchronization isn't done.
		if helper.s.fullSyncCount != fullSyncCount || !helper.s.nextFullSync.Equal(nextFullSync) {
			t.Errorf(
				"fullSyncCount, nextFullSync = %d, %s, want them unchanged (%d, %s)",
				helper.s.fullSyncCount, helper.s.nextFullSync, fullSyncCount, nextFullSync,
			)
		}
	})
}
