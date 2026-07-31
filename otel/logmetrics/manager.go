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
	"slices"
	"sync"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/crashreport"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/otel/logsource"
	"github.com/bleemeo/glouton/prometheus/registry"
	"github.com/bleemeo/glouton/types"

	"github.com/go-viper/mapstructure/v2"
	"github.com/prometheus/prometheus/storage"
	"go.opentelemetry.io/collector/component"
	otelconnector "go.opentelemetry.io/collector/connector"
	"go.opentelemetry.io/collector/consumer"
)

// Package logmetrics counts log lines matching a condition/regex and reports
// the rate as a metric, via the real vendored countconnector. It's a thin
// logsource.SinkProvider: otel/logsource.ReceiverManager owns every physical
// file/container tail and offers each resolved source to Manager.WantSource,
// which decides whether it has anything to count from it and, if so, wires a
// countconnector chain feeding the shared metricsRegistry. Independent from
// otel/logprocessing: never ships log content.

// Manager builds a countconnector pipeline for every ReceiverManager-resolved
// source that has metrics configured, and aggregates their output into one
// registry of (metric, item) rate counters. The zero value isn't usable;
// construct with New, then register it with a *logsource.ReceiverManager via
// RegisterSinkProvider before that manager's first RescanReceivers/
// UpdateContainers call.
type Manager struct {
	cfg          config.OpenTelemetry
	metricsRules map[string][]config.LogMetricEntry
	telemetry    component.TelemetrySettings

	reg *metricsRegistry

	l       sync.Mutex
	conns   []otelconnector.Logs
	sources []sourceDiagnostic
}

// New builds a Manager for cfg's receivers and metricsRules (log.metrics_rules).
// It doesn't resolve anything yet: call RegisterSinkProvider on the shared
// *logsource.ReceiverManager to start feeding it sources.
func New(cfg config.OpenTelemetry, metricsRules map[string][]config.LogMetricEntry) *Manager {
	return &Manager{
		cfg:          cfg,
		metricsRules: metricsRules,
		telemetry:    logsource.NewTelemetrySettings(),
		reg:          newMetricsRegistry(),
	}
}

// receiverMetricsField narrow-decodes just a raw LogReceiver's "metrics" key,
// following the same narrow-decode pattern as config.LogReceiverSelectors and
// otel/logsource's decodeReceiverFields.
type receiverMetricsField struct {
	Metrics []any
}

func decodeReceiverMetrics(raw config.LogReceiver) ([]any, error) {
	var fields receiverMetricsField

	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{Result: &fields})
	if err != nil {
		return nil, err
	}

	if err := decoder.Decode(raw); err != nil {
		return nil, err
	}

	return fields.Metrics, nil
}

// WantSource implements logsource.SinkProvider: it decides whether src has
// any metrics to count, and if so builds (and remembers, for Shutdown/
// DiagnosticArchive) the countconnector chain feeding it.
func (man *Manager) WantSource(ctx context.Context, src logsource.ResolvedSource) (consumer.Logs, bool) {
	switch src.Kind {
	case logsource.SourceReceiver:
		return man.wantReceiverSource(ctx, src)
	case logsource.SourceContainerLabel:
		return man.wantContainerLabelSource(ctx, src)
	default:
		return nil, false
	}
}

// wantReceiverSource resolves a SourceReceiver's own "metrics:" field. Its
// item always defaults to the receiver's own config name (src.Name), even if
// its selectors matched several containers at once -- merging them under one
// item is then an explicit config choice, not something decided here.
func (man *Manager) wantReceiverSource(ctx context.Context, src logsource.ResolvedSource) (consumer.Logs, bool) {
	raw, found := man.cfg.Receivers[src.ReceiverName]
	if !found {
		return nil, false
	}

	rawMetrics, err := decodeReceiverMetrics(raw)
	if err != nil {
		logger.V(1).Printf("logmetrics: receiver %q: failed to decode metrics: %v", src.ReceiverName, err)

		return nil, false
	}

	if len(rawMetrics) == 0 {
		return nil, false
	}

	resolved := resolveReceiverMetrics(rawMetrics, man.metricsRules)
	if len(resolved) == 0 {
		return nil, false
	}

	diag := sourceDiagnostic{Name: src.Name, Kind: "receiver", ReceiverName: src.ReceiverName}

	return man.buildSink(ctx, resolved, src.Name, diag)
}

