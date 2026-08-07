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
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/otel/logsource"
	"github.com/bleemeo/glouton/types"
)

func newTestManager(t *testing.T, cfg config.OpenTelemetry, metricsRules map[string][]config.LogMetricEntry) *Manager {
	t.Helper()

	man := New(cfg, metricsRules)
	// Most tests want release() to be synchronous; the grace-period-specific tests set their own.
	man.reg.gracePeriod = 0

	return man
}

// countsFor reads back every declared counter's raw total for a metric, keyed by item.
func countsFor(man *Manager, metric string) map[string]int64 {
	man.reg.l.Lock()
	defer man.reg.l.Unlock()

	counts := make(map[string]int64)

	for key, c := range man.reg.counters {
		if key.metric == metric {
			counts[key.item] = int64(c.counter.Total())
		}
	}

	return counts
}

// Test that a receiver with no "metrics:" field, or an unknown receiver name, is never wanted.
func TestWantSourceReceiverNoMetrics(t *testing.T) {
	t.Parallel()

	man := newTestManager(t, config.OpenTelemetry{
		Receivers: map[string]config.LogReceiver{
			"app": {"include": []string{"/var/log/app.log"}},
		},
	}, nil)

	if _, ok := man.WantSource(t.Context(), logsource.ResolvedSource{Kind: logsource.SourceReceiver, Name: "app", ReceiverName: "app"}); ok {
		t.Error("Expected a receiver with no metrics: field to not be wanted")
	}

	if _, ok := man.WantSource(t.Context(), logsource.ResolvedSource{Kind: logsource.SourceReceiver, Name: "unknown", ReceiverName: "unknown"}); ok {
		t.Error("Expected an unknown receiver name to not be wanted")
	}
}

// Test a hand-written inline metric end to end, from WantSource through to the registry.
func TestWantSourceReceiverInline(t *testing.T) {
	t.Parallel()

	man := newTestManager(t, config.OpenTelemetry{
		Receivers: map[string]config.LogReceiver{
			"app": {
				"include": []string{"/var/log/app.log"},
				"metrics": []any{
					map[string]any{"metric": "app_errors_count", "conditions": []any{`IsMatch(body, "\\[error\\]")`}},
				},
			},
		},
	}, nil)

	sink, ok := man.WantSource(t.Context(), logsource.ResolvedSource{Kind: logsource.SourceReceiver, Name: "app", ReceiverName: "app"})
	if !ok || sink == nil {
		t.Fatal("Expected the receiver to be wanted with a non-nil sink")
	}

	if err := sink.ConsumeLogs(t.Context(), logsWithBody("[error] boom")); err != nil {
		t.Fatal("ConsumeLogs returned an error:", err)
	}

	if err := sink.ConsumeLogs(t.Context(), logsWithBody("all good")); err != nil {
		t.Fatal("ConsumeLogs returned an error:", err)
	}

	got := countsFor(man, "app_errors_count")
	if got["app"] != 1 {
		t.Errorf(`Expected 1 match under item "app", got %v`, got)
	}
}

// Test the {include: name} expansion against log.metrics_rules end to end.
func TestWantSourceReceiverMetricsRulesInclude(t *testing.T) {
	t.Parallel()

	rules := map[string][]config.LogMetricEntry{
		"web_errors": {
			{"metric": "web_5xx", "conditions": []any{`IsMatch(body, "5xx")`}},
		},
	}

	man := newTestManager(t, config.OpenTelemetry{
		Receivers: map[string]config.LogReceiver{
			"web": {
				"include": []string{"/var/log/web.log"},
				"metrics": []any{map[string]any{"include": "web_errors"}},
			},
		},
	}, rules)

	sink, ok := man.WantSource(t.Context(), logsource.ResolvedSource{Kind: logsource.SourceReceiver, Name: "web", ReceiverName: "web"})
	if !ok || sink == nil {
		t.Fatal("Expected the receiver to be wanted with a non-nil sink")
	}

	if err := sink.ConsumeLogs(t.Context(), logsWithBody("got a 5xx")); err != nil {
		t.Fatal("ConsumeLogs returned an error:", err)
	}

	got := countsFor(man, "web_5xx")
	if got["web"] != 1 {
		t.Errorf(`Expected 1 match under item "web", got %v`, got)
	}
}

