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
	"strings"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/otel/logsource"
	"github.com/bleemeo/glouton/types"

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

// resolvedMetric is one metrics: entry after expanding any {include: name} against log.metrics_rules,
// with its item/labels overrides already fully resolved against the source's own default item -- see
// resolveLabels. Raw is kept too so metricInfo can decode its conditions/attributes/regex lazily.
type resolvedMetric struct {
	Metric string
	Raw    config.LogMetricEntry
	// Item is the fully-resolved item this entry reports under ("" means no item label at all). Kept
	// as its own field, redundant with Labels["item"] (which also carries it), purely for
	// groupResolvedMetricsByItem's convenience -- see resolveLabels for how both are derived together.
	Item string
	// Labels is this entry's fully-resolved static label set, including "item" when it's non-empty --
	// see resolveLabels. metricSignature/specsForEntries/buildConnectors all use this as-is; none of
	// them need to special-case item separately from the rest of the labels.
	Labels map[string]string
}

// resolveReceiverMetrics expands rawMetrics (a receiver's "metrics:" list of inline entries or
// {include: name}) against metricsRules, warning and skipping invalid entries instead of failing.
// defaultItem is the source's own default item (e.g. the receiver's name), seeded into every entry's
// resolved item/labels before its own overrides apply -- see resolveLabels. An entry that resolves to
// the same effective metric as one already resolved (same metric name, item, labels, attributes, and
// final match condition -- folding "regex:" sugar into its equivalent IsMatch(body, ...) condition and
// treating the OR'ed condition list as an unordered set, so e.g. regex: "X" and conditions:
// ['IsMatch(body, "X")'] count as the same condition) is dropped too, whether the duplication comes
// from two such entries written inline, two inside one included metrics_rules list, or an inline entry
// repeating one already pulled in by an include: each resolved entry gets its own countconnector (see
// buildConnectors), so keeping both would count every matching line twice into the same counter.
// config.validateLogReceivers already warns about the two directly-visible, verbatim shapes of this at
// load time; this is the runtime side that actually prevents the double-count, and it also catches the
// include-vs-inline case, the regex/conditions equivalence, and the item: / labels: {item: ...}
// spelling equivalence that the config-time, raw-text check can't see.
func resolveReceiverMetrics(rawMetrics []any, metricsRules map[string][]config.LogMetricEntry, defaultItem string) []resolvedMetric {
	var resolved []resolvedMetric

	for _, rawEntry := range rawMetrics {
		entry, ok := asMetricEntry(rawEntry)
		if !ok {
			logger.Printf("logmetrics: metrics entry %v is not a map, ignoring", rawEntry)

			continue
		}

		includeName, isInclude := entry["include"].(string)
		if !isInclude || includeName == "" {
			if rm, ok := resolveInlineMetric(entry, defaultItem); ok {
				resolved = appendResolvedMetric(resolved, rm)
			}

			continue
		}

		rules, found := metricsRules[includeName]
		if !found {
			logger.Printf("logmetrics: metrics_rules %q not found, ignoring include", includeName)

			continue
		}

		for _, ruleRaw := range rules {
			if rm, ok := resolveInlineMetric(ruleRaw, defaultItem); ok {
				resolved = appendResolvedMetric(resolved, rm)
			}
		}
	}

	return resolved
}

// appendResolvedMetric appends rm to resolved, unless rm resolves to the same effective metric as an
// entry already in resolved -- see resolveReceiverMetrics's doc comment. rm's own invalid config (if
// any) is left for buildConnectors to report later: an entry that can't even be signed is never
// treated as a duplicate here.
func appendResolvedMetric(resolved []resolvedMetric, rm resolvedMetric) []resolvedMetric {
	sig, ok := metricSignature(rm)
	if !ok {
		return append(resolved, rm)
	}

	for _, existing := range resolved {
		if existingSig, ok := metricSignature(existing); ok && existingSig == sig {
			logger.Printf("logmetrics: metric %q duplicates an earlier metrics entry (same condition, item, labels and attributes), ignoring the duplicate", rm.Metric)

			return resolved
		}
	}

	return append(resolved, rm)
}

