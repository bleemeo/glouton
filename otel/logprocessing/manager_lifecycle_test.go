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
	"context"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/bleemeo/glouton/agent/state"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/discovery"
	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/otel/logsource"
)

// TestHandleProcessingLifecycleShutdownDoesNotDeadlock guards against a shutdown deadlock where
// SaveState relocked the already-held pipeline lock via the external sizer.
func TestHandleProcessingLifecycleShutdownDoesNotDeadlock(t *testing.T) {
	t.Parallel()

	st, err := state.LoadReadOnly("not", "used")
	if err != nil {
		t.Fatal("Can't instantiate state:", err)
	}

	cfg := config.OpenTelemetry{}

	receiverManager, err := logsource.NewReceiverManager(cfg, "/", st, noExecRunner(t))
	if err != nil {
		t.Fatal("NewReceiverManager failed:", err)
	}

	t.Cleanup(func() { receiverManager.Shutdown(context.Background()) })

	persister := receiverManager.Persister()

	pipeline, err := makePipeline(
		t.Context(),
		cfg,
		"/",
		noExecRunner(t),
		fakeFacter(),
		func(context.Context, []byte) error { return nil },
		func() bleemeoTypes.LogsAvailability { return bleemeoTypes.LogsAvailabilityOk },
		persister,
		func(...error) {},
		cfg.KnownLogFormats,
		map[string]int64{},
		pipelineOptions{},
	)
	if err != nil {
		t.Fatal("Can't build pipeline:", err)
	}

	containerRecv := newContainerReceiver(pipeline)

	man := &Manager{
		config:            cfg,
		state:             st,
		receiverManager:   receiverManager,
		persister:         persister,
		pipeline:          pipeline,
		containerRecv:     containerRecv,
		watchedServices:   make(map[discovery.NameInstance]sourceDiagnostic),
		watchedContainers: make(map[string]sourceDiagnostic),
		serviceReceivers:  make(map[discovery.NameInstance][]*logReceiver),
		fanoutSinks:       make(map[string]*fanoutSink),
	}

	// Mirrors New's own sizer registration, the mechanism under test.
	receiverManager.RegisterExternalSizer(func() []logsource.FileSizer {
		man.pipeline.l.Lock()
		defer man.pipeline.l.Unlock()

		return mergeLastFileSizes(man.pipeline.receivers, man.containerRecv)
	})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	done := make(chan struct{})

	go func() {
		man.handleProcessingLifecycle(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for handleProcessingLifecycle to return on an already-cancelled context; its shutdown branch likely deadlocked")
	}
}

// TestProcessLogSourcesVsReceiverShippedContainers covers both directions of the service-vs-explicit
// receiver split. A container an explicit receiver already ships must not also be tailed by service
// auto-discovery (that double-ships every line), but one matched only by a receiver that ships nothing
// -- send_logs: false, e.g. a metrics-only receiver -- must still go through the service path, or
// nothing would ship its logs at all.
func TestProcessLogSourcesVsReceiverShippedContainers(t *testing.T) {
	t.Parallel()

	const ctrID = "id-postgres-1"

	ctrPostgres := facts.FakeContainer{
		FakeID:            ctrID,
		FakeContainerName: "postgres-1",
		FakeLogPath:       "/fake/postgres.log",
	}

	for _, testCase := range []struct {
		name        string
		receiver    config.LogReceiver
		wantSkipped bool
	}{
		{
			name:        "receiver ships the container's logs",
			receiver:    config.LogReceiver{"container_name": "postgres-1"},
			wantSkipped: true,
		},
		{
			name:        "receiver only counts metrics, ships nothing",
			receiver:    config.LogReceiver{"container_name": "postgres-1", "send_logs": false},
			wantSkipped: false,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			st, err := state.LoadReadOnly("not", "used")
			if err != nil {
				t.Fatal("Can't instantiate state:", err)
			}

			receiverManager, err := logsource.NewReceiverManager(config.OpenTelemetry{
				ReceiversDefaultSendLogs: true,
				Receivers:                map[string]config.LogReceiver{"explicit": testCase.receiver},
			}, "/", st, noExecRunner(t))
			if err != nil {
				t.Fatal("NewReceiverManager failed:", err)
			}

			t.Cleanup(func() { receiverManager.Shutdown(context.Background()) })

			man := &Manager{
				containerRecv:     newContainerReceiver(&pipelineContext{}),
				receiverManager:   receiverManager,
				watchedServices:   make(map[discovery.NameInstance]sourceDiagnostic),
				watchedContainers: make(map[string]sourceDiagnostic),
			}

			shippedByReceivers := receiverManager.ContainerIDsShippedByReceivers([]facts.Container{ctrPostgres})

			logSources := man.processLogSources(
				[]discovery.Service{svc("postgres", "", ctrID, true, time.Now(), discovery.ServiceLogReceiver{Format: "auto"})},
				[]facts.Container{ctrPostgres},
				shippedByReceivers,
			)

			if testCase.wantSkipped {
				if len(logSources) != 0 {
					t.Fatalf("expected the service to be skipped, got %+v", logSources)
				}

				if _, watched := man.watchedContainers[ctrID]; watched {
					t.Fatal("expected the container not to be marked watched")
				}

				return
			}

			if len(logSources) != 1 {
				t.Fatalf("expected the service to still be picked up, got %+v", logSources)
			}

			if _, watched := man.watchedContainers[ctrID]; !watched {
				t.Fatal("expected the container to be marked watched")
			}
		})
	}
}

