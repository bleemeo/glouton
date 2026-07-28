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
	"context"
	"encoding/json"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/facts"
	crTypes "github.com/bleemeo/glouton/facts/container-runtime/types"
	glmodel "github.com/bleemeo/glouton/prometheus/model"
	"github.com/bleemeo/glouton/prometheus/registry"
	"github.com/bleemeo/glouton/utils/gloutonexec"
)

// dummyRunner is a logsource.CommandRunner test double whose behavior is
// entirely defined by the given funcs.
type dummyRunner struct {
	run            func(ctx context.Context, option gloutonexec.Option, cmd string, args ...string) ([]byte, error)
	startWithPipes func(ctx context.Context, option gloutonexec.Option, cmd string, args ...string) (stdoutPipe io.ReadCloser, stderrPipe io.ReadCloser, wait func() error, err error)
}

func (dr dummyRunner) Run(ctx context.Context, option gloutonexec.Option, cmd string, args ...string) ([]byte, error) {
	return dr.run(ctx, option, cmd, args...)
}

func (dr dummyRunner) StartWithPipes(ctx context.Context, option gloutonexec.Option, cmd string, args ...string) (stdoutPipe io.ReadCloser, stderrPipe io.ReadCloser, wait func() error, err error) {
	return dr.startWithPipes(ctx, option, cmd, args...)
}

// noExecRunner returns a CommandRunner that fails the given test if any command is executed.
func noExecRunner(t *testing.T) dummyRunner {
	t.Helper()

	return dummyRunner{
		run: func(_ context.Context, _ gloutonexec.Option, cmd string, args ...string) ([]byte, error) {
			t.Errorf("No command should have been executed during this test, but: %s %s", cmd, args)

			return nil, nil
		},
		startWithPipes: func(_ context.Context, _ gloutonexec.Option, cmd string, args ...string) (io.ReadCloser, io.ReadCloser, func() error, error) {
			t.Errorf("No command should have been executed during this test, but: %s %s", cmd, args)

			return nil, nil, nil, nil
		},
	}
}

// fakeRuntime implements crTypes.RuntimeInterface by embedding a nil interface and
// overriding only Containers, the sole method the Manager under test calls. Calling
// any other method would panic, which is fine: this test never does.
type fakeRuntime struct {
	crTypes.RuntimeInterface

	containers []facts.Container
}

func (f *fakeRuntime) Containers(context.Context, time.Duration, bool) ([]facts.Container, error) {
	return f.containers, nil
}

// memoryState is a minimal, real (not no-op) bleemeoTypes.State backed by an
// in-memory JSON round-trip, so tests can verify data actually survives a
// save/reload cycle across two separate Manager instances.
type memoryState struct {
	l    sync.Mutex
	data map[string][]byte
}

func newMemoryState() *memoryState {
	return &memoryState{data: make(map[string][]byte)}
}

func (m *memoryState) Set(key string, object any) error {
	b, err := json.Marshal(object)
	if err != nil {
		return err
	}

	m.l.Lock()
	m.data[key] = b
	m.l.Unlock()

	return nil
}

func (m *memoryState) Get(key string, result any) error {
	m.l.Lock()
	b, found := m.data[key]
	m.l.Unlock()

	if !found {
		return nil
	}

	return json.Unmarshal(b, result)
}

func (m *memoryState) GetByPrefix(string, any) (map[string]any, error) { return map[string]any{}, nil }
func (m *memoryState) Delete(string) error                             { return nil }
func (m *memoryState) BleemeoCredentials() (string, string)            { return "", "" }
func (m *memoryState) SetBleemeoCredentials(string, string) error      { return nil }

