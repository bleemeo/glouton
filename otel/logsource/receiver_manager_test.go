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
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/facts"

	"github.com/google/go-cmp/cmp"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/receiver"
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

// allLogRecordAttrValues returns every log record's string value for attribute key, across every
// batch, skipping records missing it.
func allLogRecordAttrValues(batches []plog.Logs, key string) []string {
	var values []string

	for _, b := range batches {
		for _, rl := range b.ResourceLogs().All() {
			for _, sl := range rl.ScopeLogs().All() {
				for _, lr := range sl.LogRecords().All() {
					if v, ok := lr.Attributes().Get(key); ok {
						values = append(values, v.Str())
					}
				}
			}
		}
	}

	return values
}

// firstLogRecordTagValue returns the first log record's string value for the "tag" attribute (the
// value every test operator config in this file adds), across every batch.
func firstLogRecordTagValue(batches []plog.Logs) (string, bool) {
	values := allLogRecordAttrValues(batches, "tag")
	if len(values) == 0 {
		return "", false
	}

	return values[0], true
}

// newRecordingProviderFor returns a SinkProvider that only wants the named receiver's source, and a
// function reading back every batch its sink received.
func newRecordingProviderFor(receiverName string) (*fakeSinkProvider, func() []plog.Logs) {
	sink, received := recordingLogsConsumer()

	return &fakeSinkProvider{
		want: func(src ResolvedSource) (consumer.Logs, bool) {
			if src.ReceiverName != receiverName {
				return nil, false
			}

			return sink, true
		},
	}, received
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

// TestReceiverManagerStopUnwantedIncludeFilesKeepsOffsetWhenResolutionIncomplete tests that a file dropping
// out of `wanted` during an incomplete glob resolution (e.g. a transient permission/IO error on one
// pattern) stops the tail but keeps its persisted offset, instead of forgetting it like a genuine
// disappearance would.
func TestReceiverManagerStopUnwantedIncludeFilesKeepsOffsetWhenResolutionIncomplete(t *testing.T) {
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
	extIDs := ms.extIDs[logFile.Name()]
	ms.l.Unlock()

	if len(extIDs) == 0 {
		t.Fatal("expected the file to have a registered persistent extension")
	}

	name := extIDs[0].Name()

	// Seed some offset metadata for it, as if the tail had already read part of the file.
	rm.persister.l.Lock()
	rm.persister.metadataPerReceiver[name] = map[string][]byte{"offset": []byte("123")}
	rm.persister.l.Unlock()

	// Simulate this cycle's glob resolution having failed on some other pattern: the file drops out of
	// wanted, but complete=false says not to trust that as a genuine disappearance.
	rm.stopUnwantedIncludeFiles(t.Context(), ms, map[string]bool{}, false)

	ms.l.Lock()
	stillWatching := len(ms.watching)
	ms.l.Unlock()

	if stillWatching != 0 {
		t.Errorf("expected the tail to stop regardless of forget, got %d still watched", stillWatching)
	}

	rm.persister.l.Lock()
	_, stillKnown := rm.persister.metadataPerReceiver[name]
	rm.persister.l.Unlock()

	if !stillKnown {
		t.Error("expected the offset to survive stopUnwantedIncludeFiles when the resolution was incomplete (forget=false)")
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

	rm.UpdateContainers(t.Context(), []facts.Container{ctr}, nil, true)

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

	rm.UpdateContainers(t.Context(), []facts.Container{ctr}, nil, true)

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

	rm.UpdateContainers(t.Context(), []facts.Container{ctr}, nil, true)

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

	rm.UpdateContainers(t.Context(), []facts.Container{ctr}, nil, true)

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

	rm.UpdateContainers(t.Context(), []facts.Container{ctr}, nil, true)

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

	rm.UpdateContainers(t.Context(), []facts.Container{ctr}, nil, true)

	asked := provider.askedSources()
	if len(asked) != 1 || asked[0].Kind != SourceReceiver {
		t.Fatalf("expected the container to be resolved exactly once, via the receiver only, got %+v", asked)
	}
}

// TestReceiverManagerServiceTailedChangeRebuildsFanout tests that a container's label source is rebuilt,
// re-asking every provider, when it starts (and stops) being tailed by otel/logprocessing's service path.
// Without that, whichever path happened to start first would win forever: a container tailed via its
// glouton.* labels before its service became active (container up, port not listening yet) would keep
// shipping alongside the service tail, duplicating every line for good.
func TestReceiverManagerServiceTailedChangeRebuildsFanout(t *testing.T) {
	t.Parallel()

	ctr := facts.FakeContainer{
		FakeID: "id-1", FakeContainerName: "postgres-1", FakeLogPath: "/fake/postgres.log",
		FakeLabels: map[string]string{ContainerLabelPrefix + "send_logs": "true"},
	}

	rm := newTestReceiverManager(t, config.OpenTelemetry{})

	sink, _ := recordingLogsConsumer()

	// Mirrors logprocessing.WantSource: wants the label source only while the service path isn't
	// already tailing that container.
	var serviceTailed atomic.Bool

	provider := &fakeSinkProvider{
		want: func(ResolvedSource) (consumer.Logs, bool) {
			if serviceTailed.Load() {
				return nil, false
			}

			return sink, true
		},
	}
	rm.RegisterSinkProvider(provider)

	fanoutIsSet := func() bool {
		t.Helper()

		rm.l.Lock()
		defer rm.l.Unlock()

		ms, found := rm.byContainer["id-1"]
		if !found {
			t.Fatal("expected the container's label source to stay tracked")
		}

		return ms.fanout != nil
	}

	// Cycle 1: the service path isn't tailing it, so the label source ships.
	rm.UpdateContainers(t.Context(), []facts.Container{ctr}, nil, true)

	if !fanoutIsSet() {
		t.Fatal("expected the label source to be wanted before the service path tails the container")
	}

	// Cycle 2: the service path now tails it -- rebuild, so the provider is re-asked and declines.
	serviceTailed.Store(true)
	rm.UpdateContainers(t.Context(), []facts.Container{ctr}, map[string]bool{"id-1": true}, true)

	if fanoutIsSet() {
		t.Fatal("expected the rebuilt label source to be declined once the service path tails the container")
	}

	// Cycle 3: the service tail is gone (e.g. its setup started failing) -- shipping must resume.
	serviceTailed.Store(false)
	rm.UpdateContainers(t.Context(), []facts.Container{ctr}, nil, true)

	if !fanoutIsSet() {
		t.Fatal("expected the label source to be wanted again once the service path stopped tailing it")
	}

	if asked := provider.askedSources(); len(asked) != 3 {
		t.Fatalf("expected the provider to be re-asked on each change (3 total), got %d: %+v", len(asked), asked)
	}
}

// TestReceiverManagerNoTailWhenEveryProviderDeclines tests that a label-detected container whose source
// no provider wants is resolved but never tailed. This is what removes the duplicate when
// otel/logprocessing declines a container it already ships from its service path: the source still gets
// offered (so otel/logmetrics can still pick up a glouton.log_metrics rule), but with an empty fanout no
// physical tail is started.
func TestReceiverManagerNoTailWhenEveryProviderDeclines(t *testing.T) {
	t.Parallel()

	logFile, err := os.CreateTemp(t.TempDir(), "postgres-*.log")
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer logFile.Close()

	ctr := facts.FakeContainer{
		FakeID: "id-1", FakeContainerName: "postgres-1", FakeLogPath: logFile.Name(),
		FakeLabels: map[string]string{ContainerLabelPrefix + "send_logs": "true"},
	}

	rm := newTestReceiverManager(t, config.OpenTelemetry{})

	provider := declineProvider()
	rm.RegisterSinkProvider(provider)

	rm.UpdateContainers(t.Context(), []facts.Container{ctr}, nil, true)

	if asked := provider.askedSources(); len(asked) != 1 {
		t.Fatalf("expected the source to still be offered to providers, got %+v", asked)
	}

	ms, found := rm.byContainer["id-1"]
	if !found {
		t.Fatal("expected the container's source to be tracked in byContainer")
	}

	if ms.fanout != nil {
		t.Fatal("expected no fanout when every provider declines")
	}

	if len(ms.containerLogFile) != 0 {
		t.Fatalf("expected no tail to be started when every provider declines, got %+v", ms.containerLogFile)
	}
}

// TestReceiverManagerContainerLabelTailDoesNotCollideWithLogprocessingContainerReceiver guards against a
// regression where updateLabelContainers' own tail (persistNamespace == "") built the exact same
// persisted-offset identity otel/logprocessing's containerReceiver uses for a service-discovered
// container's shipping tail ("container/" + id + "/" + file -- see containers.go's makeStorageFn, not
// importable here without a package cycle). The two tails can be simultaneously live: a
// service-discovered container that also carries glouton.log_metrics has logprocessing decline it
// (already shipping) while logmetrics still wants it, so receiver_manager starts its own physical tail
// purely for the metric, alongside logprocessing's. Sharing one identity between two live tails lets each
// one's save silently clobber the other's (PersistHost.storeMetadata replaces the whole per-name entry on
// every save).
func TestReceiverManagerContainerLabelTailDoesNotCollideWithLogprocessingContainerReceiver(t *testing.T) {
	t.Parallel()

	logFile, err := os.CreateTemp(t.TempDir(), "postgres-*.log")
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer logFile.Close()

	ctr := facts.FakeContainer{
		FakeID: "id-1", FakeContainerName: "postgres-1", FakeLogPath: logFile.Name(),
		FakeLabels: map[string]string{ContainerLabelPrefix + "send_logs": "true"},
	}

	rm := newTestReceiverManager(t, config.OpenTelemetry{})

	provider, _ := newRecordingProvider()
	rm.RegisterSinkProvider(provider)

	rm.UpdateContainers(t.Context(), []facts.Container{ctr}, nil, true)

	ms, found := rm.byContainer["id-1"]
	if !found {
		t.Fatal("expected the container's source to be tracked in byContainer")
	}

	ms.l.Lock()
	extIDs := ms.containerExtIDs["id-1"]
	ms.l.Unlock()

	if len(extIDs) == 0 {
		t.Fatal("expected the container tail to have a registered persistent extension")
	}

	// Mirrors otel/logprocessing/containers.go's own construction for a service-discovered container's
	// shipping tail: "container/" + ctr.Attributes.ID + metadataKeySeparator + logFile.
	logprocessingName := "container/" + ctr.ID() + "/" + logFile.Name()

	for _, id := range extIDs {
		if id.Name() == logprocessingName {
			t.Fatalf(
				"container-label tail's persist name %q collides with logprocessing's containerReceiver name for the same container/file",
				id.Name(),
			)
		}
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

	rm.UpdateContainers(t.Context(), []facts.Container{ctr}, nil, true)

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

	rm.UpdateContainers(t.Context(), []facts.Container{ctrBefore}, nil, true)

	// Same container ID, but the glouton.log_metrics label changed (e.g. a live annotation edit).
	ctrAfter := facts.FakeContainer{
		FakeID: "id-1", FakeContainerName: "app-1", FakeLogPath: "/fake/app.log",
		FakeLabels: map[string]string{ContainerLabelPrefix + "log_metrics": "rule_b"},
	}

	rm.UpdateContainers(t.Context(), []facts.Container{ctrAfter}, nil, true)

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

	rm.UpdateContainers(t.Context(), []facts.Container{ctr}, nil, true)
	rm.UpdateContainers(t.Context(), []facts.Container{ctr}, nil, true)

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

	rm := newTestReceiverManager(t, config.OpenTelemetry{ReceiversDefaultSendLogs: false})

	provider := declineProvider()
	rm.RegisterSinkProvider(provider)

	rm.UpdateContainers(t.Context(), []facts.Container{ctr}, nil, true)

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

	rm := newTestReceiverManager(t, config.OpenTelemetry{ReceiversDefaultSendLogs: true})

	provider := declineProvider()
	rm.RegisterSinkProvider(provider)

	rm.UpdateContainers(t.Context(), []facts.Container{ctr}, nil, true)

	asked := provider.askedSources()
	if len(asked) != 1 {
		t.Fatalf("expected exactly 1 resolved source, got %d", len(asked))
	}

	if asked[0].SendLogs {
		t.Errorf("expected an explicit glouton.send_logs=false to win over log_enable=true, got SendLogs=%v", asked[0].SendLogs)
	}
}

// TestReceiverManagerUnlabeledContainerFollowsAutoDiscovery tests that an unlabeled container's SendLogs follows auto_discovery.container_and_service_enable, not OpenTelemetry.ReceiversDefaultSendLogs.
func TestReceiverManagerUnlabeledContainerFollowsAutoDiscovery(t *testing.T) {
	t.Parallel()

	ctr := facts.FakeContainer{FakeID: "id-1", FakeContainerName: "app-1", FakeLogPath: "/fake/app.log"}

	rm := newTestReceiverManager(t, config.OpenTelemetry{
		ReceiversDefaultSendLogs: true,
		AutoDiscovery:            config.AutoDiscovery{ContainerAndServiceEnable: false},
	})

	provider := declineProvider()
	rm.RegisterSinkProvider(provider)

	rm.UpdateContainers(t.Context(), []facts.Container{ctr}, nil, true)

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

	rm.UpdateContainers(t.Context(), []facts.Container{ctr}, nil, true)

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

	rm.UpdateContainers(t.Context(), nil, nil, true)

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

	rm.UpdateContainers(t.Context(), []facts.Container{ctr}, nil, true)

	if released := provider.releasedSources(); len(released) != 0 {
		t.Fatalf("expected nothing released while the container is still present, got %+v", released)
	}

	rm.UpdateContainers(t.Context(), nil, nil, true)

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
			"billing": {"from_listeners": []string{"otlp"}},
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

// TestReceiverManagerNetworkWantsAppliesOperators guards the from_listeners path against silently
// skipping a receiver's own operators: before this fix, only include/container-tail sources ran
// operators (embedded into a filelogreceiver/execlogreceiver's own config by SetupLogReceiverFactories),
// while network-sourced logs went straight into ms.fanout untouched.
func TestReceiverManagerNetworkWantsAppliesOperators(t *testing.T) {
	t.Parallel()

	cfg := config.OpenTelemetry{
		Receivers: map[string]config.LogReceiver{
			"billing": {
				"from_listeners": []string{"otlp"},
				"operators": []any{
					map[string]any{"type": "add", "field": "attributes.tag", "value": "net"},
				},
			},
		},
	}

	rm := newTestReceiverManager(t, cfg)

	provider, received := newRecordingProvider()
	rm.RegisterSinkProvider(provider)

	wants := rm.NetworkWants(t.Context())
	if len(wants) != 1 {
		t.Fatalf("expected exactly 1 NetworkWant, got %d", len(wants))
	}

	if err := wants[0].Consumer.ConsumeLogs(t.Context(), makeLogs("network")); err != nil {
		t.Fatal("ConsumeLogs returned an error:", err)
	}

	got, ok := firstLogRecordTagValue(received())
	if !ok {
		t.Fatal("expected the operator's attribute to be present on the received record")
	}

	if got != "net" {
		t.Fatalf("expected attribute value %q, got %q", "net", got)
	}
}

// TestReceiverManagerAppliesOperatorsToBothIncludeAndNetwork guards the exact scenario reported: a
// receiver mixing include (file tail) and from_listeners (network) sources must apply the same
// operators to both -- previously only the file-tailed logs got the transform, giving the impression
// that operators were ignored outright.
func TestReceiverManagerAppliesOperatorsToBothIncludeAndNetwork(t *testing.T) {
	t.Parallel()

	logFile, err := os.CreateTemp(t.TempDir(), "app-*.log")
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer logFile.Close()

	cfg := config.OpenTelemetry{
		Receivers: map[string]config.LogReceiver{
			"app": {
				"include":        []string{logFile.Name()},
				"from_listeners": []string{"otlp"},
				"operators": []any{
					map[string]any{"type": "add", "field": "attributes.tag", "value": "net_default"},
				},
			},
		},
	}

	rm := newTestReceiverManager(t, cfg)

	provider, received := newRecordingProvider()
	rm.RegisterSinkProvider(provider)

	if err := rm.RescanReceivers(t.Context()); err != nil {
		t.Fatal("RescanReceivers failed:", err)
	}

	time.Sleep(500 * time.Millisecond)

	if _, err := logFile.WriteString("line one\n"); err != nil {
		t.Fatal("Failed to write log line:", err)
	}

	if err := logFile.Sync(); err != nil {
		t.Fatal("Failed to sync log file:", err)
	}

	time.Sleep(time.Second)

	wants := rm.NetworkWants(t.Context())
	if len(wants) != 1 {
		t.Fatalf("expected exactly 1 NetworkWant, got %d", len(wants))
	}

	if err := wants[0].Consumer.ConsumeLogs(t.Context(), makeLogs("network")); err != nil {
		t.Fatal("ConsumeLogs returned an error:", err)
	}

	values := allLogRecordAttrValues(received(), "tag")
	if len(values) != 2 {
		t.Fatalf("expected 2 records carrying the operator's attribute (one file-tailed, one network-sourced), got %d: %v", len(values), values)
	}

	for _, v := range values {
		if v != "net_default" {
			t.Errorf("expected every record's tag attribute to be %q, got %q", "net_default", v)
		}
	}
}

// TestReceiverManagerNetworkWantsSharedListenerAppliesEachReceiversOwnOperators checks that two
// receivers sharing one from_listeners listener name each get their own operators applied to their own
// copy: PlanSharedNetworkListeners/FanoutLogs already deep-copy each incoming batch per referencing
// receiver before this fix's per-receiver operator wrapping runs, so per-receiver operators stay
// correctly attributed even when the physical listener is shared.
func TestReceiverManagerNetworkWantsSharedListenerAppliesEachReceiversOwnOperators(t *testing.T) {
	t.Parallel()

	cfg := config.OpenTelemetry{
		Receivers: map[string]config.LogReceiver{
			"app1": {
				"from_listeners": []string{"otlp"},
				"operators": []any{
					map[string]any{"type": "add", "field": "attributes.tag", "value": "one"},
				},
			},
			"app2": {
				"from_listeners": []string{"otlp"},
				"operators": []any{
					map[string]any{"type": "add", "field": "attributes.tag", "value": "two"},
				},
			},
		},
	}

	rm := newTestReceiverManager(t, cfg)

	provider1, received1 := newRecordingProviderFor("app1")
	provider2, received2 := newRecordingProviderFor("app2")

	rm.RegisterSinkProvider(provider1)
	rm.RegisterSinkProvider(provider2)

	wants := rm.NetworkWants(t.Context())
	if len(wants) != 2 {
		t.Fatalf("expected exactly 2 NetworkWants, got %d", len(wants))
	}

	listeners := map[string]config.NetworkListener{
		"otlp": {Protocols: config.NetworkProtocols{GRPC: &config.NetworkEndpoint{Endpoint: "127.0.0.1:4317"}}},
	}

	planned, warnings := PlanSharedNetworkListeners(listeners, wants)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}

	if len(planned) != 1 {
		t.Fatalf("expected exactly 1 shared planned receiver, got %d", len(planned))
	}

	if err := planned[0].Sink.ConsumeLogs(t.Context(), makeLogs("network")); err != nil {
		t.Fatal("ConsumeLogs returned an error:", err)
	}

	v1, ok1 := firstLogRecordTagValue(received1())
	if !ok1 || v1 != "one" {
		t.Fatalf("expected app1's own record to carry tag=one, got %q (found=%v)", v1, ok1)
	}

	v2, ok2 := firstLogRecordTagValue(received2())
	if !ok2 || v2 != "two" {
		t.Fatalf("expected app2's own record to carry tag=two, got %q (found=%v)", v2, ok2)
	}
}

// TestManagedSourceSizesByFileSkipsOnlyTheFailingFile guards against a regression where one file's
// non-ErrNotExist stat error (e.g. a permission flip after logrotate, or sudoStatFile's timeout under
// load) aborted SizesByFile entirely, discarding every other file's already-successfully-read size for
// this managedSource -- which SaveLastFileSizesToCache then drops wholesale on any error, risking a
// stale/lost resume offset for the healthy files too.
// fakeArchiveWriter is a minimal types.ArchiveWriter test double recording bytes written per filename.
type fakeArchiveWriter struct {
	files map[string]*bytes.Buffer
}

func (w *fakeArchiveWriter) Create(filename string) (io.Writer, error) {
	if w.files == nil {
		w.files = make(map[string]*bytes.Buffer)
	}

	buf := &bytes.Buffer{}
	w.files[filename] = buf

	return buf, nil
}

func (w *fakeArchiveWriter) CurrentFileName() string {
	return ""
}

// Test that ReceiverManager.DiagnosticArchive writes the shared persister's registered-extension state,
// closing the gap where a diagnostic bundle taken with log shipping/Bleemeo disabled (so
// otel/logprocessing's Manager, which shares this exact persister, never runs) had no read-offset/
// extension state at all, even though log-to-metric receivers using that persister were still active.
func TestReceiverManagerDiagnosticArchiveWritesPersisterState(t *testing.T) {
	t.Parallel()

	rm := newTestReceiverManager(t, config.OpenTelemetry{})

	id := rm.persister.NewPersistentExt("test-receiver")

	writer := &fakeArchiveWriter{}

	if err := rm.DiagnosticArchive(t.Context(), writer); err != nil {
		t.Fatal("DiagnosticArchive returned an error:", err)
	}

	buf, found := writer.files[persistArchivePath]
	if !found {
		t.Fatalf("Expected a %q entry written to the archive, got %v", persistArchivePath, writer.files)
	}

	if !strings.Contains(buf.String(), id.String()) {
		t.Errorf("Expected the archived state to mention the registered extension %q, got %s", id.String(), buf.String())
	}
}

func TestManagedSourceSizesByFileSkipsOnlyTheFailingFile(t *testing.T) {
	t.Parallel()

	ms := newManagedSource("recv", SourceReceiver, nil, nil)

	ms.sizeFnByFile["good.log"] = func() (int64, error) { return 42, nil }
	ms.sizeFnByFile["bad.log"] = func() (int64, error) { return 0, errors.New("permission denied") } //nolint:err113
	ms.sizeFnByFile["gone.log"] = func() (int64, error) { return 0, fs.ErrNotExist }

	sizes, err := ms.SizesByFile()
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if diff := cmp.Diff(map[string]int64{"good.log": 42}, sizes); diff != "" {
		t.Fatalf("Unexpected sizes (-want +got):\n%s", diff)
	}
}

// TestReceiverManagerRescanAndUpdateContainersNoopAfterShutdown guards against a regression where
// Shutdown left the ReceiverManager reusable: a discovery goroutine's RescanReceivers/UpdateContainers
// call racing (or arriving after) Shutdown would start a new tail and register a persistent extension
// that nothing would ever stop or save again, since SaveState/Shutdown already ran.
func TestReceiverManagerRescanAndUpdateContainersNoopAfterShutdown(t *testing.T) {
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

	rm, err := NewReceiverManager(cfg, "/", newMemoryState(), nil)
	if err != nil {
		t.Fatal("NewReceiverManager failed:", err)
	}

	provider, _ := newRecordingProvider()
	rm.RegisterSinkProvider(provider)

	rm.Shutdown(t.Context())

	if err := rm.RescanReceivers(t.Context()); err != nil {
		t.Fatal("RescanReceivers failed:", err)
	}

	if got := len(rm.receivers); got != 0 {
		t.Errorf("expected RescanReceivers to be a no-op after Shutdown, got %d receiver(s) started", got)
	}

	ctr := facts.FakeContainer{
		FakeID: "id-1", FakeContainerName: "postgres-1", FakeLogPath: logFile.Name(),
		FakeLabels: map[string]string{ContainerLabelPrefix + "send_logs": "true"},
	}

	rm.UpdateContainers(t.Context(), []facts.Container{ctr}, nil, true)

	if got := len(rm.byContainer); got != 0 {
		t.Errorf("expected UpdateContainers to be a no-op after Shutdown, got %d container source(s) started", got)
	}
}

// TestContainerMatchersMemoizedAndConsistentAcrossCalls guards against a regression where
// containerMatchers re-decoded every container_name/container_selectors receiver's config on every call,
// duplicating the exact same decode work across ContainerIDsShippedByReceivers and UpdateContainers in the
// same discovery cycle -- the per-cycle cost ensureReceiverSource's own decode cache was specifically added
// to avoid. Since rm.cfg never changes for the manager's lifetime, containerMatchers' result can and should
// be computed once.
func TestContainerMatchersMemoizedAndConsistentAcrossCalls(t *testing.T) {
	t.Parallel()

	logFile, err := os.CreateTemp(t.TempDir(), "app-*.log")
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer logFile.Close()

	cfg := config.OpenTelemetry{
		Receivers: map[string]config.LogReceiver{
			"app": {"container_name": "app-1", "send_logs": true},
		},
	}

	rm := newTestReceiverManager(t, cfg)

	ctr := facts.FakeContainer{FakeID: "id-1", FakeContainerName: "app-1", FakeLogPath: logFile.Name()}

	rm.l.Lock()
	first := rm.containerMatchers()
	second := rm.containerMatchers()
	rm.l.Unlock()

	if diff := cmp.Diff(first, second, cmp.AllowUnexported(containerMatcher{})); diff != "" {
		t.Fatalf("Expected containerMatchers to return identical results across calls (-first +second):\n%s", diff)
	}

	// Exercise both real call paths, in the same order agent.go's discovery cycle uses them, to prove the
	// cached value is actually correct end-to-end, not just self-consistent.
	shipped := rm.ContainerIDsShippedByReceivers([]facts.Container{ctr})
	if !shipped[ctr.ID()] {
		t.Fatalf("expected the container to be reported as shipped by the container_name receiver, got %v", shipped)
	}

	provider, _ := newRecordingProvider()
	rm.RegisterSinkProvider(provider)

	rm.UpdateContainers(t.Context(), []facts.Container{ctr}, nil, true)

	rm.l.Lock()
	_, found := rm.receivers["app"]
	rm.l.Unlock()

	if !found {
		t.Fatal("expected the container_name receiver to have matched the container and started a tail")
	}
}

// TestIncludeTailDoesNotCollideWithLogprocessingReceiver guards against a regression where an
// include-pattern tail's persisted-offset identity was the bare "<receiver>/<file>", byte-identical to
// otel/logprocessing's logReceiver naming (r.name + metadataKeySeparator + logFile) -- and both packages
// share one PersistHost. logprocessing builds receivers named literally "syslog"/"syslog-auth"/"auditd"
// for auto-discovery, so a user receiver of the same name including the same file resolved to one
// component.ID: both live tails wrote through to a single metadataPerReceiver entry, and each save
// (which replaces that entry wholesale) clobbered the other's offset. Same hazard, and same namespacing
// fix, as containerLabelPersistNamespace on the container path.
func TestIncludeTailDoesNotCollideWithLogprocessingReceiver(t *testing.T) {
	t.Parallel()

	logFile, err := os.CreateTemp(t.TempDir(), "syslog-*.log")
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer logFile.Close()

	// "syslog" is one of the names otel/logprocessing uses for its own auto-discovery receivers.
	const receiverName = "syslog"

	cfg := config.OpenTelemetry{
		Receivers: map[string]config.LogReceiver{
			receiverName: {"include": []string{logFile.Name()}},
		},
	}

	rm := newTestReceiverManager(t, cfg)

	provider, _ := newRecordingProvider()
	rm.RegisterSinkProvider(provider)

	if err := rm.RescanReceivers(t.Context()); err != nil {
		t.Fatal("RescanReceivers failed:", err)
	}

	rm.l.Lock()
	ms, found := rm.receivers[receiverName]
	rm.l.Unlock()

	if !found {
		t.Fatal("Expected a managedSource for the receiver")
	}

	ms.l.Lock()
	extIDs := ms.extIDs[logFile.Name()]
	ms.l.Unlock()

	if len(extIDs) == 0 {
		t.Fatal("Expected the include file to have registered a persistent extension")
	}

	// The exact literal otel/logprocessing's logReceiver.startFile would use for the same name/file.
	collidingName := receiverName + "/" + logFile.Name()

	for _, id := range extIDs {
		if id.Name() == collidingName {
			t.Errorf(
				"include tail persists under %q, colliding with otel/logprocessing's identity for the same receiver name and file",
				id.Name(),
			)
		}
	}
}

// TestUpdateContainersKeepsOffsetWhenListIncomplete guards against permanent offset loss on any cycle
// whose container enumeration wasn't authoritative. Both container teardown paths used to forget
// unconditionally, on the assumption that a container missing from the list is "gone for good" -- but
// agent.go's guard only catches an explicit error, and a runtime can report an empty list with no error at
// all (merge.Runtime.Containers returns (nil, nil) when no runtime yielded anything and none errored;
// docker.Docker.Containers swallows its error outright until it has worked once, covering the whole window
// around a daemon restart). Forgetting is permanent and fileconsumer's StartAt defaults to "end", so every
// line written during the gap would be skipped for good.
func TestUpdateContainersKeepsOffsetWhenListIncomplete(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		cfg        config.OpenTelemetry
		labels     map[string]string
		sourceName string
	}{
		{
			// The glouton.*-label fallback path, torn down via updateLabelContainers/shutdownSource.
			name:       "label fallback tail",
			cfg:        config.OpenTelemetry{},
			labels:     map[string]string{ContainerLabelPrefix + "send_logs": "true"},
			sourceName: "",
		},
		{
			// The container_name receiver path, torn down via stopUnwantedContainerTails.
			name: "receiver-matched tail",
			cfg: config.OpenTelemetry{
				Receivers: map[string]config.LogReceiver{
					"app": {"container_name": "app-1", "send_logs": true},
				},
			},
			sourceName: "app",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			logFile, err := os.CreateTemp(t.TempDir(), "ctr-*.log")
			if err != nil {
				t.Fatal("Can't create log file:", err)
			}

			defer logFile.Close()

			ctr := facts.FakeContainer{
				FakeID: "id-1", FakeContainerName: "app-1",
				FakeLogPath: logFile.Name(), FakeLabels: tc.labels,
			}

			rm := newTestReceiverManager(t, tc.cfg)

			provider, _ := newRecordingProvider()
			rm.RegisterSinkProvider(provider)

			rm.UpdateContainers(t.Context(), []facts.Container{ctr}, nil, true)

			rm.l.Lock()

			ms := rm.byContainer[ctr.ID()]
			if tc.sourceName != "" {
				ms = rm.receivers[tc.sourceName]
			}

			rm.l.Unlock()

			if ms == nil {
				t.Fatal("expected a managedSource for the container")
			}

			ms.l.Lock()
			extIDs := ms.containerExtIDs[ctr.ID()]
			ms.l.Unlock()

			if len(extIDs) == 0 {
				t.Fatal("expected the container tail to have a registered persistent extension")
			}

			name := extIDs[0].Name()

			// Seed offset metadata, as if the tail had already read part of the file.
			rm.persister.l.Lock()
			rm.persister.metadataPerReceiver[name] = map[string][]byte{"offset": []byte("123")}
			rm.persister.l.Unlock()

			// The runtime enumerated nothing, without reporting an error: not to be trusted as
			// "every container was removed".
			rm.UpdateContainers(t.Context(), nil, nil, false)

			rm.persister.l.Lock()
			metadata, found := rm.persister.metadataPerReceiver[name]
			rm.persister.l.Unlock()

			if !found || len(metadata) == 0 {
				t.Errorf("expected %q to keep its persisted offset after an incomplete container list, got %v", name, metadata)
			}
		})
	}
}

// TestReceiverWithIncludeAndContainerMatcherTailsFileOnce guards against a receiver that carries both
// include patterns and a container matcher -- which validateLogReceivers permits, and which a broad glob
// like /var/log/containers/*.log makes routine on Kubernetes -- starting two filelogreceivers on the same
// file. startIncludeFiles dedupes by file through ms.watching, but startContainerTail used to check only
// ms.containerLogFile[id] and never consult ms.watching, so both paths tailed it and every line was
// delivered twice: double shipping, and double counting for log-to-metric.
//
// The teardown half was worse. Both paths write the file-keyed ms.watching/ms.sizeFnByFile, and each
// teardown deleted those keys unconditionally, so losing the container erased the bookkeeping of the
// still-running include tail: the file lost its size reporting, and the next RescanReceivers, seeing it
// unwatched, started a *second* include tail beside the first. Both then checkpointed under the same
// includePersistName and overwrote each other's read offset, and nothing ever stopped the leaked one --
// one permanent duplicate per container-churn event.
func TestReceiverWithIncludeAndContainerMatcherTailsFileOnce(t *testing.T) {
	t.Parallel()

	logFile, err := os.CreateTemp(t.TempDir(), "app-*.log")
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer logFile.Close()

	cfg := config.OpenTelemetry{
		Receivers: map[string]config.LogReceiver{
			"app": {
				"include":        []string{logFile.Name()},
				"container_name": "app-1",
				"send_logs":      true,
			},
		},
	}

	rm := newTestReceiverManager(t, cfg)

	provider, received := newRecordingProvider()
	rm.RegisterSinkProvider(provider)

	// Include patterns first, matching agent.go's startup order.
	if err := rm.RescanReceivers(t.Context()); err != nil {
		t.Fatal("RescanReceivers failed:", err)
	}

	ctr := facts.FakeContainer{FakeID: "id-1", FakeContainerName: "app-1", FakeLogPath: logFile.Name()}
	rm.UpdateContainers(t.Context(), []facts.Container{ctr}, nil, true)

	rm.l.Lock()
	ms := rm.receivers["app"]
	rm.l.Unlock()

	if ms == nil {
		t.Fatal("expected a managedSource for the receiver")
	}

	ms.l.Lock()
	containerTails := len(ms.containerRecvs)
	ms.l.Unlock()

	if containerTails != 0 {
		t.Errorf("expected no container tail on a file the include patterns already cover, got %d", containerTails)
	}

	time.Sleep(600 * time.Millisecond)

	if _, err := logFile.WriteString("one single line\n"); err != nil {
		t.Fatal("Failed to write log line:", err)
	}

	if err := logFile.Sync(); err != nil {
		t.Fatal("Failed to sync log file:", err)
	}

	time.Sleep(1200 * time.Millisecond)

	if got := totalRecords(received()); got != 1 {
		t.Errorf("expected the single written line to be delivered once, got %d deliveries", got)
	}

	// The container disappears while the include tail keeps running.
	rm.UpdateContainers(t.Context(), nil, nil, true)

	ms.l.Lock()
	_, stillWatched := ms.watching[logFile.Name()]
	_, stillSized := ms.sizeFnByFile[logFile.Name()]
	ms.l.Unlock()

	if !stillWatched || !stillSized {
		t.Errorf(
			"expected the live include tail to keep its bookkeeping after the container went away, got watching=%v sizeFn=%v",
			stillWatched, stillSized,
		)
	}

	if err := rm.RescanReceivers(t.Context()); err != nil {
		t.Fatal("RescanReceivers failed:", err)
	}

	ms.l.Lock()
	includeTails := len(ms.recvs[logFile.Name()])
	ms.l.Unlock()

	if includeTails != 1 {
		t.Errorf("expected the file to still have exactly one include tail after a rescan, got %d", includeTails)
	}
}

// TestContainerOnlyTailReleasesFileBookkeepingWhenGone is releaseFileBookkeeping's other half: with no
// include pattern covering the file, the departing container is its last reader, so the shared
// watching/sizeFnByFile entries must go. Retaining them would leave the file permanently marked as tailed
// and block any later tail on it.
func TestContainerOnlyTailReleasesFileBookkeepingWhenGone(t *testing.T) {
	t.Parallel()

	logFile, err := os.CreateTemp(t.TempDir(), "app-*.log")
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer logFile.Close()

	cfg := config.OpenTelemetry{
		Receivers: map[string]config.LogReceiver{
			"app": {"container_name": "app-1", "send_logs": true},
		},
	}

	rm := newTestReceiverManager(t, cfg)

	provider, _ := newRecordingProvider()
	rm.RegisterSinkProvider(provider)

	ctr := facts.FakeContainer{FakeID: "id-1", FakeContainerName: "app-1", FakeLogPath: logFile.Name()}
	rm.UpdateContainers(t.Context(), []facts.Container{ctr}, nil, true)

	rm.l.Lock()
	ms := rm.receivers["app"]
	rm.l.Unlock()

	if ms == nil {
		t.Fatal("expected a managedSource for the receiver")
	}

	ms.l.Lock()
	_, watched := ms.watching[logFile.Name()]
	ms.l.Unlock()

	if !watched {
		t.Fatal("expected the container tail to mark its log file as watched")
	}

	rm.UpdateContainers(t.Context(), nil, nil, true)

	ms.l.Lock()
	_, stillWatched := ms.watching[logFile.Name()]
	_, stillSized := ms.sizeFnByFile[logFile.Name()]
	ms.l.Unlock()

	if stillWatched || stillSized {
		t.Errorf(
			"expected the file's bookkeeping to be released once its only tail went away, got watching=%v sizeFn=%v",
			stillWatched, stillSized,
		)
	}
}

// TestReleaseFileBookkeepingKeepsFileWithAnotherReader covers releaseFileBookkeeping directly, since the
// ms.watching start guards mean the two teardown paths can no longer produce a file with two readers on
// their own. It is the teardown-side half of the one-tail-per-file invariant: were a file ever to end up
// read by both paths, whichever teardown ran first must not erase bookkeeping the other still needs.
func TestReleaseFileBookkeepingKeepsFileWithAnotherReader(t *testing.T) {
	t.Parallel()

	const file = "/var/log/app.log"

	testCases := []struct {
		name       string
		recvs      map[string][]receiver.Logs
		ctrLogFile map[string]string
		wantKept   bool
	}{
		{
			name:     "no reader left",
			wantKept: false,
		},
		{
			name:     "an include tail still reads it",
			recvs:    map[string][]receiver.Logs{file: nil},
			wantKept: true,
		},
		{
			name:       "a container tail still reads it",
			ctrLogFile: map[string]string{"id-1": file},
			wantKept:   true,
		},
		{
			name:       "another file's container tail does not count",
			ctrLogFile: map[string]string{"id-1": "/var/log/other.log"},
			wantKept:   false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ms := &managedSource{
				watching:         map[string]ReceiverKind{file: ReceiverFileLog},
				sizeFnByFile:     map[string]func() (int64, error){file: func() (int64, error) { return 0, nil }},
				recvs:            tc.recvs,
				containerLogFile: tc.ctrLogFile,
			}

			ms.releaseFileBookkeeping(file)

			_, watched := ms.watching[file]
			_, sized := ms.sizeFnByFile[file]

			if watched != tc.wantKept || sized != tc.wantKept {
				t.Errorf("after release: watching=%v sizeFn=%v, want both %v", watched, sized, tc.wantKept)
			}
		})
	}
}

