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

package synccontainers

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bleemeo/bleemeo-go"
	"github.com/bleemeo/glouton/bleemeo/internal/cache"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/bleemeoapi"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/discovery"
	"github.com/bleemeo/glouton/facts"
	containerTypes "github.com/bleemeo/glouton/facts/container-runtime/types"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

const testAgentID = "agent-id"

func testNow() time.Time { return time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC) }

// fakeClient counts the lists of containers, and records the containers registered and updated.
// UpdateContainer returns NotFound for the IDs of notFound. Any other call panics.
type fakeClient struct {
	types.Client

	listed     int
	registered []bleemeoapi.ContainerPayload
	// updated is the payloads of UpdateContainer, by container ID.
	updated  map[string]any
	notFound map[string]bool
	nextID   int
}

func (c *fakeClient) ListContainers(context.Context, string) ([]bleemeoTypes.Container, error) {
	c.listed++

	return nil, nil
}

func (c *fakeClient) RegisterContainer(_ context.Context, payload bleemeoapi.ContainerPayload) (bleemeoapi.ContainerPayload, error) {
	c.nextID++
	payload.ID = fmt.Sprintf("new-%d", c.nextID)
	c.registered = append(c.registered, payload)

	return payload, nil
}

func (c *fakeClient) UpdateContainer(_ context.Context, id string, payload any) (bleemeoapi.ContainerPayload, error) {
	if c.notFound[id] {
		return bleemeoapi.ContainerPayload{}, &bleemeo.APIError{StatusCode: http.StatusNotFound, Err: bleemeo.ErrResourceNotFound}
	}

	if c.updated == nil {
		c.updated = make(map[string]any)
	}

	c.updated[id] = payload

	if p, ok := payload.(bleemeoapi.ContainerPayload); ok {
		p.ID = id

		return p, nil
	}

	return bleemeoapi.ContainerPayload{ID: id}, nil
}

func newEntity() *SyncContainers {
	return New(testNow)
}

// remoteOf returns the container as the cache keeps it once registered with id.
func remoteOf(id string, container facts.Container, lastUpdate time.Time) bleemeoTypes.Container {
	remote := bleemeoTypes.Container{
		ID:                   id,
		Name:                 container.ContainerName(),
		ContainerID:          container.ID(),
		ContainerInspect:     container.ContainerJSON(),
		Status:               container.State().String(),
		CreatedAt:            container.CreatedAt(),
		GloutonLastUpdatedAt: lastUpdate,
	}

	remote.FillInspectHash()
	remote.ContainerInspect = ""

	return remote
}

func newContainer(name string) facts.FakeContainer {
	return facts.FakeContainer{
		FakeContainerName: name,
		FakeID:            name + "-container-id",
		FakeState:         facts.ContainerRunning,
		FakeCreatedAt:     testNow().Add(-time.Hour),
		FakeContainerJSON: `{"name": "` + name + `"}`,
	}
}

