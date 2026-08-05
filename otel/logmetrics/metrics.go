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

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/otel/logsource"

	"github.com/go-viper/mapstructure/v2"
	"github.com/google/uuid"
	"github.com/open-telemetry/opentelemetry-collector-contrib/connector/countconnector"
	"go.opentelemetry.io/collector/component"
	otelconnector "go.opentelemetry.io/collector/connector"
	"go.opentelemetry.io/collector/consumer"
)

var (
	errNoValidCounter     = errors.New("no valid counter for source")
	errNoApplicableMetric = errors.New("no metrics entry applies to this source")
)

// resolvedMetric is one metrics: entry after expanding any {include: name} against log.metrics_rules.
// Raw is kept as-is so metricInfo can decode it lazily, once grouped by item.
type resolvedMetric struct {
	Metric string
	Raw    config.LogMetricEntry
	// Item is nil when the entry never set its own "item" (derive one from the source), non-nil when it did.
	Item *string
}

// resolveReceiverMetrics expands rawMetrics (a receiver's "metrics:" list of inline entries or {include: name}) against metricsRules, warning and skipping invalid entries instead of failing.
func resolveReceiverMetrics(rawMetrics []any, metricsRules map[string][]config.LogMetricEntry) []resolvedMetric {
	var resolved []resolvedMetric

	for _, rawEntry := range rawMetrics {
		entry, ok := asMetricEntry(rawEntry)
		if !ok {
			logger.Printf("logmetrics: metrics entry %v is not a map, ignoring", rawEntry)

			continue
		}

		includeName, isInclude := entry["include"].(string)
		if !isInclude || includeName == "" {
			if rm, ok := resolveInlineMetric(entry); ok {
				resolved = append(resolved, rm)
			}

			continue
		}

		rules, found := metricsRules[includeName]
		if !found {
			logger.Printf("logmetrics: metrics_rules %q not found, ignoring include", includeName)

			continue
		}

		for _, ruleRaw := range rules {
			if rm, ok := resolveInlineMetric(ruleRaw); ok {
				resolved = append(resolved, rm)
			}
		}
	}

	return resolved
}

// asMetricEntry narrows a raw "metrics:" list element down to a config.LogMetricEntry, rejecting a malformed one instead of panicking.
func asMetricEntry(v any) (config.LogMetricEntry, bool) {
	m, ok := v.(map[string]any)

	return m, ok
}

// resolveInlineMetric reads a self-contained metrics: entry's "metric" name and item override, warning and returning ok=false if the name is missing.
func resolveInlineMetric(raw config.LogMetricEntry) (resolvedMetric, bool) {
	metric, _ := raw["metric"].(string)
	if metric == "" {
		logger.Printf("logmetrics: metrics entry missing a \"metric\" name, ignoring: %v", raw)

		return resolvedMetric{}, false
	}

	return resolvedMetric{Metric: metric, Raw: raw, Item: extractItem(raw)}, true
}

// extractItem reads raw's "item" field, presence-sensitively: nil means "unset, derive one", a non-nil pointer (even to "") means the config explicitly set it.
// This lets legacy migration pin item="" without it being confused with "unset".
func extractItem(raw config.LogMetricEntry) *string {
	rawItem, present := raw["item"]
	if !present {
		return nil
	}

	s, ok := rawItem.(string)
	if !ok {
		logger.Printf("logmetrics: \"item\" must be a string, got %v (%T), deriving one instead", rawItem, rawItem)

		return nil
	}

	return &s
}

// groupResolvedMetricsByItem partitions entries by the item they should report under, falling back to defaultItem when an entry has no Item override.
func groupResolvedMetricsByItem(entries []resolvedMetric, defaultItem string) map[string][]resolvedMetric {
	groups := make(map[string][]resolvedMetric)

	for _, entry := range entries {
		item := defaultItem
		if entry.Item != nil {
			item = *entry.Item
		}

		groups[item] = append(groups[item], entry)
	}

	return groups
}

// buildGroupedConnectors partitions entries by item and builds one connector set per group, so a single source can feed several item buckets at once.
// A group that fails to produce a valid counter is logged and skipped, not fatal.
func buildGroupedConnectors(
	ctx context.Context,
	telemetry component.TelemetrySettings,
	entries []resolvedMetric,
	defaultItem string,
	reg *metricsRegistry,
	sourceName string,
) ([]otelconnector.Logs, []string, error) {
	groups := groupResolvedMetricsByItem(entries, defaultItem)
	if len(groups) == 0 {
		return nil, nil, errNoApplicableMetric
	}

	connFactory := countconnector.NewFactory()

	var (
		conns []otelconnector.Logs
		items []string
	)

	for item, groupEntries := range groups {
		reg.resolve(specsForEntries(groupEntries), item)

		groupConns, err := buildConnectors(ctx, connFactory, telemetry, groupEntries, reg.metricsSinkForItem(item))
		if err != nil {
			logger.Printf("logmetrics: source %q: item %q: %v", sourceName, item, err)

			continue
		}

		conns = append(conns, groupConns...)
		items = append(items, item)
	}

	if len(conns) == 0 {
		return nil, nil, fmt.Errorf("%w: %d applicable metric group(s), all failed to build a connector", errNoValidCounter, len(groups))
	}

	return conns, items, nil
}