// wantContainerLabelSource resolves a SourceContainerLabel's
// glouton.log_metrics label, if set, directly against log.metrics_rules (no
// further {include: ...} expansion: the named rule set IS the resolved
// list). Its item always defaults to the container's own runtime name
// (src.Name), keeping two containers sharing the same label value from
// merging into one series.
func (man *Manager) wantContainerLabelSource(ctx context.Context, src logsource.ResolvedSource) (consumer.Logs, bool) {
	if src.LogMetricsRule == "" {
		return nil, false
	}

	rules, found := man.metricsRules[src.LogMetricsRule]
	if !found {
		logger.Printf("logmetrics: container %s: unknown log.metrics_rules %q (glouton.log_metrics label)", src.Name, src.LogMetricsRule)

		return nil, false
	}

	resolved := make([]resolvedMetric, 0, len(rules))

	for _, raw := range rules {
		if rm, ok := resolveInlineMetric(raw); ok {
			resolved = append(resolved, rm)
		}
	}

	if len(resolved) == 0 {
		return nil, false
	}

	diag := sourceDiagnostic{Name: src.Name, Kind: "container_label", LogMetricsRule: src.LogMetricsRule}

	if src.Container != nil {
		diag.ContainerID = src.Container.ID()
	}

	return man.buildSink(ctx, resolved, src.Name, diag)
}

// buildSink builds the countconnector chain for resolved, grouped by item
// (defaultItem unless a metric overrides its own), and remembers it for
// Shutdown/DiagnosticArchive. Every metric name is declared (MetricNames()
// reflects it immediately, before any matching log line arrives) as a side
// effect of buildGroupedConnectors' own reg.resolve call, not separately here.
func (man *Manager) buildSink(ctx context.Context, resolved []resolvedMetric, defaultItem string, diag sourceDiagnostic) (consumer.Logs, bool) {
	conns, err := buildGroupedConnectors(ctx, man.telemetry, resolved, defaultItem, man.reg, diag.Name)
	if err != nil {
		logger.V(1).Printf("logmetrics: source %q: %v", diag.Name, err)

		return nil, false
	}

	diag.MetricNames = metricNamesOf(resolved)

	man.l.Lock()
	man.conns = append(man.conns, conns...)
	man.sources = append(man.sources, diag)
	man.l.Unlock()

	return logsConsumerFor(conns), true
}

func metricNamesOf(entries []resolvedMetric) []string {
	seen := make(map[string]bool, len(entries))
	names := make([]string, 0, len(entries))

	for _, entry := range entries {
		if !seen[entry.Metric] {
			seen[entry.Metric] = true

			names = append(names, entry.Metric)
		}
	}

	return names
}

// Run keeps Manager alive for ctx's lifetime. There is no periodic
// source-rescanning or container-polling left to do here -- ReceiverManager
// owns that -- and RingCounter (registry.go) discards outdated buckets lazily
// on its own Add/Total calls, so no active ticking is needed either; metrics
// emission itself happens on demand, via EmitMetrics.
func (man *Manager) Run(ctx context.Context) error {
	defer crashreport.ProcessPanic()

	<-ctx.Done()

	return ctx.Err()
}

// Shutdown stops every countconnector this Manager ever built.
func (man *Manager) Shutdown(ctx context.Context) error {
	man.l.Lock()
	conns := man.conns
	man.conns = nil
	man.l.Unlock()

	return shutdownConns(ctx, conns)
}

// EmitMetrics implements registry.AppenderFunc, reporting the current rate of
// every configured log-to-metric counter.
func (man *Manager) EmitMetrics(_ context.Context, _ registry.GatherState, app storage.Appender) error {
	return man.reg.emit(app)
}

// MetricNames returns the name of every declared log-to-metric counter.
func (man *Manager) MetricNames() []string {
	return man.reg.metricNames()
}

// sourceDiagnostic describes one resolved, wanted source for the diagnostic
// archive.
type sourceDiagnostic struct {
	Name           string
	Kind           string // "receiver" or "container_label"
	ReceiverName   string `json:",omitempty"`
	ContainerID    string `json:",omitempty"`
	LogMetricsRule string `json:",omitempty"`
	MetricNames    []string
}

func (man *Manager) DiagnosticArchive(_ context.Context, archive types.ArchiveWriter) error {
	file, err := archive.Create("log-to-metrics.json")
	if err != nil {
		return err
	}

	man.l.Lock()
	sources := slices.Clone(man.sources)
	man.l.Unlock()

	info := struct {
		MetricNames []string
		Sources     []sourceDiagnostic
	}{
		MetricNames: man.reg.metricNames(),
		Sources:     sources,
	}

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")

	return enc.Encode(info)
}