// TestRegisterAndUpdate checks which containers are registered or updated on the API.
func TestRegisterAndUpdate(t *testing.T) {
	t.Parallel()

	unchanged := newContainer("unchanged")
	stopped := newContainer("stopped")
	inspectRecent := newContainer("inspect-recent")
	inspectOld := newContainer("inspect-old")
	delayed := newContainer("delayed")
	docker := newContainer("docker")
	docker.FakeRuntimeName = containerTypes.DockerRuntime
	tooLong := newContainer(strings.Repeat("x", 300))

	remote := []bleemeoTypes.Container{
		remoteOf("1", unchanged, time.Time{}),
		remoteOf("2", stopped, time.Time{}),
		remoteOf("3", inspectRecent, testNow().Add(-time.Minute)),
		remoteOf("4", inspectOld, testNow().Add(-time.Hour)),
	}

	stopped.FakeState = facts.ContainerStopped
	// A change of the inspect alone, likely a health check log, waits 30 minutes.
	inspectRecent.FakeContainerJSON = `{"health": "new log"}`
	inspectOld.FakeContainerJSON = `{"health": "new log"}`

	c := &cache.Cache{}
	c.SetContainers(remote)

	client := &fakeClient{}
	local := []facts.Container{unchanged, stopped, inspectRecent, inspectOld, delayed, docker, tooLong}
	delayedByID := map[string]time.Time{delayed.ID(): testNow().Add(time.Minute)}

	err := newEntity().registerAndUpdate(t.Context(), client, c, local, delayedByID, testAgentID, "1.45")
	if err != nil {
		t.Fatal(err)
	}

	if len(client.registered) != 1 || client.registered[0].Name != "docker" {
		t.Fatalf("registered %v, want only the docker container", client.registered)
	}

	if got := client.registered[0]; got.Host != testAgentID || got.DockerAPIVersion != "1.45" {
		t.Errorf("registered with host %q and Docker API version %q, want %q and 1.45", got.Host, got.DockerAPIVersion, testAgentID)
	}

	updated := make([]string, 0, len(client.updated))
	for id := range client.updated {
		updated = append(updated, id)
	}

	if diff := cmp.Diff([]string{"2", "4"}, updated, cmpopts.SortSlices(func(a, b string) bool { return a < b })); diff != "" {
		t.Errorf("updated containers mismatch (-want +got)\n%s", diff)
	}

	if got := c.ContainersByUUID()["4"].GloutonLastUpdatedAt; !got.Equal(testNow()) {
		t.Errorf("last update of the updated container = %v, want %v", got, testNow())
	}

	if got := len(c.Containers()); got != 5 {
		t.Errorf("cache has %d containers, want 5", got)
	}
}

// TestDeleteFromLocal checks the containers which aren't local are marked deleted, and their
// metrics deactivated in the cache.
func TestDeleteFromLocal(t *testing.T) {
	t.Parallel()

	running := newContainer("running")
	gone := newContainer("gone")
	goneFromAPI := newContainer("gone-from-api")
	alreadyDeleted := remoteOf("4", newContainer("already-deleted"), time.Time{})
	alreadyDeleted.DeletedAt = bleemeoTypes.NullTime(testNow().Add(-time.Hour))

	c := &cache.Cache{}
	c.SetContainers([]bleemeoTypes.Container{
		remoteOf("1", running, time.Time{}),
		remoteOf("2", gone, time.Time{}),
		remoteOf("3", goneFromAPI, time.Time{}),
		alreadyDeleted,
	})
	c.SetMetrics([]bleemeoTypes.Metric{
		{ID: "metric-running", ContainerID: "1"},
		{ID: "metric-gone", ContainerID: "2"},
	})

	client := &fakeClient{notFound: map[string]bool{"3": true}}

	if deleted := newEntity().deleteFromLocal(t.Context(), client, c, []facts.Container{running}); !deleted {
		t.Error("deleteFromLocal() = false, want true")
	}

	want := map[string]any{"2": map[string]any{"deleted_at": bleemeoTypes.NullTime(testNow())}}
	if diff := cmp.Diff(want, client.updated); diff != "" {
		t.Errorf("updated containers mismatch (-want +got)\n%s", diff)
	}

	containers := c.ContainersByUUID()

	if _, ok := containers["3"]; ok {
		t.Error("the container already deleted on the API is still in the cache")
	}

	if got := time.Time(containers["2"].DeletedAt); !got.Equal(testNow()) {
		t.Errorf("deleted_at of the gone container = %v, want %v", got, testNow())
	}

	metrics := c.MetricsByUUID()

	if got := metrics["metric-gone"].DeactivatedAt; !got.Equal(testNow()) {
		t.Errorf("metric of the gone container deactivated at %v, want %v", got, testNow())
	}

	if got := metrics["metric-running"].DeactivatedAt; !got.IsZero() {
		t.Errorf("metric of the running container deactivated at %v, want active", got)
	}
}

