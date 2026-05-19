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

package fluentbit

import (
	"io"
	"os"
	"testing"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/facts"
	containerTypes "github.com/bleemeo/glouton/facts/container-runtime/types"

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

const (
	testLabelApp        = "app"
	testLabelEnv        = "env"
	testLabelProd       = "prod"
	testLabelRedis      = "redis"
	testServiceUwsgi    = "uwsgi"
	testServicePostgres = "postgres"
	testPathPostgres    = "/postgres"
	testPathDirect      = "/path"
)

func TestInputLogPaths(t *testing.T) {
	t.Parallel()

	containers := []facts.Container{
		facts.FakeContainer{
			FakeContainerName: "redis-1",
			FakeLabels: map[string]string{
				testLabelApp: testLabelRedis,
				testLabelEnv: testLabelProd,
			},
			FakeLogPath: "/redis-1",
		},
		facts.FakeContainer{
			FakeContainerName: "redis-2",
			FakeLabels: map[string]string{
				testLabelApp: testLabelRedis,
				testLabelEnv: testLabelProd,
			},
			FakeLogPath: "/redis-2",
		},
		facts.FakeContainer{
			FakeContainerName: "uwsgi-1",
			FakeAnnotations: map[string]string{
				testLabelApp: testServiceUwsgi,
				testLabelEnv: testLabelProd,
			},
			FakeLogPath: "/uwsgi-1",
		},
		facts.FakeContainer{
			FakeContainerName: "uwsgi-2",
			FakeAnnotations: map[string]string{
				testLabelApp: testServiceUwsgi,
				testLabelEnv: testLabelProd,
			},
			FakeLogPath: "/uwsgi-2",
		},
		facts.FakeContainer{
			FakeContainerName: testServicePostgres,
			FakeAnnotations:   map[string]string{testLabelEnv: testLabelProd},
			FakeLogPath:       testPathPostgres,
		},
	}

	tests := []struct {
		Name           string
		Input          config.LogInput
		HostRootPrefix string
		ExpectedPaths  []string
	}{
		{
			Name: "direct-path",
			Input: config.LogInput{
				Path: testPathDirect,
			},
			HostRootPrefix: "",
			ExpectedPaths:  []string{testPathDirect},
		},
		{
			Name: "direct-path-with-prefix",
			Input: config.LogInput{
				Path: testPathDirect,
			},
			HostRootPrefix: "/hostroot",
			ExpectedPaths:  []string{"/hostroot" + testPathDirect},
		},
		{
			Name: "container-name",
			Input: config.LogInput{
				ContainerName: testServicePostgres,
			},
			HostRootPrefix: "",
			ExpectedPaths:  []string{testPathPostgres},
		},
		{
			Name: "container-name-with-hostroot",
			Input: config.LogInput{
				ContainerName: testServicePostgres,
			},
			HostRootPrefix: "/hostroot",
			ExpectedPaths:  []string{"/hostroot" + testPathPostgres},
		},
		{
			Name: "select-labels",
			Input: config.LogInput{
				Selectors: map[string]string{testLabelApp: testLabelRedis},
			},
			HostRootPrefix: "",
			ExpectedPaths:  []string{"/redis-1", "/redis-2"},
		},
		{
			Name: "select-annotations",
			Input: config.LogInput{
				Selectors: map[string]string{
					testLabelApp: testServiceUwsgi,
					testLabelEnv: testLabelProd,
				},
			},
			HostRootPrefix: "",
			ExpectedPaths:  []string{"/uwsgi-1", "/uwsgi-2"},
		},
		{
			Name: "container-name-and-selector",
			Input: config.LogInput{
				ContainerName: testServicePostgres,
				Selectors:     map[string]string{testLabelEnv: testLabelProd},
			},
			HostRootPrefix: "",
			ExpectedPaths:  []string{testPathPostgres},
		},
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			m := Manager{
				config: config.Log{
					HostRootPrefix: test.HostRootPrefix,
				},
			}

			gotLogPaths, _ := m.inputLogPaths(test.Input, containers)

			if diff := cmp.Diff(test.ExpectedPaths, gotLogPaths); diff != "" {
				t.Fatalf("Unexpected log paths:\n%s", diff)
			}
		})
	}
}
