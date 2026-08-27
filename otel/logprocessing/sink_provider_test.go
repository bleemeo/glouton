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
	"slices"
	"testing"
	"time"

	"github.com/bleemeo/glouton/agent/state"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/otel/logsource"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"go.opentelemetry.io/collector/pdata/plog"
)

// makeSimpleLogs builds a minimal plog.Logs with one record per body.
func makeSimpleLogs(bodies ...string) plog.Logs {
	logs := plog.NewLogs()
	scopeLogs := logs.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty()

	for _, body := range bodies {
		scopeLogs.LogRecords().AppendEmpty().Body().SetStr(body)
	}

	return logs
}

func bodiesOf(records []logRecord) []string {
	bodies := make([]string, 0, len(records))
	for _, r := range records {
		bodies = append(bodies, r.Body)
	}

	return bodies
}

// newSinkTestManager builds a Manager around a real pipeline, without going through New().
func newSinkTestManager(t *testing.T, cfg config.OpenTelemetry, logBuf *logBuffer) *Manager {
	t.Helper()

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

	pipeline, err := makePipeline(
		t.Context(),
		cfg,
		"/",
		noExecRunner(t),
		fakeFacter(),
		func(_ context.Context, b []byte) error {
			logs, err := new(plog.ProtoUnmarshaler).UnmarshalLogs(b)
			if err != nil {
				t.Fatal("Failed to unmarshal logs:", err)
			}

			logBuf.add(logs)

			return nil
		},
		func() bleemeoTypes.LogsAvailability { return bleemeoTypes.LogsAvailabilityOk },
		persister,
		func(errs ...error) { t.Log("Warnings:", errs) },
		cfg.KnownLogFormats,
		map[string]int64{},
		pipelineOptions{
			batcherTimeout:           50 * time.Millisecond,
			logsAvailabilityCacheTTL: 50 * time.Millisecond,
		},
	)
	if err != nil {
		t.Fatal("Can't build pipeline:", err)
	}

	t.Cleanup(pipeline.shutdownAll)

	return &Manager{
		config:          cfg,
		pipeline:        pipeline,
		containerRecv:   newContainerReceiver(pipeline),
		containerFilter: validateContainerFilters(cfg.ContainerFilter, cfg.KnownLogFilters),
		fanoutSinks:     make(map[string]*fanoutSink),
	}
}

func TestWantSourceDeclinesWhenSendLogsFalse(t *testing.T) {
	t.Parallel()

	man := newSinkTestManager(t, config.OpenTelemetry{
		Receivers: map[string]config.LogReceiver{"recv": {}},
	}, &logBuffer{})

	sink, ok := man.WantSource(t.Context(), logsource.ResolvedSource{
		Kind: logsource.SourceReceiver, ReceiverName: "recv", SendLogs: false,
	})
	if ok || sink != nil {
		t.Fatalf("expected WantSource to decline a SendLogs=false source, got ok=%v sink=%v", ok, sink)
	}
}

// TestWantSourceDeclinesContainerLabelSourceWithNilContainer guards against a regression where
// WantSource dereferenced src.Container unconditionally for SourceContainerLabel (via
// man.containerRecv.isTailing(src.Container.ID())), panicking the discovery goroutine (which calls this
// with rm.l held) instead of declining, for any ResolvedSource whose Container field isn't set.
func TestWantSourceDeclinesContainerLabelSourceWithNilContainer(t *testing.T) {
	t.Parallel()

	man := newSinkTestManager(t, config.OpenTelemetry{}, &logBuffer{})

	sink, ok := man.WantSource(t.Context(), logsource.ResolvedSource{
		Kind: logsource.SourceContainerLabel, SendLogs: true, Container: nil,
	})
	if ok || sink != nil {
		t.Fatalf("expected WantSource to decline a nil-Container SourceContainerLabel source, got ok=%v sink=%v", ok, sink)
	}
}

// Test that a receiver's own filters apply even with no file/container fields to resolve first.
func TestWantSourceReceiverFilterAppliesUnconditionally(t *testing.T) {
	t.Parallel()

	var logBuf logBuffer

	cfg := config.OpenTelemetry{
		Receivers: map[string]config.LogReceiver{
			"net-recv": {
				"filters": config.OTELFilters{
					"exclude": map[string]any{
						"match_type": "regexp",
						"bodies":     []string{"drop"},
					},
				},
			},
		},
	}

	man := newSinkTestManager(t, cfg, &logBuf)

	sink, ok := man.WantSource(t.Context(), logsource.ResolvedSource{
		Kind: logsource.SourceReceiver, ReceiverName: "net-recv", SendLogs: true,
	})
	if !ok || sink == nil {
		t.Fatal("expected WantSource to want this source")
	}

	if err := sink.ConsumeLogs(t.Context(), makeSimpleLogs("keep me", "please drop me")); err != nil {
		t.Fatal("Failed to push logs:", err)
	}

	time.Sleep(300 * time.Millisecond)

	if diff := cmp.Diff([]string{"keep me"}, bodiesOf(logBuf.getAllRecords())); diff != "" {
		t.Fatalf("Unexpected records (-want +got):\n%s", diff)
	}
}

