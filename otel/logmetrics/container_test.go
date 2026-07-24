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
var testCounterErrors = []config.LogCounter{{Metric: "errors_count", Regex: `\[error\]`}}

// TestResolveContainerCounters ports the container-matching scenarios that used to be
// covered by fluentbit.Manager.inputLogPaths (fluentbit/config_test.go), since the
// same container_name/container_selectors matching now lives here.
func TestResolveContainerCounters(t *testing.T) {
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
		"label-selected": facts.FakeContainer{
			FakeContainerName: "label-selected",
			FakeLabels:        map[string]string{containerLogCounterLabel: "grp-label"},
		},
		"label-unknown-falls-back": facts.FakeContainer{
			FakeContainerName: "label-unknown-falls-back",
			FakeLabels:        map[string]string{containerLogCounterLabel: "nonexistent-group"},
		},
	}

	tests := []struct {
		Name             string
		Cfg              config.Log
		ContainerName    string
		ExpectedCounters []config.LogCounter
	}{
		{
			Name: "matches-by-container-name",
			Cfg: config.Log{Inputs: []config.LogInput{
				{ContainerName: testServicePostgres, Counters: testCounterErrors},
			}},
			ContainerName:    testServicePostgres,
			ExpectedCounters: testCounterErrors,
		},
		{
			Name: "matches-by-label-selector",
			Cfg: config.Log{Inputs: []config.LogInput{
				{Selectors: map[string]string{testLabelApp: testLabelRedis}, Counters: testCounterErrors},
			}},
			ContainerName:    "redis-1",
			ExpectedCounters: testCounterErrors,
		},
		{
			Name: "matches-by-annotation-selector",
			Cfg: config.Log{Inputs: []config.LogInput{
				{
					Selectors: map[string]string{
						testLabelApp: testServiceUwsgi,
						testLabelEnv: testLabelProd,
					},
					Counters: testCounterErrors,
				},
			}},
			ContainerName:    "uwsgi-1",
			ExpectedCounters: testCounterErrors,
		},
		{
			Name: "container-name-and-selector-both-required",
			Cfg: config.Log{Inputs: []config.LogInput{
				{
					ContainerName: testServicePostgres,
					Selectors:     map[string]string{testLabelEnv: testLabelProd},
					Counters:      testCounterErrors,
				},
			}},
			ContainerName:    testServicePostgres,
			ExpectedCounters: testCounterErrors,
		},
		{
			Name: "no-match",
			Cfg: config.Log{Inputs: []config.LogInput{
				{ContainerName: testServicePostgres, Counters: testCounterErrors},
			}},
			ContainerName:    "unrelated",
			ExpectedCounters: nil,
		},
		{
			Name: "path-based-inputs-are-ignored",
			Cfg: config.Log{Inputs: []config.LogInput{
				{Path: "/var/log/foo.log", Counters: testCounterErrors},
			}},
			ContainerName:    testServicePostgres,
			ExpectedCounters: nil,
		},
		{
			Name: "new-style-container-counters-config",
			Cfg: config.Log{Metrics: config.LogMetricsConfig{
				KnownCounters:     map[string][]config.LogCounter{"grp": testCounterErrors},
				ContainerCounters: map[string]string{"new-style": "grp"},
			}},
			ContainerName:    "new-style",
			ExpectedCounters: testCounterErrors,
		},
		{
			// Mirrors otel/logprocessing's glouton.log_filter/glouton.log_format
			// container-label handling.
			Name: "glouton-log-counter-label-selects-known-counters",
			Cfg: config.Log{Metrics: config.LogMetricsConfig{
				KnownCounters: map[string][]config.LogCounter{"grp-label": testCounterErrors},
			}},
			ContainerName:    "label-selected",
			ExpectedCounters: testCounterErrors,
		},
		{
			Name: "glouton-log-counter-label-takes-precedence-over-static-config-map",
			Cfg: config.Log{Metrics: config.LogMetricsConfig{
				KnownCounters: map[string][]config.LogCounter{
					"grp-label":  testCounterErrors,
					"grp-static": {{Metric: "other_count", Regex: "other"}},
				},
				ContainerCounters: map[string]string{"label-selected": "grp-static"},
			}},
			ContainerName:    "label-selected",
			ExpectedCounters: testCounterErrors,
		},
		{
			Name: "unknown-glouton-log-counter-label-falls-back-to-static-config-map",
			Cfg: config.Log{Metrics: config.LogMetricsConfig{
				KnownCounters:     map[string][]config.LogCounter{"grp-static": testCounterErrors},
				ContainerCounters: map[string]string{"label-unknown-falls-back": "grp-static"},
			}},
			ContainerName:    "label-unknown-falls-back",
			ExpectedCounters: testCounterErrors,
		},
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			t.Parallel()

			got := resolveContainerCounters(test.Cfg, containers[test.ContainerName])

			if diff := cmp.Diff(test.ExpectedCounters, got); diff != "" {
				t.Fatalf("Unexpected counters:\n%s", diff)
			}
		})
	}
}

// TestHasContainerCounters is the regression test for a purely label-driven
// setup (glouton.log_counter labels + known_counters, no static
// container_counters entry, no container-based log.inputs entry): container
// watching must still be enabled, otherwise resolveContainerCounters' label
// lookup is never reached at all.
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
			Name: "legacy-container-name-input",
			Cfg: config.Log{Inputs: []config.LogInput{
				{ContainerName: "redis", Counters: testCounterErrors},
			}},
			Expected: true,
		},
		{
			Name: "static-container-counters-map",
			Cfg: config.Log{Metrics: config.LogMetricsConfig{
				ContainerCounters: map[string]string{"ctr": "grp"},
			}},
			Expected: true,
		},
		{
			Name: "known-counters-only-label-driven",
			Cfg: config.Log{Metrics: config.LogMetricsConfig{
				KnownCounters: map[string][]config.LogCounter{"grp-label": testCounterErrors},
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
