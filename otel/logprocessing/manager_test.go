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

package logprocessing

import (
	"maps"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/discovery"
	"github.com/bleemeo/glouton/facts"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

func svc(
	name, instance, containerID string,
	active bool,
	lastTimeSeen time.Time,
	logProcessing ...discovery.ServiceLogReceiver,
) discovery.Service {
	return discovery.Service{
		Name:          name,
		Instance:      instance,
		ContainerID:   containerID,
		Active:        active,
		LogProcessing: logProcessing,
		LastTimeSeen:  lastTimeSeen,
	}
}

func ctr(id, name string, labels, annotations map[string]string) facts.Container { //nolint: unparam
	return facts.FakeContainer{
		FakeID:            id,
		FakeContainerName: name,
		FakeLabels:        labels,
		FakeAnnotations:   annotations,
	}
}

func logSourceComparer(x, y logSource) bool {
	if (x.container == nil || y.container == nil) && x.container != y.container {
		return false
	}

	if x.container != nil && y.container != nil {
		if x.container.ID() != y.container.ID() {
			return false
		}

		if x.container.ImageID() != y.container.ImageID() {
			return false
		}
	}

	if !reflect.DeepEqual(x.serviceID, y.serviceID) {
		// serviceID is a basic type, so we can delegate this work to reflect
		return false
	}

	if x.logFilePath != y.logFilePath {
		return false
	}

	if !reflect.DeepEqual(x.operators, y.operators) {
		// operators are just basic types, so we can delegate this work to reflect
		return false
	}

	if !reflect.DeepEqual(x.filters, y.filters) {
		return false
	}

	return true
}

func TestProcessLogSources(t *testing.T) {
	t.Parallel()

	knownLogFormats := map[string][]config.OTELOperator{
		"nginx_both": {
			{
				"type": "op",
				"prop": "value",
				// and so on
			},
		},
		"apache_access": {
			{
				"type": "op",
				"prop": "value access",
			},
		},
		"apache_error": {
			{
				"type": "op",
				"prop": "value error",
			},
		},
		"custom_app_fmt": {
			{
				"type":  "custom-op",
				"field": 1,
			},
		},
	}
	knownLogFilters := map[string]config.OTELFilters{
		"drop_get": {
			"exclude": map[string]any{
				"match_type": "regexp",
				"bodies": []string{
					"GET",
				},
			},
		},
		"no_password": {
			"log_record": []string{
				".*password.*",
			},
		},
		"min_level_info": {
			"include": map[string]any{
				"severity_number": map[string]any{
					"min": "9",
				},
			},
		},
	}

	containerOperators := map[string]string{}
	containerFilters := map[string]string{
		"Custom-App-2": "min_level_info",
	}

	svcNginx := svc("nginx", "Nginx-1", "ngx-1", true, time.Now(), discovery.ServiceLogReceiver{Format: "nginx_both", Filter: "drop_get"})

	ctrNgx1 := ctr("ngx-1", "Nginx-1", nil, nil)
	ctrDisabled := ctr("disabled", "Disabled", map[string]string{"glouton.log_enable": "False"}, nil)
	ctrApp1 := ctr("app-1", "Custom-App-1", map[string]string{"glouton.log_format": "custom_app_fmt", "glouton.log_filter": "no_password"}, nil)
	ctrApp2 := ctr("app-2", "Custom-App-2", nil, nil)

	executionSteps := []struct {
		name                      string
		containers                []facts.Container
		services                  []discovery.Service
		expectedLogSources        []logSource
		expectedWatchedServices   map[discovery.NameInstance]struct{}
		expectedWatchedContainers map[string]struct{} // map key: container ID
	}{
		{
			name: "an nginx service in a container and a container with log disabled",
			containers: []facts.Container{
				ctrNgx1,
				ctrDisabled,
			},
			services: []discovery.Service{
				svcNginx,
			},
			expectedLogSources: []logSource{
				{
					container: ctrNgx1,
					serviceID: &discovery.NameInstance{Name: "nginx", Instance: "Nginx-1"},
					operators: append(
						[]config.OTELOperator{
							{
								"field": "resource['service.name']",
								"type":  "add",
								"value": "nginx",
							},
						},
						knownLogFormats["nginx_both"]...,
					),
					filters: knownLogFilters["drop_get"],
				},
			},
			expectedWatchedServices: map[discovery.NameInstance]struct{}{
				{Name: "nginx", Instance: "Nginx-1"}: {},
			},
			expectedWatchedContainers: map[string]struct{}{
				"ngx-1": {},
			},
		},
		{
			name: "and a custom app in a container",
			containers: []facts.Container{
				ctrNgx1,
				ctrApp1, // new
			},
			services: []discovery.Service{
				svcNginx,
			},
			expectedLogSources: []logSource{
				{
					container: ctrApp1,
					operators: knownLogFormats["custom_app_fmt"],
					filters:   knownLogFilters["no_password"],
				},
			},
			expectedWatchedServices: map[discovery.NameInstance]struct{}{
				{Name: "nginx", Instance: "Nginx-1"}: {}, // still present
			},
			expectedWatchedContainers: map[string]struct{}{
				"ngx-1": {}, // still present
				"app-1": {},
			},
		},
		{
			name: "with a non-active service",
			containers: []facts.Container{
				ctrNgx1,
				ctrApp1,
			},
			services: []discovery.Service{
				svcNginx,
				svc("old", "outdated", "", false, time.Now().Add(-365*24*time.Hour)),
			},
			expectedLogSources: nil, // thus nothing
			expectedWatchedServices: map[discovery.NameInstance]struct{}{
				{Name: "nginx", Instance: "Nginx-1"}: {}, // still present
			},
			expectedWatchedContainers: map[string]struct{}{
				"ngx-1": {}, // still present
				"app-1": {}, // still present
			},
		},
		{
			name: "no more nginx but an apache running on the host",
			containers: []facts.Container{
				ctrApp1,
			},
			services: []discovery.Service{
				svc(
					"apache", "", "", true, time.Now(),
					discovery.ServiceLogReceiver{FilePath: "/var/log/apache2/access.log", Format: "apache_access"},
					discovery.ServiceLogReceiver{FilePath: "/var/log/apache2/error.log", Format: "apache_error"},
				),
			},
			expectedLogSources: []logSource{
				{
					serviceID:   &discovery.NameInstance{Name: "apache", Instance: ""},
					logFilePath: "/var/log/apache2/access.log",
					operators: append(
						[]config.OTELOperator{
							{
								"field": "resource['service.name']",
								"type":  "add",
								"value": "apache",
							},
						},
						knownLogFormats["apache_access"]...,
					),
				},
				{
					serviceID:   &discovery.NameInstance{Name: "apache", Instance: ""},
					logFilePath: "/var/log/apache2/error.log",
					operators: append(
						[]config.OTELOperator{
							{
								"field": "resource['service.name']",
								"type":  "add",
								"value": "apache",
							},
						},
						knownLogFormats["apache_error"]...,
					),
				},
			},
			expectedWatchedServices: map[discovery.NameInstance]struct{}{
				{Name: "nginx", Instance: "Nginx-1"}: {}, // would've been removed if removeOldSources() had been run
				{Name: "apache", Instance: ""}:       {},
			},
			expectedWatchedContainers: map[string]struct{}{
				"ngx-1": {}, // would've been removed if removeOldSources() had been run
				"app-1": {}, // still present
			},
		},
		{
			name: "no more apache but another custom application in a container",
			containers: []facts.Container{
				ctrApp1,
				ctrApp2,
			},
			services: []discovery.Service{},
			expectedLogSources: []logSource{
				{
					container: ctrApp2,
					filters:   knownLogFilters["min_level_info"],
				},
			},
			expectedWatchedServices: map[discovery.NameInstance]struct{}{
				{Name: "nginx", Instance: "Nginx-1"}: {}, // would've been removed if removeOldSources() had been run
				{Name: "apache", Instance: ""}:       {}, // same
			},
			expectedWatchedContainers: map[string]struct{}{
				"ngx-1": {}, // would've been removed if removeOldSources() had been run
				"app-1": {}, // still present
				"app-2": {},
			},
		},
	}

	logMan := &Manager{
		config: config.OpenTelemetry{
			KnownLogFormats: knownLogFormats,
			ContainerFormat: containerOperators,
			KnownLogFilters: knownLogFilters,
			ContainerFilter: containerFilters,
		},
		knownLogFormats:   knownLogFormats,
		containerRecv:     newContainerReceiver(&pipelineContext{}, containerOperators, knownLogFormats, containerFilters, knownLogFilters),
		watchedServices:   make(map[discovery.NameInstance]sourceDiagnostic),
		watchedContainers: make(map[string]sourceDiagnostic),
	}

	for _, step := range executionSteps {
		logSources := logMan.processLogSources(step.services, step.containers)
		if diff := cmp.Diff(step.expectedLogSources, logSources, cmp.Comparer(logSourceComparer)); diff != "" {
			t.Fatalf("Unexpected log sources at step %q (-want +got):\n%s", step.name, diff)
		}

		// We don't check the content of expectedWatchedServices's sourceDiagnostic. Only it's existence
		expectedKeys := slices.Collect(maps.Keys(step.expectedWatchedServices))
		gotKeys := slices.Collect(maps.Keys(logMan.watchedServices))

		// The SortSlices assume we don't have two identical name with different instance.
		if diff := cmp.Diff(expectedKeys, gotKeys, cmpopts.SortSlices(func(x, y discovery.NameInstance) bool { return x.Name < y.Name })); diff != "" {
			t.Fatalf("Unexpected watched services at step %q (-want +got):\n%s", step.name, diff)
		}

		expectedKeys2 := slices.Collect(maps.Keys(step.expectedWatchedContainers))
		gotKeys2 := slices.Collect(maps.Keys(logMan.watchedContainers))

		if diff := cmp.Diff(expectedKeys2, gotKeys2, cmpopts.SortSlices(func(x, y string) bool { return x < y })); diff != "" {
			t.Fatalf("Unexpected watched containers at step %q (-want +got):\n%s", step.name, diff)
		}
	}
}
