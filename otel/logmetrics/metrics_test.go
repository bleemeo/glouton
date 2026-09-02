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
	"slices"
	"testing"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/types"

	"github.com/google/go-cmp/cmp"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
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

// testRegistry returns a fresh metricsRegistry and a function reading back each metric's raw match count.
func testRegistry() (*metricsRegistry, func() map[counterKey]int64) {
	reg := newMetricsRegistry(0)

	return reg, func() map[counterKey]int64 {
		reg.l.Lock()
		defer reg.l.Unlock()

		totals := make(map[counterKey]int64, len(reg.counters))
		for key, c := range reg.counters {
			totals[key] += int64(c.peekSum())
		}

		return totals
	}
}

// logsWithBody builds a single-record plog.Logs with the given body.
func logsWithBody(body string) plog.Logs {
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	rl.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty().Body().SetStr(body)

	return ld
}

// logsWithBodyAndAttrs builds a single-record plog.Logs with the given body and log record attributes,
// the level a metrics: entry's "attributes:" list extracts from (see countconnector's counter.update).
func logsWithBodyAndAttrs(body string, attrs map[string]string) plog.Logs {
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	record := rl.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
	record.Body().SetStr(body)

	for k, v := range attrs {
		record.Attributes().PutStr(k, v)
	}

	return ld
}

// TestResolveLabels exercises resolveLabels' full precedence: defaultItem seeded first, a labels:
// {item: ...} entry overriding it, a top-level item: overriding that in turn, and any label -- item
// included -- left with an empty string value dropped from the result entirely.
func TestResolveLabels(t *testing.T) {
	t.Parallel()

	// No override at all: falls back to defaultItem.
	got := resolveLabels(config.LogMetricEntry{}, "recv")
	if diff := cmp.Diff(map[string]string{"item": "recv"}, got); diff != "" {
		t.Errorf("Unexpected labels with no override (-want +got):\n%s", diff)
	}

	// Top-level item: overrides defaultItem.
	got = resolveLabels(config.LogMetricEntry{"item": "custom"}, "recv")
	if diff := cmp.Diff(map[string]string{"item": "custom"}, got); diff != "" {
		t.Errorf("Unexpected labels with a top-level item override (-want +got):\n%s", diff)
	}

	// item explicitly set to "" (legacy migration's way of pinning "no item at all") drops the key
	// entirely, rather than defaulting back to defaultItem or being kept as an empty value.
	got = resolveLabels(config.LogMetricEntry{"item": ""}, "recv")
	if diff := cmp.Diff(map[string]string(nil), got); diff != "" {
		t.Errorf(`Unexpected labels with item: "" (-want +got):\n%s`, diff)
	}

	// A non-string item is ignored (falls back to whatever was already resolved -- defaultItem here).
	got = resolveLabels(config.LogMetricEntry{"item": 5}, "recv")
	if diff := cmp.Diff(map[string]string{"item": "recv"}, got); diff != "" {
		t.Errorf("Unexpected labels with a non-string item (-want +got):\n%s", diff)
	}

	// labels: {item: ...} overrides defaultItem when there's no top-level item: field.
	got = resolveLabels(config.LogMetricEntry{"labels": map[string]any{"item": "from-labels"}}, "recv")
	if diff := cmp.Diff(map[string]string{"item": "from-labels"}, got); diff != "" {
		t.Errorf("Unexpected labels with labels: {item: ...} (-want +got):\n%s", diff)
	}

	// A top-level item: wins over a labels: {item: ...} entry when both are set.
	got = resolveLabels(config.LogMetricEntry{
		"item":   "from-top-level",
		"labels": map[string]any{"item": "from-labels"},
	}, "recv")
	if diff := cmp.Diff(map[string]string{"item": "from-top-level"}, got); diff != "" {
		t.Errorf("Unexpected labels with both item: and labels: {item: ...} (-want +got):\n%s", diff)
	}

	// A regular label merges in alongside item, and a non-string one is dropped with a warning, not
	// this call failing.
	got = resolveLabels(config.LogMetricEntry{"labels": map[string]any{"env": "prod", "not_a_string": 5}}, "recv")
	if diff := cmp.Diff(map[string]string{"item": "recv", "env": "prod"}, got); diff != "" {
		t.Errorf("Unexpected labels with a regular label (-want +got):\n%s", diff)
	}

	// A regular label explicitly set to "" is dropped too, same as item: empty means unset for every
	// label, not just item.
	got = resolveLabels(config.LogMetricEntry{"labels": map[string]any{"env": ""}}, "recv")
	if diff := cmp.Diff(map[string]string{"item": "recv"}, got); diff != "" {
		t.Errorf(`Unexpected labels with env: "" (-want +got):\n%s`, diff)
	}

	// defaultItem itself can be "" (e.g. a legacy-migrated entry that never gets one): with no other
	// override, the result has no item key at all, not item: "".
	if got := resolveLabels(config.LogMetricEntry{}, ""); got != nil {
		t.Errorf(`Expected nil labels with an empty defaultItem and no override, got %v`, got)
	}
}

