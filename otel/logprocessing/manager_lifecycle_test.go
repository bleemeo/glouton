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
	"testing"
	"time"

	"github.com/bleemeo/glouton/agent/state"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/discovery"
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
