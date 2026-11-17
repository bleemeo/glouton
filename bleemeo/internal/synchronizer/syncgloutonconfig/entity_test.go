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

package syncgloutonconfig

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"testing"

	"github.com/bleemeo/bleemeo-go"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/config"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

const testAgentID = "agent-id"

var errTestAPI = errors.New("test API error")

// fakeClient holds the config items of the API. Any call not about config items panics.
type fakeClient struct {
	types.Client

	items []bleemeoTypes.GloutonConfigItem
	// registerBatches is the size of each RegisterGloutonConfigItems call.
	registerBatches []int
	deleted         []string
	listErr         error
	registerErr     error
	deleteErr       error
}

func (c *fakeClient) ListGloutonConfigItems(_ context.Context, agentID string) ([]bleemeoTypes.GloutonConfigItem, error) {
	if c.listErr != nil {
		return nil, c.listErr
	}

	return slices.DeleteFunc(slices.Clone(c.items), func(i bleemeoTypes.GloutonConfigItem) bool { return i.Agent != agentID }), nil
}

func (c *fakeClient) RegisterGloutonConfigItems(_ context.Context, items []bleemeoTypes.GloutonConfigItem) error {
	if c.registerErr != nil {
		return c.registerErr
	}

	c.registerBatches = append(c.registerBatches, len(items))

	for _, item := range items {
		item.ID = "id-" + strconv.Itoa(len(c.items))
		c.items = append(c.items, item)
	}

	return nil
}

func (c *fakeClient) DeleteGloutonConfigItem(_ context.Context, id string) error {
	c.deleted = append(c.deleted, id)

	if c.deleteErr != nil {
		return c.deleteErr
	}

	c.items = slices.DeleteFunc(c.items, func(i bleemeoTypes.GloutonConfigItem) bool { return i.ID == id })

	return nil
}

type fakeState struct {
	bleemeoTypes.State
}

func (fakeState) BleemeoCredentials() (string, string) { return testAgentID, "password" }

// fakeExecution gives the client and the State to the entity, and records whether it requested
// its own full synchronization. Any other call panics.
type fakeExecution struct {
	types.SynchronizationExecution

	client        *fakeClient
	onlyEssential bool
	requestedFull bool
}

func (e *fakeExecution) RequestSynchronization(entityName types.EntityName, requestFull bool) {
	if entityName == types.EntityGloutonConfig && requestFull {
		e.requestedFull = true
	}
}

func (e *fakeExecution) BleemeoAPIClient() types.Client { return e.client }
func (e *fakeExecution) IsOnlyEssential() bool          { return e.onlyEssential }

func (e *fakeExecution) Option() types.Option {
	return types.Option{State: fakeState{}}
}