// Test that a metric's own "item" field always wins over the receiver's default item, including item explicitly set to "".
func TestWantSourceReceiverItemOverride(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		receiverName string
		metric       string
		item         string
		matchingBody string
	}{
		{
			name:         "explicit item",
			receiverName: "app",
			metric:       "app_warn_count",
			item:         "app-warnings",
			matchingBody: "a warn line",
		},
		{
			name:         "migrated empty item",
			receiverName: "legacy_input_0",
			metric:       "legacy_metric",
			item:         "",
			matchingBody: "x",
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			man := newTestManager(t, config.OpenTelemetry{
				Receivers: map[string]config.LogReceiver{
					tt.receiverName: {
						"include": []string{"/var/log/app.log"},
						"metrics": []any{
							map[string]any{"metric": tt.metric, "conditions": []any{fmt.Sprintf("IsMatch(body, %q)", tt.matchingBody)}, "item": tt.item},
						},
					},
				},
			}, nil)

			sink, ok := man.WantSource(t.Context(), logsource.ResolvedSource{Kind: logsource.SourceReceiver, Name: tt.receiverName, ReceiverName: tt.receiverName})
			if !ok || sink == nil {
				t.Fatal("Expected the receiver to be wanted with a non-nil sink")
			}

			if err := sink.ConsumeLogs(t.Context(), logsWithBody(tt.matchingBody)); err != nil {
				t.Fatal("ConsumeLogs returned an error:", err)
			}

			got := countsFor(man, tt.metric)
			if got[tt.item] != 1 {
				t.Errorf("Expected 1 match under item %q, got %v", tt.item, got)
			}

			if _, gotDefault := got[tt.receiverName]; gotDefault {
				t.Errorf("Expected no series under the receiver's own default item %q, got %v", tt.receiverName, got)
			}
		})
	}
}

// Test that two receivers defining the same metric name inline get distinct, independently-counted series.
func TestWantSourceTwoReceiversGetDistinctItems(t *testing.T) {
	t.Parallel()

	metric := func() config.LogReceiver {
		return config.LogReceiver{
			"include": []string{"/var/log/x.log"},
			"metrics": []any{
				map[string]any{"metric": "shared_name_metric", "conditions": []any{`IsMatch(body, "x")`}},
			},
		}
	}

	man := newTestManager(t, config.OpenTelemetry{
		Receivers: map[string]config.LogReceiver{
			"recv_a": metric(),
			"recv_b": metric(),
		},
	}, nil)

	sinkA, okA := man.WantSource(t.Context(), logsource.ResolvedSource{Kind: logsource.SourceReceiver, Name: "recv_a", ReceiverName: "recv_a"})
	sinkB, okB := man.WantSource(t.Context(), logsource.ResolvedSource{Kind: logsource.SourceReceiver, Name: "recv_b", ReceiverName: "recv_b"})

	if !okA || !okB {
		t.Fatal("Expected both receivers to be wanted")
	}

	if err := sinkA.ConsumeLogs(t.Context(), logsWithBody("x")); err != nil {
		t.Fatal(err)
	}

	if err := sinkB.ConsumeLogs(t.Context(), logsWithBody("x")); err != nil {
		t.Fatal(err)
	}

	if err := sinkB.ConsumeLogs(t.Context(), logsWithBody("x")); err != nil {
		t.Fatal(err)
	}

	got := countsFor(man, "shared_name_metric")
	if got["recv_a"] != 1 || got["recv_b"] != 2 {
		t.Errorf("Expected independent per-receiver counts recv_a=1 recv_b=2, got %v", got)
	}
}

