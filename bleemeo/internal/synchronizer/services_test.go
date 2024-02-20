// Copyright 2015-2023 Bleemeo
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
	"glouton/bleemeo/types"
	"testing"
)

func Test_serviceHadSameTags(t *testing.T) {
	tests := []struct {
		name       string
		remoteTags []types.Tag
		localTags  []string
		want       bool
	}{
		{
			name:       "empty-list",
			remoteTags: []types.Tag{},
			localTags:  []string{},
			want:       true,
		},
		{
			name:       "nil-empty",
			remoteTags: nil,
			localTags:  []string{}, want: true,
		},
		{
			name: "one-tag",
			remoteTags: []types.Tag{
				{
					ID:      "dedd669b-f139-4517-a410-4c6d3f66f85f",
					Name:    "tag-one",
					TagType: types.TagTypeIsCreatedByGlouton,
				},
			},
			localTags: []string{
				"tag-one",
			},
			want: true,
		},
		{
			name:       "missing-tag",
			remoteTags: []types.Tag{},
			localTags: []string{
				"tag-one",
			},
			want: false,
		},
		{
			name: "mismatch-tag",
			remoteTags: []types.Tag{
				{
					ID:      "dedd669b-f139-4517-a410-4c6d3f66f85f",
					Name:    "tag-one",
					TagType: types.TagTypeIsCreatedByGlouton,
				},
			},
			localTags: []string{
				"tag-two",
			},
			want: false,
		},
		{
			name: "not-glouton-tag",
			remoteTags: []types.Tag{
				{
					ID:      "dedd669b-f139-4517-a410-4c6d3f66f85f",
					Name:    "tag-one",
					TagType: types.TagTypeIsAutomatic,
				},
			},
			localTags: []string{
				"tag-one",
			},
			want: false,
		},
		{
			name: "truncated-local-tag",
			remoteTags: []types.Tag{
				{
					ID:      "dedd669b-f139-4517-a410-4c6d3f66f85f",
					Name:    "tag-one",
					TagType: types.TagTypeIsCreatedByGlouton,
				},
			},
			localTags: []string{
				"tag-one",
				"tag-longer-than-100-character-are-ignored-since-bleemeo-api-dont-support-them---filler-to-reach-100-char",
			},
			want: true,
		},
	}
	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := serviceHadSameTags(tt.remoteTags, tt.localTags); got != tt.want {
				t.Errorf("serviceHadSameTags() = %v, want %v", got, tt.want)
			}
		})
	}
}
