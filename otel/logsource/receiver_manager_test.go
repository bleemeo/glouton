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

package logsource

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/facts"

	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/plog"
)

// fakeSinkProvider is a SinkProvider test double recording every
// ResolvedSource it was asked about, answering according to want.
type fakeSinkProvider struct {
	want func(src ResolvedSource) (consumer.Logs, bool)

	l     sync.Mutex
	asked []ResolvedSource
}

func (f *fakeSinkProvider) WantSource(_ context.Context, src ResolvedSource) (consumer.Logs, bool) {
	f.l.Lock()
	f.asked = append(f.asked, src)
	f.l.Unlock()

	return f.want(src)
}

func (f *fakeSinkProvider) askedSources() []ResolvedSource {
	f.l.Lock()
	defer f.l.Unlock()

	return append([]ResolvedSource(nil), f.asked...)
}

// newRecordingProvider returns a SinkProvider that always wants every source,
// and a function reading back every batch its sink received.
func newRecordingProvider() (*fakeSinkProvider, func() []plog.Logs) {
	sink, received := recordingLogsConsumer()

	return &fakeSinkProvider{
		want: func(ResolvedSource) (consumer.Logs, bool) { return sink, true },
	}, received
}

// declineProvider is a SinkProvider that never wants anything, only useful
// to observe what it was asked (see fakeSinkProvider.askedSources).
func declineProvider() *fakeSinkProvider {
	return &fakeSinkProvider{want: func(ResolvedSource) (consumer.Logs, bool) { return nil, false }}
}

func totalRecords(batches []plog.Logs) int {
	total := 0

	for _, b := range batches {
		total += b.LogRecordCount()
	}

	return total
}

func newTestReceiverManager(t *testing.T, cfg config.OpenTelemetry) *ReceiverManager {
	t.Helper()

	rm, err := NewReceiverManager(cfg, "/", newMemoryState(), nil)
	if err != nil {
		t.Fatal("NewReceiverManager failed:", err)
	}

	t.Cleanup(func() { rm.Shutdown(t.Context()) })

	return rm
}

// TestReceiverManagerFanout checks that a single physical file tail is fanned
// out to every registered SinkProvider that wants it, and that nothing is
// tailed at all when none do.
func TestReceiverManagerFanout(t *testing.T) {
	t.Parallel()

	for _, n := range []int{0, 1, 2} {
		t.Run(fmt.Sprintf("%d_provider(s)", n), func(t *testing.T) {
			t.Parallel()

			logFile, err := os.CreateTemp(t.TempDir(), "app-*.log")
			if err != nil {
				t.Fatal("Can't create log file:", err)
			}

			defer logFile.Close()

			cfg := config.OpenTelemetry{
				Receivers: map[string]config.LogReceiver{
					"app": {"include": []string{logFile.Name()}},
				},
			}

			rm := newTestReceiverManager(t, cfg)

			receivedFns := make([]func() []plog.Logs, n)

			for i := range n {
				p, received := newRecordingProvider()
				rm.RegisterSinkProvider(p)

				receivedFns[i] = received
			}

			if err := rm.RescanReceivers(t.Context()); err != nil {
				t.Fatal("RescanReceivers failed:", err)
			}

			time.Sleep(500 * time.Millisecond)

			if _, err := logFile.WriteString("line one\nline two\nline three\n"); err != nil {
				t.Fatal("Failed to write log lines:", err)
			}

			if err := logFile.Sync(); err != nil {
				t.Fatal("Failed to sync log file:", err)
			}

			time.Sleep(time.Second)

			for i, fn := range receivedFns {
				if got := totalRecords(fn()); got != 3 {
					t.Errorf("provider %d: expected 3 records, got %d", i, got)
				}
			}

			if n == 0 {
				rm.l.Lock()
				ms := rm.receivers["app"]
				rm.l.Unlock()

				ms.l.Lock()
				watching := len(ms.watching)
				ms.l.Unlock()

				if watching != 0 {
					t.Errorf("expected no physical tail when no provider wants the source, got %d watched file(s)", watching)
				}
			}
		})
	}
}