// Test that a container with no glouton.log_metrics label is never wanted.
func TestWantSourceContainerLabelNoRule(t *testing.T) {
	t.Parallel()

	man := newTestManager(t, config.OpenTelemetry{}, nil)

	ctr := facts.FakeContainer{FakeContainerName: "my-app"}

	if _, ok := man.WantSource(t.Context(), logsource.ResolvedSource{
		Kind: logsource.SourceContainerLabel, Name: "my-app", Container: ctr,
	}); ok {
		t.Error("Expected a container with no glouton.log_metrics label to not be wanted")
	}
}

// Test that a glouton.log_metrics label naming an unknown rule set is rejected, not silently ignored.
func TestWantSourceContainerLabelUnknownRule(t *testing.T) {
	t.Parallel()

	man := newTestManager(t, config.OpenTelemetry{}, map[string][]config.LogMetricEntry{
		"known_rule": {{"metric": "m", "conditions": []any{`IsMatch(body, "x")`}}},
	})

	ctr := facts.FakeContainer{FakeContainerName: "my-app"}

	if _, ok := man.WantSource(t.Context(), logsource.ResolvedSource{
		Kind: logsource.SourceContainerLabel, Name: "my-app", Container: ctr, LogMetricsRule: "does_not_exist",
	}); ok {
		t.Error("Expected an unknown log.metrics_rules name to not be wanted")
	}
}

// Test a container opted in via glouton.log_metrics: its item must be the container's own name, not the rule set's name.
func TestWantSourceContainerLabelValid(t *testing.T) {
	t.Parallel()

	man := newTestManager(t, config.OpenTelemetry{}, map[string][]config.LogMetricEntry{
		"web_errors": {{"metric": "web_errors_count", "conditions": []any{`IsMatch(body, "error")`}}},
	})

	ctr := facts.FakeContainer{FakeContainerName: "web-1"}

	sink, ok := man.WantSource(t.Context(), logsource.ResolvedSource{
		Kind: logsource.SourceContainerLabel, Name: "web-1", Container: ctr, LogMetricsRule: "web_errors",
	})
	if !ok || sink == nil {
		t.Fatal("Expected the container to be wanted with a non-nil sink")
	}

	if err := sink.ConsumeLogs(t.Context(), logsWithBody("an error happened")); err != nil {
		t.Fatal(err)
	}

	got := countsFor(man, "web_errors_count")
	if got["web-1"] != 1 {
		t.Errorf(`Expected 1 match under item "web-1" (the container's own name), got %v`, got)
	}
}

// Test that two containers sharing the same glouton.log_metrics value don't merge into one series.
func TestWantSourceContainerLabelTwoContainersSameRuleGetDistinctItems(t *testing.T) {
	t.Parallel()

	man := newTestManager(t, config.OpenTelemetry{}, map[string][]config.LogMetricEntry{
		"shared_rule": {{"metric": "shared_metric", "conditions": []any{`IsMatch(body, "x")`}}},
	})

	ctrA := facts.FakeContainer{FakeID: "id-a", FakeContainerName: "app-a"}
	ctrB := facts.FakeContainer{FakeID: "id-b", FakeContainerName: "app-b"}

	sinkA, okA := man.WantSource(t.Context(), logsource.ResolvedSource{Kind: logsource.SourceContainerLabel, Name: "app-a", Container: ctrA, LogMetricsRule: "shared_rule"})
	_, okB := man.WantSource(t.Context(), logsource.ResolvedSource{Kind: logsource.SourceContainerLabel, Name: "app-b", Container: ctrB, LogMetricsRule: "shared_rule"})

	if !okA || !okB {
		t.Fatal("Expected both containers to be wanted")
	}

	if err := sinkA.ConsumeLogs(t.Context(), logsWithBody("x")); err != nil {
		t.Fatal(err)
	}

	got := countsFor(man, "shared_metric")
	if got["app-a"] != 1 {
		t.Errorf(`Expected 1 match under "app-a", got %v`, got)
	}

	// app-b's counter exists but was never incremented.
	if got["app-b"] != 0 {
		t.Errorf("Expected app-b to have no match, got %v", got)
	}
}