// TestRemoveOldSourcesStopsVanishedServiceContainerTail checks the container-hosted half of service
// teardown. Such a service has no serviceReceivers entry -- its tail lives in containerRecv -- and the
// container branch only fires once the container itself disappears. So a service that stopped being
// reported while its container kept running used to leave its tail in place, and the next time discovery
// re-detected it, processLogSources (no longer finding it in watchedServices) set it up again: two live
// tails on one file, both shipping every line, under the byte-identical persisted-offset name.
func TestRemoveOldSourcesStopsVanishedServiceContainerTail(t *testing.T) {
	t.Parallel()

	const ctrID = "id-nginx-1"

	pipeline := pipelineContext{persister: mustNewPersistHost(t)}

	man := &Manager{
		pipeline:          &pipeline,
		persister:         pipeline.persister,
		containerRecv:     newContainerReceiver(&pipeline),
		watchedServices:   make(map[discovery.NameInstance]sourceDiagnostic),
		watchedContainers: make(map[string]sourceDiagnostic),
		serviceReceivers:  make(map[discovery.NameInstance][]*logReceiver),
	}

	serviceKey := discovery.NameInstance{Name: "nginx", Instance: ""}

	// Stand in for a service-path tail already running for this container.
	man.watchedServices[serviceKey] = sourceDiagnostic{IsFromService: true, ServiceKey: serviceKey, ContainerID: ctrID}
	man.watchedContainers[ctrID] = sourceDiagnostic{IsFromService: true, ServiceKey: serviceKey, ContainerID: ctrID}
	man.containerRecv.containers[ctrID] = Container{LogFilePath: "/var/log/nginx.log", RealLogFilePath: "/var/log/nginx.log"}

	ctrNginx := ctr(ctrID, "nginx-1", nil)

	// The service is no longer reported, but its container is still running.
	man.removeOldSources(t.Context(), nil, []facts.Container{ctrNginx}, true)

	if man.containerRecv.isTailing(ctrID) {
		t.Error("expected the vanished service's container tail to be stopped")
	}

	if _, watched := man.watchedContainers[ctrID]; watched {
		t.Error("expected the container to stop being reported as watched")
	}

	if _, watched := man.watchedServices[serviceKey]; watched {
		t.Error("expected the service to stop being reported as watched")
	}
}

// TestProcessLogSourcesHonoursContainerFormatAndFilterLabels checks that a containerised service's tail
// applies the container's own glouton.log_format/glouton.log_filter rather than what auto-discovery
// inferred from the service type. This tail is the container's only one (WantSource declines its
// glouton.*-label source because this path already ships it), so without this those explicit labels would
// silently stop having any effect.
func TestProcessLogSourcesHonoursContainerFormatAndFilterLabels(t *testing.T) {
	t.Parallel()

	const ctrID = "id-nginx-1"

	labelFormat := []config.OTELOperator{{"type": "add", "field": "attributes.from", "value": "label_format"}}
	serviceFormat := []config.OTELOperator{{"type": "add", "field": "attributes.from", "value": "service_format"}}
	labelFilter := config.OTELFilters{"log_record": []any{`IsMatch(body, "label")`}}
	serviceFilter := config.OTELFilters{"log_record": []any{`IsMatch(body, "service")`}}

	man := &Manager{
		config: config.OpenTelemetry{
			KnownLogFilters: map[string]config.OTELFilters{"label_filter": labelFilter, "service_filter": serviceFilter},
		},
		knownLogFormats:   map[string][]config.OTELOperator{"label_format": labelFormat, "service_format": serviceFormat},
		containerRecv:     newContainerReceiver(&pipelineContext{}),
		watchedServices:   make(map[discovery.NameInstance]sourceDiagnostic),
		watchedContainers: make(map[string]sourceDiagnostic),
	}

	ctrNginx := ctr(ctrID, "nginx-1", map[string]string{
		logsource.ContainerLabelPrefix + "log_format": "label_format",
		logsource.ContainerLabelPrefix + "log_filter": "label_filter",
	})

	logSources := man.processLogSources(
		[]discovery.Service{svc("nginx", "", ctrID, true, time.Now(), discovery.ServiceLogReceiver{
			Format: "service_format", Filter: "service_filter",
		})},
		[]facts.Container{ctrNginx},
		nil,
	)

	if len(logSources) != 1 {
		t.Fatalf("expected exactly one log source, got %+v", logSources)
	}

	// operatorsForService prepends the service.name operator, so the format lands last.
	gotOperators := logSources[0].operators
	if len(gotOperators) == 0 || !reflect.DeepEqual(gotOperators[len(gotOperators)-1], labelFormat[0]) {
		t.Errorf("expected the container's glouton.log_format to win, got operators %+v", gotOperators)
	}

	if !reflect.DeepEqual(logSources[0].filters, labelFilter) {
		t.Errorf("expected the container's glouton.log_filter to win, got %+v", logSources[0].filters)
	}
}

