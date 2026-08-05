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
	"sync"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/crashreport"
	"github.com/bleemeo/glouton/facts"
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

// Package logmetrics counts log lines matching a condition/regex and reports the rate as a metric, via the vendored countconnector.

// Manager builds a countconnector pipeline for every resolved source that has metrics configured, and aggregates their output into one registry of (metric, item) rate counters.
// The zero value isn't usable; construct with New.
type Manager struct {
	cfg          config.OpenTelemetry
	metricsRules map[string][]config.LogMetricEntry
	telemetry    component.TelemetrySettings

	reg *metricsRegistry

	l     sync.Mutex
	built []builtSource
}

// builtSource is one WantSource-accepted source's countconnector chain, kept so ReleaseSource can shut it down individually.
type builtSource struct {
	diag  sourceDiagnostic
	conns []otelconnector.Logs
	// items are every registry item this source resolved counters for (usually just diag.Name, but a
	// metrics: entry's own "item" override can add more); ReleaseSource uses it to purge the registry.
	items []string
}

// New builds a Manager for cfg's receivers and metricsRules (log.metrics_rules).
func New(cfg config.OpenTelemetry, metricsRules map[string][]config.LogMetricEntry) *Manager {
	return &Manager{
		cfg:          cfg,
		metricsRules: metricsRules,
		telemetry:    logsource.NewTelemetrySettings(),
		reg:          newMetricsRegistry(releaseGracePeriod),
	}
}

// receiverMetricsField narrow-decodes just a raw LogReceiver's "metrics" key.
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

// WantSource implements logsource.SinkProvider: it decides whether src has any metrics to count, and builds the countconnector chain feeding it.
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

// wantReceiverSource resolves a SourceReceiver's own "metrics:" field, defaulting its item to the receiver's config name (src.Name).
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

// wantContainerLabelSource resolves a SourceContainerLabel's glouton.log_metrics label against log.metrics_rules, defaulting its item to the container's runtime name (src.Name).
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

// buildSink builds the countconnector chain for resolved, grouped by item, and remembers it for Shutdown/DiagnosticArchive.
func (man *Manager) buildSink(ctx context.Context, resolved []resolvedMetric, defaultItem string, diag sourceDiagnostic) (consumer.Logs, bool) {
	conns, items, err := buildGroupedConnectors(ctx, man.telemetry, resolved, defaultItem, man.reg, diag.Name)
	if err != nil {
		logger.V(1).Printf("logmetrics: source %q: %v", diag.Name, err)

		return nil, false
	}

	diag.MetricNames = metricNamesOf(resolved)

	man.l.Lock()
	man.built = append(man.built, builtSource{diag: diag, conns: conns, items: items})
	man.l.Unlock()

	return logsConsumerFor(conns), true
}

// ReleaseSource implements logsource.SinkProvider: it shuts down and forgets the countconnector chain built for container, if any,
// and purges the registry counters it fed so a recreated container's old name/hash doesn't keep emitting a permanent 0.
// Called when a container disappears, since container recreation assigns a new ID and would otherwise leak the old chain.
func (man *Manager) ReleaseSource(ctx context.Context, container facts.Container) {
	if container == nil {
		return
	}

	containerID := container.ID()

	man.l.Lock()

	kept := make([]builtSource, 0, len(man.built))

	var (
		toStop        []otelconnector.Logs
		releasedItems []string
	)

	for _, b := range man.built {
		if b.diag.Kind == "container_label" && b.diag.ContainerID == containerID {
			toStop = append(toStop, b.conns...)
			releasedItems = append(releasedItems, b.items...)

			continue
		}

		kept = append(kept, b)
	}

	man.built = kept

	// Some other still-kept source may resolve the same item (e.g. a shared "item" override):
	// don't purge those out from under it.
	stillUsed := make(map[string]bool)

	for _, b := range kept {
		for _, item := range b.items {
			stillUsed[item] = true
		}
	}

	man.l.Unlock()

	if len(toStop) == 0 {
		return
	}

	for _, item := range releasedItems {
		if !stillUsed[item] {
			man.reg.release(item)
		}
	}

	logger.V(2).Printf("logmetrics: releasing container %s (%d connector(s) stopped)", containerID, len(toStop))

	if err := shutdownConns(ctx, toStop); err != nil {
		logger.V(1).Printf("logmetrics: releasing container %s: %v", containerID, err)
	}
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

// Run keeps Manager alive for ctx's lifetime, then shuts down every countconnector it built.
func (man *Manager) Run(ctx context.Context) error {
	defer crashreport.ProcessPanic()

	<-ctx.Done()

	if err := man.Shutdown(context.Background()); err != nil {
		logger.V(1).Printf("logmetrics: shutdown: %v", err)
	}

	return ctx.Err()
}

// Shutdown stops every countconnector this Manager ever built.
func (man *Manager) Shutdown(ctx context.Context) error {
	man.l.Lock()
	built := man.built
	man.built = nil
	man.l.Unlock()

	var conns []otelconnector.Logs

	for _, b := range built {
		conns = append(conns, b.conns...)
	}

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

// sourceDiagnostic describes one resolved, wanted source for the diagnostic archive.
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
	sources := make([]sourceDiagnostic, 0, len(man.built))

	for _, b := range man.built {
		sources = append(sources, b.diag)
	}
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