// Test that a metric's name is declared as soon as its source is resolved, before any log line is seen.
func TestManagerMetricNamesDeclaredBeforeAnyData(t *testing.T) {
	t.Parallel()

	man := newTestManager(t, config.OpenTelemetry{
		Receivers: map[string]config.LogReceiver{
			"app": {
				"include": []string{"/var/log/app.log"},
				"metrics": []any{
					map[string]any{"metric": "never_matched_count", "conditions": []any{`IsMatch(body, "never")`}},
				},
			},
		},
	}, nil)

	if names := man.MetricNames(); len(names) != 0 {
		t.Fatalf("Expected no declared metric before any source is resolved, got %v", names)
	}

	if _, ok := man.WantSource(t.Context(), logsource.ResolvedSource{Kind: logsource.SourceReceiver, Name: "app", ReceiverName: "app"}); !ok {
		t.Fatal("Expected the receiver to be wanted")
	}

	names := man.MetricNames()
	if !slices.Contains(names, "never_matched_count") {
		t.Fatalf("Expected never_matched_count to be declared immediately, got %v", names)
	}
}

// Test that ReleaseSource removes only the released container's connectors, leaving an unrelated receiver's untouched.
func TestReleaseSourceStopsAndForgetsContainerConnectors(t *testing.T) {
	t.Parallel()

	man := newTestManager(t, config.OpenTelemetry{
		Receivers: map[string]config.LogReceiver{
			"app": {
				"include": []string{"/var/log/app.log"},
				"metrics": []any{
					map[string]any{"metric": "app_count", "conditions": []any{`IsMatch(body, "x")`}},
				},
			},
		},
	}, map[string][]config.LogMetricEntry{
		"web_errors": {{"metric": "web_errors_count", "conditions": []any{`IsMatch(body, "error")`}}},
	})

	ctr := facts.FakeContainer{FakeID: "id-1", FakeContainerName: "web-1"}

	if _, ok := man.WantSource(t.Context(), logsource.ResolvedSource{Kind: logsource.SourceReceiver, Name: "app", ReceiverName: "app"}); !ok {
		t.Fatal("Expected the receiver to be wanted")
	}

	if _, ok := man.WantSource(t.Context(), logsource.ResolvedSource{
		Kind: logsource.SourceContainerLabel, Name: "web-1", Container: ctr, LogMetricsRule: "web_errors",
	}); !ok {
		t.Fatal("Expected the container to be wanted")
	}

	man.l.Lock()
	builtBefore := len(man.built)
	man.l.Unlock()

	if builtBefore != 2 {
		t.Fatalf("expected 2 built sources before release, got %d", builtBefore)
	}

	man.ReleaseSource(t.Context(), ctr)

	man.l.Lock()
	defer man.l.Unlock()

	if len(man.built) != 1 {
		t.Fatalf("expected exactly 1 built source to remain after releasing the container, got %d: %+v", len(man.built), man.built)
	}

	if man.built[0].diag.Kind != "receiver" {
		t.Fatalf("expected the surviving built source to be the receiver's, got %+v", man.built[0].diag)
	}
}

// Test that ReleaseSource purges the released container's counters from the registry, so a container churning
// through names/hashes (Kubernetes pod recreation, ...) doesn't keep emitting a permanent 0 for its old item forever.
func TestReleaseSourcePurgesRegistryCounters(t *testing.T) {
	t.Parallel()

	man := newTestManager(t, config.OpenTelemetry{}, map[string][]config.LogMetricEntry{
		"web_errors": {{"metric": "web_errors_count", "conditions": []any{`IsMatch(body, "error")`}}},
	})

	ctr := facts.FakeContainer{FakeID: "id-1", FakeContainerName: "web-1"}

	sink, ok := man.WantSource(t.Context(), logsource.ResolvedSource{
		Kind: logsource.SourceContainerLabel, Name: "web-1", Container: ctr, LogMetricsRule: "web_errors",
	})
	if !ok {
		t.Fatal("Expected the container to be wanted")
	}

	if err := sink.ConsumeLogs(t.Context(), logsWithBody("an error happened")); err != nil {
		t.Fatal(err)
	}

	if got := countsFor(man, "web_errors_count"); got["web-1"] != 1 {
		t.Fatalf(`Expected 1 match under "web-1" before release, got %v`, got)
	}

	man.ReleaseSource(t.Context(), ctr)

	if got := countsFor(man, "web_errors_count"); len(got) != 0 {
		t.Errorf("Expected the registry to have purged item %q after release, got %v", "web-1", got)
	}

	// The metric name itself must stay declared: another container may still report it.
	if names := man.MetricNames(); !slices.Contains(names, "web_errors_count") {
		t.Errorf("Expected web_errors_count to remain a declared metric name, got %v", names)
	}
}

