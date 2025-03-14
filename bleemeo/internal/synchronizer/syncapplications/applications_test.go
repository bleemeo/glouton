// Copyright 2015-2025 Bleemeo
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
	"context"
	"strconv"
	"testing"

	"github.com/bleemeo/bleemeo-go"
	"github.com/bleemeo/glouton/bleemeo/internal/cache"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/discovery"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

func Test_syncRemoteAndLocal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                  string
		localServices         []discovery.Service
		remoteServices        []bleemeoTypes.Service
		remoteApplication     []bleemeoTypes.Application
		wantCreateApplication []bleemeoTypes.Application
	}{
		{
			name:                  "no-service",
			localServices:         []discovery.Service{},
			remoteServices:        []bleemeoTypes.Service{},
			remoteApplication:     []bleemeoTypes.Application{},
			wantCreateApplication: []bleemeoTypes.Application{},
		},
		{
			name: "no-applications",
			localServices: []discovery.Service{
				{
					Name: "redis",
					Tags: []string{"tag-arent-application"},
				},
				{
					Name: "apache",
					Tags: []string{"tag-arent-application2"},
				},
			},
			remoteServices: []bleemeoTypes.Service{
				{
					ID:     "1",
					Active: true,
					Label:  "redis",
					Tags: []bleemeoTypes.Tag{
						{ID: "1", Name: "tag-arent-application", TagType: bleemeo.TagType_CreatedByGlouton},
					},
				},
			},
			remoteApplication:     []bleemeoTypes.Application{},
			wantCreateApplication: []bleemeoTypes.Application{},
		},
		{
			name: "application",
			localServices: []discovery.Service{
				{
					Name:   "redis",
					Active: true,
					Tags:   []string{"user-tag"},
					Applications: []discovery.Application{
						{
							Name: "my-compose",
							Type: discovery.ApplicationDockerCompose,
						},
					},
				},
			},
			remoteServices:    []bleemeoTypes.Service{},
			remoteApplication: []bleemeoTypes.Application{},
			wantCreateApplication: []bleemeoTypes.Application{
				{
					Name: "Docker compose my-compose",
					Tag:  "docker-compose-my-compose",
				},
			},
		},
		{
			name: "application-already-exists",
			localServices: []discovery.Service{
				{
					Name: "redis",
					Tags: []string{"user-tag"},
					Applications: []discovery.Application{
						{
							Name: "my-compose",
							Type: discovery.ApplicationDockerCompose,
						},
					},
				},
			},
			remoteServices: []bleemeoTypes.Service{},
			remoteApplication: []bleemeoTypes.Application{
				{
					ID:   "1",
					Name: "Docker compose my-compose",
					Tag:  "docker-compose-my-compose",
				},
			},
			wantCreateApplication: []bleemeoTypes.Application{},
		},
		{
			name: "manually-created-application",
			localServices: []discovery.Service{
				{
					Name: "redis",
					Tags: []string{"user-tag"},
					Applications: []discovery.Application{
						{
							Name: "my-compose",
							Type: discovery.ApplicationDockerCompose,
						},
					},
				},
			},
			remoteServices: []bleemeoTypes.Service{},
			remoteApplication: []bleemeoTypes.Application{
				{
					ID:   "1",
					Name: "Docker compose my-compose",
					Tag:  "custom-tag",
				},
			},
			wantCreateApplication: []bleemeoTypes.Application{},
		},
		{
			// Glouton only create the application if its the there is not already
			// a service with this automatic tag.
			// This is an approximation of first time that tag is seen.
			// It avoid re-creating the application if user delete the application from the API.
			name: "application-is-deleted-service-is-tagged",
			localServices: []discovery.Service{
				{
					Name: "redis",
					Tags: []string{"user-tag"},
					Applications: []discovery.Application{
						{
							Name: "my-compose",
							Type: discovery.ApplicationDockerCompose,
						},
					},
				},
			},
			remoteServices: []bleemeoTypes.Service{
				{
					ID:     "1",
					Active: true,
					Label:  "apache",
					Tags: []bleemeoTypes.Tag{
						{
							ID:      "1",
							Name:    "docker-compose-my-compose",
							TagType: bleemeo.TagType_AutomaticGlouton,
						},
					},
				},
			},
			remoteApplication:     []bleemeoTypes.Application{},
			wantCreateApplication: []bleemeoTypes.Application{},
		},
		{
			name: "inactive application",
			localServices: []discovery.Service{
				{
					Name:   "redis",
					Active: false,
					Applications: []discovery.Application{
						{
							Name: "my-compose",
							Type: discovery.ApplicationDockerCompose,
						},
					},
				},
			},
			remoteServices:        []bleemeoTypes.Service{},
			remoteApplication:     []bleemeoTypes.Application{},
			wantCreateApplication: []bleemeoTypes.Application{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cache := &cache.Cache{}
			cache.SetServices(tt.remoteServices)
			cache.SetApplications(tt.remoteApplication)

			apiClient := &mockAPI{
				ListResponse: tt.remoteApplication,
			}

			if err := syncRemoteAndLocal(t.Context(), tt.localServices, apiClient, cache); err != nil {
				t.Errorf("syncRemoteAndLocal() error = %v", err)
			}

			if len(cache.Applications()) != len(tt.remoteApplication)+len(tt.wantCreateApplication) {
				t.Errorf("application not added to cache")
			}

			if diff := cmp.Diff(tt.wantCreateApplication, apiClient.CreatedApplications, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("CreatedApplications mismatch (-want +got)\n%s", diff)
			}
		})
	}
}

type mockAPI struct {
	lastID              int
	ListResponse        []bleemeoTypes.Application
	CreatedApplications []bleemeoTypes.Application
}

func (api *mockAPI) ListApplications(ctx context.Context) ([]bleemeoTypes.Application, error) {
	_ = ctx

	return api.ListResponse, nil
}

func (api *mockAPI) CreateApplication(ctx context.Context, app bleemeoTypes.Application) (bleemeoTypes.Application, error) {
	_ = ctx

	api.CreatedApplications = append(api.CreatedApplications, app)

	api.lastID++
	app.ID = strconv.Itoa(api.lastID)

	return app, nil
}
