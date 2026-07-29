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
var testCount = map[string]config.LogMetricsCount{"errors_count": {"conditions": []any{`IsMatch(body, "\\[error\\]")`}}}

// TestIsContainerWatched ports the container-matching scenarios previously
// covered by fluentbit.Manager.inputLogPaths: since log.metrics.count is
// global, watching is now a pure yes/no decision.
func TestIsContainerWatched(t *testing.T) {
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
		Name          string
		Cfg           config.Log
		ContainerName string
		Expected      bool
	}{
		{
			Name: "matches-by-container-name",
			Cfg: config.Log{Metrics: config.LogMetricsConfig{
				ContainerCounters: []string{testServicePostgres},
			}},
			ContainerName: testServicePostgres,
			Expected:      true,
		},
		{
			Name: "matches-by-label-selector",
			Cfg: config.Log{Metrics: config.LogMetricsConfig{
				ContainerSelectorCounters: []config.ContainerSelectorRule{
					{Selectors: map[string]string{testLabelApp: testLabelRedis}},
				},
			}},
			ContainerName: "redis-1",
			Expected:      true,
		},
		{
			Name: "matches-by-annotation-selector",
			Cfg: config.Log{Metrics: config.LogMetricsConfig{
				ContainerSelectorCounters: []config.ContainerSelectorRule{
					{
						Selectors: map[string]string{
							testLabelApp: testServiceUwsgi,
							testLabelEnv: testLabelProd,
						},
					},
				},
			}},
			ContainerName: "uwsgi-1",
			Expected:      true,
		},
		{
			Name: "container-name-and-selector-both-required",
			Cfg: config.Log{Metrics: config.LogMetricsConfig{
				ContainerSelectorCounters: []config.ContainerSelectorRule{
					{
						ContainerName: testServicePostgres,
						Selectors:     map[string]string{testLabelEnv: testLabelProd},
					},
				},
			}},
			ContainerName: testServicePostgres,
			Expected:      true,
		},
		{
			Name: "container-name-and-selector-both-required-name-mismatch",
			Cfg: config.Log{Metrics: config.LogMetricsConfig{
				ContainerSelectorCounters: []config.ContainerSelectorRule{
					{
						ContainerName: "some-other-name",
						Selectors:     map[string]string{testLabelEnv: testLabelProd},
					},
				},
			}},
			ContainerName: testServicePostgres,
			Expected:      false,
		},
		{
			Name: "no-match",
			Cfg: config.Log{Metrics: config.LogMetricsConfig{
				ContainerCounters: []string{testServicePostgres},
			}},
			ContainerName: "unrelated",
			Expected:      false,
		},
		{
			Name: "new-style-container-counters-config",
			Cfg: config.Log{Metrics: config.LogMetricsConfig{
				ContainerCounters: []string{"new-style"},
			}},
			ContainerName: "new-style",
			Expected:      true,
		},
		{
			Name:          "no-config-at-all-never-matches",
			Cfg:           config.Log{},
			ContainerName: "unrelated",
			Expected:      false,
		},
		{
			Name: "container-exclude-by-name-overrides-container-counters",
			Cfg: config.Log{Metrics: config.LogMetricsConfig{
				ContainerCounters: []string{testServicePostgres},
				ContainerExclude:  []config.ContainerExcludeRule{{ContainerName: testServicePostgres}},
			}},
			ContainerName: testServicePostgres,
			Expected:      false,
		},
		{
			Name: "container-exclude-by-selector-overrides-container-selector-counters",
			Cfg: config.Log{Metrics: config.LogMetricsConfig{
				ContainerSelectorCounters: []config.ContainerSelectorRule{
					{Selectors: map[string]string{testLabelApp: testLabelRedis}},
				},
				ContainerExclude: []config.ContainerExcludeRule{
					{Selectors: map[string]string{testLabelApp: testLabelRedis}},
				},
			}},
			ContainerName: "redis-1",
			Expected:      false,
		},
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			t.Parallel()

			if got := isContainerWatched(test.Cfg, containers[test.ContainerName]); got != test.Expected {
				t.Errorf("Expected %v, got %v", test.Expected, got)
			}
		})
	}
}

// TestHasContainerCounters checks that container watching requires both a
// metric and a selection mechanism to be configured.
func TestHasContainerCounters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		Name     string
		Cfg      config.Log
		Expected bool
	}{
		{
			Name:     "nothing-configured",
			Cfg:      config.Log{},
			Expected: false,
		},
		{
			Name: "container-selector-counters-but-no-count",
			Cfg: config.Log{Metrics: config.LogMetricsConfig{
				ContainerSelectorCounters: []config.ContainerSelectorRule{
					{Selectors: map[string]string{"app": "redis"}},
				},
			}},
			Expected: false,
		},
		{
			Name: "count-but-no-container-selection",
			Cfg: config.Log{Metrics: config.LogMetricsConfig{
				Count: testCount,
			}},
			Expected: false,
		},
		{
			Name: "count-and-container-counters",
			Cfg: config.Log{Metrics: config.LogMetricsConfig{
				Count:             testCount,
				ContainerCounters: []string{"redis"},
			}},
			Expected: true,
		},
		{
			Name: "count-and-container-selector-counters",
			Cfg: config.Log{Metrics: config.LogMetricsConfig{
				Count: testCount,
				ContainerSelectorCounters: []config.ContainerSelectorRule{
					{Selectors: map[string]string{"app": "redis"}},
				},
			}},
			Expected: true,
		},
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			t.Parallel()

			if got := hasContainerCounters(test.Cfg); got != test.Expected {
				t.Errorf("Expected %v, got %v", test.Expected, got)
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
