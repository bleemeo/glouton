package types_test

import (
	"glouton/config2"
	"glouton/facts/container-runtime/types"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestContainerRuntimeAddresses_ExpandAddresses(t *testing.T) {
	tests := []struct {
		name                    string
		containerRuntimeAddress config2.ContainerRuntimeAddresses
		hostRoot                string
		want                    []string
	}{
		{
			name: "default containerd on host",
			containerRuntimeAddress: config2.ContainerRuntimeAddresses{
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
			containerRuntimeAddress: config2.ContainerRuntimeAddresses{
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
			containerRuntimeAddress: config2.ContainerRuntimeAddresses{
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
			containerRuntimeAddress: config2.ContainerRuntimeAddresses{
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
			containerRuntimeAddress: config2.ContainerRuntimeAddresses{
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
			containerRuntimeAddress: config2.ContainerRuntimeAddresses{
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