// specsForEntries reduces entries down to what the registry's declare/resolve need (name + static labels).
func specsForEntries(entries []resolvedMetric) []metricSpec {
	specs := make([]metricSpec, 0, len(entries))

	for _, entry := range entries {
		specs = append(specs, metricSpec{Metric: entry.Metric, Labels: extractLabels(entry.Raw)})
	}

	return specs
}

// buildConnectors validates each entry's counter independently -- countconnector.Config.Validate() already checks
// every Logs entry on its own, with no cross-entry state -- so a bad condition only disables its own metric, then
// builds a single connector from the survivors instead of one connector per metric.
func buildConnectors(
	ctx context.Context,
	connFactory otelconnector.Factory,
	telemetry component.TelemetrySettings,
	entries []resolvedMetric,
	sink consumer.Metrics,
) ([]otelconnector.Logs, error) {
	infos := make(map[string]countconnector.MetricInfo, len(entries))

	for _, entry := range entries {
		info, err := metricInfo(entry.Metric, entry.Raw)
		if err != nil {
			logger.Printf("logmetrics: metric %q disabled, invalid config: %v", entry.Metric, err)

			continue
		}

		counterCfg := &countconnector.Config{Logs: map[string]countconnector.MetricInfo{entry.Metric: info}}

		if err := counterCfg.Validate(); err != nil {
			logger.Printf("logmetrics: metric %q disabled, invalid counter: %v", entry.Metric, err)

			continue
		}

		infos[entry.Metric] = info
	}

	if len(infos) == 0 {
		return nil, errNoValidCounter
	}

	conn, err := createConnector(ctx, connFactory, telemetry, &countconnector.Config{Logs: infos}, sink)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errNoValidCounter, err)
	}

	return []otelconnector.Logs{conn}, nil
}

// metricInfo builds the countconnector.MetricInfo for metric name from its raw config.LogMetricEntry.
// "item"/"labels" are ignored here (read separately); "regex" is Glouton's own sugar, expanded into Conditions.
func metricInfo(name string, raw config.LogMetricEntry) (countconnector.MetricInfo, error) {
	info := countconnector.MetricInfo{
		Description: "log-to-metric: " + name,
	}

	if len(raw) == 0 {
		return info, nil
	}

	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{Result: &info})
	if err != nil {
		return countconnector.MetricInfo{}, fmt.Errorf("creating decoder: %w", err)
	}

	if err := decoder.Decode(raw); err != nil {
		return countconnector.MetricInfo{}, fmt.Errorf("decoding config: %w", err)
	}

	if regex, ok := raw["regex"].(string); ok && regex != "" {
		info.Conditions = append(info.Conditions, fmt.Sprintf("IsMatch(body, %q)", regex))
	}

	return info, nil
}

// extractLabels reads the "labels" field out of a raw config.LogMetricEntry.
func extractLabels(raw config.LogMetricEntry) map[string]string {
	rawLabels, _ := raw["labels"].(map[string]any)
	if len(rawLabels) == 0 {
		return nil
	}

	labels := make(map[string]string, len(rawLabels))

	for k, v := range rawLabels {
		if s, ok := v.(string); ok {
			labels[k] = s
		}
	}

	return labels
}

func createConnector(
	ctx context.Context,
	factory otelconnector.Factory,
	telemetry component.TelemetrySettings,
	cfg *countconnector.Config,
	sink consumer.Metrics,
) (otelconnector.Logs, error) {
	conn, err := factory.CreateLogsToMetrics(
		ctx,
		otelconnector.Settings{
			ID:                component.NewIDWithName(factory.Type(), uuid.NewString()),
			TelemetrySettings: telemetry,
		},
		cfg,
		sink,
	)
	if err != nil {
		return nil, fmt.Errorf("build connector: %w", err)
	}

	if err := conn.Start(ctx, nil); err != nil {
		_ = conn.Shutdown(ctx)

		return nil, fmt.Errorf("start connector: %w", err)
	}

	return conn, nil
}

// logsConsumerFor fans a source's connectors into a single consumer.Logs.
func logsConsumerFor(conns []otelconnector.Logs) consumer.Logs {
	sinks := make([]consumer.Logs, len(conns))

	for i, conn := range conns {
		sinks[i] = conn
	}

	return logsource.FanoutLogs(sinks...)
}

func shutdownConns(ctx context.Context, conns []otelconnector.Logs) error {
	var errs error

	for _, conn := range conns {
		errs = errors.Join(errs, conn.Shutdown(ctx))
	}

	return errs
}
