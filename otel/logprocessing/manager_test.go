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

	"github.com/bleemeo/glouton/agent/state"
	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/discovery"
	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/otel/logsource"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/extension/xextension/storage"
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

// ctr builds a fake container. Only labels are parameterised: glouton.* annotations resolve through the
// same facts.LabelsAndAnnotations lookup as labels, so no test here needs to set them separately.
func ctr(id, name string, labels map[string]string) facts.Container {
	return facts.FakeContainer{
		FakeID:            id,
		FakeContainerName: name,
		FakeLabels:        labels,
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

	ctrNgx1 := ctr(testContainerIDNgx1, testContainerNginx1, nil)
	ctrDisabled := ctr("disabled", "Disabled", map[string]string{"glouton.log_enable": "False"})
	svcDisabled := svc("disabled-svc", "", "disabled", true, time.Now(), discovery.ServiceLogReceiver{Format: "nginx_both"})
	// A service Glouton auto-discovers, opted out of shipping through the same label that would apply if
	// it weren't recognized as a service -- see logprocessing's own glouton.send_logs check.
	ctrSendLogsOff := ctr("sendlogsoff", "SendLogsOff", map[string]string{"glouton.send_logs": "false"})
	svcSendLogsOff := svc("sendlogsoff-svc", "", "sendlogsoff", true, time.Now(), discovery.ServiceLogReceiver{Format: "nginx_both"})

	executionSteps := []struct {
		name                      string
		containers                []facts.Container
		services                  []discovery.Service
		expectedLogSources        []logSource
		expectedWatchedServices   map[discovery.NameInstance]struct{}
		expectedWatchedContainers map[string]struct{} // map key: container ID
	}{
		{
			name: "an nginx service in a container, a service in a log-disabled container, and one opted out of shipping",
			containers: []facts.Container{
				ctrNgx1,
				ctrDisabled,
				ctrSendLogsOff,
			},
			services: []discovery.Service{
				svcNginx,
				svcDisabled,
				svcSendLogsOff,
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
				// disabled-svc and sendlogsoff-svc are both skipped entirely: glouton.log_enable=false and
				// glouton.send_logs=false on their respective containers veto them before either is ever
				// marked watched.
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

// TestRemoveOldSourcesForgetsOffsetWhenContainerAndItsServiceBothVanish guards against a regression
// where a container's persisted read-offset was never actually forgotten on the ordinary "container
// removed" path: removeOldSources tears the container down twice in the same cycle -- once through the
// vanished-service branch (forget=false, since the container itself might still be there) and once
// through the vanished-container branch (forget=true, since it's gone for good) -- but
// stopWatchingForContainers unconditionally clears cr.registeredExtensions[ctrID] on the first call
// regardless of forget, so the second call had nothing left to forget from.
func TestRemoveOldSourcesForgetsOffsetWhenContainerAndItsServiceBothVanish(t *testing.T) {
	t.Parallel()

	st, err := state.LoadReadOnly("not", "used")
	if err != nil {
		t.Fatal("Can't instantiate state:", err)
	}

	persister, err := logsource.NewPersistHost(st, logsource.PersistConfig{
		StorageType:  logsource.PersistStorageType,
		CacheKey:     logsource.LogFileMetadataCacheKey,
		ArchivePath:  "log-processing/persister.json",
		SaveThrottle: saveFileSizesToCachePeriod,
	})
	if err != nil {
		t.Fatal("Can't instantiate persist host:", err)
	}

	pipeline := &pipelineContext{persister: persister}
	containerRecv := newContainerReceiver(pipeline)

	const (
		ctrID    = "ctr-1"
		persName = "container/" + ctrID + "/app.log"
	)

	extID := persister.NewPersistentExt(persName)

	ext, ok := persister.GetExtensions()[extID].(storage.Extension)
	if !ok {
		t.Fatal("Expected the registered extension to implement storage.Extension")
	}

	client, err := ext.GetClient(t.Context(), component.KindReceiver, extID, "")
	if err != nil {
		t.Fatal("Failed to get storage client:", err)
	}

	if err := client.Set(t.Context(), "offset", []byte("42")); err != nil {
		t.Fatal("Failed to set offset:", err)
	}

	// Simulates the receiver's own shutdown sequence saving its final offset before teardown.
	if err := client.Close(t.Context()); err != nil {
		t.Fatal("Failed to close client:", err)
	}

	containerRecv.registeredExtensions[ctrID] = []component.ID{extID}
	containerRecv.containers[ctrID] = Container{LogFilePath: "app.log"}

	svcKey := discovery.NameInstance{Name: "nginx", Instance: ctrID}

	man := &Manager{
		persister:         persister,
		containerRecv:     containerRecv,
		watchedServices:   map[discovery.NameInstance]sourceDiagnostic{svcKey: {ContainerID: ctrID}},
		watchedContainers: map[string]sourceDiagnostic{ctrID: {ContainerID: ctrID}},
		serviceReceivers:  map[discovery.NameInstance][]*logReceiver{},
	}

	// Both the service and the container it ran in disappear together: the ordinary container-removal case.
	man.removeOldSources(t.Context(), nil, nil, true)

	persister.SaveToState(st)

	var saved map[string]map[string][]byte
	if err := st.Get(logsource.LogFileMetadataCacheKey, &saved); err != nil {
		t.Fatal("Failed to read back saved state:", err)
	}

	if _, found := saved[persName]; found {
		t.Errorf("Expected the removed container's offset to be forgotten, got %v", saved)
	}
}

// TestRemoveOldSourcesKeepsOffsetWhenContainerListIncomplete is the counterpart to the test above: the
// permanent offset-forget must be gated on the container list having been authoritative. agent.go's guard
// only catches an explicit error from the runtime, but an empty list with no error is possible
// (merge.Runtime.Containers returns (nil, nil) when no runtime yielded anything and none errored;
// docker.Docker.Containers swallows its error outright until it has worked once, so the whole window around
// a daemon restart looks like an empty host). Forgetting is permanent and fileconsumer's StartAt defaults
// to "end", so treating that as "every container was removed" would skip every line written in the gap.
func TestRemoveOldSourcesKeepsOffsetWhenContainerListIncomplete(t *testing.T) {
	t.Parallel()

	st, err := state.LoadReadOnly("not", "used")
	if err != nil {
		t.Fatal("Can't instantiate state:", err)
	}

	persister, err := logsource.NewPersistHost(st, logsource.PersistConfig{
		StorageType:  logsource.PersistStorageType,
		CacheKey:     logsource.LogFileMetadataCacheKey,
		ArchivePath:  "log-processing/persister.json",
		SaveThrottle: saveFileSizesToCachePeriod,
	})
	if err != nil {
		t.Fatal("Can't instantiate persist host:", err)
	}

	pipeline := &pipelineContext{persister: persister}
	containerRecv := newContainerReceiver(pipeline)

	const (
		ctrID    = "ctr-1"
		persName = "container/" + ctrID + "/app.log"
	)

	extID := persister.NewPersistentExt(persName)

	ext, ok := persister.GetExtensions()[extID].(storage.Extension)
	if !ok {
		t.Fatal("Expected the registered extension to implement storage.Extension")
	}

	client, err := ext.GetClient(t.Context(), component.KindReceiver, extID, "")
	if err != nil {
		t.Fatal("Failed to get storage client:", err)
	}

	if err := client.Set(t.Context(), "offset", []byte("42")); err != nil {
		t.Fatal("Failed to set offset:", err)
	}

	if err := client.Close(t.Context()); err != nil {
		t.Fatal("Failed to close client:", err)
	}

	containerRecv.registeredExtensions[ctrID] = []component.ID{extID}
	containerRecv.containers[ctrID] = Container{LogFilePath: "app.log"}

	man := &Manager{
		persister:         persister,
		containerRecv:     containerRecv,
		watchedServices:   map[discovery.NameInstance]sourceDiagnostic{},
		watchedContainers: map[string]sourceDiagnostic{ctrID: {ContainerID: ctrID}},
		serviceReceivers:  map[discovery.NameInstance][]*logReceiver{},
	}

	// The runtime enumerated nothing, without reporting an error: not to be trusted as a removal.
	man.removeOldSources(t.Context(), nil, nil, false)

	persister.SaveToState(st)

	var saved map[string]map[string][]byte
	if err := st.Get(logsource.LogFileMetadataCacheKey, &saved); err != nil {
		t.Fatal("Failed to read back saved state:", err)
	}

	if _, found := saved[persName]; !found {
		t.Errorf("Expected the container's offset to survive an incomplete container list, got %v", saved)
	}
}