// TestReceiverManagerPersistsOffsetAcrossRestart checks that a line written
// while no ReceiverManager is running is still picked up on "restart" (a
// second instance sharing the same state), since it resumes from the offset
// persisted by the first instance instead of the file's end.
func TestReceiverManagerPersistsOffsetAcrossRestart(t *testing.T) {
	t.Parallel()

	logFile, err := os.CreateTemp(t.TempDir(), "app-*.log")
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer logFile.Close()

	cfg := config.OpenTelemetry{
		Receivers: map[string]config.LogReceiver{
			"app": {"include": []string{logFile.Name()}},
		},
	}

	state := newMemoryState()

	rm1, err := NewReceiverManager(cfg, "/", state, nil)
	if err != nil {
		t.Fatal("NewReceiverManager failed:", err)
	}

	provider1, received1 := newRecordingProvider()
	rm1.RegisterSinkProvider(provider1)

	if err := rm1.RescanReceivers(t.Context()); err != nil {
		t.Fatal("RescanReceivers failed:", err)
	}

	time.Sleep(500 * time.Millisecond)

	if _, err := logFile.WriteString("[error] first\n"); err != nil {
		t.Fatal("Failed to write log line:", err)
	}

	if err := logFile.Sync(); err != nil {
		t.Fatal("Failed to sync log file:", err)
	}

	time.Sleep(time.Second)

	if got := totalRecords(received1()); got != 1 {
		t.Fatalf("expected 1 record in the first run, got %d", got)
	}

	rm1.Shutdown(t.Context())
	rm1.SaveState()

	// Written entirely during the "restart gap": no manager is running yet.
	if _, err := logFile.WriteString("[error] second\n"); err != nil {
		t.Fatal("Failed to write log line:", err)
	}

	if err := logFile.Sync(); err != nil {
		t.Fatal("Failed to sync log file:", err)
	}

	rm2, err := NewReceiverManager(cfg, "/", state, nil)
	if err != nil {
		t.Fatal("NewReceiverManager failed:", err)
	}

	t.Cleanup(func() { rm2.Shutdown(t.Context()) })

	provider2, received2 := newRecordingProvider()
	rm2.RegisterSinkProvider(provider2)

	if err := rm2.RescanReceivers(t.Context()); err != nil {
		t.Fatal("RescanReceivers failed:", err)
	}

	time.Sleep(time.Second)

	if got := totalRecords(received2()); got != 1 {
		t.Fatalf("expected the second run to pick up exactly 1 new record via the persisted offset, got %d", got)
	}
}

// TestReceiverManagerContainerSelectorMatch checks that a container matching
// a receiver's container_selectors is tailed under that receiver (Docker
// envelope unwrapped) and that the resolved source correctly names it.
func TestReceiverManagerContainerSelectorMatch(t *testing.T) {
	t.Parallel()

	logFile, err := os.CreateTemp(t.TempDir(), "web-*.log")
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer logFile.Close()

	ctr := facts.FakeContainer{
		FakeID:            "id-web-1",
		FakeContainerName: "web-1",
		FakeLogPath:       logFile.Name(),
		FakeLabels:        map[string]string{"app": "web"},
	}

	cfg := config.OpenTelemetry{
		Receivers: map[string]config.LogReceiver{
			"web": {"container_selectors": map[string]string{"app": "web"}},
		},
	}

	rm := newTestReceiverManager(t, cfg)

	provider, received := newRecordingProvider()
	rm.RegisterSinkProvider(provider)

	rm.UpdateContainers(t.Context(), []facts.Container{ctr})

	time.Sleep(500 * time.Millisecond)

	dockerLine := `{"log":"error something broke\n","stream":"stdout","time":"2024-01-15T10:23:45.123Z"}` + "\n"

	if _, err := logFile.WriteString(dockerLine); err != nil {
		t.Fatal("Failed to write log line:", err)
	}

	if err := logFile.Sync(); err != nil {
		t.Fatal("Failed to sync log file:", err)
	}

	time.Sleep(time.Second)

	if got := totalRecords(received()); got != 1 {
		t.Fatalf("expected 1 record, got %d", got)
	}

	asked := provider.askedSources()
	if len(asked) != 1 || asked[0].Kind != SourceReceiver || asked[0].ReceiverName != "web" {
		t.Fatalf("unexpected resolved source(s): %+v", asked)
	}
}

