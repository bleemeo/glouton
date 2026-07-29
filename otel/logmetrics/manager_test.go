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
// receiver through a real filelogreceiver+countconnector pipeline: the
// receiver never names the metric, since log.metrics.count is global and
// applies to every watched source automatically.
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

// TestManagerScopedReceiversGetDistinctItems is the end-to-end test for
// per-receiver metrics: scoping: two receivers, each scoped to its own
// metric via an identical condition, must produce genuinely distinct
// item-labeled series -- neither receiver's item ever reports the other's
// metric, even though both conditions could match either file's content.
func TestManagerScopedReceiversGetDistinctItems(t *testing.T) {
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

	cfg := config.Log{
		Metrics: config.LogMetricsConfig{
			Receivers: map[string]config.LogMetricsReceiver{
				"app_a": {"include": []string{logFileA.Name()}},
				"app_b": {"include": []string{logFileB.Name()}},
			},
			Count: map[string]config.LogMetricsCount{
				"metric_a": {"conditions": []any{`IsMatch(body, "error")`}, "sources": []any{"app_a"}},
				"metric_b": {"conditions": []any{`IsMatch(body, "error")`}, "sources": []any{"app_b"}},
			},
		},
	}

	man := New(t.Context(), cfg, "/", &fakeRuntime{}, newMemoryState(), noExecRunner(t))

	man.startStaticSources(t.Context())

	defer man.stopAll(t.Context())

	time.Sleep(500 * time.Millisecond)

	if _, err := logFileA.WriteString("error in A\n"); err != nil {
		t.Fatal("Failed to write log line:", err)
	}

	if _, err := logFileB.WriteString("error in B\n"); err != nil {
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

	gotItemByMetric := make(map[string]string, len(mfs))

	for _, mf := range mfs {
		for _, m := range mf.GetMetric() {
			for _, lbl := range m.GetLabel() {
				if lbl.GetName() == "item" {
					gotItemByMetric[mf.GetName()] = lbl.GetValue()
				}
			}
		}
	}

	if got := gotItemByMetric["metric_a"]; got != "app_a" {
		t.Errorf(`Expected metric_a's item to be "app_a", got %q`, got)
	}

	if got := gotItemByMetric["metric_b"]; got != "app_b" {
		t.Errorf(`Expected metric_b's item to be "app_b", got %q`, got)
	}
}

// TestManagerUnscopedReceiversShareAggregateItem is the regression test
// guarding against making item-per-receiver unconditional: two receivers
// with no metrics: field must keep sharing item="" and summing into one
// series, exactly like before this field existed -- e.g. for a metric
// meant to aggregate matches across every source.
func TestManagerUnscopedReceiversShareAggregateItem(t *testing.T) {
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

	cfg := config.Log{
		Metrics: config.LogMetricsConfig{
			Receivers: map[string]config.LogMetricsReceiver{
				"app_a": {"include": []string{logFileA.Name()}},
				"app_b": {"include": []string{logFileB.Name()}},
			},
			Count: map[string]config.LogMetricsCount{
				"global_error_count": {"conditions": []any{`IsMatch(body, "error")`}},
			},
		},
	}

	man := New(t.Context(), cfg, "/", &fakeRuntime{}, newMemoryState(), noExecRunner(t))

	man.startStaticSources(t.Context())

	defer man.stopAll(t.Context())

	time.Sleep(500 * time.Millisecond)

	if _, err := logFileA.WriteString("error in A\n"); err != nil {
		t.Fatal("Failed to write log line:", err)
	}

	if _, err := logFileB.WriteString("error in B\n"); err != nil {
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

	if len(mfs) != 1 || mfs[0].GetName() != "global_error_count" {
		t.Fatalf("Expected exactly 1 metric family named global_error_count, got %v", mfs)
	}

	if len(mfs[0].GetMetric()) != 1 {
		t.Fatalf("Expected both unscoped receivers to sum into 1 series (item=\"\"), got %d", len(mfs[0].GetMetric()))
	}

	for _, lbl := range mfs[0].GetMetric()[0].GetLabel() {
		if lbl.GetName() == "item" {
			t.Errorf(`Expected no "item" label on the shared bucket, got %q`, lbl.GetValue())
		}
	}

	if got := mfs[0].GetMetric()[0].GetUntyped().GetValue(); got != 2.0/windowSecs {
		t.Errorf("Expected both receivers' matches to sum into one rate %v, got %v", 2.0/windowSecs, got)
	}
}

// TestManagerMultiSourceMetricMerges checks the 2+-sources case: a metric
// naming two receivers under its own "sources" merges both into a single,
// item-less series, while each receiver still independently feeds an
// unrelated global metric (no sources) exactly as before.
func TestManagerMultiSourceMetricMerges(t *testing.T) {
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

	cfg := config.Log{
		Metrics: config.LogMetricsConfig{
			Receivers: map[string]config.LogMetricsReceiver{
				"app_a": {"include": []string{logFileA.Name()}},
				"app_b": {"include": []string{logFileB.Name()}},
			},
			Count: map[string]config.LogMetricsCount{
				"merged_errors":     {"conditions": []any{`IsMatch(body, "error")`}, "sources": []any{"app_a", "app_b"}},
				"global_everywhere": {"conditions": []any{`IsMatch(body, "error")`}},
			},
		},
	}

	man := New(t.Context(), cfg, "/", &fakeRuntime{}, newMemoryState(), noExecRunner(t))

	man.startStaticSources(t.Context())

	defer man.stopAll(t.Context())

	time.Sleep(500 * time.Millisecond)

	if _, err := logFileA.WriteString("error in A\n"); err != nil {
		t.Fatal("Failed to write log line:", err)
	}

	if _, err := logFileB.WriteString("error in B\n"); err != nil {
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

	if len(mfs) != 2 {
		t.Fatalf("Expected exactly 2 metric families, got %v", mfs)
	}

	for _, mf := range mfs {
		switch mf.GetName() {
		case "merged_errors":
			if len(mf.GetMetric()) != 1 {
				t.Fatalf("Expected merged_errors to be a single merged series, got %d samples", len(mf.GetMetric()))
			}

			for _, lbl := range mf.GetMetric()[0].GetLabel() {
				if lbl.GetName() == "item" {
					t.Errorf(`Expected no "item" label on the merged series, got %q`, lbl.GetValue())
				}
			}

			if got := mf.GetMetric()[0].GetUntyped().GetValue(); got != 2.0/windowSecs {
				t.Errorf("Expected both sources' matches to sum into one rate %v, got %v", 2.0/windowSecs, got)
			}
		case "global_everywhere":
			if len(mf.GetMetric()) != 1 {
				t.Fatalf("Expected global_everywhere to also be a single shared series, got %d samples", len(mf.GetMetric()))
			}

			if got := mf.GetMetric()[0].GetUntyped().GetValue(); got != 2.0/windowSecs {
				t.Errorf("Expected global_everywhere to independently sum both receivers' matches too (rate %v), got %v", 2.0/windowSecs, got)
			}
		}
	}
}

// TestManagerStaticSourceInlineOperatorsAttributeCounter is an end-to-end test
// of a static receiver using raw inline operators (config.OTELOperator)
// instead of the log_format shortcut, with the metric matching a parsed
// attribute instead of the raw body.
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

// TestManagerStaticSourceLogFormatAttributeCounter is the regression test for
// "count Apache 5xx responses": a static receiver applies a known_log_format
// before counting, so the counter can match a parsed attribute
// (http.response.status_code) instead of the raw body.
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

// TestManagerContainerSource is the regression test for the original
// RabbitMQ bug: a container_counters entry's log line is wrapped exactly
// like a real Docker container log, and the counter only matches the
// unwrapped message, so the "container" envelope operator must run before
// OTTL matching.
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

// TestManagerTwoContainersSameMetricGetDistinctItems checks that two
// containers watched for the same global metric produce two distinctly
// item-labeled series with independent counts, not one merged series.
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

// TestManagerContainerScopedToOwnMetric is the end-to-end test for a
// container-scoped metric: a metric naming a *different* source in "sources"
// must not register for this container's item at all -- not merely report it
// as zero, but have it genuinely absent from EmitMetrics's output for that
// item -- while a metric naming this container specifically still works.
func TestManagerContainerScopedToOwnMetric(t *testing.T) {
	t.Parallel()

	logFile, err := os.CreateTemp(t.TempDir(), "app-*.log")
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer logFile.Close()

	ctr := facts.FakeContainer{FakeID: "id-1", FakeContainerName: "app-1", FakeLogPath: logFile.Name()}

	cfg := config.Log{
		Metrics: config.LogMetricsConfig{
			Count: map[string]config.LogMetricsCount{
				"app_errors":          {"conditions": []any{`IsMatch(body, "ERROR")`}, "sources": []any{ctr.ContainerName()}},
				"unrelated_elsewhere": {"conditions": []any{`IsMatch(body, "ERROR")`}, "sources": []any{"some-other-container"}},
			},
			ContainerCounters: []string{ctr.ContainerName()},
		},
	}

	man := New(t.Context(), cfg, "/", &fakeRuntime{containers: []facts.Container{ctr}}, newMemoryState(), noExecRunner(t))

	man.updateContainerSources(t.Context())

	defer man.stopAll(t.Context())

	time.Sleep(500 * time.Millisecond)

	dockerLine := `{"log":"ERROR something broke\n","stream":"stdout","time":"2024-01-15T10:23:45.123Z"}` + "\n"

	if _, err := logFile.WriteString(dockerLine); err != nil {
		t.Fatal("Failed to write log line:", err)
	}

	if err := logFile.Sync(); err != nil {
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

	gotNames := make(map[string]bool, len(mfs))

	for _, mf := range mfs {
		for _, m := range mf.GetMetric() {
			for _, lbl := range m.GetLabel() {
				if lbl.GetName() == "item" && lbl.GetValue() == ctr.ContainerName() {
					gotNames[mf.GetName()] = true
				}
			}
		}
	}

	if !gotNames["app_errors"] {
		t.Errorf("Expected app_errors to be registered for %s, got %v", ctr.ContainerName(), gotNames)
	}

	if gotNames["unrelated_elsewhere"] {
		t.Errorf("Expected unrelated_elsewhere to be genuinely absent for %s (scoped out), got %v", ctr.ContainerName(), gotNames)
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

// TestManagerContainerWatchStopsWhenLabelRemoved checks that a container
// matched via a ContainerSelectorCounters label rule stops being watched as
// soon as that label is removed live, without the container itself
// disappearing or the config changing.
func TestManagerContainerWatchStopsWhenLabelRemoved(t *testing.T) {
	t.Parallel()

	logFile, err := os.CreateTemp(t.TempDir(), "app-*.log")
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer logFile.Close()

	ctrWatched := facts.FakeContainer{
		FakeID:            "id-1",
		FakeContainerName: "app-1",
		FakeLogPath:       logFile.Name(),
		FakeLabels:        map[string]string{"app": "web"},
	}

	cfg := config.Log{
		Metrics: config.LogMetricsConfig{
			Count: map[string]config.LogMetricsCount{"app_errors": {"conditions": []any{`IsMatch(body, "ERROR")`}}},
			ContainerSelectorCounters: []config.ContainerSelectorRule{
				{Selectors: map[string]string{"app": "web"}},
			},
		},
	}

	runtime := &fakeRuntime{containers: []facts.Container{ctrWatched}}
	man := New(t.Context(), cfg, "/", runtime, newMemoryState(), noExecRunner(t))

	man.updateContainerSources(t.Context())

	man.l.Lock()
	_, watching := man.containerSources[ctrWatched.ID()]
	man.l.Unlock()

	if !watching {
		t.Fatal("Expected the container to be watched once it carries the matching label")
	}

	ctrUnwatched := ctrWatched
	ctrUnwatched.FakeLabels = nil
	runtime.containers = []facts.Container{ctrUnwatched}

	man.updateContainerSources(t.Context())

	man.l.Lock()
	_, stillWatching := man.containerSources[ctrWatched.ID()]
	man.l.Unlock()

	if stillWatching {
		t.Fatal("Expected the container's source to be stopped once its matching label was removed")
	}
}

// TestManagerPersistsOffsetAcrossRestart checks that a line written while no
// Manager is running is still picked up on restart, since it resumes tailing
// from the previously persisted offset instead of the file's end.
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

// TestManagerPersistsFileSizesAcrossRestart checks that Manager.lastFileSizes
// round-trips through the state cache independently of the offset persister,
// since it's the fallback signal used when the persister is unavailable.
func TestManagerPersistsFileSizesAcrossRestart(t *testing.T) {
	t.Parallel()

	logFile, err := os.CreateTemp(t.TempDir(), "app-*.log")
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer logFile.Close()

	if _, err := logFile.WriteString("[error] first\n"); err != nil {
		t.Fatal("Failed to write log line:", err)
	}

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

	man1 := New(t.Context(), cfg, "/", &fakeRuntime{}, state, noExecRunner(t))
	man1.startStaticSources(t.Context())
	man1.saveState()
	man1.stopAll(t.Context())

	var sizes map[string]int64

	if err := state.Get(lastFileSizesCacheKey, &sizes); err != nil {
		t.Fatal("Failed to read the lastFileSizes cache:", err)
	}

	if _, ok := sizes[logFile.Name()]; !ok {
		t.Fatalf("Expected %q's size to be persisted to the lastFileSizes cache, got %v", logFile.Name(), sizes)
	}

	man2 := New(t.Context(), cfg, "/", &fakeRuntime{}, state, noExecRunner(t))

	if _, ok := man2.lastFileSizes[logFile.Name()]; !ok {
		t.Fatalf("Expected the second Manager to load the previous run's lastFileSizes cache, got %v", man2.lastFileSizes)
	}
}

// TestManagerRegistersEveryCountEntry checks that collectAllCounters
// registers every log.metrics.count entry at startup, even with no
// receiver/container feeding it yet -- otherwise its data points would be
// silently dropped and it would never appear in EmitMetrics/MetricNames.
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