func TestResolveInlineMetric(t *testing.T) {
	t.Parallel()

	rm, ok := resolveInlineMetric(config.LogMetricEntry{"metric": "app_errors", "conditions": []any{`IsMatch(body, "x")`}}, "recv")
	if !ok || rm.Metric != "app_errors" {
		t.Fatalf("Expected a resolved metric named app_errors, got %+v, ok=%v", rm, ok)
	}

	if rm.Item != "recv" {
		t.Errorf("Expected Item to fall back to defaultItem with no explicit override, got %q", rm.Item)
	}

	if _, ok := resolveInlineMetric(config.LogMetricEntry{"conditions": []any{`IsMatch(body, "x")`}}, "recv"); ok {
		t.Error("Expected an entry with no \"metric\" name to be rejected")
	}
}

// Test include+inline mixing across multiple libraries, plus an unknown include.
func TestResolveReceiverMetrics(t *testing.T) {
	t.Parallel()

	rules := map[string][]config.LogMetricEntry{
		"web_errors": {
			{"metric": "http_5xx", "conditions": []any{`IsMatch(attributes["code"], "5..")`}},
			{"metric": "http_4xx", "conditions": []any{`IsMatch(attributes["code"], "4..")`}},
		},
		"other_lib": {
			{"metric": "other_metric", "conditions": []any{`IsMatch(body, "x")`}},
		},
	}

	rawMetrics := []any{
		map[string]any{"include": "web_errors"},
		map[string]any{"include": "other_lib"},
		map[string]any{"metric": "inline_metric", "conditions": []any{`IsMatch(body, "y")`}},
		map[string]any{"include": "does_not_exist"},
	}

	resolved := resolveReceiverMetrics(rawMetrics, rules, "recv")

	names := make([]string, 0, len(resolved))
	for _, rm := range resolved {
		names = append(names, rm.Metric)
	}

	slices.Sort(names)

	want := []string{"http_4xx", "http_5xx", "inline_metric", "other_metric"}
	if diff := cmp.Diff(want, names); diff != "" {
		t.Fatalf("Unexpected resolved metric names (-want +got):\n%s", diff)
	}
}

func TestResolveReceiverMetricsMalformedEntry(t *testing.T) {
	t.Parallel()

	resolved := resolveReceiverMetrics([]any{"not-a-map", 5}, nil, "recv")
	if len(resolved) != 0 {
		t.Errorf("Expected malformed entries to be ignored, got %v", resolved)
	}
}