// TestReceiverManagerContainerMatchedByTwoReceiversBothTail is the regression
// test for the "two receivers match the same container" design decision: no
// special-casing, both independently tail it under their own item.
func TestReceiverManagerContainerMatchedByTwoReceiversBothTail(t *testing.T) {
	t.Parallel()

	logFile, err := os.CreateTemp(t.TempDir(), "app-*.log")
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer logFile.Close()

	ctr := facts.FakeContainer{
		FakeID:            "id-1",
		FakeContainerName: "app-1",
		FakeLogPath:       logFile.Name(),
		FakeLabels:        map[string]string{"app": "web", "tier": "backend"},
	}

	cfg := config.OpenTelemetry{
		Receivers: map[string]config.LogReceiver{
			"recv-a": {"container_selectors": map[string]string{"app": "web"}},
			"recv-b": {"container_selectors": map[string]string{"tier": "backend"}},
		},
	}

	rm := newTestReceiverManager(t, cfg)

	provider := declineProvider()
	rm.RegisterSinkProvider(provider)

	rm.UpdateContainers(t.Context(), []facts.Container{ctr})

	asked := provider.askedSources()
	if len(asked) != 2 {
		t.Fatalf("expected the provider to be asked once per matching receiver (2 total), got %d: %+v", len(asked), asked)
	}
}

// TestReceiverManagerContainerExcludeVetoesEverything checks that a
// container matching an OpenTelemetry.ContainerExclude rule is never
// resolved at all, even though it would otherwise match a receiver's
// container_selectors.
func TestReceiverManagerContainerExcludeVetoesEverything(t *testing.T) {
	t.Parallel()

	ctr := facts.FakeContainer{
		FakeID: "id-1", FakeContainerName: "noisy", FakeLogPath: "/fake/noisy.log",
		FakeLabels: map[string]string{"app": "web"},
	}

	cfg := config.OpenTelemetry{
		Receivers: map[string]config.LogReceiver{
			"web": {"container_selectors": map[string]string{"app": "web"}},
		},
		ContainerExclude: []config.ContainerExcludeRule{{ContainerName: "noisy"}},
	}

	rm := newTestReceiverManager(t, cfg)

	provider := declineProvider()
	rm.RegisterSinkProvider(provider)

	rm.UpdateContainers(t.Context(), []facts.Container{ctr})

	if asked := provider.askedSources(); len(asked) != 0 {
		t.Fatalf("expected the excluded container to never be resolved, got %+v", asked)
	}
}

// TestReceiverManagerLogEnableFalseVetoesReceiverMatch checks that
// glouton.log_enable=false vetoes a container even when it's matched by an
// explicit receiver's container_selectors -- the veto always runs first.
func TestReceiverManagerLogEnableFalseVetoesReceiverMatch(t *testing.T) {
	t.Parallel()

	ctr := facts.FakeContainer{
		FakeID: "id-1", FakeContainerName: "app-1", FakeLogPath: "/fake/app.log",
		FakeLabels: map[string]string{"app": "web", ContainerLabelPrefix + "log_enable": "false"},
	}

	cfg := config.OpenTelemetry{
		Receivers: map[string]config.LogReceiver{
			"web": {"container_selectors": map[string]string{"app": "web"}},
		},
	}

	rm := newTestReceiverManager(t, cfg)

	provider := declineProvider()
	rm.RegisterSinkProvider(provider)

	rm.UpdateContainers(t.Context(), []facts.Container{ctr})

	if asked := provider.askedSources(); len(asked) != 0 {
		t.Fatalf("expected glouton.log_enable=false to veto even an explicit receiver match, got %+v", asked)
	}
}

// TestReceiverManagerReceiverMatchSkipsLabelFallback checks that a container
// matched by an explicit receiver is resolved exactly once (via the
// receiver), never additionally through the glouton.* label fallback path --
// avoiding double-counting.
func TestReceiverManagerReceiverMatchSkipsLabelFallback(t *testing.T) {
	t.Parallel()

	ctr := facts.FakeContainer{
		FakeID: "id-1", FakeContainerName: "app-1", FakeLogPath: "/fake/app.log",
		FakeLabels: map[string]string{"app": "web", ContainerLabelPrefix + "log_metrics": "some_rule"},
	}

	cfg := config.OpenTelemetry{
		Receivers: map[string]config.LogReceiver{
			"web": {"container_selectors": map[string]string{"app": "web"}},
		},
	}

	rm := newTestReceiverManager(t, cfg)

	provider := declineProvider()
	rm.RegisterSinkProvider(provider)

	rm.UpdateContainers(t.Context(), []facts.Container{ctr})

	asked := provider.askedSources()
	if len(asked) != 1 || asked[0].Kind != SourceReceiver {
		t.Fatalf("expected the container to be resolved exactly once, via the receiver only, got %+v", asked)
	}
}