// TestWantSourceDeclinesContainerAlreadyTailedByService checks the shipping-side deduplication: a
// container already tailed via the service path must be declined by WantSource (else every line ships
// twice), while a container whose service-path setup failed must still be accepted -- declining that one
// would leave nothing tailing it at all, silently dropping its logs instead of merely duplicating them.
func TestWantSourceDeclinesContainerAlreadyTailedByService(t *testing.T) {
	t.Parallel()

	st, err := state.LoadReadOnly("not", "used")
	if err != nil {
		t.Fatal("Can't instantiate state:", err)
	}

	cfg := config.OpenTelemetry{}

	receiverManager, err := logsource.NewReceiverManager(cfg, "/", st, noExecRunner(t))
	if err != nil {
		t.Fatal("NewReceiverManager failed:", err)
	}

	t.Cleanup(func() { receiverManager.Shutdown(context.Background()) })

	pipeline, err := makePipeline(
		t.Context(),
		cfg,
		"/",
		noExecRunner(t),
		fakeFacter(),
		func(context.Context, []byte) error { return nil },
		func() bleemeoTypes.LogsAvailability { return bleemeoTypes.LogsAvailabilityOk },
		receiverManager.Persister(),
		func(...error) {},
		cfg.KnownLogFormats,
		map[string]int64{},
		pipelineOptions{},
	)
	if err != nil {
		t.Fatal("Can't build pipeline:", err)
	}

	man := &Manager{
		config:            cfg,
		state:             st,
		receiverManager:   receiverManager,
		persister:         receiverManager.Persister(),
		pipeline:          pipeline,
		containerRecv:     newContainerReceiver(pipeline),
		watchedServices:   make(map[discovery.NameInstance]sourceDiagnostic),
		watchedContainers: make(map[string]sourceDiagnostic),
		serviceReceivers:  make(map[discovery.NameInstance][]*logReceiver),
		fanoutSinks:       make(map[string]*fanoutSink),
	}

	// This test starts real tails and pipeline components; without teardown they keep reading the temp
	// file and pushing batches for the rest of the package run, next to timing-sensitive neighbours.
	t.Cleanup(func() {
		man.containerRecv.stop()
		pipeline.shutdownAll()
	})

	// A service container whose log file doesn't exist: setup fails, so nothing tails it.
	ctrBad := facts.FakeContainer{
		FakeID: "id-bad", FakeContainerName: "bad-1", FakeLogPath: "/definitely/does/not/exist/nope.log",
	}

	man.HandleLogsFromDynamicSources(
		t.Context(),
		[]discovery.Service{svc("postgres", "", "id-bad", true, time.Now(), discovery.ServiceLogReceiver{})},
		[]facts.Container{ctrBad},
		true,
	)

	if _, ok := man.WantSource(t.Context(), logsource.ResolvedSource{
		Kind: logsource.SourceContainerLabel, Name: "bad-1", Container: ctrBad, SendLogs: true,
	}); !ok {
		t.Error("a container whose service-path setup failed must still be wanted, or nothing tails it at all")
	}

	// Sanity check the other direction, so this test can't pass by simply always accepting.
	logFile, err := os.CreateTemp(t.TempDir(), "good-*.log")
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer logFile.Close()

	ctrGood := facts.FakeContainer{
		FakeID: "id-good", FakeContainerName: "good-1", FakeLogPath: logFile.Name(),
	}

	man.HandleLogsFromDynamicSources(
		t.Context(),
		[]discovery.Service{svc("redis", "", "id-good", true, time.Now(), discovery.ServiceLogReceiver{})},
		[]facts.Container{ctrGood},
		true,
	)

	if _, ok := man.WantSource(t.Context(), logsource.ResolvedSource{
		Kind: logsource.SourceContainerLabel, Name: "good-1", Container: ctrGood, SendLogs: true,
	}); ok {
		t.Error("a container already tailed by the service path must be declined, else its logs ship twice")
	}
}
