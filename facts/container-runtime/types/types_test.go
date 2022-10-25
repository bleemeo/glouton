// Copyright 2015-2022 Bleemeo
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

package types_test

import (
	"glouton/config"
	"glouton/facts/container-runtime/types"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestContainerRuntimeAddresses_ExpandAddresses(t *testing.T) {
	tests := []struct {
		name                    string
		containerRuntimeAddress config.ContainerRuntimeAddresses
		hostRoot                string
		want                    []string
	}{
		{
			name: "default containerd on host",
			containerRuntimeAddress: config.ContainerRuntimeAddresses{
				Addresses: []string{
					"/run/containerd/containerd.sock",
					"/run/k3s/containerd/containerd.sock",
				},
				PrefixHostRoot: true,
			},
			hostRoot: "/",
			want: []string{
				"/run/containerd/containerd.sock",
				"/run/k3s/containerd/containerd.sock",
			},
		},
		{
			name: "default containerd on container",
			containerRuntimeAddress: config.ContainerRuntimeAddresses{
				Addresses: []string{
					"/run/containerd/containerd.sock",
					"/run/k3s/containerd/containerd.sock",
				},
				PrefixHostRoot: true,
			},
			hostRoot: "/hostroot",
			want: []string{
				"/run/containerd/containerd.sock",
				"/hostroot/run/containerd/containerd.sock",
				"/run/k3s/containerd/containerd.sock",
				"/hostroot/run/k3s/containerd/containerd.sock",
			},
		},
		{
			name: "default docker on host",
			containerRuntimeAddress: config.ContainerRuntimeAddresses{
				Addresses: []string{
					"",
					"unix:///run/docker.sock",
					"unix:///var/run/docker.sock",
				},
				PrefixHostRoot: true,
			},
			hostRoot: "/",
			want: []string{
				"",
				"unix:///run/docker.sock",
				"unix:///var/run/docker.sock",
			},
		},
		{
			name: "default docker on container",
			containerRuntimeAddress: config.ContainerRuntimeAddresses{
				Addresses: []string{
					"",
					"unix:///run/docker.sock",
					"unix:///var/run/docker.sock",
				},
				PrefixHostRoot: true,
			},
			hostRoot: "/hostroot",
			want: []string{
				"",
				"unix:///run/docker.sock",
				"unix:///hostroot/run/docker.sock",
				"unix:///var/run/docker.sock",
				"unix:///hostroot/var/run/docker.sock",
			},
		},
		{
			name: "docker on container prefix disabled",
			containerRuntimeAddress: config.ContainerRuntimeAddresses{
				Addresses: []string{
					"",
					"unix:///run/docker.sock",
					"unix:///var/run/docker.sock",
				},
				PrefixHostRoot: false,
			},
			hostRoot: "/hostroot",
			want: []string{
				"",
				"unix:///run/docker.sock",
				"unix:///var/run/docker.sock",
			},
		},
		{
			name: "docker custom",
			containerRuntimeAddress: config.ContainerRuntimeAddresses{
				Addresses: []string{
					"unix:///run/docker.sock",
					"",
					"https://localhost:8080",
					"unix://tmp/test",
					"relative/path.sock",
				},
				PrefixHostRoot: true,
			},
			hostRoot: "/hostroot",
			want: []string{
				"unix:///run/docker.sock",
				"unix:///hostroot/run/docker.sock",
				"",
				"https://localhost:8080",
				"unix://tmp/test",
				"unix:///hostroot/tmp/test",
				"relative/path.sock",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := types.ExpandRuntimeAddresses(tt.containerRuntimeAddress, tt.hostRoot)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("ContainerRuntimeAddresses.ExpandAddresses(mismatch (-want +got)\n%s", diff)
			}
		})
	}
}
