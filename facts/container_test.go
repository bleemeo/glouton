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
