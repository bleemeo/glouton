// Copyright 2015-2026 Bleemeo
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

package logmetrics

import (
	"testing"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/facts"

	"github.com/google/go-cmp/cmp"
)

const (
	testLabelApp        = "app"
	testLabelEnv        = "env"
	testLabelProd       = "prod"
	testLabelRedis      = "redis"
	testServiceUwsgi    = "uwsgi"
	testServicePostgres = "postgres"
)

//nolint:gochecknoglobals
var testFilterErrors = []config.LogFilter{{Metric: "errors_count", Regex: `\[error\]`}}

// TestResolveContainerFilters ports the container-matching scenarios that used to be
// covered by fluentbit.Manager.inputLogPaths (fluentbit/config_test.go), since the
// same container_name/container_selectors matching now lives here.
func TestResolveContainerFilters(t *testing.T) {
	t.Parallel()

	containers := map[string]facts.Container{
		"redis-1": facts.FakeContainer{
			FakeContainerName: "redis-1",
			FakeLabels: map[string]string{
				testLabelApp: testLabelRedis,
				testLabelEnv: testLabelProd,
			},
		},
		"uwsgi-1": facts.FakeContainer{
			FakeContainerName: "uwsgi-1",
			FakeAnnotations: map[string]string{
				testLabelApp: testServiceUwsgi,
				testLabelEnv: testLabelProd,
			},
		},
		testServicePostgres: facts.FakeContainer{
			FakeContainerName: testServicePostgres,
			FakeAnnotations:   map[string]string{testLabelEnv: testLabelProd},
		},
		"unrelated": facts.FakeContainer{
			FakeContainerName: "unrelated",
		},
		"new-style": facts.FakeContainer{
			FakeContainerName: "new-style",
		},
	}

	tests := []struct {
		Name            string
		Cfg             config.Log
		ContainerName   string
		ExpectedFilters []config.LogFilter
	}{
		{
			Name: "matches-by-container-name",
			Cfg: config.Log{Inputs: []config.LogInput{
				{ContainerName: testServicePostgres, Filters: testFilterErrors},
			}},
			ContainerName:   testServicePostgres,
			ExpectedFilters: testFilterErrors,
		},
		{
			Name: "matches-by-label-selector",
			Cfg: config.Log{Inputs: []config.LogInput{
				{Selectors: map[string]string{testLabelApp: testLabelRedis}, Filters: testFilterErrors},
			}},
			ContainerName:   "redis-1",
			ExpectedFilters: testFilterErrors,
		},
		{
			Name: "matches-by-annotation-selector",
			Cfg: config.Log{Inputs: []config.LogInput{
				{
					Selectors: map[string]string{
						testLabelApp: testServiceUwsgi,
						testLabelEnv: testLabelProd,
					},
					Filters: testFilterErrors,
				},
			}},
			ContainerName:   "uwsgi-1",
			ExpectedFilters: testFilterErrors,
		},
		{
			Name: "container-name-and-selector-both-required",
			Cfg: config.Log{Inputs: []config.LogInput{
				{
					ContainerName: testServicePostgres,
					Selectors:     map[string]string{testLabelEnv: testLabelProd},
					Filters:       testFilterErrors,
				},
			}},
			ContainerName:   testServicePostgres,
			ExpectedFilters: testFilterErrors,
		},
		{
			Name: "no-match",
			Cfg: config.Log{Inputs: []config.LogInput{
				{ContainerName: testServicePostgres, Filters: testFilterErrors},
			}},
			ContainerName:   "unrelated",
			ExpectedFilters: nil,
		},
		{
			Name: "path-based-inputs-are-ignored",
			Cfg: config.Log{Inputs: []config.LogInput{
				{Path: "/var/log/foo.log", Filters: testFilterErrors},
			}},
			ContainerName:   testServicePostgres,
			ExpectedFilters: nil,
		},
		{
			Name: "new-style-container-filters-config",
			Cfg: config.Log{Metrics: config.LogMetricsConfig{
				KnownFilters:     map[string][]config.LogFilter{"grp": testFilterErrors},
				ContainerFilters: map[string]string{"new-style": "grp"},
			}},
			ContainerName:   "new-style",
			ExpectedFilters: testFilterErrors,
		},
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			t.Parallel()

			got := resolveContainerFilters(test.Cfg, containers[test.ContainerName])

			if diff := cmp.Diff(test.ExpectedFilters, got); diff != "" {
				t.Fatalf("Unexpected filters:\n%s", diff)
			}
		})
	}
}

func TestResolveContainerLogPath(t *testing.T) {
	t.Parallel()

	ctr := facts.FakeContainer{FakeLogPath: "/var/lib/docker/containers/abc/abc-json.log"}

	if got := resolveContainerLogPath(ctr, "/"); got != ctr.LogPath() {
		t.Errorf("Expected %q, got %q", ctr.LogPath(), got)
	}

	if got, want := resolveContainerLogPath(ctr, "/hostroot"), "/hostroot"+ctr.LogPath(); got != want {
		t.Errorf("Expected %q, got %q", want, got)
	}

	if got := resolveContainerLogPath(facts.FakeContainer{}, "/"); got != "" {
		t.Errorf("Expected empty path for a container without a log file, got %q", got)
	}
}