// run runs one execution of the entity, the way the synchronizer does, and returns whether it
// asked to be synchronized. syncType is the synchronization requested by others, if any.
func run(t *testing.T, s *SyncGloutonConfig, execution *fakeExecution, syncType types.SyncType) (bool, error) {
	t.Helper()

	ctx := t.Context()

	e, err := s.PrepareExecution(ctx, execution)
	if err != nil {
		t.Fatal(err)
	}

	defer e.FinishExecution(ctx)

	need, err := e.NeedSynchronization(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if execution.requestedFull {
		syncType = types.SyncTypeForceCacheRefresh
	} else if need && syncType == types.SyncTypeNone {
		syncType = types.SyncTypeNormal
	}

	need = need || execution.requestedFull

	if syncType == types.SyncTypeNone {
		return need, nil
	}

	if err := e.RefreshCache(ctx, syncType); err != nil {
		return need, err
	}

	return need, e.SyncRemoteAndLocal(ctx, syncType)
}

func fileItem(key string, value any) config.Item {
	return config.Item{Key: key, Value: value, Type: config.TypeString, Source: config.SourceFile, Path: "/etc/glouton/glouton.conf"}
}

func apiItem(id string, item config.Item) bleemeoTypes.GloutonConfigItem {
	return bleemeoTypes.GloutonConfigItem{
		ID:       id,
		Agent:    testAgentID,
		Key:      item.Key,
		Value:    item.Value,
		Priority: item.Priority,
		Source:   bleemeoItemSourceFromConfigSource(item.Source),
		Path:     item.Path,
		Type:     bleemeoItemTypeFromConfigType(item.Type),
	}
}

// TestSyncRemoteAndLocal checks only the differences are written to the API: missing or changed
// items are registered, items gone locally are deleted, and the items of the API are kept.
func TestSyncRemoteAndLocal(t *testing.T) {
	t.Parallel()

	unchanged := fileItem("agent.unchanged", "same")
	changed := fileItem("agent.changed", "new value")
	added := fileItem("agent.added", "value")
	removed := fileItem("agent.removed", "value")
	fromAPI := bleemeoTypes.GloutonConfigItem{ID: "api", Agent: testAgentID, Key: "from.api", Source: bleemeo.ConfigItemSource_API}

	changedOnAPI := apiItem("changed", changed)
	changedOnAPI.Value = "old value"

	client := &fakeClient{items: []bleemeoTypes.GloutonConfigItem{
		apiItem("unchanged", unchanged), changedOnAPI, apiItem("removed", removed), fromAPI,
	}}

	if _, err := run(t, New([]config.Item{unchanged, changed, added}), &fakeExecution{client: client}, types.SyncTypeForceCacheRefresh); err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff([]int{2}, client.registerBatches); diff != "" {
		t.Errorf("register batches mismatch (-want +got)\n%s", diff)
	}

	if diff := cmp.Diff([]string{"changed", "removed"}, client.deleted, cmpopts.SortSlices(func(a, b string) bool { return a < b })); diff != "" {
		t.Errorf("deleted mismatch (-want +got)\n%s", diff)
	}

	got := make(map[string]any, len(client.items))
	for _, item := range client.items {
		got[item.Key] = item.Value
	}

	want := map[string]any{
		"agent.unchanged": "same",
		"agent.changed":   "new value",
		"agent.added":     "value",
		"from.api":        nil,
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("items on the API mismatch (-want +got)\n%s", diff)
	}
}

// TestRegisterBatches checks the items are registered by batches the API accepts.
func TestRegisterBatches(t *testing.T) {
	t.Parallel()

	items := make([]config.Item, 0, 250)
	for i := range 250 {
		items = append(items, fileItem("key."+strconv.Itoa(i), "value"))
	}

	client := &fakeClient{}

	if _, err := run(t, New(items), &fakeExecution{client: client}, types.SyncTypeForceCacheRefresh); err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff([]int{100, 100, 50}, client.registerBatches); diff != "" {
		t.Errorf("register batches mismatch (-want +got)\n%s", diff)
	}
}

// TestDeleteNotFound checks an item already deleted from the API isn't an error.
func TestDeleteNotFound(t *testing.T) {
	t.Parallel()

	client := &fakeClient{
		items:     []bleemeoTypes.GloutonConfigItem{apiItem("gone", fileItem("agent.gone", "value"))},
		deleteErr: &bleemeo.APIError{StatusCode: http.StatusNotFound},
	}

	if _, err := run(t, New(nil), &fakeExecution{client: client}, types.SyncTypeForceCacheRefresh); err != nil {
		t.Errorf("a deleted item not found on the API failed the synchronization: %v", err)
	}

	client.deleteErr = errTestAPI

	if _, err := run(t, New(nil), &fakeExecution{client: client}, types.SyncTypeForceCacheRefresh); !errors.Is(err, errTestAPI) {
		t.Errorf("run() = %v, want %v", err, errTestAPI)
	}
}

// TestSyncDone checks the config items are synchronized on any execution until they were once,
// as after a reload, and then only on full synchronizations.
func TestSyncDone(t *testing.T) {
	t.Parallel()

	s := New([]config.Item{fileItem("agent.key", "value")})
	client := &fakeClient{listErr: errTestAPI}

	// The config items aren't essential: an essential-only execution leaves them alone.
	if need, err := run(t, s, &fakeExecution{client: client, onlyEssential: true}, types.SyncTypeNone); err != nil || !need {
		t.Fatalf("essential-only: need = %v, err = %v; want true, nil", need, err)
	}

	if len(client.registerBatches) != 0 {
		t.Fatal("an essential-only execution registered config items")
	}

	// A failure keeps the items to synchronize.
	if need, err := run(t, s, &fakeExecution{client: client}, types.SyncTypeNone); !need || !errors.Is(err, errTestAPI) {
		t.Fatalf("failure: need = %v, err = %v; want true, %v", need, err, errTestAPI)
	}

	client.listErr = nil

	if need, err := run(t, s, &fakeExecution{client: client}, types.SyncTypeNone); err != nil || !need {
		t.Fatalf("first success: need = %v, err = %v; want true, nil", need, err)
	}

	if diff := cmp.Diff([]int{1}, client.registerBatches); diff != "" {
		t.Fatalf("register batches mismatch (-want +got)\n%s", diff)
	}

	// Synchronized: only a full synchronization synchronizes them again.
	client.items = nil

	if need, err := run(t, s, &fakeExecution{client: client}, types.SyncTypeNormal); err != nil || need {
		t.Fatalf("after success: need = %v, err = %v; want false, nil", need, err)
	}

	if len(client.registerBatches) != 1 {
		t.Fatal("a normal synchronization registered the config items again")
	}

	if _, err := run(t, s, &fakeExecution{client: client}, types.SyncTypeForceCacheRefresh); err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff([]int{1, 1}, client.registerBatches); diff != "" {
		t.Errorf("register batches mismatch (-want +got)\n%s", diff)
	}
}