// TestLabelContainerForgetsOffsetAfterTailTransition guards against a state-cache leak for every container
// that stops being tailed before it disappears. updateLabelContainers' rebuild path tears the source down
// keeping the offset -- correct, the container is still there -- and re-asks the providers. When none wants
// it anymore (the ordinary outcome once the service path picks the container up and no glouton.log_metrics
// rule keeps logmetrics interested) the rebuilt source starts no tail, so containerExtIDs stays empty and
// the eventual "gone for good" teardown had nothing to forget from. The offset then sat in
// metadataPerReceiver and was re-serialized into the state cache by every later save for the rest of the
// process's life: one leaked entry per container, growing without bound on a churny node.
func TestLabelContainerForgetsOffsetAfterTailTransition(t *testing.T) {
	t.Parallel()

	logFile, err := os.CreateTemp(t.TempDir(), "ctr-*.log")
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer logFile.Close()

	rm := newTestReceiverManager(t, config.OpenTelemetry{})

	// Wants the source at first, then declines -- exactly what logprocessing does once serviceTailed flips.
	sink, _ := recordingLogsConsumer()
	wanted := true

	rm.RegisterSinkProvider(&fakeSinkProvider{
		want: func(ResolvedSource) (consumer.Logs, bool) {
			if !wanted {
				return nil, false
			}

			return sink, true
		},
	})

	ctr := facts.FakeContainer{
		FakeID: "id-1", FakeContainerName: "app-1", FakeLogPath: logFile.Name(),
		FakeLabels: map[string]string{ContainerLabelPrefix + "send_logs": "true"},
	}

	rm.UpdateContainers(t.Context(), []facts.Container{ctr}, nil, true)

	rm.l.Lock()
	ms := rm.byContainer[ctr.ID()]
	rm.l.Unlock()

	if ms == nil {
		t.Fatal("expected a label-fallback source for the container")
	}

	ms.l.Lock()
	extIDs := ms.containerExtIDs[ctr.ID()]
	ms.l.Unlock()

	if len(extIDs) == 0 {
		t.Fatal("expected the container tail to have registered a persistent extension")
	}

	name := extIDs[0].Name()

	// Seed offset metadata, as a real tail would have.
	rm.persister.l.Lock()
	rm.persister.metadataPerReceiver[name] = map[string][]byte{"offset": []byte("42")}
	rm.persister.updatedKeys[name] = struct{}{}
	rm.persister.l.Unlock()

	// The service path picks the container up and the provider declines: rebuilt with no tail at all.
	wanted = false

	rm.UpdateContainers(t.Context(), []facts.Container{ctr}, map[string]bool{ctr.ID(): true}, true)

	rm.l.Lock()
	rebuilt := rm.byContainer[ctr.ID()]
	rm.l.Unlock()

	if rebuilt == nil || rebuilt.fanout != nil {
		t.Fatalf("expected the container to be rebuilt with no fanout, got %+v", rebuilt)
	}

	// A second identical transition must not lose track of the offset either.
	rm.UpdateContainers(t.Context(), []facts.Container{ctr}, nil, true)
	rm.UpdateContainers(t.Context(), []facts.Container{ctr}, map[string]bool{ctr.ID(): true}, true)

	// The offset must still be there while the container is: it may yet be wanted again.
	rm.persister.l.Lock()
	_, keptWhilePresent := rm.persister.metadataPerReceiver[name]
	rm.persister.l.Unlock()

	if !keptWhilePresent {
		t.Error("expected the offset to survive while the container is still present")
	}

	// Now it is gone for good, with a complete enumeration.
	rm.UpdateContainers(t.Context(), nil, nil, true)

	rm.persister.l.Lock()
	_, leakedMetadata := rm.persister.metadataPerReceiver[name]
	_, leakedUpdatedKey := rm.persister.updatedKeys[name]
	rm.persister.l.Unlock()

	if leakedMetadata || leakedUpdatedKey {
		t.Errorf(
			"expected %q to be forgotten once the container was removed, got metadata=%v updatedKeys=%v",
			name, leakedMetadata, leakedUpdatedKey,
		)
	}

	if got := rm.persister.getAllMetadata(); len(got) != 0 {
		t.Errorf("expected nothing left to re-serialize into the state cache, got %v", got)
	}
}
