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
	t.Parallel()

	tests := []struct {
		Name           string
		Inputs         []input
		ExpectedConfig string
	}{
		{
			Name: "full-config",
			Inputs: []input{
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
			},
			ExpectedConfig: "testdata/fluentbit.conf",
		},
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			expectedFile, err := os.Open(test.ExpectedConfig)
			if err != nil {
				t.Fatal(err)
			}

			expectedConfig, err := io.ReadAll(expectedFile)
			if err != nil {
				t.Fatal(err)
			}

			gotConfig := inputsToFluentBitConfig(test.Inputs)

			if diff := cmp.Diff(gotConfig, string(expectedConfig)); diff != "" {
				t.Fatalf("Unexpected config:\n%s", diff)
			}
		})
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
		Name           string
		Input          config.LogInput
		PrefixHostRoot bool
		ExpectedPaths  []string
	}{
		{
			Name: "direct-path",
			Input: config.LogInput{
				Path: "/path",
			},
			PrefixHostRoot: false,
			ExpectedPaths:  []string{"/path"},
		},
		{
			Name: "direct-path-with-prefix",
			Input: config.LogInput{
				Path: "/path",
			},
			PrefixHostRoot: true,
			ExpectedPaths:  []string{"/hostroot/path"},
		},
		{
			Name: "container-name",
			Input: config.LogInput{
				ContainerName: "postgres",
			},
			PrefixHostRoot: false,
			ExpectedPaths:  []string{"/postgres"},
		},
		{
			Name: "container-name-with-hostroot",
			Input: config.LogInput{
				ContainerName: "postgres",
			},
			PrefixHostRoot: true,
			ExpectedPaths:  []string{"/hostroot/postgres"},
		},
		{
			Name: "select-labels",
			Input: config.LogInput{
				Selectors: map[string]string{"app": "redis"},
			},
			PrefixHostRoot: false,
			ExpectedPaths:  []string{"/redis-1", "/redis-2"},
		},
		{
			Name: "select-annotations",
			Input: config.LogInput{
				Selectors: map[string]string{
					"app": "uwsgi",
					"env": "prod",
				},
			},
			PrefixHostRoot: false,
			ExpectedPaths:  []string{"/uwsgi-1", "/uwsgi-2"},
		},
		{
			Name: "container-name-and-selector",
			Input: config.LogInput{
				ContainerName: "postgres",
				Selectors:     map[string]string{"env": "prod"},
			},
			PrefixHostRoot: false,
			ExpectedPaths:  []string{"/postgres"},
		},
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			m := Manager{
				config: config.Log{
					PrefixHostRoot: test.PrefixHostRoot,
				},
			}

			gotLogPaths, _ := m.inputLogPaths(test.Input, containers)

			if diff := cmp.Diff(test.ExpectedPaths, gotLogPaths); diff != "" {
				t.Fatalf("Unexpected log paths:\n%s", diff)
			}
		})
	}
}
