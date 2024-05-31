// Copyright 2015-2024 Bleemeo
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

package syncservices

import (
	"testing"

	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/discovery"

	"github.com/google/go-cmp/cmp"
)

func Test_serviceHadSameTags(t *testing.T) {
	tests := []struct {
		name          string
		remoteTags    []bleemeoTypes.Tag
		localServices discovery.Service
		want          bool
	}{
		{
			name:          "empty-list",
			remoteTags:    []bleemeoTypes.Tag{},
			localServices: discovery.Service{},
			want:          true,
		},
		{
			name:          "nil-empty",
			remoteTags:    nil,
			localServices: discovery.Service{}, want: true,
		},
		{
			name: "one-tag",
			remoteTags: []bleemeoTypes.Tag{
				{
					ID:      "dedd669b-f139-4517-a410-4c6d3f66f85f",
					Name:    "tag-one",
					TagType: bleemeoTypes.TagTypeIsCreatedByGlouton,
				},
			},
			localServices: discovery.Service{
				Tags: []string{"tag-one"},
			},
			want: true,
		},
		{
			name:       "missing-tag",
			remoteTags: []bleemeoTypes.Tag{},
			localServices: discovery.Service{
				Tags: []string{"tag-one"},
			},
			want: false,
		},
		{
			name: "mismatch-tag",
			remoteTags: []bleemeoTypes.Tag{
				{
					ID:      "dedd669b-f139-4517-a410-4c6d3f66f85f",
					Name:    "tag-one",
					TagType: bleemeoTypes.TagTypeIsCreatedByGlouton,
				},
			},
			localServices: discovery.Service{
				Tags: []string{"tag-two"},
			},
			want: false,
		},
		{
			name: "not-glouton-tag",
			remoteTags: []bleemeoTypes.Tag{
				{
					ID:      "dedd669b-f139-4517-a410-4c6d3f66f85f",
					Name:    "tag-one",
					TagType: bleemeoTypes.TagTypeIsAutomatic,
				},
			},
			localServices: discovery.Service{
				Tags: []string{"tag-one"},
			},
			want: false,
		},
		{
			name: "truncated-local-tag",
			remoteTags: []bleemeoTypes.Tag{
				{
					ID:      "dedd669b-f139-4517-a410-4c6d3f66f85f",
					Name:    "tag-one",
					TagType: bleemeoTypes.TagTypeIsCreatedByGlouton,
				},
			},
			localServices: discovery.Service{
				Tags: []string{
					"tag-one",
					"tag-longer-than-100-character-are-ignored-since-bleemeo-api-dont-support-them---filler-to-reach-100-char",
				},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := serviceHadSameTags(tt.remoteTags, tt.localServices); got != tt.want {
				t.Errorf("serviceHadSameTags() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_getTagsFromLocal(t *testing.T) {
	tests := []struct {
		name    string
		service discovery.Service
		want    []bleemeoTypes.Tag
	}{
		{
			name: "no-tags",
			service: discovery.Service{
				Tags:         nil,
				Applications: nil,
			},
			want: []bleemeoTypes.Tag{},
		},
		{
			name: "custom-tag",
			service: discovery.Service{
				Tags: []string{
					"tag-user1",
				},
				Applications: nil,
			},
			want: []bleemeoTypes.Tag{
				{
					Name:    "tag-user1",
					TagType: bleemeoTypes.TagTypeIsCreatedByGlouton,
				},
			},
		},
		{
			name: "app-tag",
			service: discovery.Service{
				Tags: []string{},
				Applications: []discovery.Application{
					{
						Name: "my_compose_project",
						Type: discovery.ApplicationDockerCompose,
					},
				},
			},
			want: []bleemeoTypes.Tag{
				{
					Name:    "docker-compose-my-compose-project",
					TagType: bleemeoTypes.TagTypeIsAutomaticByGlouton,
				},
			},
		},
		{
			name: "both-tag",
			service: discovery.Service{
				Tags: []string{
					"tag-user1",
				},
				Applications: []discovery.Application{
					{
						Name: "my_compose_project",
						Type: discovery.ApplicationDockerCompose,
					},
				},
			},
			want: []bleemeoTypes.Tag{
				{
					Name:    "tag-user1",
					TagType: bleemeoTypes.TagTypeIsCreatedByGlouton,
				},
				{
					Name:    "docker-compose-my-compose-project",
					TagType: bleemeoTypes.TagTypeIsAutomaticByGlouton,
				},
			},
		},
		{
			name: "same-tag",
			service: discovery.Service{
				Tags: []string{
					"docker-compose-my-compose-project",
				},
				Applications: []discovery.Application{
					{
						Name: "my_compose_project",
						Type: discovery.ApplicationDockerCompose,
					},
				},
			},
			want: []bleemeoTypes.Tag{
				{
					Name:    "docker-compose-my-compose-project",
					TagType: bleemeoTypes.TagTypeIsCreatedByGlouton,
				},
				{
					Name:    "docker-compose-my-compose-project",
					TagType: bleemeoTypes.TagTypeIsAutomaticByGlouton,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := getTagsFromLocal(tt.service)

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("getTagsFromLocal() mismatch (-want +got)\n%s", diff)
			}
		})
	}
}
