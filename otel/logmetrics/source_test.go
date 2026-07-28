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
	"maps"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/otel/logsource"

	"github.com/google/go-cmp/cmp"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	noopM "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace/noop"
)

func testTelemetrySettings() component.TelemetrySettings {
	return component.TelemetrySettings{
		Logger:         logger.ZapLogger(),
		TracerProvider: noop.NewTracerProvider(),
		MeterProvider:  noopM.NewMeterProvider(),
		Resource:       pcommon.NewResource(),
	}
}

// collectingSink returns a consumer.Metrics recording every Sum data point it sees,
// keyed by metric name, and a function to read the accumulated totals. The consumer
// callback runs on the OTel pipeline's own goroutine, concurrently with the test
// reading the totals, hence the mutex.
func collectingSink() (consumer.Metrics, func() map[string]int64) {
	var l sync.Mutex

	totals := make(map[string]int64)

	sink, err := consumer.NewMetrics(func(_ context.Context, md pmetric.Metrics) error {
		l.Lock()
		defer l.Unlock()

		for i := range md.ResourceMetrics().Len() {
			sms := md.ResourceMetrics().At(i).ScopeMetrics()
			for j := range sms.Len() {
				ms := sms.At(j).Metrics()
				for k := range ms.Len() {
					m := ms.At(k)
					if m.Type() != pmetric.MetricTypeSum {
						continue
					}

					dps := m.Sum().DataPoints()
					for d := range dps.Len() {
						totals[m.Name()] += dps.At(d).IntValue()
					}
				}
			}
		}

		return nil
	})
	if err != nil {
		panic(err)
	}

	return sink, func() map[string]int64 {
		l.Lock()
		defer l.Unlock()

		return maps.Clone(totals)
	}
}