// TestResolveReceiverMetricsDropsVerbatimDuplicates guards against a metrics: entry appearing twice,
// verbatim, from any of the three ways that can happen -- two identical inline entries, two identical
// entries inside one included metrics_rules list, and an inline entry repeating one already pulled in
// by an include -- each of which would otherwise get its own countconnector and double-count every
// matching line into the same counter (same metric+item+labels key).
func TestResolveReceiverMetricsDropsVerbatimDuplicates(t *testing.T) {
	t.Parallel()

	rules := map[string][]config.LogMetricEntry{
		"dup_rule": {
			{"metric": "rule_metric", "conditions": []any{`IsMatch(body, "x")`}},
			{"metric": "rule_metric", "conditions": []any{`IsMatch(body, "x")`}},
		},
	}

	rawMetrics := []any{
		map[string]any{"metric": "inline_dup", "conditions": []any{`IsMatch(body, "y")`}},
		map[string]any{"metric": "inline_dup", "conditions": []any{`IsMatch(body, "y")`}},
		map[string]any{"include": "dup_rule"},
		map[string]any{"metric": "rule_metric", "conditions": []any{`IsMatch(body, "x")`}},
	}

	resolved := resolveReceiverMetrics(rawMetrics, rules, "recv")

	names := make([]string, 0, len(resolved))
	for _, rm := range resolved {
		names = append(names, rm.Metric)
	}

	slices.Sort(names)

	want := []string{"inline_dup", "rule_metric"}
	if diff := cmp.Diff(want, names); diff != "" {
		t.Fatalf("Unexpected resolved metric names (-want +got), duplicates should have been dropped:\n%s", diff)
	}
}

// TestResolveReceiverMetricsDropsRegexConditionsEquivalentDuplicate guards against a duplicate spelled
// two different ways: "regex:" is documented sugar for a single IsMatch(body, ...) condition, so an
// entry using regex: "X" and one using conditions: ['IsMatch(body, "X")'] resolve to the exact same
// final condition and must be treated as the same duplicate, not two distinct entries.
func TestResolveReceiverMetricsDropsRegexConditionsEquivalentDuplicate(t *testing.T) {
	t.Parallel()

	rawMetrics := []any{
		map[string]any{"metric": "app_errors", "regex": "boom"},
		map[string]any{"metric": "app_errors", "conditions": []any{`IsMatch(body, "boom")`}},
	}

	resolved := resolveReceiverMetrics(rawMetrics, nil, "recv")
	if len(resolved) != 1 {
		t.Fatalf("Expected the regex:/conditions: equivalent entry to be dropped as a duplicate, got %d resolved: %v", len(resolved), resolved)
	}
}

// TestResolveReceiverMetricsDuplicateConditionOrderDoesNotMatter guards against two entries whose
// conditions: list has the same elements in a different order being treated as distinct: OR'ed
// conditions are commutative, so reordering them changes nothing about which lines match.
func TestResolveReceiverMetricsDuplicateConditionOrderDoesNotMatter(t *testing.T) {
	t.Parallel()

	rawMetrics := []any{
		map[string]any{"metric": "app_errors", "conditions": []any{`IsMatch(body, "a")`, `IsMatch(body, "b")`}},
		map[string]any{"metric": "app_errors", "conditions": []any{`IsMatch(body, "b")`, `IsMatch(body, "a")`}},
	}

	resolved := resolveReceiverMetrics(rawMetrics, nil, "recv")
	if len(resolved) != 1 {
		t.Fatalf("Expected the reordered-conditions entry to be dropped as a duplicate, got %d resolved: %v", len(resolved), resolved)
	}
}

// TestResolveReceiverMetricsKeepsEntriesThatActuallyDiffer is the flip side of the duplicate-detection
// tests above: entries that differ in item, labels, attributes, or the actual condition must never be
// folded together, since each legitimately produces its own distinct series.
func TestResolveReceiverMetricsKeepsEntriesThatActuallyDiffer(t *testing.T) {
	t.Parallel()

	rawMetrics := []any{
		map[string]any{"metric": "m", "conditions": []any{`IsMatch(body, "x")`}},
		map[string]any{"metric": "m", "conditions": []any{`IsMatch(body, "x")`}, "item": "custom-item"},
		map[string]any{"metric": "m", "conditions": []any{`IsMatch(body, "x")`}, "labels": map[string]any{"code": "2xx"}},
		map[string]any{"metric": "m", "conditions": []any{`IsMatch(body, "x")`}, "labels": map[string]any{"code": "3xx"}},
		map[string]any{"metric": "m", "conditions": []any{`IsMatch(body, "x")`}, "attributes": []any{map[string]any{"key": "status"}}},
		map[string]any{"metric": "m", "conditions": []any{`IsMatch(body, "y")`}},
	}

	resolved := resolveReceiverMetrics(rawMetrics, nil, "recv")
	if len(resolved) != len(rawMetrics) {
		t.Fatalf("Expected all %d entries to be kept as distinct, got %d resolved: %v", len(rawMetrics), len(resolved), resolved)
	}
}