// TestReceiverManagerContainerLabelFallbackSurfacesLogMetricsRule checks that
// a container matched by no receiver, carrying glouton.log_metrics, is
// resolved as a SourceContainerLabel source surfacing that label's raw
// value (otel/logmetrics resolves it against Log.MetricsRules itself).
func TestReceiverManagerContainerLabelFallbackSurfacesLogMetricsRule(t *testing.T) {
	t.Parallel()

	ctr := facts.FakeContainer{
		FakeID: "id-1", FakeContainerName: "app-1", FakeLogPath: "/fake/app.log",
		FakeLabels: map[string]string{ContainerLabelPrefix + "log_metrics": "known_web_errors"},
	}

	rm := newTestReceiverManager(t, config.OpenTelemetry{})

	provider := declineProvider()
	rm.RegisterSinkProvider(provider)

	rm.UpdateContainers(t.Context(), []facts.Container{ctr})

	asked := provider.askedSources()
	if len(asked) != 1 {
		t.Fatalf("expected exactly 1 resolved source, got %d: %+v", len(asked), asked)
	}

	src := asked[0]
	if src.Kind != SourceContainerLabel || src.LogMetricsRule != "known_web_errors" || src.Name != "app-1" || src.Container == nil {
		t.Fatalf("unexpected resolved source: %+v", src)
	}
}

// TestReceiverManagerLogEnableTrueImpliesSendLogs checks the back-compat
// fallback: glouton.log_enable=true implies send_logs=true unless send_logs
// is set explicitly, even when the global default is false.
func TestReceiverManagerLogEnableTrueImpliesSendLogs(t *testing.T) {
	t.Parallel()

	ctr := facts.FakeContainer{
		FakeID: "id-1", FakeContainerName: "app-1", FakeLogPath: "/fake/app.log",
		FakeLabels: map[string]string{ContainerLabelPrefix + "log_enable": "true"},
	}

	rm := newTestReceiverManager(t, config.OpenTelemetry{SendLogs: false})

	provider := declineProvider()
	rm.RegisterSinkProvider(provider)

	rm.UpdateContainers(t.Context(), []facts.Container{ctr})

	asked := provider.askedSources()
	if len(asked) != 1 {
		t.Fatalf("expected exactly 1 resolved source, got %d", len(asked))
	}

	if !asked[0].SendLogs {
		t.Errorf("expected glouton.log_enable=true to imply send_logs=true (global default is false), got SendLogs=%v", asked[0].SendLogs)
	}
}

// TestReceiverManagerExplicitSendLogsOverridesLogEnable checks that an
// explicit glouton.send_logs always wins over the log_enable=true
// implication.
func TestReceiverManagerExplicitSendLogsOverridesLogEnable(t *testing.T) {
	t.Parallel()

	ctr := facts.FakeContainer{
		FakeID: "id-1", FakeContainerName: "app-1", FakeLogPath: "/fake/app.log",
		FakeLabels: map[string]string{
			ContainerLabelPrefix + "log_enable": "true",
			ContainerLabelPrefix + "send_logs":  "false",
		},
	}

	rm := newTestReceiverManager(t, config.OpenTelemetry{SendLogs: true})

	provider := declineProvider()
	rm.RegisterSinkProvider(provider)

	rm.UpdateContainers(t.Context(), []facts.Container{ctr})

	asked := provider.askedSources()
	if len(asked) != 1 {
		t.Fatalf("expected exactly 1 resolved source, got %d", len(asked))
	}

	if asked[0].SendLogs {
		t.Errorf("expected an explicit glouton.send_logs=false to win over log_enable=true, got SendLogs=%v", asked[0].SendLogs)
	}
}