// TestManagerStaticSource is an end-to-end test of a plain path-based
// receiver: a real file, a real OTel filelogreceiver+countconnector pipeline,
// through the Manager. The receiver never references the metric by name --
// log.metrics.count is global (see LogMetricsConfig's doc comment), so it
// applies automatically to every watched source.
func TestManagerStaticSource(t *testing.T) {
	t.Parallel()

	logFile, err := os.CreateTemp(t.TempDir(), "app-*.log")
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer logFile.Close()

	cfg := config.Log{
		Metrics: config.LogMetricsConfig{
			Receivers: map[string]config.LogMetricsReceiver{
				"app": {"include": []string{logFile.Name()}},
			},
			Count: map[string]config.LogMetricsCount{
				"app_errors_count": {"conditions": []any{`IsMatch(body, "\\[error\\]")`}},
			},
		},
	}

	man := New(t.Context(), cfg, "/", &fakeRuntime{}, newMemoryState(), noExecRunner(t))

	// Metric names must be known immediately, before any matching line was seen.
	if names := man.MetricNames(); len(names) != 1 || names[0] != "app_errors_count" {
		t.Fatalf("Expected metric %q to be pre-registered, got %v", "app_errors_count", names)
	}

	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan error, 1)

	go func() {
		done <- man.Run(ctx)
	}()

	time.Sleep(500 * time.Millisecond)

	lines := []string{
		"normal request line\n",
		"[error] something broke\n",
		"another normal line\n",
		"[error] something else broke\n",
	}

	for _, line := range lines {
		if _, err := logFile.WriteString(line); err != nil {
			t.Fatal("Failed to write log line:", err)
		}
	}

	if err := logFile.Sync(); err != nil {
		t.Fatal("Failed to sync log file:", err)
	}

	time.Sleep(time.Second)

	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Manager.Run did not return after context cancellation")
	}

	appender := glmodel.NewBufferAppender()

	if err := man.EmitMetrics(t.Context(), registry.GatherState{}, appender); err != nil {
		t.Fatal("EmitMetrics returned an error:", err)
	}

	mfs, err := appender.AsMF()
	if err != nil {
		t.Fatal("AsMF returned an error:", err)
	}

	if len(mfs) != 1 || mfs[0].GetName() != "app_errors_count" {
		t.Fatalf("Expected exactly 1 metric family named app_errors_count, got %v", mfs)
	}

	if got := mfs[0].GetMetric()[0].GetUntyped().GetValue(); got != 2.0/windowSecs {
		t.Errorf("Expected rate %v, got %v", 2.0/windowSecs, got)
	}
}

// TestManagerStaticSourceInlineOperatorsAttributeCounter is an end-to-end test
// of a static receiver using raw inline operators (config.OTELOperator, the
// same shape/mechanism as OTLPReceiver.Operators) instead of the log_format
// shortcut, matching real OpenTelemetry receiver config more directly: no
// named-format indirection, just the operators themselves. The metric then
// matches against a parsed attribute instead of the raw body.
func TestManagerStaticSourceInlineOperatorsAttributeCounter(t *testing.T) {
	t.Parallel()

	logFile, err := os.CreateTemp(t.TempDir(), "app-*.log")
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer logFile.Close()

	cfg := config.Log{
		Metrics: config.LogMetricsConfig{
			Receivers: map[string]config.LogMetricsReceiver{
				"app": {
					"include": []string{logFile.Name()},
					"operators": []config.OTELOperator{
						{
							"type":  "regex_parser",
							"regex": `^level=(?P<level>\w+) `,
						},
					},
				},
			},
			Count: map[string]config.LogMetricsCount{
				"app_warn_count": {"conditions": []any{`IsMatch(attributes["level"], "warn")`}},
			},
		},
	}

	man := New(t.Context(), cfg, "/", &fakeRuntime{}, newMemoryState(), noExecRunner(t))

	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan error, 1)

	go func() {
		done <- man.Run(ctx)
	}()

	time.Sleep(500 * time.Millisecond)

	lines := []string{
		"level=info normal startup\n",
		"level=warn disk almost full\n",
		"level=warn cpu almost full\n",
	}

	for _, line := range lines {
		if _, err := logFile.WriteString(line); err != nil {
			t.Fatal("Failed to write log line:", err)
		}
	}

	if err := logFile.Sync(); err != nil {
		t.Fatal("Failed to sync log file:", err)
	}

	time.Sleep(time.Second)

	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Manager.Run did not return after context cancellation")
	}

	appender := glmodel.NewBufferAppender()

	if err := man.EmitMetrics(t.Context(), registry.GatherState{}, appender); err != nil {
		t.Fatal("EmitMetrics returned an error:", err)
	}

	mfs, err := appender.AsMF()
	if err != nil {
		t.Fatal("AsMF returned an error:", err)
	}

	if len(mfs) != 1 || mfs[0].GetName() != "app_warn_count" {
		t.Fatalf("Expected exactly 1 metric family named app_warn_count, got %v", mfs)
	}

	if got := mfs[0].GetMetric()[0].GetUntyped().GetValue(); got != 2.0/windowSecs {
		t.Errorf("Expected rate %v (2 matching warn lines out of 3), got %v", 2.0/windowSecs, got)
	}
}