// Test default-item grouping and explicit per-metric item overrides, including an override to "".
func TestGroupResolvedMetricsByItem(t *testing.T) {
	t.Parallel()

	entries := []resolvedMetric{
		{Metric: "default_a", Item: "recv"},
		{Metric: "default_b", Item: "recv"},
		{Metric: "overridden", Item: "custom-item"},
		{Metric: "migrated_empty", Item: ""},
	}

	groups := groupResolvedMetricsByItem(entries)

	wantDefault := []string{"default_a", "default_b"}
	gotDefault := namesOf(groups["recv"])
	slices.Sort(gotDefault)

	if diff := cmp.Diff(wantDefault, gotDefault); diff != "" {
		t.Errorf("Unexpected default-item group (-want +got):\n%s", diff)
	}

	if got := namesOf(groups["custom-item"]); len(got) != 1 || got[0] != "overridden" {
		t.Errorf("Expected the explicit item override to get its own group, got %v", got)
	}

	if got := namesOf(groups[""]); len(got) != 1 || got[0] != "migrated_empty" {
		t.Errorf(`Expected item:"" to be honored as its own group (not merged into "recv"), got %v`, got)
	}
}

// mustResolveInline resolves raw (which must include a "metric" key) via resolveInlineMetric,
// failing the test if it's rejected. Building []resolvedMetric fixtures this way, instead of hand-built
// resolvedMetric{Raw: ...} literals, keeps them shaped exactly like the real pipeline would produce --
// Item/Labels derived from Raw+defaultItem by resolveLabels -- instead of each test having to duplicate
// that derivation (and risk it drifting out of sync with the real one).
func mustResolveInline(t *testing.T, raw config.LogMetricEntry, defaultItem string) resolvedMetric {
	t.Helper()

	rm, ok := resolveInlineMetric(raw, defaultItem)
	if !ok {
		t.Fatalf("resolveInlineMetric rejected %v", raw)
	}

	return rm
}

// itemLabelsKey encodes the counterKey.labels component for an entry whose only static label is its
// own (non-empty) item -- see resolveLabels, which always folds item into the resolved label set.
func itemLabelsKey(item string) string {
	return types.LabelsToText(map[string]string{"item": item})
}

func namesOf(entries []resolvedMetric) []string {
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Metric)
	}

	return names
}

