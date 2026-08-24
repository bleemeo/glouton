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

package config

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/go-viper/mapstructure/v2"
)

var (
	errReceiverNoSelector               = errors.New("log.opentelemetry receiver has no source selector (include, container_name, container_selectors, or from_listeners)")
	errContainerExcludeEmpty            = errors.New("log.opentelemetry.container_exclude entry has neither container_name nor selectors set")
	errReceiverNetworkListenerUndefined = errors.New("network listener not defined in opentelemetry.listeners")
	errReceiverMetricsRuleUndefined     = errors.New("metrics_rules entry not defined in log.metrics_rules")
	errDuplicateMetricEntry             = errors.New("metrics entry duplicates an earlier one in the same list verbatim (same metric, conditions/regex, item, labels and attributes) -- would double-count every matching line")
)

// receiverSelectors is the subset of a raw LogReceiver's keys that decide
// what it watches, narrow-decoded out of the rest of the receiver's
// (otherwise real vendored fileconsumer/filelogreceiver) fields.
type receiverSelectors struct {
	Include            []string          `mapstructure:"include"`
	ContainerName      string            `mapstructure:"container_name"`
	ContainerSelectors map[string]string `mapstructure:"container_selectors"`
	FromListeners      []string          `mapstructure:"from_listeners"`
}

// LogReceiverSelectors narrow-decodes just the selector-related keys out of
// a raw LogReceiver, ignoring every other (real vendored fileconsumer/
// filelogreceiver) field it may carry. Used both by validateLogReceivers and
// by the runtime layer (e.g. to check whether a container already matches a
// configured receiver before falling back to container-label detection).
func LogReceiverSelectors(raw LogReceiver) (include []string, containerName string, containerSelectors map[string]string, fromListeners []string, err error) {
	var probe receiverSelectors

	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{Result: &probe})
	if err != nil {
		return nil, "", nil, nil, fmt.Errorf("creating decoder: %w", err)
	}

	if err := decoder.Decode(raw); err != nil {
		return nil, "", nil, nil, err
	}

	return probe.Include, probe.ContainerName, probe.ContainerSelectors, probe.FromListeners, nil
}

// receiverMetricsIncludeNames returns every distinct {include: name} value found in raw's "metrics" list.
// This narrowly duplicates how otel/logmetrics.resolveReceiverMetrics itself walks the same list (see that
// package's asMetricEntry) -- config can't import otel/logmetrics to share the logic, since it's the other
// way around (that package depends on this one). Anything not shaped like {include: "somestring"} (an
// inline metric entry, a malformed entry) is silently skipped here: those are otel/logmetrics's own
// concern (resolveInlineMetric/asMetricEntry already warn on them at runtime), this helper only cares
// about include references, the one thing that can be cross-checked against static config right now.
func receiverMetricsIncludeNames(raw LogReceiver) []string {
	var names []string

	for _, entry := range rawMetricEntries(raw) {
		if name, ok := entry["include"].(string); ok && name != "" {
			names = append(names, name)
		}
	}

	return names
}

// rawMetricEntries returns raw's "metrics" list, keeping only the entries actually shaped like a
// map (an inline definition or an {include: name} reference) -- anything else is malformed and left
// for otel/logmetrics's own runtime warnings to catch, same as receiverMetricsIncludeNames.
func rawMetricEntries(raw LogReceiver) []map[string]any {
	rawMetrics, _ := raw["metrics"].([]any)

	entries := make([]map[string]any, 0, len(rawMetrics))

	for _, rawEntry := range rawMetrics {
		if entry, ok := rawEntry.(map[string]any); ok {
			entries = append(entries, entry)
		}
	}

	return entries
}

// firstDuplicateMetricEntry returns the index of the first entry in entries that's a verbatim
// duplicate of an earlier one in the same list, and the index of that earlier one. Each metrics:
// entry gets its own countconnector (see otel/logmetrics's buildConnectors), so two byte-identical
// entries -- same metric, conditions/regex, item, labels and attributes -- would both independently
// match and count every line, silently doubling the resulting series' value.
func firstDuplicateMetricEntry(entries []map[string]any) (dupIndex, origIndex int, found bool) {
	for i := 1; i < len(entries); i++ {
		for j := range i {
			if reflect.DeepEqual(entries[i], entries[j]) {
				return i, j, true
			}
		}
	}

	return 0, 0, false
}

// validateLogReceivers rejects any log.opentelemetry.receivers entry with no
// source selector at all (include, container_name, container_selectors, or
// from_listeners) -- almost certainly a typo/mistake, caught at load time instead
// of silently doing nothing. It also rejects undefined from_listeners entries, metric includes
// that don't exist, and verbatim-duplicate metrics: entries (within a receiver's own list, or
// within a log.metrics_rules list).
func validateLogReceivers(cfg Config) error {
	var errs []error

	for name, raw := range cfg.Log.OpenTelemetry.Receivers {
		include, containerName, containerSelectors, fromListeners, err := LogReceiverSelectors(raw)
		if err != nil {
			errs = append(errs, fmt.Errorf("log.opentelemetry.receivers.%s: %w", name, err))

			continue
		}

		if len(include) == 0 && containerName == "" && len(containerSelectors) == 0 && len(fromListeners) == 0 {
			errs = append(errs, fmt.Errorf("%w: %q", errReceiverNoSelector, name))
		}

		for _, listenerName := range fromListeners {
			if _, ok := cfg.OpenTelemetry.NetworkListeners[listenerName]; !ok {
				errs = append(errs, fmt.Errorf(
					"%w: log.opentelemetry.receivers.%s.from_listeners references %q",
					errReceiverNetworkListenerUndefined, name, listenerName,
				))
			}
		}

		for _, ruleName := range receiverMetricsIncludeNames(raw) {
			if _, ok := cfg.Log.MetricsRules[ruleName]; !ok {
				errs = append(errs, fmt.Errorf(
					"%w: log.opentelemetry.receivers.%s.metrics references %q",
					errReceiverMetricsRuleUndefined, name, ruleName,
				))
			}
		}

		if dup, orig, found := firstDuplicateMetricEntry(rawMetricEntries(raw)); found {
			errs = append(errs, fmt.Errorf(
				"%w: log.opentelemetry.receivers.%s.metrics[%d] duplicates metrics[%d]",
				errDuplicateMetricEntry, name, dup, orig,
			))
		}
	}

	for ruleName, entries := range cfg.Log.MetricsRules {
		if dup, orig, found := firstDuplicateMetricEntry(entries); found {
			errs = append(errs, fmt.Errorf(
				"%w: log.metrics_rules.%s[%d] duplicates [%d]",
				errDuplicateMetricEntry, ruleName, dup, orig,
			))
		}
	}

	return errors.Join(errs...)
}

// validateContainerExcludeRules rejects any log.opentelemetry.container_exclude entry with neither
// container_name nor selectors set -- almost certainly a typo/mistake, since MatchesContainerRule treats
// an unset field as a wildcard: such an entry would silently veto every container from both log shipping
// auto_discovery and metrics container-label detection, instead of the one container it was meant to match.
func validateContainerExcludeRules(cfg Config) error {
	var errs []error

	for i, rule := range cfg.Log.OpenTelemetry.ContainerExclude {
		if rule.ContainerName == "" && len(rule.Selectors) == 0 {
			errs = append(errs, fmt.Errorf("%w: index %d", errContainerExcludeEmpty, i))
		}
	}

	return errors.Join(errs...)
}
