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

func ctr(id, name string, labels, annotations map[string]string) facts.Container {
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
		return false
	}

	if !reflect.DeepEqual(x.filters, y.filters) {
		return false
	}

	return true
}

// TestProcessLogSources tests deriving log sources from discovered services, in containers or as bare processes.
func TestProcessLogSources(t *testing.T) {
	t.Parallel()

	knownLogFormats := map[string][]config.OTELOperator{
		"nginx_both": {
			{
				testFieldType: "op",
				testProp:      testFieldValue,
				// and so on
			},
		},
		testFmtApacheAccess: {
			{
				testFieldType: "op",
				testProp:      "value access",
			},
		},
		testFmtApacheError: {
			{
				testFieldType: "op",
				testProp:      "value error",
			},
		},
	}
	knownLogFilters := map[string]config.OTELFilters{
		"drop_get": {
			testFilterExclude: map[string]any{
				testFilterMatchType: testRegexp,
				testFilterBodies: []string{
					testMethodGET,
				},
			},
		},
	}

	svcNginx := svc(testServiceNginx, testContainerNginx1, testContainerIDNgx1, true, time.Now(), discovery.ServiceLogReceiver{Format: "nginx_both", Filter: "drop_get"})

	ctrNgx1 := ctr(testContainerIDNgx1, testContainerNginx1, nil, nil)
	ctrDisabled := ctr("disabled", "Disabled", map[string]string{"glouton.log_enable": "False"}, nil)
	svcDisabled := svc("disabled-svc", "", "disabled", true, time.Now(), discovery.ServiceLogReceiver{Format: "nginx_both"})

	executionSteps := []struct {
		name                      string
		containers                []facts.Container
		services                  []discovery.Service
		expectedLogSources        []logSource
		expectedWatchedServices   map[discovery.NameInstance]struct{}
		expectedWatchedContainers map[string]struct{} // map key: container ID
	}{
		{
			name: "an nginx service in a container, and a service in a log-disabled container",
			containers: []facts.Container{
				ctrNgx1,
				ctrDisabled,
			},
			services: []discovery.Service{
				svcNginx,
				svcDisabled,
			},
			expectedLogSources: []logSource{
				{
					container: ctrNgx1,
					serviceID: &discovery.NameInstance{Name: testServiceNginx, Instance: testContainerNginx1},
					operators: append(
						[]config.OTELOperator{
							{
								testFieldName:  testRouteServiceName,
								testFieldType:  testFieldAdd,
								testFieldValue: testServiceNginx,
							},
						},
						knownLogFormats["nginx_both"]...,
					),
					filters: knownLogFilters["drop_get"],
				},
			},
			expectedWatchedServices: map[discovery.NameInstance]struct{}{
				{Name: testServiceNginx, Instance: testContainerNginx1}: {},
				// disabled-svc is skipped entirely: glouton.log_enable=false
				// on its container vetoes it before it's ever marked watched.
			},
			expectedWatchedContainers: map[string]struct{}{
				testContainerIDNgx1: {},
			},
		},
		{
			name: "with a non-active service",
			containers: []facts.Container{
				ctrNgx1,
			},
			services: []discovery.Service{
				svcNginx,
				svc("old", "outdated", "", false, time.Now().Add(-365*24*time.Hour)),
			},
			expectedLogSources: nil,
			expectedWatchedServices: map[discovery.NameInstance]struct{}{
				{Name: testServiceNginx, Instance: testContainerNginx1}: {},
			},
			expectedWatchedContainers: map[string]struct{}{
				testContainerIDNgx1: {},
			},
		},
		{
			name:       "no more nginx but an apache running on the host",
			containers: []facts.Container{},
			services: []discovery.Service{
				svc(
					testServiceApacheHTTPD, "", "", true, time.Now(),
					discovery.ServiceLogReceiver{FilePath: "/var/log/apache2/access.log", Format: testFmtApacheAccess},
					discovery.ServiceLogReceiver{FilePath: "/var/log/apache2/error.log", Format: testFmtApacheError},
				),
			},
			expectedLogSources: []logSource{
				{
					serviceID:   &discovery.NameInstance{Name: testServiceApacheHTTPD, Instance: ""},
					logFilePath: "/var/log/apache2/access.log",
					operators: append(
						[]config.OTELOperator{
							{
								testFieldName:  testRouteServiceName,
								testFieldType:  testFieldAdd,
								testFieldValue: testServiceApacheHTTPD,
							},
						},
						knownLogFormats[testFmtApacheAccess]...,
					),
				},
				{
					serviceID:   &discovery.NameInstance{Name: testServiceApacheHTTPD, Instance: ""},
					logFilePath: "/var/log/apache2/error.log",
					operators: append(
						[]config.OTELOperator{
							{
								testFieldName:  testRouteServiceName,
								testFieldType:  testFieldAdd,
								testFieldValue: testServiceApacheHTTPD,
							},
						},
						knownLogFormats[testFmtApacheError]...,
					),
				},
			},
			expectedWatchedServices: map[discovery.NameInstance]struct{}{
				{Name: testServiceNginx, Instance: testContainerNginx1}: {}, // removeOldSources() isn't run here, so stale entries remain.
				{Name: testServiceApacheHTTPD, Instance: ""}:            {},
			},
			expectedWatchedContainers: map[string]struct{}{
				testContainerIDNgx1: {},
			},
		},
	}

	logMan := &Manager{
		config: config.OpenTelemetry{
			KnownLogFormats: knownLogFormats,
			KnownLogFilters: knownLogFilters,
		},
		knownLogFormats:   knownLogFormats,
		containerRecv:     newContainerReceiver(&pipelineContext{}),
		watchedServices:   make(map[discovery.NameInstance]sourceDiagnostic),
		watchedContainers: make(map[string]sourceDiagnostic),
	}

	for _, step := range executionSteps {
		logSources := logMan.processLogSources(step.services, step.containers, nil)
		if diff := cmp.Diff(step.expectedLogSources, logSources, cmp.Comparer(logSourceComparer)); diff != "" {
			t.Fatalf("Unexpected log sources at step %q (-want +got):\n%s", step.name, diff)
		}

		// Only key existence is checked, not sourceDiagnostic content.
		expectedKeys := slices.Collect(maps.Keys(step.expectedWatchedServices))
		gotKeys := slices.Collect(maps.Keys(logMan.watchedServices))

		// Assumes no two services share a name with different instances.
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
