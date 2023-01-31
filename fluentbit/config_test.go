package fluentbit

import (
	"glouton/config"
	"glouton/facts"
	containerTypes "glouton/facts/container-runtime/types"
	"io"
	"os"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestInputsToFluentBitConfig(t *testing.T) {
	inputs := []input{
		{
			Path:    "/var/log/apache/access.log",
			Runtime: containerTypes.DockerRuntime,
			Filters: []config.LogFilter{
				{
					Metric: "apache_errors_count",
					Regex:  "\\[error\\]",
				},
				{
					Metric: "apache_requests_count",
					Regex:  "GET /",
				},
			},
		},
		{
			Path:    "/var/log/pods/redis1.log,/var/log/pods/redis2.log",
			Runtime: containerTypes.ContainerDRuntime,
			Filters: []config.LogFilter{
				{
					Metric: "redis_logs_count",
					Regex:  ".*",
				},
			},
		},
		{
			Path: "/var/log/uwsgi/uwsgi.log",
			Filters: []config.LogFilter{
				{
					Metric: "uwsgi_logs_count",
					Regex:  ".*",
				},
			},
		},
	}

	expectedFile, err := os.Open("testdata/fluentbit.conf")
	if err != nil {
		t.Fatal(err)
	}

	expectedConfig, err := io.ReadAll(expectedFile)
	if err != nil {
		t.Fatal(err)
	}

	gotConfig := inputsToFluentBitConfig(inputs)

	if diff := cmp.Diff(gotConfig, string(expectedConfig)); diff != "" {
		t.Fatalf("Unexpected config:\n%s", diff)
	}
}

func TestInputLogPaths(t *testing.T) {
	t.Parallel()

	containers := []facts.Container{
		facts.FakeContainer{
			FakeContainerName: "redis-1",
			FakeLabels: map[string]string{
				"app": "redis",
				"env": "prod",
			},
			FakeLogPath: "/redis-1",
		},
		facts.FakeContainer{
			FakeContainerName: "redis-2",
			FakeLabels: map[string]string{
				"app": "redis",
				"env": "prod",
			},
			FakeLogPath: "/redis-2",
		},
		facts.FakeContainer{
			FakeContainerName: "uwsgi-1",
			FakeAnnotations: map[string]string{
				"app": "uwsgi",
				"env": "prod",
			},
			FakeLogPath: "/uwsgi-1",
		},
		facts.FakeContainer{
			FakeContainerName: "uwsgi-2",
			FakeAnnotations: map[string]string{
				"app": "uwsgi",
				"env": "prod",
			},
			FakeLogPath: "/uwsgi-2",
		},
		facts.FakeContainer{
			FakeContainerName: "postgres",
			FakeAnnotations:   map[string]string{"env": "prod"},
			FakeLogPath:       "/postgres",
		},
	}

	tests := []struct {
		Name          string
		Input         config.LogInput
		ExpectedPaths []string
	}{
		{
			Name: "direct-path",
			Input: config.LogInput{
				Path: "/path",
			},
			ExpectedPaths: []string{"/path"},
		},
		{
			Name: "container-name",
			Input: config.LogInput{
				ContainerName: "postgres",
			},
			ExpectedPaths: []string{"/postgres"},
		},
		{
			Name: "select-labels",
			Input: config.LogInput{
				Selectors: []config.LogSelector{
					{
						Name:  "app",
						Value: "redis",
					},
				},
			},
			ExpectedPaths: []string{"/redis-1", "/redis-2"},
		},
		{
			Name: "select-annotations",
			Input: config.LogInput{
				Selectors: []config.LogSelector{
					{
						Name:  "app",
						Value: "uwsgi",
					},
					{
						Name:  "env",
						Value: "prod",
					},
				},
			},
			ExpectedPaths: []string{"/uwsgi-1", "/uwsgi-2"},
		},
		{
			Name: "container-name-and-selector",
			Input: config.LogInput{
				ContainerName: "postgres",
				Selectors: []config.LogSelector{
					{
						Name:  "env",
						Value: "prod",
					},
				},
			},
			ExpectedPaths: []string{"/postgres"},
		},
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			gotLogPaths, _ := inputLogPaths(test.Input, containers)

			if diff := cmp.Diff(test.ExpectedPaths, gotLogPaths); diff != "" {
				t.Fatalf("Unexpected log paths:\n%s", diff)
			}
		})
	}
}