// Test that a container recreated (new ID, same item) shortly after the old one is released reuses the
// existing counter instead of resetting to 0: guards the ReceiverManager-level race where a scan can
// release an old container's item and want a same-named replacement's item in close succession.
func TestReleaseSourceThenWantSourceReusesCounterWithinGracePeriod(t *testing.T) {
	t.Parallel()

	man := newTestManager(t, config.OpenTelemetry{}, map[string][]config.LogMetricEntry{
		"web_errors": {{"metric": "web_errors_count", "conditions": []any{`IsMatch(body, "error")`}}},
	})
	man.reg.gracePeriod = time.Hour // long enough that the timer never fires during this test

	oldCtr := facts.FakeContainer{FakeID: "id-old", FakeContainerName: "web-1"}

	oldSink, ok := man.WantSource(t.Context(), logsource.ResolvedSource{
		Kind: logsource.SourceContainerLabel, Name: "web-1", Container: oldCtr, LogMetricsRule: "web_errors",
	})
	if !ok {
		t.Fatal("Expected the old container to be wanted")
	}

	if err := oldSink.ConsumeLogs(t.Context(), logsWithBody("an error happened")); err != nil {
		t.Fatal(err)
	}

	if got := countsFor(man, "web_errors_count"); got["web-1"] != 1 {
		t.Fatalf(`Expected 1 match under "web-1" before recreation, got %v`, got)
	}

	// The container is recreated: a new ID, but the same runtime name (so the same registry item).
	man.ReleaseSource(t.Context(), oldCtr)

	newCtr := facts.FakeContainer{FakeID: "id-new", FakeContainerName: "web-1"}

	newSink, ok := man.WantSource(t.Context(), logsource.ResolvedSource{
		Kind: logsource.SourceContainerLabel, Name: "web-1", Container: newCtr, LogMetricsRule: "web_errors",
	})
	if !ok {
		t.Fatal("Expected the new container to be wanted")
	}

	if got := countsFor(man, "web_errors_count"); got["web-1"] != 1 {
		t.Fatalf(`Expected the prior match under "web-1" to survive the recreation (grace period), got %v`, got)
	}

	if err := newSink.ConsumeLogs(t.Context(), logsWithBody("another error")); err != nil {
		t.Fatal(err)
	}

	if got := countsFor(man, "web_errors_count"); got["web-1"] != 2 {
		t.Errorf(`Expected a continuous count (1 before + 1 after recreation = 2) under "web-1", got %v`, got)
	}
}

// Test that ReleaseSource doesn't purge an item still used by another, still-active built source
// (e.g. two containers sharing an explicit "item" override).
func TestReleaseSourceKeepsSharedItemInUse(t *testing.T) {
	t.Parallel()

	man := newTestManager(t, config.OpenTelemetry{}, map[string][]config.LogMetricEntry{
		"shared_rule": {{"metric": "shared_metric", "item": "shared-item", "conditions": []any{`IsMatch(body, "x")`}}},
	})

	ctrA := facts.FakeContainer{FakeID: "id-a", FakeContainerName: "app-a"}
	ctrB := facts.FakeContainer{FakeID: "id-b", FakeContainerName: "app-b"}

	if _, ok := man.WantSource(t.Context(), logsource.ResolvedSource{Kind: logsource.SourceContainerLabel, Name: "app-a", Container: ctrA, LogMetricsRule: "shared_rule"}); !ok {
		t.Fatal("Expected container app-a to be wanted")
	}

	sinkB, ok := man.WantSource(t.Context(), logsource.ResolvedSource{Kind: logsource.SourceContainerLabel, Name: "app-b", Container: ctrB, LogMetricsRule: "shared_rule"})
	if !ok {
		t.Fatal("Expected container app-b to be wanted")
	}

	man.ReleaseSource(t.Context(), ctrA)

	if err := sinkB.ConsumeLogs(t.Context(), logsWithBody("x")); err != nil {
		t.Fatal(err)
	}

	got := countsFor(man, "shared_metric")
	if got["shared-item"] != 1 {
		t.Errorf(`Expected "shared-item" counter to survive app-a's release since app-b still uses it, got %v`, got)
	}
}

