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

package facts

import (
	"reflect"
	"testing"
)

func TestContainerEnabled(t *testing.T) {
	tests := []struct {
		name         string
		filter       ContainerFilter
		container    FakeContainer
		wantEnabled  bool
		wantExplicit bool
	}{
		{
			name: "Default",
			container: FakeContainer{
				FakeContainerName: "does-not-matter",
			},
			wantEnabled:  true,
			wantExplicit: false,
		},
		{
			name: "with labels",
			container: FakeContainer{
				FakeLabels: map[string]string{
					"some-label": "value",
				},
			},
			wantEnabled:  true,
			wantExplicit: false,
		},
		{
			name: "with labels and annotations",
			container: FakeContainer{
				FakeLabels: map[string]string{
					"some-label": "value",
				},
				FakeAnnotations: map[string]string{
					"kubernetes.io/annoation": "yes",
				},
			},
			wantEnabled:  true,
			wantExplicit: false,
		},
		{
			name: "legacy-ignore",
			container: FakeContainer{
				FakeLabels: map[string]string{
					"bleemeo.enable": "false",
				},
			},
			wantEnabled:  false,
			wantExplicit: true,
		},
		{
			name: "ignore",
			container: FakeContainer{
				FakeLabels: map[string]string{
					"glouton.enable": "false",
				},
			},
			wantEnabled:  false,
			wantExplicit: true,
		},
		{
			name: "explicit-enable",
			container: FakeContainer{
				FakeLabels: map[string]string{
					"glouton.enable": "true",
				},
			},
			wantEnabled:  true,
			wantExplicit: true,
		},
		{
			name: "annotation-enable",
			container: FakeContainer{
				FakeLabels: map[string]string{
					"glouton.enable": "false",
				},
				FakeAnnotations: map[string]string{
					"glouton.enable": "on",
				},
			},
			wantEnabled:  true,
			wantExplicit: true,
		},
		{
			name: "config",
			container: FakeContainer{
				FakeContainerName: "does-not-matter",
			},
			filter: ContainerFilter{
				DisabledByDefault: true,
			},
			wantEnabled:  false,
			wantExplicit: false,
		},
		{
			name: "config-allow-list",
			container: FakeContainer{
				FakeContainerName: "does-not-matter",
			},
			filter: ContainerFilter{
				DisabledByDefault: true,
				AllowList:         []string{"does-not-matter"},
			},
			wantEnabled:  true,
			wantExplicit: true,
		},
		{
			name: "config-allow-list-glob",
			container: FakeContainer{
				FakeContainerName: "does-not-matter",
			},
			filter: ContainerFilter{
				DisabledByDefault: false,
				AllowList:         []string{"does*"},
			},
			wantEnabled:  true,
			wantExplicit: true,
		},
		{
			name: "config-allow-list-glob-2",
			container: FakeContainer{
				FakeContainerName: "another",
			},
			filter: ContainerFilter{
				DisabledByDefault: false,
				AllowList:         []string{"does*"},
			},
			wantEnabled:  true,
			wantExplicit: false,
		},
		{
			name: "config-allow-deny-list",
			container: FakeContainer{
				FakeContainerName: "does-not-matter",
			},
			filter: ContainerFilter{
				DisabledByDefault: false,
				AllowList:         []string{"does*"},
				DenyList:          []string{"does-not-matter"},
			},
			wantEnabled:  false,
			wantExplicit: true,
		},
	}
	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			gotEnabled, gotExplicit := tt.filter.ContainerEnabled(tt.container)
			if gotEnabled != tt.wantEnabled {
				t.Errorf("ContainerEnabled() gotEnabled = %v, want %v", gotEnabled, tt.wantEnabled)
			}
			if gotExplicit != tt.wantExplicit {
				t.Errorf("ContainerEnabled() gotExplicit = %v, want %v", gotExplicit, tt.wantExplicit)
			}

			got := tt.filter.ContainerIgnored(tt.container)
			if got != !tt.wantEnabled {
				t.Errorf("ContainerIgnored() = %v, want %v", got, !tt.wantEnabled)
			}
		})
	}
}

func TestContainerIgnoredPorts(t *testing.T) {
	tests := []struct {
		name      string
		container FakeContainer
		want      map[int]bool
	}{
		{
			name: "No label",
			container: FakeContainer{
				FakeID:            "1234",
				FakeContainerName: "test",
			},
			want: map[int]bool{},
		},
		{
			name: "ignore-443",
			container: FakeContainer{
				FakeID:            "1234",
				FakeContainerName: "test",
				FakeLabels: map[string]string{
					"glouton.check.ignore.port.443": "true",
				},
			},
			want: map[int]bool{
				443: true,
			},
		},
		{
			name: "unknown-labels",
			container: FakeContainer{
				FakeID:            "1234",
				FakeContainerName: "test",
				FakeLabels: map[string]string{
					"prometheus.io/scrape-port=443": "true",
					"check.ignore.port.443":         "true",
					"port":                          "443",
				},
			},
			want: map[int]bool{},
		},
		{
			name: "multiple-ignore",
			container: FakeContainer{
				FakeID:            "1234",
				FakeContainerName: "test",
				FakeLabels: map[string]string{
					"glouton.check.ignore.port.1000": "true",
					"glouton.check.ignore.port.1001": "tRuE",
					"glouton.check.ignore.port.1002": "on",
					"glouton.check.ignore.port.1003": "1",
				},
			},
			want: map[int]bool{
				1000: true,
				1001: true,
				1002: true,
				1003: true,
			},
		},
		{
			name: "with-ignore-and-not-ignore",
			container: FakeContainer{
				FakeID:            "1234",
				FakeContainerName: "test",
				FakeLabels: map[string]string{
					"glouton.check.ignore.port.1000": "true",
					"glouton.check.ignore.port.1001": "faLse",
					"glouton.check.ignore.port.1002": "oFf",
					"glouton.check.ignore.port.1003": "0",
					"another-label":                  "unread",
				},
			},
			want: map[int]bool{
				1000: true,
				1001: false,
				1002: false,
				1003: false,
			},
		},
	}
	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			if got := ContainerIgnoredPorts(tt.container); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ContainerIgnoredPorts() = %v, want %v", got, tt.want)
			}
		})
	}
}
