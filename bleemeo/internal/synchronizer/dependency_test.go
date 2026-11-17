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

	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	"github.com/bleemeo/glouton/facts"
	gloutonTypes "github.com/bleemeo/glouton/types"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/prometheus/prometheus/model/labels"
)

// TestMetricWaitingForContainer checks a metric waiting for its container is registered on the
// execution after the container synchronized, and that the metrics don't synchronize with the
// containers otherwise.
func TestMetricWaitingForContainer(t *testing.T) {
	helper := newHelper(t)
	defer helper.Close()

	helper.preregisterAgent(t)
	helper.containers = []facts.Container{}
	helper.initSynchronizer(t)

	helper.pushPoints(t, []labels.Labels{
		labels.New(labels.Label{Name: gloutonTypes.LabelName, Value: metricCPUUsed}),
	})

	if err := helper.runOnce(t); err != nil {
		t.Fatal(err)
	}

	webContainer := facts.FakeContainer{
		FakeContainerName: "web",
		FakeID:            "web-container-id",
		FakeState:         facts.ContainerRunning,
		FakeCreatedAt:     helper.Now().Add(-time.Hour),
	}

	// The container's metric comes before the container is synchronized.
	helper.AddTime(time.Minute)
	helper.pushPoints(t, []labels.Labels{
		labels.New(
			labels.Label{Name: gloutonTypes.LabelName, Value: "container_cpu_used"},
			labels.Label{Name: gloutonTypes.LabelItem, Value: "web"},
			labels.Label{Name: gloutonTypes.LabelMetaContainerID, Value: webContainer.FakeID},
		),
	})

	steps := []struct {
		name     string
		prepare  func()
		wantSync map[types.EntityName]types.SyncType
		// wantRegistered is whether the container's metric is registered after the step.
		wantRegistered bool
	}{
		{
			name:     "metric waiting for its container",
			wantSync: map[types.EntityName]types.SyncType{types.EntityMetric: types.SyncTypeNormal},
		},
		{
			name: "container synchronized",
			prepare: func() {
				helper.containers = []facts.Container{webContainer}
				helper.s.UpdateContainers()
			},
			wantSync: map[types.EntityName]types.SyncType{types.EntityContainer: types.SyncTypeNormal},
		},
		{
			name:           "metric registered after its container",
			wantSync:       map[types.EntityName]types.SyncType{types.EntityMetric: types.SyncTypeNormal},
			wantRegistered: true,
		},
		{
			name:           "nothing waiting",
			wantRegistered: true,
		},
		{
			name: "container synchronized, no metric waiting",
			prepare: func() {
				helper.s.UpdateContainers()
			},
			wantSync:       map[types.EntityName]types.SyncType{types.EntityContainer: types.SyncTypeNormal},
			wantRegistered: true,
		},
		{
			name:           "metrics not synchronized after the containers",
			wantRegistered: true,
		},
	}

	for _, step := range steps {
		helper.AddTime(time.Minute)

		if step.prepare != nil {
			step.prepare()
		}

		result := helper.runOnceWithResult(t)
		if result.err != nil {
			t.Fatalf("step %q: %v", step.name, result.err)
		}

		if diff := cmp.Diff(step.wantSync, result.syncPerEntity, cmpopts.EquateEmpty()); diff != "" {
			t.Errorf("step %q: synchronized entities mismatch (-want +got)\n%s", step.name, diff)
		}

		registered := false

		for _, metric := range helper.MetricsFromAPI() {
			if metric.Name == "container_cpu_used" && metric.ContainerID != "" {
				registered = true
			}
		}

		if registered != step.wantRegistered {
			t.Errorf("step %q: container metric registered = %v, want %v", step.name, registered, step.wantRegistered)
		}
	}
}