// Test that Shutdown tears down every countconnector the Manager built, without error.
func TestManagerShutdownStopsConnectors(t *testing.T) {
	t.Parallel()

	man := newTestManager(t, config.OpenTelemetry{
		Receivers: map[string]config.LogReceiver{
			"app": {
				"include": []string{"/var/log/app.log"},
				"metrics": []any{
					map[string]any{"metric": "app_errors_count", "conditions": []any{`IsMatch(body, "error")`}},
				},
			},
		},
	}, nil)

	if _, ok := man.WantSource(t.Context(), logsource.ResolvedSource{Kind: logsource.SourceReceiver, Name: "app", ReceiverName: "app"}); !ok {
		t.Fatal("Expected the receiver to be wanted")
	}

	if err := man.Shutdown(t.Context()); err != nil {
		t.Fatalf("Shutdown returned an error: %v", err)
	}
}

// TestManagerRunShutsDownConnectorsOnCtxDone guards against a regression where Run only blocked on
// ctx.Done() and returned, never calling Shutdown -- leaving every countconnector chain built by
// WantSource running (and its underlying resources unshut-down) after the agent stops using this Manager.
func TestManagerRunShutsDownConnectorsOnCtxDone(t *testing.T) {
	t.Parallel()

	man := newTestManager(t, config.OpenTelemetry{
		Receivers: map[string]config.LogReceiver{
			"app": {
				"include": []string{"/var/log/app.log"},
				"metrics": []any{
					map[string]any{"metric": "app_errors_count", "conditions": []any{`IsMatch(body, "error")`}},
				},
			},
		},
	}, nil)

	if _, ok := man.WantSource(t.Context(), logsource.ResolvedSource{Kind: logsource.SourceReceiver, Name: "app", ReceiverName: "app"}); !ok {
		t.Fatal("Expected the receiver to be wanted")
	}

	man.l.Lock()
	builtBefore := len(man.built)
	man.l.Unlock()

	if builtBefore == 0 {
		t.Fatal("Expected at least 1 built source before Run returns")
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := man.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Expected Run to return context.Canceled, got %v", err)
	}

	man.l.Lock()
	builtAfter := len(man.built)
	man.l.Unlock()

	if builtAfter != 0 {
		t.Errorf("Expected Run to have called Shutdown (built sources forgotten), got %d still built", builtAfter)
	}
}

// Smoke test that DiagnosticArchive reports the resolved source and its metric names.
func TestManagerDiagnosticArchiveListsResolvedSources(t *testing.T) {
	t.Parallel()

	man := newTestManager(t, config.OpenTelemetry{
		Receivers: map[string]config.LogReceiver{
			"app": {
				"include": []string{"/var/log/app.log"},
				"metrics": []any{
					map[string]any{"metric": "app_errors_count", "conditions": []any{`IsMatch(body, "error")`}},
				},
			},
		},
	}, nil)

	if _, ok := man.WantSource(t.Context(), logsource.ResolvedSource{Kind: logsource.SourceReceiver, Name: "app", ReceiverName: "app"}); !ok {
		t.Fatal("Expected the receiver to be wanted")
	}

	man.l.Lock()
	sources := make([]sourceDiagnostic, 0, len(man.built))

	for _, b := range man.built {
		sources = append(sources, b.diag)
	}
	man.l.Unlock()

	if len(sources) != 1 || sources[0].Name != "app" || sources[0].Kind != "receiver" {
		t.Fatalf("Expected exactly one resolved receiver source named %q, got %+v", "app", sources)
	}

	if !slices.Contains(sources[0].MetricNames, "app_errors_count") {
		t.Errorf("Expected the resolved source to list its metric name, got %+v", sources[0])
	}
}

