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

package syncapplications

import (
	"errors"
	"net/http"
	"slices"
	"testing"

	"github.com/bleemeo/bleemeo-go"
	"github.com/bleemeo/glouton/bleemeo/internal/cache"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/executiontest"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/discovery"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

var errAPIUnavailable = errors.New("API unavailable")

// fakeExecution gives the option and the client to the entity, and records the synchronizations
// it requests and the entities it fails. Any other call panics.
type fakeExecution struct {
	executiontest.Recorder

	option types.Option
	api    *mockAPI
}

func (e *fakeExecution) Option() types.Option           { return e.option }
func (e *fakeExecution) BleemeoAPIClient() types.Client { return e.api }

// newExecution returns an execution of the entity, with a cache whose account has the
// application integration when integration is true, and a discovery of services.
func newExecution(t *testing.T, api *mockAPI, integration bool, services []discovery.Service) (types.EntitySynchronizerExecution, *fakeExecution) {
	t.Helper()

	c := &cache.Cache{}
	c.SetAccountID("account-id")
	c.SetAgentTypes([]bleemeoTypes.AgentType{{ID: "agent-type-id", Name: bleemeo.AgentType_Agent}})
	// The account config is only complete with a config of the agent's type: the allow-list
	// gives one, its value doesn't matter.
	c.SetConfigs([]bleemeoTypes.Config{
		{ID: "allowlist-id", Type: bleemeoTypes.ConfigTypeAgentMetricsAllowlist, Account: "account-id", AgentType: "agent-type-id"},
		{ID: "application-id", Type: bleemeoTypes.ConfigTypeApplicationIntegration, Account: "account-id", Value: integration},
	})

	mockDiscovery := &discovery.MockDiscoverer{}
	mockDiscovery.SetResult(services, nil)

	execution := &fakeExecution{
		option: types.Option{Cache: c, Discovery: mockDiscovery},
		api:    api,
	}

	e, err := New().PrepareExecution(t.Context(), execution)
	if err != nil {
		t.Fatal(err)
	}

	return e, execution
}

// TestNeedSynchronization checks the applications are synchronized with the services, and
// never on their own.
func TestNeedSynchronization(t *testing.T) {
	t.Parallel()

	e, execution := newExecution(t, &mockAPI{}, true, nil)

	need, err := e.NeedSynchronization(t.Context())
	if err != nil || need {
		t.Errorf("NeedSynchronization() = %v, %v, want false, nil", need, err)
	}

	want := [][2]types.EntityName{{types.EntityApplication, types.EntityService}}
	if diff := cmp.Diff(want, execution.Linked); diff != "" {
		t.Errorf("linked synchronizations mismatch (-want +got)\n%s", diff)
	}
}

// TestRefreshCache checks the applications are only read on a forced cache refresh, and a read
// failure fails the services, which need their applications.
func TestRefreshCache(t *testing.T) {
	t.Parallel()

	apps := []bleemeoTypes.Application{{ID: "1", Name: testAppDockerCompose, Tag: testTagDockerCompose}}

	tests := []struct {
		name      string
		syncType  types.SyncType
		listErr   error
		wantCalls int
		wantApps  []bleemeoTypes.Application
		wantErr   error
	}{
		{name: "normal", syncType: types.SyncTypeNormal},
		{name: "forced", syncType: types.SyncTypeForceCacheRefresh, wantCalls: 1, wantApps: apps},
		{name: "forced read failed", syncType: types.SyncTypeForceCacheRefresh, listErr: errAPIUnavailable, wantCalls: 1, wantErr: errAPIUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			api := &mockAPI{ListResponse: apps, ListErr: tt.listErr}
			e, execution := newExecution(t, api, true, nil)

			if err := e.RefreshCache(t.Context(), tt.syncType); !errors.Is(err, tt.wantErr) {
				t.Errorf("RefreshCache() = %v, want %v", err, tt.wantErr)
			}

			if api.ListCalls != tt.wantCalls {
				t.Errorf("ListApplications called %d times, want %d", api.ListCalls, tt.wantCalls)
			}

			if diff := cmp.Diff(tt.wantApps, execution.option.Cache.Applications(), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("cached applications mismatch (-want +got)\n%s", diff)
			}

			checkFailedService(t, execution, tt.wantErr != nil)
		})
	}
}