// TestManagerStaticSourceLogFormatAttributeCounter is an end-to-end test of a
// static receiver applying a known_log_format (reused from
// log.opentelemetry.known_log_formats) before counting, so a counter can
// match on a parsed attribute (http.response.status_code) instead of the raw
// body -- the regression test for "count Apache 5xx responses".
func TestManagerStaticSourceLogFormatAttributeCounter(t *testing.T) {
	t.Parallel()

	logFile, err := os.CreateTemp(t.TempDir(), "apache-*.log")
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer logFile.Close()

	cfg := config.Log{
		OpenTelemetry: config.OpenTelemetry{
			KnownLogFormats: config.DefaultKnownLogFormats(),
		},
		Metrics: config.LogMetricsConfig{
			Receivers: map[string]config.LogMetricsReceiver{
				"apache": {
					"include":    []string{logFile.Name()},
					"log_format": "apache_access",
				},
			},
			Count: map[string]config.LogMetricsCount{
				"apache_server_error": {"conditions": []any{`IsMatch(attributes["http.response.status_code"], "5..")`}},
			},
		},
	}

	man := New(t.Context(), cfg, "/", &fakeRuntime{}, newMemoryState(), noExecRunner(t))

	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan error, 1)

	go func() {
		done <- man.Run(ctx)
	}()

	time.Sleep(500 * time.Millisecond)

	lines := []string{
		`127.0.0.1 - - [10/Oct/2023:13:55:36 +0000] "GET /index.html HTTP/1.1" 200 1234 "-" "curl/7.68.0"` + "\n",
		`127.0.0.1 - - [10/Oct/2023:13:55:37 +0000] "GET /broken HTTP/1.1" 500 1234 "-" "curl/7.68.0"` + "\n",
		`127.0.0.1 - - [10/Oct/2023:13:55:38 +0000] "GET /also-broken HTTP/1.1" 503 1234 "-" "curl/7.68.0"` + "\n",
	}

	for _, line := range lines {
		if _, err := logFile.WriteString(line); err != nil {
			t.Fatal("Failed to write log line:", err)
		}
	}

	if err := logFile.Sync(); err != nil {
		t.Fatal("Failed to sync log file:", err)
	}

	time.Sleep(time.Second)

	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Manager.Run did not return after context cancellation")
	}

	appender := glmodel.NewBufferAppender()

	if err := man.EmitMetrics(t.Context(), registry.GatherState{}, appender); err != nil {
		t.Fatal("EmitMetrics returned an error:", err)
	}

	mfs, err := appender.AsMF()
	if err != nil {
		t.Fatal("AsMF returned an error:", err)
	}

	if len(mfs) != 1 || mfs[0].GetName() != "apache_server_error" {
		t.Fatalf("Expected exactly 1 metric family named apache_server_error, got %v", mfs)
	}

	if got := mfs[0].GetMetric()[0].GetUntyped().GetValue(); got != 2.0/windowSecs {
		t.Errorf("Expected rate %v (2 matching 5xx lines out of 3), got %v", 2.0/windowSecs, got)
	}
}