// Test that a pasted connectors.count.logs.<metric> definition decodes into countconnector.MetricInfo.
func TestMetricInfo(t *testing.T) {
	t.Parallel()

	raw := config.LogMetricEntry{
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

// Test that "regex" expands into a single IsMatch(body, ...) condition, on top of any explicit "conditions".
func TestMetricInfoRegexSugar(t *testing.T) {
	t.Parallel()

	info, err := metricInfo("m", config.LogMetricEntry{"regex": `\[error\]`})
	if err != nil {
		t.Fatal("metricInfo returned an error:", err)
	}

	// %q escapes the literal backslashes so the OTTL string literal represents the same regex bytes.
	want := []string{`IsMatch(body, "\\[error\\]")`}
	if diff := cmp.Diff(want, info.Conditions); diff != "" {
		t.Fatalf("Unexpected conditions from regex sugar (-want +got):\n%s", diff)
	}

	info, err = metricInfo("m", config.LogMetricEntry{
		"conditions": []any{`IsMatch(attributes["level"], "warn")`},
		"regex":      `oops`,
	})
	if err != nil {
		t.Fatal("metricInfo returned an error:", err)
	}

	wantBoth := []string{`IsMatch(attributes["level"], "warn")`, `IsMatch(body, "oops")`}
	if diff := cmp.Diff(wantBoth, info.Conditions); diff != "" {
		t.Fatalf("Unexpected conditions with both regex and conditions set (-want +got):\n%s", diff)
	}
}

// Test that a metric with no raw config gets a default description and no conditions.
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

// Test that a raw config.LogMetricEntry which fails to decode returns an error rather than matching every record.
func TestMetricInfoDecodeError(t *testing.T) {
	t.Parallel()

	raw := config.LogMetricEntry{
		"conditions": `IsMatch(body, "error")`, // should be []any, not a bare string
	}

	if _, err := metricInfo("broken_metric", raw); err == nil {
		t.Fatal("Expected metricInfo to return an error for a malformed raw config")
	}
}

// TestAppendResolvedMetricDedupsItemSpellings guards against a regression where item: "home" and
// labels: {item: "home"} -- documented as equivalent spellings of the same override -- escaped
// duplicate detection: metricSignature's labels used to come from a raw, unresolved view of the
// entry's own labels:, which still included "item" for the labels: {item: ...} spelling only, giving
// the two entries different signatures despite resolving to the exact same final series. Now that
// resolveLabels resolves item once, upfront, both spellings produce the exact same rm.Labels.
func TestAppendResolvedMetricDedupsItemSpellings(t *testing.T) {
	t.Parallel()

	resolved := resolveReceiverMetrics([]any{
		map[string]any{
			"metric":     "requests_total",
			"conditions": []any{`IsMatch(body, "a")`},
			"item":       "home",
		},
		map[string]any{
			"metric":     "requests_total",
			"conditions": []any{`IsMatch(body, "a")`},
			"labels":     map[string]any{"item": "home"},
		},
	}, nil, "recv")

	if len(resolved) != 1 {
		t.Fatalf("Expected the labels: {item: ...} spelling to be deduped against item: \"home\", got %d entries: %v", len(resolved), resolved)
	}
}

// TestAppendResolvedMetricNeverDedupsUnsetAgainstExplicitEmptyItem guards a case that used to need a
// separate itemSet flag to get right: an entry that never sets item at all (falls back to this
// receiver's own default item, "recv") must never be deduped against one that explicitly sets item: ""
// (legacy migration's way of saying "no item label at all") -- they resolve to two different final
// groups/counterKeys (one under "recv", the other under "") whenever the source's own default item
// isn't itself "" (true in every real case), so treating them as the same signature would wrongly drop
// one of two entries that are, in fact, going to produce two distinct series. Now that resolveLabels
// resolves item eagerly against defaultItem, this falls out naturally: the two entries simply end up
// with different rm.Item/rm.Labels["item"] values, no special-casing needed.
func TestAppendResolvedMetricNeverDedupsUnsetAgainstExplicitEmptyItem(t *testing.T) {
	t.Parallel()

	resolved := resolveReceiverMetrics([]any{
		map[string]any{
			"metric":     "requests_total",
			"conditions": []any{`IsMatch(body, "a")`},
			// item left unset entirely: falls back to defaultItem ("recv").
		},
		map[string]any{
			"metric":     "requests_total",
			"conditions": []any{`IsMatch(body, "a")`},
			"item":       "",
		},
	}, nil, "recv")

	if len(resolved) != 2 {
		t.Fatalf("Expected the unset-item and explicit item:\"\" entries to stay distinct, got %d entries: %v", len(resolved), resolved)
	}
}

// Test the real countconnector wiring end to end: logs fed through the built connectors increment the shared registry.
func TestBuildGroupedConnectorsEndToEnd(t *testing.T) {
	t.Parallel()

	entries := []resolvedMetric{
		mustResolveInline(t, config.LogMetricEntry{"metric": "app_errors_count", "conditions": []any{`IsMatch(body, "\\[error\\]")`}}, "my-receiver"),
		mustResolveInline(t, config.LogMetricEntry{"metric": "app_requests_count", "conditions": []any{`IsMatch(body, "GET /")`}}, "my-receiver"),
	}

	reg, totals := testRegistry()

	conns, _, err := buildGroupedConnectors(t.Context(), testTelemetrySettings(), entries, reg, "my-receiver")
	if err != nil {
		t.Fatal("buildGroupedConnectors returned an error:", err)
	}

	defer shutdownConns(t.Context(), conns) //nolint:errcheck

	sink := logsConsumerFor(conns)

	lines := []string{
		"127.0.0.1 GET / 200",
		"[error] something broke",
		"127.0.0.1 GET / [error] weird combo",
		"just a normal line",
	}

	for _, line := range lines {
		if err := sink.ConsumeLogs(t.Context(), logsWithBody(line)); err != nil {
			t.Fatal("ConsumeLogs returned an error:", err)
		}
	}

	got := totals()

	errorsKey := counterKey{metric: "app_errors_count", item: "my-receiver", labels: itemLabelsKey("my-receiver")}
	if got[errorsKey] != 2 {
		t.Errorf("Expected 2 matches for app_errors_count, got %d", got[errorsKey])
	}

	requestsKey := counterKey{metric: "app_requests_count", item: "my-receiver", labels: itemLabelsKey("my-receiver")}
	if got[requestsKey] != 2 {
		t.Errorf("Expected 2 matches for app_requests_count, got %d", got[requestsKey])
	}
}

// Test that two metrics: entries sharing the same metric name, but with different conditions and static
// labels, produce two independently-counted, distinctly-labeled series -- not one series whose condition
// and label come from different entries (the reported bug: e.g. a single "log_common_code" split into
// code=2xx/code=3xx variants by two same-named entries).
func TestBuildGroupedConnectorsSameMetricNameDifferentLabelsStayDistinct(t *testing.T) {
	t.Parallel()

	entries := []resolvedMetric{
		mustResolveInline(t, config.LogMetricEntry{
			"metric":     "log_common_code",
			"conditions": []any{`IsMatch(body, "status=2")`},
			"labels":     map[string]any{"code": "2xx"},
		}, "my-receiver"),
		mustResolveInline(t, config.LogMetricEntry{
			"metric":     "log_common_code",
			"conditions": []any{`IsMatch(body, "status=3")`},
			"labels":     map[string]any{"code": "3xx"},
		}, "my-receiver"),
	}

	reg, _ := testRegistry()

	conns, _, err := buildGroupedConnectors(t.Context(), testTelemetrySettings(), entries, reg, "my-receiver")
	if err != nil {
		t.Fatal("buildGroupedConnectors returned an error:", err)
	}

	defer shutdownConns(t.Context(), conns) //nolint:errcheck

	if len(conns) != 2 {
		t.Fatalf("Expected 2 independent connectors (one per entry), got %d", len(conns))
	}

	sink := logsConsumerFor(conns)

	lines := []string{"status=200 ok", "status=201 created", "status=301 moved", "status=302 found", "status=302 found"}

	for _, line := range lines {
		if err := sink.ConsumeLogs(t.Context(), logsWithBody(line)); err != nil {
			t.Fatal("ConsumeLogs returned an error:", err)
		}
	}

	var code2xx, code3xx *counter

	reg.l.Lock()

	for key, c := range reg.counters {
		if key.metric != "log_common_code" {
			continue
		}

		switch c.lbls.Get("code") {
		case "2xx":
			code2xx = c
		case "3xx":
			code3xx = c
		}
	}

	reg.l.Unlock()

	if code2xx == nil || code3xx == nil {
		t.Fatalf("Expected distinct code=2xx and code=3xx counters, got counters=%+v", reg.counters)
	}

	if got := code2xx.peekSum(); got != 2 {
		t.Errorf("Expected 2 matches for code=2xx (200, 201), got %d", got)
	}

	if got := code3xx.peekSum(); got != 3 {
		t.Errorf("Expected 3 matches for code=3xx (301, 302, 302), got %d", got)
	}
}

// Test that a metric overriding its own item is built as a distinct group from the receiver's default-item metrics.
func TestBuildGroupedConnectorsExplicitItemSplitsGroup(t *testing.T) {
	t.Parallel()

	entries := []resolvedMetric{
		mustResolveInline(t, config.LogMetricEntry{"metric": "default_item_metric", "conditions": []any{`IsMatch(body, "a")`}}, "recv"),
		mustResolveInline(t, config.LogMetricEntry{"metric": "custom_item_metric", "conditions": []any{`IsMatch(body, "a")`}, "item": "custom-item"}, "recv"),
	}

	reg, totals := testRegistry()

	conns, _, err := buildGroupedConnectors(t.Context(), testTelemetrySettings(), entries, reg, "recv")
	if err != nil {
		t.Fatal("buildGroupedConnectors returned an error:", err)
	}

	defer shutdownConns(t.Context(), conns) //nolint:errcheck

	sink := logsConsumerFor(conns)

	if err := sink.ConsumeLogs(t.Context(), logsWithBody("a")); err != nil {
		t.Fatal("ConsumeLogs returned an error:", err)
	}

	got := totals()

	if got[counterKey{metric: "default_item_metric", item: "recv", labels: itemLabelsKey("recv")}] != 1 {
		t.Errorf("Expected 1 match under item %q, got %v", "recv", got)
	}

	if got[counterKey{metric: "custom_item_metric", item: "custom-item", labels: itemLabelsKey("custom-item")}] != 1 {
		t.Errorf("Expected 1 match under item %q, got %v", "custom-item", got)
	}
}

// Test that a metric overriding its item via labels: {item: ...} (no top-level item: field) is
// grouped/counted under that item, exactly as if it had used the top-level item: field -- guards
// against a regression where labels: {item: ...} was silently shadowed by the auto-derived item
// instead of being honored as an equivalent override.
func TestBuildGroupedConnectorsLabelsItemActsLikeTopLevelItem(t *testing.T) {
	t.Parallel()

	rm, ok := resolveInlineMetric(config.LogMetricEntry{
		"metric":     "custom_item_metric",
		"conditions": []any{`IsMatch(body, "a")`},
		"labels":     map[string]any{"item": "custom-item"},
	}, "recv")
	if !ok {
		t.Fatal("resolveInlineMetric rejected a valid entry")
	}

	entries := []resolvedMetric{rm}

	reg, totals := testRegistry()

	conns, gotItems, err := buildGroupedConnectors(t.Context(), testTelemetrySettings(), entries, reg, "recv")
	if err != nil {
		t.Fatal("buildGroupedConnectors returned an error:", err)
	}

	defer shutdownConns(t.Context(), conns) //nolint:errcheck

	if diff := cmp.Diff([]string{"custom-item"}, gotItems); diff != "" {
		t.Fatalf("Unexpected grouped items:\n%s", diff)
	}

	sink := logsConsumerFor(conns)

	if err := sink.ConsumeLogs(t.Context(), logsWithBody("a")); err != nil {
		t.Fatal("ConsumeLogs returned an error:", err)
	}

	got := totals()

	// The counter's key carries item baked into its "labels" component too (see resolveLabels: item is
	// just another label), so labels: {item: ...} and a top-level item: field produce the exact same
	// counterKey.
	key := counterKey{metric: "custom_item_metric", item: "custom-item", labels: itemLabelsKey("custom-item")}
	if got[key] != 1 {
		t.Errorf("Expected 1 match under item %q (from labels: {item: ...}), got %v", "custom-item", got)
	}

	if got[counterKey{metric: "custom_item_metric", item: "recv", labels: itemLabelsKey("recv")}] != 0 {
		t.Errorf("Expected no match under the receiver's own default item %q, got %v", "recv", got)
	}
}

// Test that with both valid and invalid counters, buildConnectors falls back to one connector per valid counter.
func TestBuildGroupedConnectorsIsolatesInvalidCounter(t *testing.T) {
	t.Parallel()

	entries := []resolvedMetric{
		mustResolveInline(t, config.LogMetricEntry{"metric": "app_errors_count", "conditions": []any{`IsMatch(body, "\\[error\\]")`}}, "recv"),
		mustResolveInline(t, config.LogMetricEntry{"metric": "app_broken_count", "conditions": []any{`IsMatch(body, "(")`}}, "recv"),
	}

	reg, totals := testRegistry()

	conns, _, err := buildGroupedConnectors(t.Context(), testTelemetrySettings(), entries, reg, "recv")
	if err != nil {
		t.Fatal("buildGroupedConnectors returned an error despite one valid counter:", err)
	}

	defer shutdownConns(t.Context(), conns) //nolint:errcheck

	if len(conns) != 1 {
		t.Errorf("Expected exactly 1 connector (the invalid one isolated out), got %d", len(conns))
	}

	sink := logsConsumerFor(conns)

	if err := sink.ConsumeLogs(t.Context(), logsWithBody("[error] boom")); err != nil {
		t.Fatal("ConsumeLogs returned an error:", err)
	}

	got := totals()

	if got[counterKey{metric: "app_errors_count", item: "recv", labels: itemLabelsKey("recv")}] != 1 {
		t.Errorf("Expected 1 match for app_errors_count, got %v", got)
	}

	// app_broken_count's counter may be pre-declared but must never be incremented.
	if got[counterKey{metric: "app_broken_count", item: "recv", labels: itemLabelsKey("recv")}] != 0 {
		t.Errorf("app_broken_count should never receive any data, got %v", got)
	}

	if !slices.Contains(reg.metricNames(), "app_broken_count") {
		t.Error("Expected app_broken_count to still be declared, even though its connector never built")
	}
}

func TestBuildGroupedConnectorsNoEntries(t *testing.T) {
	t.Parallel()

	reg, _ := testRegistry()

	_, _, err := buildGroupedConnectors(t.Context(), testTelemetrySettings(), nil, reg, "recv")
	if !errors.Is(err, errNoApplicableMetric) {
		t.Fatalf("Expected errNoApplicableMetric for an empty entry list, got %v", err)
	}
}

func TestBuildGroupedConnectorsAllInvalid(t *testing.T) {
	t.Parallel()

	entries := []resolvedMetric{
		mustResolveInline(t, config.LogMetricEntry{"metric": "bad", "conditions": []any{`IsMatch(body, "(")`}}, "recv"),
	}

	reg, _ := testRegistry()

	_, gotItems, err := buildGroupedConnectors(t.Context(), testTelemetrySettings(), entries, reg, "recv")
	if !errors.Is(err, errNoValidCounter) {
		t.Fatalf("Expected errNoValidCounter (a metric applied but failed to build), got %v", err)
	}

	if len(gotItems) != 0 {
		t.Errorf("Expected no items reported for a group that failed entirely, got %v", gotItems)
	}

	// Regression: resolve() must not have declared a permanent counter for "recv" here -- since the
	// item never makes it into buildGroupedConnectors' returned items, Manager.ReleaseSource would
	// never release it, and reg.emit would keep reporting a stale, always-zero "bad" series forever.
	reg.l.Lock()
	defer reg.l.Unlock()

	for key := range reg.counters {
		if key.item == "recv" {
			t.Errorf("Expected no counter left registered for item %q after the whole group failed to build, got %+v", "recv", key)
		}
	}
}

// Test the 0/1/N-connector special cases delegated to logsource.FanoutLogs.
func TestLogsConsumerFor(t *testing.T) {
	t.Parallel()

	if got := logsConsumerFor(nil); got != nil {
		t.Errorf("Expected a nil consumer for zero connectors, got %v", got)
	}
}