// resolvedMetricSignature is what actually decides whether two entries would double-count the same
// matching lines onto the same series: the same metric name, the same final match condition, and the
// same final label set (item included: it's just another label -- see resolveLabels) -- not the same
// raw config. Two entries can be spelled completely differently (e.g. one using regex:, the other an
// equivalent conditions:, or one using item: "home" and the other labels: {item: "home"}) and still
// resolve to the same signature. There's no separate item/itemSet field: rm.Item/rm.Labels are already
// fully resolved by the time this runs (defaultItem is known before resolveReceiverMetrics is ever
// called -- see its doc comment), so there's no "still unknown" state left to represent.
type resolvedMetricSignature struct {
	metric     string
	labels     string
	conditions string
	attributes string
}

// metricSignature builds rm's resolvedMetricSignature, or ok=false if metricInfo can't even decode
// rm's config -- buildConnectors will report that error on its own later, so an entry in that state is
// simply never treated as a duplicate here.
func metricSignature(rm resolvedMetric) (resolvedMetricSignature, bool) {
	info, err := metricInfo(rm.Metric, rm.Raw)
	if err != nil {
		return resolvedMetricSignature{}, false
	}

	return resolvedMetricSignature{
		metric:     rm.Metric,
		labels:     types.LabelsToText(rm.Labels),
		conditions: encodeStringSet(info.Conditions),
		attributes: encodeAttributeSet(info.Attributes),
	}, true
}

// encodeStringSet canonically encodes a set of strings (order-independent -- e.g. an OR'ed
// conditions: list, where order never changes the result) into a deterministic string: sorted, then
// %q-quoted so a value containing ',' or '"' can't make two distinct sets collide onto the same string.
func encodeStringSet(values []string) string {
	if len(values) == 0 {
		return ""
	}

	sorted := slices.Clone(values)
	slices.Sort(sorted)

	var sb strings.Builder

	for _, v := range sorted {
		fmt.Fprintf(&sb, "%q,", v)
	}

	return sb.String()
}

// encodeAttributeSet canonically encodes a countconnector attributes list, sorted by key so a purely
// cosmetic reordering doesn't produce a different signature.
func encodeAttributeSet(attrs []countconnector.AttributeConfig) string {
	if len(attrs) == 0 {
		return ""
	}

	sorted := slices.Clone(attrs)
	slices.SortFunc(sorted, func(a, b countconnector.AttributeConfig) int { return strings.Compare(a.Key, b.Key) })

	var sb strings.Builder

	for _, a := range sorted {
		fmt.Fprintf(&sb, "%q=%v,", a.Key, a.DefaultValue)
	}

	return sb.String()
}

// asMetricEntry narrows a raw "metrics:" list element down to a config.LogMetricEntry, rejecting a malformed one instead of panicking.
func asMetricEntry(v any) (config.LogMetricEntry, bool) {
	m, ok := v.(map[string]any)

	return m, ok
}

// resolveInlineMetric reads a self-contained metrics: entry's "metric" name, warning and returning
// ok=false if it's missing, and fully resolves its item/labels against defaultItem -- see resolveLabels.
func resolveInlineMetric(raw config.LogMetricEntry, defaultItem string) (resolvedMetric, bool) {
	metric, _ := raw["metric"].(string)
	if metric == "" {
		logger.Printf("logmetrics: metrics entry missing a \"metric\" name, ignoring: %v", raw)

		return resolvedMetric{}, false
	}

	labels := resolveLabels(raw, defaultItem)

	return resolvedMetric{Metric: metric, Raw: raw, Item: labels["item"], Labels: labels}, true
}

// resolveLabels builds a metrics: entry's final, canonical label set: the same one resolve()/
// counterKey end up rendering for it once grouped by item, computed eagerly here (instead of deferred)
// since every caller already knows defaultItem before this runs -- so there's no "still unknown, derive
// later" state left to track separately from a genuine value.
//
// Built by seeding "item" with defaultItem (every entry starts under its own source's default item),
// merging in the entry's own labels: map -- an "item" key there overrides the seeded default, which is
// labels: {item: ...}'s whole purpose -- then applying the top-level item: field if it's set (it wins
// over labels: {item: ...}, the same precedence as before). Finally, any label left with an empty
// string value -- "item" or otherwise -- is dropped entirely: an empty value means "unset" for every
// label, generalizing resolve()'s historical "if item != \"\" {...}" special case for item to all of
// them. So labels: {item: ""} or item: "" (legacy migration's way of pinning "no item at all",
// overriding whatever default this source would otherwise seed) ends up with no item key at all, same
// as if this source's own default item happened to be "" (never true in practice: a receiver/container
// name is never empty).
func resolveLabels(raw config.LogMetricEntry, defaultItem string) map[string]string {
	merged := map[string]string{"item": defaultItem}

	if rawLabels, ok := raw["labels"].(map[string]any); ok {
		for k, v := range rawLabels {
			s, ok := v.(string)
			if !ok {
				logger.Printf("logmetrics: label %q must be a string, got %v (%T), dropping it", k, v, v)

				continue
			}

			merged[k] = s
		}
	}

	if rawItem, present := raw["item"]; present {
		if s, ok := rawItem.(string); ok {
			merged["item"] = s
		} else {
			logger.Printf("logmetrics: \"item\" must be a string, got %v (%T), deriving one instead", rawItem, rawItem)
		}
	}

	for k, v := range merged {
		if v == "" {
			delete(merged, k)
		}
	}

	if len(merged) == 0 {
		return nil
	}

	return merged
}