// TestManagerContainerSource is an end-to-end test of a container_counters
// entry: a fake container whose log file is a real, Docker-JSON-wrapped
// temp file, resolved and processed dynamically by the Manager's container polling
// loop. This is the regression test for the original RabbitMQ bug: the log line is
// wrapped exactly like a real Docker container log, and the counter only matches the
// unwrapped message, so it also verifies the "container" envelope operator runs
// before OTTL matching.
func TestManagerContainerSource(t *testing.T) {
	t.Parallel()

	logFile, err := os.CreateTemp(t.TempDir(), "rabbitmq-*.log")
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer logFile.Close()

	ctr := facts.FakeContainer{
		FakeContainerName: "bleemeo_minimal-rabbitmq-1",
		FakeLogPath:       logFile.Name(),
	}

	// The container parser preserves the trailing newline embedded in Docker's JSON
	// "log" field value, so the body is "[error] something broke\n", not "...broke".
	cfg := config.Log{
		Metrics: config.LogMetricsConfig{
			Count: map[string]config.LogMetricsCount{
				"rabbitmq_errors": {"conditions": []any{`IsMatch(body, "^\\[error\\] something broke\\n?$")`}},
			},
			ContainerCounters: []string{ctr.ContainerName()},
		},
	}

	man := New(t.Context(), cfg, "/", &fakeRuntime{containers: []facts.Container{ctr}}, newMemoryState(), noExecRunner(t))

	man.startStaticSources(t.Context())
	man.updateContainerSources(t.Context())

	defer man.stopAll(t.Context())

	time.Sleep(500 * time.Millisecond)

	dockerLine := `{"log":"[error] something broke\n","stream":"stdout","time":"2024-01-15T10:23:45.123Z"}` + "\n"

	if _, err := logFile.WriteString(dockerLine); err != nil {
		t.Fatal("Failed to write log line:", err)
	}

	if err := logFile.Sync(); err != nil {
		t.Fatal("Failed to sync log file:", err)
	}

	time.Sleep(time.Second)

	man.l.Lock()
	counter, found := man.reg.counters[counterKey{metric: "rabbitmq_errors", item: ctr.ContainerName()}]
	man.l.Unlock()

	if !found {
		t.Fatal("Expected a counter for rabbitmq_errors")
	}

	if got := counter.counter.Total(); got != 1 {
		t.Errorf("Expected 1 match for rabbitmq_errors, got %d", got)
	}
}