func TestWantSourceContainerLabelFilter(t *testing.T) {
	t.Parallel()

	knownLogFilters := map[string]config.OTELFilters{
		"drop-filter": {
			"exclude": map[string]any{
				"match_type": "regexp",
				"bodies":     []string{"drop"},
			},
		},
		"noop-filter": {},
	}

	testCases := []struct {
		name            string
		containerFilter map[string]string
		labels          map[string]string
		wantBodies      []string
	}{
		{
			name:            "glouton.log_filter label wins over container_filter fallback",
			containerFilter: map[string]string{"my-ctr": "noop-filter"},
			labels:          map[string]string{"glouton.log_filter": "drop-filter"},
			wantBodies:      []string{"keep me"},
		},
		{
			name:            "falls back to container_filter map when label absent",
			containerFilter: map[string]string{"my-ctr": "drop-filter"},
			labels:          nil,
			wantBodies:      []string{"keep me"},
		},
		{
			name:            "no filter resolved at all: everything ships",
			containerFilter: nil,
			labels:          nil,
			wantBodies:      []string{"keep me", "please drop me"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var logBuf logBuffer

			cfg := config.OpenTelemetry{
				ContainerFilter: tc.containerFilter,
				KnownLogFilters: knownLogFilters,
			}

			man := newSinkTestManager(t, cfg, &logBuf)

			container := facts.FakeContainer{
				FakeID:            "ctr-id",
				FakeContainerName: "my-ctr",
				FakeLabels:        tc.labels,
			}

			sink, ok := man.WantSource(t.Context(), logsource.ResolvedSource{
				Kind: logsource.SourceContainerLabel, Container: container, SendLogs: true,
			})
			if !ok || sink == nil {
				t.Fatal("expected WantSource to want this source")
			}

			if err := sink.ConsumeLogs(t.Context(), makeSimpleLogs("keep me", "please drop me")); err != nil {
				t.Fatal("Failed to push logs:", err)
			}

			time.Sleep(300 * time.Millisecond)

			got := bodiesOf(logBuf.getAllRecords())
			if diff := cmp.Diff(tc.wantBodies, got, cmpopts.SortSlices(func(a, b string) bool { return a < b })); diff != "" {
				t.Fatalf("Unexpected records (-want +got):\n%s", diff)
			}
		})
	}
}

// Test that ReleaseSource removes the released container's filter processor from both fanoutSinks and startedComponents.
func TestReleaseSourceStopsAndForgetsContainerFilter(t *testing.T) {
	t.Parallel()

	var logBuf logBuffer

	man := newSinkTestManager(t, config.OpenTelemetry{}, &logBuf)

	container := facts.FakeContainer{FakeID: "ctr-id", FakeContainerName: "my-ctr"}

	sink, ok := man.WantSource(t.Context(), logsource.ResolvedSource{
		Kind: logsource.SourceContainerLabel, Container: container, SendLogs: true,
	})
	if !ok || sink == nil {
		t.Fatal("expected WantSource to want this source")
	}

	man.l.Lock()
	sinkEntry, found := man.fanoutSinks["ctr-"+container.ID()]
	man.l.Unlock()

	if !found {
		t.Fatal("expected a fanoutSinks entry for the container before release")
	}

	man.pipeline.l.Lock()
	hasComponent := slices.Contains(man.pipeline.startedComponents, sinkEntry.comp)
	man.pipeline.l.Unlock()

	if !hasComponent {
		t.Fatal("expected the container's filter processor in pipeline.startedComponents before release")
	}

	man.ReleaseSource(t.Context(), container)

	man.l.Lock()
	_, stillFound := man.fanoutSinks["ctr-"+container.ID()]
	man.l.Unlock()

	if stillFound {
		t.Fatal("expected the fanoutSinks entry to be removed after release")
	}

	man.pipeline.l.Lock()
	defer man.pipeline.l.Unlock()

	if slices.Contains(man.pipeline.startedComponents, sinkEntry.comp) {
		t.Fatal("expected the filter processor to be removed from pipeline.startedComponents after release")
	}
}
