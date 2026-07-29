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
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/otel/logsource"

	"github.com/google/go-cmp/cmp"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/pdata/pcommon"
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

// specsForCount builds the metricSpec list newSource needs to actually
// register a counter for every name in count (see metricsRegistry.resolve) --
// in production this is Manager.metricSpecs, reduced from the same count map.
func specsForCount(count map[string]config.LogMetricsCount) []metricSpec {
	specs := make([]metricSpec, 0, len(count))
	for name := range count {
		specs = append(specs, metricSpec{Metric: name})
	}

	return specs
}

// testRegistry returns a fresh metricsRegistry and a function reading back
// each metric's raw match count across every item. It reads
// RingCounter.Total() directly rather than going through emit()'s windowed
// rate, since these tests run in well under windowSecs: every match added is
// still in the ring when read back, so Total() equals the raw count.
func testRegistry() (*metricsRegistry, func() map[string]int64) {
	reg := newMetricsRegistry()

	return reg, func() map[string]int64 {
		reg.l.Lock()
		defer reg.l.Unlock()

		totals := make(map[string]int64, len(reg.counters))
		for key, c := range reg.counters {
			totals[key.metric] += int64(c.counter.Total())
		}

		return totals
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

	count := map[string]config.LogMetricsCount{
		"app_errors_count":   {"conditions": []any{`IsMatch(body, "\\[error\\]")`}},
		"app_requests_count": {"conditions": []any{`IsMatch(body, "GET /")`}},
	}
	reg, totals := testRegistry()

	src, err := newSource(t.Context(), testTelemetrySettings(), []string{logFile.Name()}, false, count, nil, nil, specsForCount(count), "src", kindReceiver, reg, nil, nil, noExecRunner(t), logsource.StatFile, "")
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

	count := map[string]config.LogMetricsCount{
		"app_errors_count": {"conditions": []any{`IsMatch(body, "\\[error\\]") and not IsMatch(body, "connection reset")`}},
	}
	reg, totals := testRegistry()

	src, err := newSource(t.Context(), testTelemetrySettings(), []string{logFile.Name()}, false, count, nil, nil, specsForCount(count), "src", kindReceiver, reg, nil, nil, noExecRunner(t), logsource.StatFile, "")
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
// RabbitMQ bug: a regex matching only the unwrapped message must still match
// a Docker-JSON-wrapped raw line once the container envelope operator runs.
func TestSourceUnwrapsContainerEnvelope(t *testing.T) {
	t.Parallel()

	logFile, err := os.CreateTemp(t.TempDir(), "container-*.log")
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer logFile.Close()

	count := map[string]config.LogMetricsCount{
		"container_errors_count": {"conditions": []any{`IsMatch(body, "^\\[error\\] something broke\\n?$")`}},
	}
	reg, totals := testRegistry()

	// The container parser preserves the trailing newline embedded in Docker's JSON
	// "log" field value, so the body is "[error] something broke\n", not "...broke".
	src, err := newSource(t.Context(), testTelemetrySettings(), []string{logFile.Name()}, false, count, nil, nil, specsForCount(count), "test-container", kindContainer, reg, nil, nil, noExecRunner(t), logsource.StatFile, "")
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

	count := map[string]config.LogMetricsCount{
		"bad": {"conditions": []any{`IsMatch(body, "(")`}},
	}
	reg, _ := testRegistry()

	_, err := newSource(t.Context(), testTelemetrySettings(), []string{"/nonexistent"}, false, count, nil, nil, specsForCount(count), "src", kindReceiver, reg, nil, nil, noExecRunner(t), logsource.StatFile, "")
	if !errors.Is(err, errNoValidCounter) {
		t.Fatalf("Expected errNoValidCounter (a metric applied but failed to build), got %v", err)
	}
}

// TestSourceNoApplicableMetric distinguishes "nothing named this source at
// all" (errNoApplicableMetric) from "metrics applied but all failed to build"
// (errNoValidCounter, see TestSourceInvalidRegex) -- the two log differently
// in Manager (see manager.go), so the underlying error must stay distinct.
func TestSourceNoApplicableMetric(t *testing.T) {
	t.Parallel()

	count := map[string]config.LogMetricsCount{
		"scoped_elsewhere": {"conditions": []any{`IsMatch(body, "error")`}, "sources": []any{"other_source"}},
	}
	reg, _ := testRegistry()

	_, err := newSource(t.Context(), testTelemetrySettings(), []string{"/nonexistent"}, false, count, nil, nil, specsForCount(count), "src", kindReceiver, reg, nil, nil, noExecRunner(t), logsource.StatFile, "")
	if !errors.Is(err, errNoApplicableMetric) {
		t.Fatalf("Expected errNoApplicableMetric (nothing scoped to this source), got %v", err)
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

	count := map[string]config.LogMetricsCount{
		"a_count": {"conditions": []any{`IsMatch(body, "a")`}},
		"b_count": {"conditions": []any{`IsMatch(body, "b")`}},
		"c_count": {"conditions": []any{`IsMatch(body, "c")`}},
	}
	reg, _ := testRegistry()

	src, err := newSource(t.Context(), testTelemetrySettings(), []string{logFile.Name()}, false, count, nil, nil, specsForCount(count), "src", kindReceiver, reg, nil, nil, noExecRunner(t), logsource.StatFile, "")
	if err != nil {
		t.Fatal("Failed to build source:", err)
	}

	defer src.stop(t.Context()) //nolint:errcheck

	if len(src.conns) != 1 {
		t.Errorf("Expected exactly 1 connector on the fast path, got %d", len(src.conns))
	}
}

// TestSourceIsolatesInvalidCounter checks buildConnectors' fallback path:
// with both valid and invalid counters, it falls back to one connector per
// valid counter, so the invalid one is disabled but its siblings keep working.
func TestSourceIsolatesInvalidCounter(t *testing.T) {
	t.Parallel()

	logFile, err := os.CreateTemp(t.TempDir(), "app-*.log")
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer logFile.Close()

	count := map[string]config.LogMetricsCount{
		"app_errors_count":   {"conditions": []any{`IsMatch(body, "\\[error\\]")`}},
		"app_requests_count": {"conditions": []any{`IsMatch(body, "GET /")`}},
		"app_broken_count":   {"conditions": []any{`IsMatch(body, "(")`}},
	}
	reg, totals := testRegistry()

	src, err := newSource(t.Context(), testTelemetrySettings(), []string{logFile.Name()}, false, count, nil, nil, specsForCount(count), "src", kindReceiver, reg, nil, nil, noExecRunner(t), logsource.StatFile, "")
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

	// The registry may still pre-declare a counter for app_broken_count (same
	// group as the two valid metrics), but it must never be incremented: no
	// connector was ever built for it.
	if got["app_broken_count"] != 0 {
		t.Errorf("app_broken_count should never receive any data, got %d", got["app_broken_count"])
	}
}

// TestSourceUpdatePicksUpNewFile checks that a source resolving a glob at
// creation time needs update() called (Manager.updateStaticSources does so
// every updateInterval) to notice a new file, e.g. a daily-rotated log.
func TestSourceUpdatePicksUpNewFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	file1, err := os.Create(filepath.Join(tmpDir, "app-2026-07-24.log"))
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer file1.Close()

	count := map[string]config.LogMetricsCount{
		"rotated_errors_count": {"conditions": []any{`IsMatch(body, "\\[error\\]")`}},
	}
	reg, totals := testRegistry()

	src, err := newSource(t.Context(), testTelemetrySettings(), []string{filepath.Join(tmpDir, "*.log")}, false, count, nil, nil, specsForCount(count), "src", kindReceiver, reg, nil, nil, noExecRunner(t), logsource.StatFile, "")
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

// TestMetricInfo checks that a pasted connectors.count.logs.<metric>
// definition decodes straight into countconnector.MetricInfo, with the raw
// description overriding the "log-to-metric: <name>" default.
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

// TestMetricInfoDefaultDescription checks that a metric with no raw config
// still gets a usable default description and no conditions (which
// countconnector treats as "count every log record unconditionally").
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

// TestMetricInfoDecodeError checks that a raw config.LogMetricsCount that
// fails to decode returns an error, instead of an empty-Conditions MetricInfo
// that would silently match every log record.
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

func TestFilterCount(t *testing.T) {
	t.Parallel()

	count := map[string]config.LogMetricsCount{
		"a": {"conditions": []any{`IsMatch(body, "a")`}},
		"b": {"conditions": []any{`IsMatch(body, "b")`}},
	}

	if got := filterCount(count, nil, "ctx"); len(got) != 2 {
		t.Errorf("Expected no scoping (nil names) to return count unchanged, got %v", got)
	}

	got := filterCount(count, []string{"a"}, "ctx")
	if len(got) != 1 || got["a"] == nil {
		t.Fatalf("Expected exactly metric %q, got %v", "a", got)
	}

	got = filterCount(count, []string{"a", "does_not_exist"}, "ctx")
	if len(got) != 1 || got["a"] == nil {
		t.Errorf("Expected the unknown name to be dropped, keeping only %q, got %v", "a", got)
	}
}

func TestFilterSpecs(t *testing.T) {
	t.Parallel()

	specs := []metricSpec{{Metric: "a"}, {Metric: "b"}}

	if got := filterSpecs(specs, nil); len(got) != 2 {
		t.Errorf("Expected no scoping (nil names) to return specs unchanged, got %v", got)
	}

	got := filterSpecs(specs, []string{"b"})
	if len(got) != 1 || got[0].Metric != "b" {
		t.Fatalf("Expected exactly metric %q, got %v", "b", got)
	}
}

func TestExtractSources(t *testing.T) {
	t.Parallel()

	if got := extractSources(nil); got != nil {
		t.Errorf("Expected nil sources for an empty raw config, got %v", got)
	}

	if got := extractSources(config.LogMetricsCount{"sources": []any{}}); got != nil {
		t.Errorf("Expected nil sources for an empty list, got %v", got)
	}

	got := extractSources(config.LogMetricsCount{"sources": []any{"app_full", "vault"}})

	want := []string{"app_full", "vault"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("Unexpected sources (-want +got):\n%s", diff)
	}
}

// TestGroupMetricsByItem covers the 0/1/2+-sources item-assignment rules: a
// global metric's item depends on its per_<kind>_item flag (see
// TestExtractPerItemFlag), a metric naming only sourceName gets its own item,
// a metric naming 2+ sources including sourceName merges into "", and a
// metric naming sources that don't include sourceName isn't fed by it.
func TestGroupMetricsByItem(t *testing.T) {
	t.Parallel()

	count := map[string]config.LogMetricsCount{
		"global_metric":       {},
		"scoped_to_self":      {"sources": []any{"recv-a"}},
		"scoped_to_other":     {"sources": []any{"recv-b"}},
		"merged_metric":       {"sources": []any{"recv-a", "recv-b"}},
		"merged_metric_other": {"sources": []any{"recv-b", "recv-c"}},
	}

	got := groupMetricsByItem(count, "recv-a", kindReceiver)

	want := map[string][]string{
		"":       {"global_metric", "merged_metric"},
		"recv-a": {"scoped_to_self"},
	}

	for item, names := range want {
		slices.Sort(names)

		gotNames := slices.Clone(got[item])
		slices.Sort(gotNames)

		if diff := cmp.Diff(names, gotNames); diff != "" {
			t.Errorf("Unexpected metric names for item %q (-want +got):\n%s", item, diff)
		}
	}

	if names, ok := got["recv-b"]; ok {
		t.Errorf("recv-a should never produce a group for another source's item, got %v", names)
	}

	if slices.Contains(got[""], "merged_metric_other") || slices.Contains(got[""], "scoped_to_other") {
		t.Errorf("recv-a should not be fed metrics scoped to other sources, got %v", got[""])
	}
}

// TestGroupMetricsByItemPerKindFlags covers the per_<kind>_item override for
// global (sourceless) metrics: a container gets its own item by default, a
// receiver/network source doesn't -- and either can be flipped explicitly.
func TestGroupMetricsByItemPerKindFlags(t *testing.T) {
	t.Parallel()

	count := map[string]config.LogMetricsCount{
		"default_container": {},
		"default_receiver":  {},
		"opt_in_receiver":   {"per_receiver_item": true},
		"opt_out_container": {"per_container_item": false},
	}

	gotContainer := groupMetricsByItem(count, "ctr-1", kindContainer)

	if !slices.Contains(gotContainer["ctr-1"], "default_container") {
		t.Errorf("Expected a container to get its own item by default, got groups %v", gotContainer)
	}

	if !slices.Contains(gotContainer[""], "opt_out_container") {
		t.Errorf("Expected per_container_item:false to merge into item=\"\", got groups %v", gotContainer)
	}

	gotReceiver := groupMetricsByItem(count, "recv-1", kindReceiver)

	if !slices.Contains(gotReceiver[""], "default_receiver") {
		t.Errorf("Expected a receiver to share item=\"\" by default, got groups %v", gotReceiver)
	}

	if !slices.Contains(gotReceiver["recv-1"], "opt_in_receiver") {
		t.Errorf("Expected per_receiver_item:true to give the receiver its own item, got groups %v", gotReceiver)
	}
}

func TestExtractPerItemFlag(t *testing.T) {
	t.Parallel()

	if !extractPerItemFlag(config.LogMetricsCount{}, kindContainer) {
		t.Error("Expected per_container_item to default to true")
	}

	if extractPerItemFlag(config.LogMetricsCount{}, kindReceiver) {
		t.Error("Expected per_receiver_item to default to false")
	}

	if extractPerItemFlag(config.LogMetricsCount{}, kindNetwork) {
		t.Error("Expected per_network_item to default to false")
	}

	if extractPerItemFlag(config.LogMetricsCount{"per_container_item": false}, kindContainer) {
		t.Error("Expected an explicit per_container_item:false to override the default")
	}

	if !extractPerItemFlag(config.LogMetricsCount{"per_receiver_item": true}, kindReceiver) {
		t.Error("Expected an explicit per_receiver_item:true to override the default")
	}

	// A present but wrongly-typed value falls back to the default rather
	// than panicking or silently misbehaving (it also logs a warning, not
	// asserted here).
	if extractPerItemFlag(config.LogMetricsCount{"per_container_item": "true"}, kindContainer) != perItemDefault(kindContainer) {
		t.Error("Expected a non-bool per_container_item value to fall back to the default")
	}
}