// fakeGlobalState gives the first delayed container. Any other call panics.
type fakeGlobalState struct {
	types.SynchronizedGlobalState

	minDelayed time.Time
}

func (s fakeGlobalState) DelayedContainers() (map[string]time.Time, time.Time) {
	return nil, s.minDelayed
}

// fakeExecution gives the discovery, the times, the cache, the client and the failures in a row
// to the entity. Any other call panics.
type fakeExecution struct {
	types.SynchronizationExecution

	discovery          *discovery.MockDiscoverer
	minDelayed         time.Time
	lastSuccessfulSync time.Time
	cache              *cache.Cache
	client             types.Client
	failures           int
}

func (e *fakeExecution) Option() types.Option {
	return types.Option{Discovery: e.discovery, Cache: e.cache}
}

func (e *fakeExecution) BleemeoAPIClient() types.Client { return e.client }

func (e *fakeExecution) ConsecutiveFailures(types.EntityName) int { return e.failures }

func (e *fakeExecution) GlobalState() types.SynchronizedGlobalState {
	return fakeGlobalState{minDelayed: e.minDelayed}
}

func (e *fakeExecution) LastSuccessfulSync(types.EntityName) time.Time { return e.lastSuccessfulSync }
func (e *fakeExecution) StartedAt() time.Time                          { return testNow() }

// TestNeedSynchronization checks the containers are synchronized after a discovery, and once a
// delayed container is due.
func TestNeedSynchronization(t *testing.T) {
	t.Parallel()

	lastSync := testNow().Add(-time.Minute)

	tests := []struct {
		name       string
		discovery  time.Time
		minDelayed time.Time
		want       bool
	}{
		{name: "nothing new", discovery: lastSync.Add(-time.Minute), want: false},
		{name: "discovery", discovery: lastSync.Add(time.Second), want: true},
		{name: "delayed container not due", discovery: lastSync.Add(-time.Minute), minDelayed: testNow().Add(time.Second), want: false},
		{name: "delayed container due", discovery: lastSync.Add(-time.Minute), minDelayed: testNow().Add(-time.Second), want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			execution := &fakeExecution{
				discovery:          &discovery.MockDiscoverer{UpdatedAt: tt.discovery},
				minDelayed:         tt.minDelayed,
				lastSuccessfulSync: lastSync,
			}

			s := newEntity()

			e, err := s.PrepareExecution(t.Context(), execution)
			if err != nil {
				t.Fatal(err)
			}

			need, err := e.NeedSynchronization(t.Context())
			if err != nil || need != tt.want {
				t.Errorf("NeedSynchronization() = %v, %v, want %v", need, err, tt.want)
			}
		})
	}
}

// TestRefreshCacheAfterFailures checks the containers are listed on a cache refresh, and after
// their own 3 failures in a row.
func TestRefreshCacheAfterFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		syncType   types.SyncType
		failures   int
		wantListed bool
	}{
		{name: "normal", syncType: types.SyncTypeNormal, failures: 0, wantListed: false},
		{name: "cache refresh", syncType: types.SyncTypeForceCacheRefresh, failures: 0, wantListed: true},
		{name: "2 failures", syncType: types.SyncTypeNormal, failures: 2, wantListed: false},
		{name: "3 failures", syncType: types.SyncTypeNormal, failures: 3, wantListed: true},
		{name: "4 failures", syncType: types.SyncTypeNormal, failures: 4, wantListed: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			client := &fakeClient{}
			execution := &fakeExecution{cache: &cache.Cache{}, client: client, failures: test.failures}

			e, err := New(testNow).PrepareExecution(t.Context(), execution)
			if err != nil {
				t.Fatal(err)
			}

			if err := e.RefreshCache(t.Context(), test.syncType); err != nil {
				t.Fatal(err)
			}

			if got := client.listed > 0; got != test.wantListed {
				t.Errorf("containers listed = %v, want %v", got, test.wantListed)
			}
		})
	}
}