// TestReceiverManagerUnlabeledContainerFollowsAutoDiscovery checks that a
// container with NO glouton.* labels at all falls back to
// auto_discovery.container_and_service_enable for SendLogs, NOT the
// receiver-oriented OpenTelemetry.SendLogs default -- otherwise every
// container would start shipping by default (SendLogs defaults to true)
// regardless of auto_discovery being off by default.
func TestReceiverManagerUnlabeledContainerFollowsAutoDiscovery(t *testing.T) {
	t.Parallel()

	ctr := facts.FakeContainer{FakeID: "id-1", FakeContainerName: "app-1", FakeLogPath: "/fake/app.log"}

	rm := newTestReceiverManager(t, config.OpenTelemetry{
		SendLogs:      true,
		AutoDiscovery: config.AutoDiscovery{ContainerAndServiceEnable: false},
	})

	provider := declineProvider()
	rm.RegisterSinkProvider(provider)

	rm.UpdateContainers(t.Context(), []facts.Container{ctr})

	asked := provider.askedSources()
	if len(asked) != 1 {
		t.Fatalf("expected exactly 1 resolved source, got %d", len(asked))
	}

	if asked[0].SendLogs {
		t.Errorf("expected an unlabeled container to follow auto_discovery.container_and_service_enable=false, got SendLogs=%v", asked[0].SendLogs)
	}
}

// TestReceiverManagerUpdateContainersAddRemove checks that UpdateContainers
// reacts to a container's arrival (starting its tail) and disappearance
// (stopping and removing it) across two successive calls.
func TestReceiverManagerUpdateContainersAddRemove(t *testing.T) {
	t.Parallel()

	logFile, err := os.CreateTemp(t.TempDir(), "app-*.log")
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer logFile.Close()

	ctr := facts.FakeContainer{FakeID: "id-1", FakeContainerName: "app-1", FakeLogPath: logFile.Name()}

	rm := newTestReceiverManager(t, config.OpenTelemetry{})

	provider, _ := newRecordingProvider()
	rm.RegisterSinkProvider(provider)

	rm.UpdateContainers(t.Context(), []facts.Container{ctr})

	rm.l.Lock()
	ms, found := rm.byContainer[ctr.ID()]
	rm.l.Unlock()

	if !found {
		t.Fatal("Expected a label-fallback source for the container after it appeared")
	}

	ms.l.Lock()
	_, tailed := ms.containerLogFile[ctr.ID()]
	ms.l.Unlock()

	if !tailed {
		t.Fatal("Expected the container's log file to be tailed")
	}

	rm.UpdateContainers(t.Context(), nil)

	rm.l.Lock()
	_, stillPresent := rm.byContainer[ctr.ID()]
	rm.l.Unlock()

	if stillPresent {
		t.Fatal("Expected the container's source to be removed once it disappeared")
	}
}

// TestReceiverManagerNetworkWants checks that a receiver with a network:
// participation yields exactly one NetworkWant, its Consumer already fanned
// out to every SinkProvider that wants it.
func TestReceiverManagerNetworkWants(t *testing.T) {
	t.Parallel()

	cfg := config.OpenTelemetry{
		Receivers: map[string]config.LogReceiver{
			"billing": {"network": map[string]any{"receivers": []string{"otlp"}}},
		},
	}

	rm := newTestReceiverManager(t, cfg)

	provider, received := newRecordingProvider()
	rm.RegisterSinkProvider(provider)

	wants := rm.NetworkWants(t.Context())
	if len(wants) != 1 {
		t.Fatalf("expected exactly 1 NetworkWant, got %d", len(wants))
	}

	if len(wants[0].Receivers) != 1 || wants[0].Receivers[0] != "otlp" {
		t.Fatalf("expected the want to reference [otlp], got %v", wants[0].Receivers)
	}

	if wants[0].Consumer == nil {
		t.Fatal("expected a non-nil Consumer since a provider wants this receiver")
	}

	if err := wants[0].Consumer.ConsumeLogs(t.Context(), makeLogs("network")); err != nil {
		t.Fatal("ConsumeLogs returned an error:", err)
	}

	if got := totalRecords(received()); got != 1 {
		t.Fatalf("expected 1 record to reach the provider, got %d", got)
	}
}