// TestManagerTwoContainersSameMetricGetDistinctItems is the end-to-end regression test
// for the item-label feature: two different containers both watched for the same
// (global) metric must come out as two distinctly-labeled series with independent
// counts, not merge into one.
func TestManagerTwoContainersSameMetricGetDistinctItems(t *testing.T) {
	t.Parallel()

	logFileA, err := os.CreateTemp(t.TempDir(), "app-a-*.log")
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer logFileA.Close()

	logFileB, err := os.CreateTemp(t.TempDir(), "app-b-*.log")
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer logFileB.Close()

	ctrA := facts.FakeContainer{FakeID: "id-a", FakeContainerName: "app-a", FakeLogPath: logFileA.Name()}
	ctrB := facts.FakeContainer{FakeID: "id-b", FakeContainerName: "app-b", FakeLogPath: logFileB.Name()}

	cfg := config.Log{
		Metrics: config.LogMetricsConfig{
			Count: map[string]config.LogMetricsCount{
				"shared_errors_count": {"conditions": []any{`IsMatch(body, "\\[error\\]")`}},
			},
			ContainerCounters: []string{ctrA.ContainerName(), ctrB.ContainerName()},
		},
	}

	man := New(t.Context(), cfg, "/", &fakeRuntime{containers: []facts.Container{ctrA, ctrB}}, newMemoryState(), noExecRunner(t))

	man.updateContainerSources(t.Context())

	defer man.stopAll(t.Context())

	time.Sleep(500 * time.Millisecond)

	dockerLine := func(msg string) string {
		return `{"log":"` + msg + `\n","stream":"stdout","time":"2024-01-15T10:23:45.123Z"}` + "\n"
	}

	if _, err := logFileA.WriteString(dockerLine("[error] a1") + dockerLine("[error] a2")); err != nil {
		t.Fatal("Failed to write log line:", err)
	}

	if _, err := logFileB.WriteString(dockerLine("[error] b1")); err != nil {
		t.Fatal("Failed to write log line:", err)
	}

	if err := logFileA.Sync(); err != nil {
		t.Fatal("Failed to sync log file:", err)
	}

	if err := logFileB.Sync(); err != nil {
		t.Fatal("Failed to sync log file:", err)
	}

	time.Sleep(time.Second)

	appender := glmodel.NewBufferAppender()

	if err := man.EmitMetrics(t.Context(), registry.GatherState{}, appender); err != nil {
		t.Fatal("EmitMetrics returned an error:", err)
	}

	mfs, err := appender.AsMF()
	if err != nil {
		t.Fatal("AsMF returned an error:", err)
	}

	if len(mfs) != 1 || mfs[0].GetName() != "shared_errors_count" {
		t.Fatalf("Expected exactly 1 metric family named shared_errors_count, got %v", mfs)
	}

	if len(mfs[0].GetMetric()) != 2 {
		t.Fatalf("Expected 2 distinctly-labeled samples (one per container), got %d", len(mfs[0].GetMetric()))
	}

	gotRates := make(map[string]float64, 2)

	for _, m := range mfs[0].GetMetric() {
		for _, lbl := range m.GetLabel() {
			if lbl.GetName() == "item" {
				gotRates[lbl.GetValue()] = m.GetUntyped().GetValue()
			}
		}
	}

	if got := gotRates[ctrA.ContainerName()]; got != 2.0/windowSecs {
		t.Errorf("Expected rate %v for %s, got %v", 2.0/windowSecs, ctrA.ContainerName(), got)
	}

	if got := gotRates[ctrB.ContainerName()]; got != 1.0/windowSecs {
		t.Errorf("Expected rate %v for %s, got %v", 1.0/windowSecs, ctrB.ContainerName(), got)
	}
}

func TestManagerContainerRemoved(t *testing.T) {
	t.Parallel()

	logFile, err := os.CreateTemp(t.TempDir(), "app-*.log")
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer logFile.Close()

	ctr := facts.FakeContainer{FakeContainerName: "app-1", FakeLogPath: logFile.Name()}

	cfg := config.Log{
		Metrics: config.LogMetricsConfig{
			Count:             map[string]config.LogMetricsCount{"app_errors": {"conditions": []any{`IsMatch(body, "ERROR")`}}},
			ContainerCounters: []string{ctr.ContainerName()},
		},
	}

	runtime := &fakeRuntime{containers: []facts.Container{ctr}}
	man := New(t.Context(), cfg, "/", runtime, newMemoryState(), noExecRunner(t))

	man.updateContainerSources(t.Context())

	man.l.Lock()
	_, watching := man.containerSources[ctr.ID()]
	man.l.Unlock()

	if !watching {
		t.Fatal("Expected the container to be watched after the first update")
	}

	runtime.containers = nil

	man.updateContainerSources(t.Context())

	man.l.Lock()
	_, stillWatching := man.containerSources[ctr.ID()]
	man.l.Unlock()

	if stillWatching {
		t.Fatal("Expected the container's source to be stopped once the container disappeared")
	}
}