// TestSourceCountsRealFile is an end-to-end test of a real OTel
// filelogreceiver+countconnector source: it must count matching lines via OTTL.
func TestSourceCountsRealFile(t *testing.T) {
	t.Parallel()

	logFile, err := os.CreateTemp(t.TempDir(), "app-*.log")
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer logFile.Close()

	sink, totals := collectingSink()

	src, err := newSource(t.Context(), testTelemetrySettings(), []string{logFile.Name()}, false, false, map[string]config.LogMetricsCount{
		"app_errors_count":   {"conditions": []any{`IsMatch(body, "\\[error\\]")`}},
		"app_requests_count": {"conditions": []any{`IsMatch(body, "GET /")`}},
	}, nil, nil, sink, nil, noExecRunner(t), logsource.StatFile, "")
	if err != nil {
		t.Fatal("Failed to build source:", err)
	}

	defer src.stop(t.Context()) //nolint:errcheck

	time.Sleep(500 * time.Millisecond)

	lines := []string{
		"127.0.0.1 GET / 200\n",
		"[error] something broke\n",
		"127.0.0.1 GET / [error] weird combo\n",
		"just a normal line\n",
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

	got := totals()

	if got["app_errors_count"] != 2 {
		t.Errorf("Expected 2 matches for app_errors_count, got %d", got["app_errors_count"])
	}

	if got["app_requests_count"] != 2 {
		t.Errorf("Expected 2 matches for app_requests_count, got %d", got["app_requests_count"])
	}
}

// TestSourceExcludeRegex confirms a LogCounter.Exclude regex suppresses lines that
// would otherwise match Regex, via the extra "not IsMatch(...)" OTTL condition in
// metricInfo -- a known-noisy line stays uncounted, an ordinary matching line doesn't.
func TestSourceExcludeRegex(t *testing.T) {
	t.Parallel()

	logFile, err := os.CreateTemp(t.TempDir(), "app-*.log")
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer logFile.Close()

	sink, totals := collectingSink()

	src, err := newSource(t.Context(), testTelemetrySettings(), []string{logFile.Name()}, false, false, map[string]config.LogMetricsCount{
		"app_errors_count": {"conditions": []any{`IsMatch(body, "\\[error\\]") and not IsMatch(body, "connection reset")`}},
	}, nil, nil, sink, nil, noExecRunner(t), logsource.StatFile, "")
	if err != nil {
		t.Fatal("Failed to build source:", err)
	}

	defer src.stop(t.Context()) //nolint:errcheck

	time.Sleep(500 * time.Millisecond)

	lines := []string{
		"[error] something broke\n",
		"[error] connection reset by peer\n",
		"[error] another real problem\n",
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

	got := totals()

	if got["app_errors_count"] != 2 {
		t.Errorf("Expected 2 matches for app_errors_count (excluding the connection reset line), got %d", got["app_errors_count"])
	}
}

// TestSourceUnwrapsContainerEnvelope is the regression test for the original
// RabbitMQ bug: a regex that only matches the unwrapped message, fed through a
// Docker-JSON-wrapped raw line, via a real filelogreceiver+countconnector pipeline
// with the container envelope operator enabled.
func TestSourceUnwrapsContainerEnvelope(t *testing.T) {
	t.Parallel()

	logFile, err := os.CreateTemp(t.TempDir(), "container-*.log")
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer logFile.Close()

	sink, totals := collectingSink()

	// The container parser preserves the trailing newline embedded in Docker's JSON
	// "log" field value, so the body is "[error] something broke\n", not "...broke".
	src, err := newSource(t.Context(), testTelemetrySettings(), []string{logFile.Name()}, true, false, map[string]config.LogMetricsCount{
		"container_errors_count": {"conditions": []any{`IsMatch(body, "^\\[error\\] something broke\\n?$")`}},
	}, nil, nil, sink, nil, noExecRunner(t), logsource.StatFile, "")
	if err != nil {
		t.Fatal("Failed to build source:", err)
	}

	defer src.stop(t.Context()) //nolint:errcheck

	time.Sleep(500 * time.Millisecond)

	dockerLine := `{"log":"[error] something broke\n","stream":"stdout","time":"2024-01-15T10:23:45.123Z"}` + "\n"

	if _, err := logFile.WriteString(dockerLine); err != nil {
		t.Fatal("Failed to write log line:", err)
	}

	if err := logFile.Sync(); err != nil {
		t.Fatal("Failed to sync log file:", err)
	}

	time.Sleep(time.Second)

	if got := totals()["container_errors_count"]; got != 1 {
		t.Errorf("Expected 1 match for container_errors_count, got %d", got)
	}
}

func TestSourceInvalidRegex(t *testing.T) {
	t.Parallel()

	sink, _ := collectingSink()

	_, err := newSource(t.Context(), testTelemetrySettings(), []string{"/nonexistent"}, false, false, map[string]config.LogMetricsCount{
		"bad": {"conditions": []any{`IsMatch(body, "(")`}},
	}, nil, nil, sink, nil, noExecRunner(t), logsource.StatFile, "")
	if err == nil {
		t.Fatal("Expected an error for an invalid regex")
	}
}

// TestSourceFastPathSingleConnector locks in the optimization: when every counter
// is valid, buildConnectors uses one combined connector instead of one per counter.
func TestSourceFastPathSingleConnector(t *testing.T) {
	t.Parallel()

	logFile, err := os.CreateTemp(t.TempDir(), "fastpath-*.log")
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer logFile.Close()

	sink, _ := collectingSink()

	src, err := newSource(t.Context(), testTelemetrySettings(), []string{logFile.Name()}, false, false, map[string]config.LogMetricsCount{
		"a_count": {"conditions": []any{`IsMatch(body, "a")`}},
		"b_count": {"conditions": []any{`IsMatch(body, "b")`}},
		"c_count": {"conditions": []any{`IsMatch(body, "c")`}},
	}, nil, nil, sink, nil, noExecRunner(t), logsource.StatFile, "")
	if err != nil {
		t.Fatal("Failed to build source:", err)
	}

	defer src.stop(t.Context()) //nolint:errcheck

	if len(src.conns) != 1 {
		t.Errorf("Expected exactly 1 connector on the fast path, got %d", len(src.conns))
	}
}

// TestSourceIsolatesInvalidCounter is the regression test for the fallback path in
// buildConnectors: when a source has both valid and invalid counters, the combined
// fast path fails validation, so it falls back to one connector per valid counter --
// the invalid one is disabled but its siblings keep counting normally.
func TestSourceIsolatesInvalidCounter(t *testing.T) {
	t.Parallel()

	logFile, err := os.CreateTemp(t.TempDir(), "app-*.log")
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer logFile.Close()

	sink, totals := collectingSink()

	src, err := newSource(t.Context(), testTelemetrySettings(), []string{logFile.Name()}, false, false, map[string]config.LogMetricsCount{
		"app_errors_count":   {"conditions": []any{`IsMatch(body, "\\[error\\]")`}},
		"app_requests_count": {"conditions": []any{`IsMatch(body, "GET /")`}},
		"app_broken_count":   {"conditions": []any{`IsMatch(body, "(")`}},
	}, nil, nil, sink, nil, noExecRunner(t), logsource.StatFile, "")
	if err != nil {
		t.Fatal("Failed to build source despite one invalid counter:", err)
	}

	defer src.stop(t.Context()) //nolint:errcheck

	if len(src.conns) != 2 {
		t.Errorf("Expected exactly 2 connectors (valid counters only, isolated per-counter), got %d", len(src.conns))
	}

	time.Sleep(500 * time.Millisecond)

	lines := []string{
		"[error] something broke\n",
		"127.0.0.1 GET / 200\n",
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

	got := totals()

	if got["app_errors_count"] != 1 {
		t.Errorf("Expected 1 match for app_errors_count, got %d", got["app_errors_count"])
	}

	if got["app_requests_count"] != 1 {
		t.Errorf("Expected 1 match for app_requests_count, got %d", got["app_requests_count"])
	}

	if _, found := got["app_broken_count"]; found {
		t.Errorf("app_broken_count should never receive any data, got %d", got["app_broken_count"])
	}
}

// TestSourceUpdatePicksUpNewFile is the regression test for a source that
// resolves a glob against at least one file at creation time: unlike a
// pattern handed directly to a single long-lived filelogreceiver (which polls
// for new matches internally), this source needs update() to be called
// (Manager.updateStaticSources does so every updateInterval) to notice a new
// file -- e.g. a daily-rotated log -- created after the source started.
func TestSourceUpdatePicksUpNewFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	file1, err := os.Create(filepath.Join(tmpDir, "app-2026-07-24.log"))
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer file1.Close()

	sink, totals := collectingSink()

	src, err := newSource(t.Context(), testTelemetrySettings(), []string{filepath.Join(tmpDir, "*.log")}, false, false, map[string]config.LogMetricsCount{
		"rotated_errors_count": {"conditions": []any{`IsMatch(body, "\\[error\\]")`}},
	}, nil, nil, sink, nil, noExecRunner(t), logsource.StatFile, "")
	if err != nil {
		t.Fatal("Failed to build source:", err)
	}

	defer src.stop(t.Context()) //nolint:errcheck

	time.Sleep(500 * time.Millisecond)

	if _, err := file1.WriteString("[error] from day one\n"); err != nil {
		t.Fatal("Failed to write log line:", err)
	}

	if err := file1.Sync(); err != nil {
		t.Fatal("Failed to sync log file:", err)
	}

	time.Sleep(time.Second)

	if got := totals()["rotated_errors_count"]; got != 1 {
		t.Fatalf("Expected 1 match from the original file, got %d", got)
	}

	// A new file appears matching the same glob (e.g. the next day's rotated log).
	file2, err := os.Create(filepath.Join(tmpDir, "app-2026-07-25.log"))
	if err != nil {
		t.Fatal("Can't create second log file:", err)
	}

	defer file2.Close()

	if err := src.update(t.Context()); err != nil {
		t.Fatal("Failed to update source:", err)
	}

	time.Sleep(500 * time.Millisecond)

	if _, err := file2.WriteString("[error] from day two\n"); err != nil {
		t.Fatal("Failed to write log line:", err)
	}

	if err := file2.Sync(); err != nil {
		t.Fatal("Failed to sync log file:", err)
	}

	time.Sleep(time.Second)

	if got := totals()["rotated_errors_count"]; got != 2 {
		t.Errorf("Expected 2 matches total after update() picked up the new file, got %d", got)
	}
}

// TestMetricInfo is the regression test for pasting an existing
// connectors.count.logs.<metric> definition almost verbatim: description,
// conditions and attributes must decode straight into the real
// countconnector.MetricInfo, with the raw description overriding the
// "log-to-metric: <name>" default.
func TestMetricInfo(t *testing.T) {
	t.Parallel()

	raw := config.LogMetricsCount{
		"description": "pasted from an existing OTel config",
		"conditions":  []any{`IsMatch(body, "error")`, `IsMatch(body, "warn")`},
		"attributes":  []any{map[string]any{"key": "level"}},
	}

	info, err := metricInfo("ignored_description_source", raw)
	if err != nil {
		t.Fatal("metricInfo returned an error:", err)
	}

	if info.Description != "pasted from an existing OTel config" {
		t.Errorf("Expected the raw description to apply, got %q", info.Description)
	}

	wantConditions := []string{`IsMatch(body, "error")`, `IsMatch(body, "warn")`}
	if diff := cmp.Diff(wantConditions, info.Conditions); diff != "" {
		t.Fatalf("Unexpected conditions (-want +got):\n%s", diff)
	}

	if len(info.Attributes) != 1 || info.Attributes[0].Key != "level" {
		t.Errorf("Expected the raw attributes to apply, got %v", info.Attributes)
	}
}

// TestMetricInfoDefaultDescription is the regression test for a metric with
// no raw config at all (e.g. declared purely to be fed by another source's
// conditions, see LogMetricsConfig.Count's doc comment): it must still get a
// usable default description and no conditions (which countconnector treats
// as "count every log record unconditionally").
func TestMetricInfoDefaultDescription(t *testing.T) {
	t.Parallel()

	info, err := metricInfo("my_metric", nil)
	if err != nil {
		t.Fatal("metricInfo returned an error:", err)
	}

	if info.Description != "log-to-metric: my_metric" {
		t.Errorf("Expected a default description, got %q", info.Description)
	}

	if len(info.Conditions) != 0 {
		t.Errorf("Expected no conditions with an empty raw config, got %v", info.Conditions)
	}
}

// TestMetricInfoDecodeError is the regression test for the fix that stops a
// malformed metric from silently turning into "count everything": a raw
// config.LogMetricsCount that fails to decode (here, "conditions" given as a
// bare string instead of a list) must return an error instead of an empty-
// Conditions MetricInfo, which would otherwise pass countconnector's own
// Validate() and silently match every log record.
func TestMetricInfoDecodeError(t *testing.T) {
	t.Parallel()

	raw := config.LogMetricsCount{
		"conditions": `IsMatch(body, "error")`, // should be a []any, not a bare string
	}

	if _, err := metricInfo("broken_metric", raw); err == nil {
		t.Fatal("Expected metricInfo to return an error for a malformed raw config")
	}
}

// TestExtractLabels covers the one field metricInfo's decode never sets
// (labels isn't a real countconnector field, see LogMetricsCount's doc
// comment).
func TestExtractLabels(t *testing.T) {
	t.Parallel()

	raw := config.LogMetricsCount{
		"conditions": []any{`IsMatch(body, "error")`},
		"labels":     map[string]any{"env": "prod", "not_a_string": 5},
	}

	got := extractLabels(raw)

	want := map[string]string{"env": "prod"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("Unexpected labels (-want +got):\n%s", diff)
	}

	if got := extractLabels(nil); got != nil {
		t.Errorf("Expected nil labels for an empty raw config, got %v", got)
	}
}
