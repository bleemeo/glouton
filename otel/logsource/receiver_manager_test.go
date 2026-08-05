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
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/facts"

	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/plog"
)

// fakeSinkProvider is a SinkProvider test double recording asked sources and released containers.
type fakeSinkProvider struct {
	want func(src ResolvedSource) (consumer.Logs, bool)

	l        sync.Mutex
	asked    []ResolvedSource
	released []facts.Container
}

func (f *fakeSinkProvider) WantSource(_ context.Context, src ResolvedSource) (consumer.Logs, bool) {
	f.l.Lock()
	f.asked = append(f.asked, src)
	f.l.Unlock()

	return f.want(src)
}

func (f *fakeSinkProvider) ReleaseSource(_ context.Context, container facts.Container) {
	f.l.Lock()
	defer f.l.Unlock()

	f.released = append(f.released, container)
}

func (f *fakeSinkProvider) askedSources() []ResolvedSource {
	f.l.Lock()
	defer f.l.Unlock()

	return append([]ResolvedSource(nil), f.asked...)
}

func (f *fakeSinkProvider) releasedSources() []facts.Container {
	f.l.Lock()
	defer f.l.Unlock()

	return append([]facts.Container(nil), f.released...)
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

// TestReceiverManagerFanout tests that a single file tail fans out to every SinkProvider that wants it, and none is tailed when none do.
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

// TestReceiverManagerPersistsOffsetAcrossRestart tests that a line written while no manager is running is still picked up on restart, via the persisted offset.
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

// TestReceiverManagerStopsIncludeFileWhenItDisappears guards against a regression where a file matched by
// an include glob (e.g. a daily-rotated log) kept its receiver/extension running forever once the file
// stopped existing/matching, since only container-derived tails had a cleanup path (stopUnwantedContainerTails).
func TestReceiverManagerStopsIncludeFileWhenItDisappears(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	logFile, err := os.CreateTemp(dir, "app-*.log")
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer logFile.Close()

	cfg := config.OpenTelemetry{
		Receivers: map[string]config.LogReceiver{
			"app": {"include": []string{filepath.Join(dir, "*.log")}},
		},
	}

	rm := newTestReceiverManager(t, cfg)

	provider, _ := newRecordingProvider()
	rm.RegisterSinkProvider(provider)

	if err := rm.RescanReceivers(t.Context()); err != nil {
		t.Fatal("RescanReceivers failed:", err)
	}

	rm.l.Lock()
	ms := rm.receivers["app"]
	rm.l.Unlock()

	ms.l.Lock()
	watchingBefore := len(ms.watching)
	recvsBefore := len(ms.recvs)
	ms.l.Unlock()

	if watchingBefore != 1 || recvsBefore != 1 {
		t.Fatalf("expected exactly 1 watched/started file before removal, got watching=%d recvs=%d", watchingBefore, recvsBefore)
	}

	if err := os.Remove(logFile.Name()); err != nil {
		t.Fatal("Failed to remove log file:", err)
	}

	if err := rm.RescanReceivers(t.Context()); err != nil {
		t.Fatal("RescanReceivers failed:", err)
	}

	ms.l.Lock()
	defer ms.l.Unlock()

	if got := len(ms.watching); got != 0 {
		t.Errorf("expected the disappeared file to be forgotten from watching, got %d entries: %v", got, ms.watching)
	}

	if got := len(ms.recvs); got != 0 {
		t.Errorf("expected the disappeared file's receiver to be stopped and forgotten, got %d entries", got)
	}

	if got := len(ms.extIDs); got != 0 {
		t.Errorf("expected the disappeared file's extension to be forgotten, got %d entries", got)
	}
}

// fakeFileSizer is a FileSizer test double returning a fixed set of sizes.
type fakeFileSizer map[string]int64

func (f fakeFileSizer) SizesByFile() (map[string]int64, error) {
	return f, nil
}

// TestReceiverManagerSaveStateFoldsExternalSizers tests that RegisterExternalSizer's callback is folded into SaveState's snapshot, so it doesn't overwrite the other side's sizes.
func TestReceiverManagerSaveStateFoldsExternalSizers(t *testing.T) {
	t.Parallel()

	state := newMemoryState()

	rm, err := NewReceiverManager(config.OpenTelemetry{}, "/", state, nil)
	if err != nil {
		t.Fatal("NewReceiverManager failed:", err)
	}

	t.Cleanup(func() { rm.Shutdown(t.Context()) })

	rm.RegisterExternalSizer(func() []FileSizer {
		return []FileSizer{fakeFileSizer{"external/file.log": 42}}
	})

	rm.SaveState()

	sizes := GetLastFileSizesFromCache(state, LogFileSizesCacheKey)
	if sizes["external/file.log"] != 42 {
		t.Fatalf("expected the external sizer's file to be persisted under %q, got %+v", LogFileSizesCacheKey, sizes)
	}
}

// TestReceiverManagerContainerSelectorMatch tests that a container matching container_selectors is tailed under that receiver with the Docker envelope unwrapped.
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

// TestReceiverManagerContainerMatchedByTwoReceiversBothTail tests that a container matched by two receivers is tailed independently by both, with no special-casing.
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

// TestReceiverManagerContainerExcludeVetoesEverything tests that ContainerExclude vetoes a container even when it also matches a receiver's container_selectors.
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

// TestReceiverManagerLogEnableFalseDoesNotVetoReceiverMatch tests that glouton.log_enable=false does not veto a container explicitly matched by a receiver's container_selectors.
func TestReceiverManagerLogEnableFalseDoesNotVetoReceiverMatch(t *testing.T) {
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

	if asked := provider.askedSources(); len(asked) != 1 {
		t.Fatalf("expected the explicit receiver match to still be resolved despite glouton.log_enable=false, got %+v", asked)
	}
}

// TestReceiverManagerContainerExcludeStillVetoesReceiverMatch tests that ContainerExclude still vetoes a container even when explicitly matched by a receiver, unlike log_enable=false.
func TestReceiverManagerContainerExcludeStillVetoesReceiverMatch(t *testing.T) {
	t.Parallel()

	ctr := facts.FakeContainer{
		FakeID: "id-1", FakeContainerName: "app-1", FakeLogPath: "/fake/app.log",
		FakeLabels: map[string]string{"app": "web"},
	}

	cfg := config.OpenTelemetry{
		Receivers: map[string]config.LogReceiver{
			"web": {"container_selectors": map[string]string{"app": "web"}},
		},
		ContainerExclude: []config.ContainerExcludeRule{
			{ContainerName: "app-1"},
		},
	}

	rm := newTestReceiverManager(t, cfg)

	provider := declineProvider()
	rm.RegisterSinkProvider(provider)

	rm.UpdateContainers(t.Context(), []facts.Container{ctr})

	if asked := provider.askedSources(); len(asked) != 0 {
		t.Fatalf("expected container_exclude to still veto an explicit receiver match, got %+v", asked)
	}
}

// TestReceiverManagerReceiverMatchSkipsLabelFallback tests that a container matched by a receiver is resolved once, not also via the label fallback.
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

// TestReceiverManagerContainerLabelFallbackSurfacesLogMetricsRule tests that an unmatched container with glouton.log_metrics resolves as a SourceContainerLabel carrying that label's raw value.
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

// TestReceiverManagerContainerLabelChangeRebuildsFanout guards against a regression where an already-tracked
// container's glouton.* labels were only ever parsed and applied once (at first discovery); a later label
// change (e.g. a live "kubectl annotate" without recreating the container) was silently ignored forever.
func TestReceiverManagerContainerLabelChangeRebuildsFanout(t *testing.T) {
	t.Parallel()

	rm := newTestReceiverManager(t, config.OpenTelemetry{})

	provider := declineProvider()
	rm.RegisterSinkProvider(provider)

	ctrBefore := facts.FakeContainer{
		FakeID: "id-1", FakeContainerName: "app-1", FakeLogPath: "/fake/app.log",
		FakeLabels: map[string]string{ContainerLabelPrefix + "log_metrics": "rule_a"},
	}

	rm.UpdateContainers(t.Context(), []facts.Container{ctrBefore})

	// Same container ID, but the glouton.log_metrics label changed (e.g. a live annotation edit).
	ctrAfter := facts.FakeContainer{
		FakeID: "id-1", FakeContainerName: "app-1", FakeLogPath: "/fake/app.log",
		FakeLabels: map[string]string{ContainerLabelPrefix + "log_metrics": "rule_b"},
	}

	rm.UpdateContainers(t.Context(), []facts.Container{ctrAfter})

	asked := provider.askedSources()
	if len(asked) != 2 {
		t.Fatalf("expected 2 resolved sources (one per scan), got %d: %+v", len(asked), asked)
	}

	if asked[0].LogMetricsRule != "rule_a" {
		t.Errorf("expected the first scan to resolve LogMetricsRule %q, got %q", "rule_a", asked[0].LogMetricsRule)
	}

	if asked[1].LogMetricsRule != "rule_b" {
		t.Errorf("expected the label change to be picked up on the second scan (LogMetricsRule %q), got %q -- labels are only applied once at first discovery", "rule_b", asked[1].LogMetricsRule)
	}

	released := provider.releasedSources()
	if len(released) != 1 || released[0].ID() != "id-1" {
		t.Errorf("expected the stale fanout to be released once for container id-1 before rebuilding, got %+v", released)
	}
}

// TestReceiverManagerContainerLabelUnchangedDoesNotRebuild tests that an unchanged label set across scans
// doesn't spuriously rebuild the fanout (e.g. via a fresh *bool pointer that looks different on ==).
func TestReceiverManagerContainerLabelUnchangedDoesNotRebuild(t *testing.T) {
	t.Parallel()

	rm := newTestReceiverManager(t, config.OpenTelemetry{})

	provider := declineProvider()
	rm.RegisterSinkProvider(provider)

	ctr := facts.FakeContainer{
		FakeID: "id-1", FakeContainerName: "app-1", FakeLogPath: "/fake/app.log",
		FakeLabels: map[string]string{
			ContainerLabelPrefix + "log_enable":  "true",
			ContainerLabelPrefix + "log_metrics": "rule_a",
		},
	}

	rm.UpdateContainers(t.Context(), []facts.Container{ctr})
	rm.UpdateContainers(t.Context(), []facts.Container{ctr})

	if released := provider.releasedSources(); len(released) != 0 {
		t.Errorf("expected no release across scans with identical labels, got %+v", released)
	}

	asked := provider.askedSources()
	if len(asked) != 1 {
		t.Errorf("expected exactly 1 resolved source (second scan is a no-op since nothing changed), got %d: %+v", len(asked), asked)
	}
}

// TestReceiverManagerLogEnableTrueImpliesSendLogs tests that glouton.log_enable=true implies send_logs=true unless overridden, even if the global default is false.
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

// TestReceiverManagerExplicitSendLogsOverridesLogEnable tests that an explicit glouton.send_logs always wins over log_enable=true.
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

// TestReceiverManagerUnlabeledContainerFollowsAutoDiscovery tests that an unlabeled container's SendLogs follows auto_discovery.container_and_service_enable, not OpenTelemetry.SendLogs.
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

// TestReceiverManagerUpdateContainersAddRemove tests that UpdateContainers starts tailing on arrival and stops/removes on disappearance.
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

// TestReceiverManagerReleasesProvidersWhenContainerDisappears tests that a container's disappearance calls ReleaseSource on every SinkProvider, not just tearing down the tail, to avoid leaking per-container resources.
func TestReceiverManagerReleasesProvidersWhenContainerDisappears(t *testing.T) {
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

	if released := provider.releasedSources(); len(released) != 0 {
		t.Fatalf("expected nothing released while the container is still present, got %+v", released)
	}

	rm.UpdateContainers(t.Context(), nil)

	released := provider.releasedSources()
	if len(released) != 1 {
		t.Fatalf("expected exactly one ReleaseSource call once the container disappeared, got %+v", released)
	}

	if released[0] == nil || released[0].ID() != ctr.ID() {
		t.Fatalf("expected the released source to identify the disappeared container, got %+v", released[0])
	}
}

// TestReceiverManagerNetworkWants tests that a receiver with network participation yields one NetworkWant whose Consumer fans out to every SinkProvider that wants it.
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