// TestSyncRemoteAndLocal checks the applications are created in the cache only with a loaded
// account config that has the application integration, a refusal from the plan refreshes the
// account config instead of failing, and any other failure fails the services.
func TestSyncRemoteAndLocal(t *testing.T) {
	t.Parallel()

	notAllowed := &bleemeo.APIError{
		StatusCode: http.StatusBadRequest,
		Response:   []byte(`["Don't allow to create an application in this current plan"]`),
	}
	serverError := &bleemeo.APIError{StatusCode: http.StatusInternalServerError}

	tests := []struct {
		name            string
		integration     bool
		noAccountConfig bool
		createErr       error
		wantCalls       int
		wantTags        []string
		wantErr         error
		wantConfig      bool
		wantFailedSrv   bool
	}{
		{name: "no integration", integration: false},
		{name: "no account config", integration: true, noAccountConfig: true},
		{name: "created", integration: true, wantCalls: 1, wantTags: []string{testTagDockerCompose}},
		{name: "not allowed in the plan", integration: true, createErr: notAllowed, wantCalls: 1, wantConfig: true},
		{name: "other error", integration: true, createErr: serverError, wantCalls: 1, wantErr: serverError, wantFailedSrv: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Each run its own slice, as the discovery gives: the services are filtered in place.
			services := []discovery.Service{{
				Name:         testServiceRedis,
				Active:       true,
				Applications: []discovery.Application{{Name: testComposeName, Type: discovery.ApplicationDockerCompose}},
			}}

			api := &mockAPI{CreateErr: tt.createErr}
			e, execution := newExecution(t, api, tt.integration, services)

			if tt.noAccountConfig {
				// Without the agent's config, the account config isn't complete, even with the
				// application integration on.
				execution.option.Cache.SetConfigs(slices.DeleteFunc(execution.option.Cache.Configs(), func(cfg bleemeoTypes.Config) bool {
					return cfg.AgentType != ""
				}))
			}

			if err := e.SyncRemoteAndLocal(t.Context(), types.SyncTypeNormal); !errors.Is(err, tt.wantErr) {
				t.Errorf("SyncRemoteAndLocal() = %v, want %v", err, tt.wantErr)
			}

			if api.CreateCalls != tt.wantCalls {
				t.Errorf("CreateApplication called %d times, want %d", api.CreateCalls, tt.wantCalls)
			}

			var gotTags []string

			for _, app := range execution.option.Cache.Applications() {
				gotTags = append(gotTags, app.Tag)
			}

			if diff := cmp.Diff(tt.wantTags, gotTags); diff != "" {
				t.Errorf("cached application tags mismatch (-want +got)\n%s", diff)
			}

			if full, ok := execution.Requested[types.EntityConfig]; ok != tt.wantConfig || (ok && !full) {
				t.Errorf("config synchronization requested = %v (full %v), want %v (full)", ok, full, tt.wantConfig)
			}

			checkFailedService(t, execution, tt.wantFailedSrv)
		})
	}
}

// checkFailedService checks the services were failed with ErrApplicationFirst when want is true,
// and nothing was failed otherwise.
func checkFailedService(t *testing.T, execution *fakeExecution, want bool) {
	t.Helper()

	var wantFailed map[types.EntityName]error

	if want {
		wantFailed = map[types.EntityName]error{types.EntityService: ErrApplicationFirst}
	}

	if diff := cmp.Diff(wantFailed, execution.Failed, cmpopts.EquateErrors()); diff != "" {
		t.Errorf("failed entities mismatch (-want +got)\n%s", diff)
	}
}