// countersFor reads back every counter (base and attribute-derived) declared for metric, end to end
// through the real countconnector.
func countersFor(man *Manager, metric string) map[counterKey]*counter {
	man.reg.l.Lock()
	defer man.reg.l.Unlock()

	found := make(map[counterKey]*counter)

	for key, c := range man.reg.counters {
		if key.metric == metric {
			found[key] = c
		}
	}

	return found
}

// Test that a metrics: entry's "attributes:" list produces one distinct, correctly labeled series per
// attribute-value combination actually observed, end to end through the real countconnector -- and that a
// same-named static "labels:" entry always wins over the dynamic attribute (item > labels > attributes).
func TestWantSourceReceiverAttributesProduceDistinctLabeledSeries(t *testing.T) {
	t.Parallel()

	man := newTestManager(t, config.OpenTelemetry{
		Receivers: map[string]config.LogReceiver{
			"app": {
				"include": []string{"/var/log/app.log"},
				"metrics": []any{
					map[string]any{
						"metric":     "http_requests_count",
						"conditions": []any{`IsMatch(body, "request")`},
						"labels":     map[string]any{"status": "shadowed-by-labels"},
						"attributes": []any{
							map[string]any{"key": "status"}, // shadowed: "labels" already claims "status"
							map[string]any{"key": "method"}, // unclaimed: becomes a real dynamic label
						},
					},
				},
			},
		},
	}, nil)

	sink, ok := man.WantSource(t.Context(), logsource.ResolvedSource{Kind: logsource.SourceReceiver, Name: "app", ReceiverName: "app"})
	if !ok || sink == nil {
		t.Fatal("Expected the receiver to be wanted with a non-nil sink")
	}

	lines := []map[string]string{
		{"status": "200", "method": "GET"},
		{"status": "500", "method": "POST"},
		{"status": "200", "method": "GET"}, // repeats the first combo: must accumulate, not duplicate
	}

	for _, attrs := range lines {
		if err := sink.ConsumeLogs(t.Context(), logsWithBodyAndAttrs("a request", attrs)); err != nil {
			t.Fatal("ConsumeLogs returned an error:", err)
		}
	}

	found := countersFor(man, "http_requests_count")
	if len(found) != 3 {
		t.Fatalf("Expected 3 counters (base + method=GET + method=POST), got %d: %+v", len(found), found)
	}

	var base *counter

	byMethod := make(map[string]*counter)

	for key, c := range found {
		if key.attrs == "" {
			base = c

			continue
		}

		byMethod[c.lbls.Get("method")] = c
	}

	if base == nil {
		t.Fatal("Expected a base (attrs-less) counter declared for the metric")
	}

	if got := int64(base.counter.Total()); got != 0 {
		t.Errorf("Expected the base counter to stay at 0 (every match carries attributes), got %d", got)
	}

	for _, c := range []*counter{base, byMethod["GET"], byMethod["POST"]} {
		if c == nil {
			t.Fatalf("Missing expected counter, got byMethod=%+v", byMethod)
		}

		if got := c.lbls.Get("status"); got != "shadowed-by-labels" {
			t.Errorf("Expected labels: to always win over the dynamic \"status\" attribute, got status=%q", got)
		}
	}

	if got := int64(byMethod["GET"].counter.Total()); got != 2 {
		t.Errorf("Expected method=GET to total 2 (two matching lines), got %d", got)
	}

	if got := int64(byMethod["POST"].counter.Total()); got != 1 {
		t.Errorf("Expected method=POST to total 1, got %d", got)
	}

	if got := byMethod["GET"].lbls.Get(types.LabelName); got != "http_requests_count" {
		t.Errorf("Expected __name__=http_requests_count on the attribute-derived series, got %q", got)
	}
}