// groupResolvedMetricsByItem partitions entries by the item they resolved to (see resolveLabels).
func groupResolvedMetricsByItem(entries []resolvedMetric) map[string][]resolvedMetric {
	groups := make(map[string][]resolvedMetric)

	for _, entry := range entries {
		groups[entry.Item] = append(groups[entry.Item], entry)
	}

	return groups
}

// buildGroupedConnectors partitions entries by item and builds one connector set per group, so a single source can feed several item buckets at once.
// A group that fails to produce a valid counter is logged and skipped, not fatal.
func buildGroupedConnectors(
	ctx context.Context,
	telemetry component.TelemetrySettings,
	entries []resolvedMetric,
	reg *metricsRegistry,
	sourceName string,
) ([]otelconnector.Logs, []string, error) {
	groups := groupResolvedMetricsByItem(entries)
	if len(groups) == 0 {
		return nil, nil, errNoApplicableMetric
	}

	connFactory := countconnector.NewFactory()

	var (
		conns []otelconnector.Logs
		items []string
	)

	for item, groupEntries := range groups {
		// reg.resolve() only runs once buildConnectors has confirmed at least one entry in this group
		// is valid: resolving first would declare (permanent, zero-value) counters for an item that
		// then never makes it into items below, so Manager.ReleaseSource would never release them --
		// a leak for the process lifetime once the item's source (e.g. its container) is gone.
		// metricsSinkForEntry is safe to call before resolve(): it looks up reg.counters fresh on every
		// incoming data point and no-ops if the key isn't there yet.
		groupConns, err := buildConnectors(ctx, connFactory, telemetry, groupEntries, reg, item)
		if err != nil {
			logger.Printf("logmetrics: source %q: item %q: %v", sourceName, item, err)

			continue
		}

		reg.resolve(specsForEntries(groupEntries), item)

		conns = append(conns, groupConns...)
		items = append(items, item)
	}

	if len(conns) == 0 {
		return nil, nil, fmt.Errorf("%w: %d applicable metric group(s), all failed to build a connector", errNoValidCounter, len(groups))
	}

	return conns, items, nil
}

// specsForEntries reduces entries down to what the registry's declare/resolve need (name + resolved labels).
func specsForEntries(entries []resolvedMetric) []metricSpec {
	specs := make([]metricSpec, 0, len(entries))

	for _, entry := range entries {
		specs = append(specs, metricSpec{Metric: entry.Metric, Labels: entry.Labels})
	}

	return specs
}

// buildConnectors validates each entry's counter independently -- countconnector.Config.Validate() already checks
// every Logs entry on its own, with no cross-entry state -- so a bad condition only disables its own metric. It
// builds one connector per valid entry, not one shared connector for the whole group: two entries sharing the same
// metric name (e.g. the same metric split into several conditions/labels combinations) would otherwise silently
// collide into a single map entry, losing all but the last one's condition (see reg.metricsSinkForEntry's doc
// comment for why the resulting per-entry sink also needs each entry's own static "labels:" threaded through).
func buildConnectors(
	ctx context.Context,
	connFactory otelconnector.Factory,
	telemetry component.TelemetrySettings,
	entries []resolvedMetric,
	reg *metricsRegistry,
	item string,
) ([]otelconnector.Logs, error) {
	var conns []otelconnector.Logs

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

		labelsKey := types.LabelsToText(entry.Labels)

		conn, err := createConnector(ctx, connFactory, telemetry, counterCfg, reg.metricsSinkForEntry(item, labelsKey))
		if err != nil {
			logger.Printf("logmetrics: metric %q disabled, failed to build connector: %v", entry.Metric, err)

			continue
		}

		conns = append(conns, conn)
	}

	if len(conns) == 0 {
		return nil, errNoValidCounter
	}

	return conns, nil
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