// TestManagerPersistsOffsetAcrossRestart is the regression test for persisted read
// offsets: a line written entirely during the "restart gap" (while no Manager is
// running) must still be picked up by the next run, because it resumes tailing
// from the offset saved by the previous run instead of defaulting to the file's end.
func TestManagerPersistsOffsetAcrossRestart(t *testing.T) {
	t.Parallel()

	logFile, err := os.CreateTemp(t.TempDir(), "app-*.log")
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer logFile.Close()

	cfg := config.Log{
		Metrics: config.LogMetricsConfig{
			Receivers: map[string]config.LogMetricsReceiver{
				"app": {"include": []string{logFile.Name()}},
			},
			Count: map[string]config.LogMetricsCount{
				"app_errors_count": {"conditions": []any{`IsMatch(body, "\\[error\\]")`}},
			},
		},
	}

	state := newMemoryState()

	// First run: starts tailing an empty file, then "first" is written and read
	// while it's actively running, so the offset saved on stop is past "first".
	man1 := New(t.Context(), cfg, "/", &fakeRuntime{}, state, noExecRunner(t))

	ctx1, cancel1 := context.WithCancel(t.Context())
	done1 := make(chan error, 1)

	go func() { done1 <- man1.Run(ctx1) }()

	time.Sleep(500 * time.Millisecond)

	if _, err := logFile.WriteString("[error] first\n"); err != nil {
		t.Fatal("Failed to write log line:", err)
	}

	if err := logFile.Sync(); err != nil {
		t.Fatal("Failed to sync log file:", err)
	}

	time.Sleep(time.Second)

	cancel1()

	select {
	case <-done1:
	case <-time.After(5 * time.Second):
		t.Fatal("First Manager.Run did not return after context cancellation")
	}

	// Written entirely during the "restart gap": no Manager is running yet.
	if _, err := logFile.WriteString("[error] second\n"); err != nil {
		t.Fatal("Failed to write log line:", err)
	}

	if err := logFile.Sync(); err != nil {
		t.Fatal("Failed to sync log file:", err)
	}

	// Second run, same persisted state: must pick up "second" via the offset saved
	// by the previous run, even though it was written before this run even started.
	man := New(t.Context(), cfg, "/", &fakeRuntime{}, state, noExecRunner(t))

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)

	go func() { done <- man.Run(ctx) }()

	time.Sleep(time.Second)

	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Manager.Run did not return after context cancellation")
	}

	appender := glmodel.NewBufferAppender()

	if err := man.EmitMetrics(t.Context(), registry.GatherState{}, appender); err != nil {
		t.Fatal("EmitMetrics returned an error:", err)
	}

	mfs, err := appender.AsMF()
	if err != nil {
		t.Fatal("AsMF returned an error:", err)
	}

	if len(mfs) != 1 || mfs[0].GetName() != "app_errors_count" {
		t.Fatalf("Expected exactly 1 metric family named app_errors_count, got %v", mfs)
	}

	if got := mfs[0].GetMetric()[0].GetUntyped().GetValue(); got != 1.0/windowSecs {
		t.Errorf("Expected the restarted run to pick up exactly 1 new match via the persisted offset (rate %v), got %v", 1.0/windowSecs, got)
	}
}

// TestManagerRegistersEveryCountEntry is the regression test for
// collectAllCounters: every log.metrics.count entry must be registered at
// startup regardless of whether any receiver/container is even configured to
// feed it yet, or its data points get silently dropped by addSumDataPoints
// (registry lookup miss) and it never appears in EmitMetrics/MetricNames, no
// matter what actually gets pushed to it later (e.g. by the network source).
func TestManagerRegistersEveryCountEntry(t *testing.T) {
	t.Parallel()

	cfg := config.Log{
		Metrics: config.LogMetricsConfig{
			Count: map[string]config.LogMetricsCount{
				"network_only_count": {"conditions": []any{`IsMatch(body, "\\[error\\]")`}},
			},
		},
	}

	man := New(t.Context(), cfg, "/", &fakeRuntime{}, newMemoryState(), noExecRunner(t))

	names := man.MetricNames()

	if len(names) != 1 || names[0] != "network_only_count" {
		t.Fatalf("Expected MetricNames to contain exactly [network_only_count], got %v", names)
	}
}
