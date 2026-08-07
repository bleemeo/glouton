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

	"github.com/go-viper/mapstructure/v2"
)

var (
	errReceiverNoSelector               = errors.New("log.opentelemetry receiver has no source selector (include, container_name, container_selectors, or network)")
	errContainerExcludeEmpty            = errors.New("log.opentelemetry.container_exclude entry has neither container_name nor selectors set")
	errReceiverNetworkListenerUndefined = errors.New("network listener not defined in opentelemetry.network_listeners")
	errReceiverMetricsRuleUndefined     = errors.New("metrics_rules entry not defined in log.metrics_rules")
)

// receiverSelectors is the subset of a raw LogReceiver's keys that decide
// what it watches, narrow-decoded out of the rest of the receiver's
// (otherwise real vendored fileconsumer/filelogreceiver) fields.
type receiverSelectors struct {
	Include            []string                 `mapstructure:"include"`
	ContainerName      string                   `mapstructure:"container_name"`
	ContainerSelectors map[string]string        `mapstructure:"container_selectors"`
	Network            OTLPNetworkParticipation `mapstructure:"network"`
}

// LogReceiverSelectors narrow-decodes just the selector-related keys out of
// a raw LogReceiver, ignoring every other (real vendored fileconsumer/
// filelogreceiver) field it may carry. Used both by validateLogReceivers and
// by the runtime layer (e.g. to check whether a container already matches a
// configured receiver before falling back to container-label detection).
func LogReceiverSelectors(raw LogReceiver) (include []string, containerName string, containerSelectors map[string]string, network OTLPNetworkParticipation, err error) {
	var probe receiverSelectors

	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{Result: &probe})
	if err != nil {
		return nil, "", nil, OTLPNetworkParticipation{}, fmt.Errorf("creating decoder: %w", err)
	}

	if err := decoder.Decode(raw); err != nil {
		return nil, "", nil, OTLPNetworkParticipation{}, err
	}

	return probe.Include, probe.ContainerName, probe.ContainerSelectors, probe.Network, nil
}

// receiverMetricsIncludeNames returns every distinct {include: name} value found in raw's "metrics" list.
// This narrowly duplicates how otel/logmetrics.resolveReceiverMetrics itself walks the same list (see that
// package's asMetricEntry) -- config can't import otel/logmetrics to share the logic, since it's the other
// way around (that package depends on this one). Anything not shaped like {include: "somestring"} (an
// inline metric entry, a malformed entry) is silently skipped here: those are otel/logmetrics's own
// concern (resolveInlineMetric/asMetricEntry already warn on them at runtime), this helper only cares
// about include references, the one thing that can be cross-checked against static config right now.
func receiverMetricsIncludeNames(raw LogReceiver) []string {
	rawMetrics, _ := raw["metrics"].([]any)

	var names []string

	for _, rawEntry := range rawMetrics {
		entry, ok := rawEntry.(map[string]any)
		if !ok {
			continue
		}

		name, ok := entry["include"].(string)
		if ok && name != "" {
			names = append(names, name)
		}
	}

	return names
}

// validateLogReceivers rejects any log.opentelemetry.receivers entry with no
// source selector at all (include, container_name, container_selectors, or
// network) -- almost certainly a typo/mistake, caught at load time instead
// of silently doing nothing. It also rejects network receivers and metric includes
// that don't exist.
func validateLogReceivers(cfg Config) error {
	var errs []error

	for name, raw := range cfg.Log.OpenTelemetry.Receivers {
		include, containerName, containerSelectors, network, err := LogReceiverSelectors(raw)
		if err != nil {
			errs = append(errs, fmt.Errorf("log.opentelemetry.receivers.%s: %w", name, err))

			continue
		}

		if len(include) == 0 && containerName == "" && len(containerSelectors) == 0 && len(network.Receivers) == 0 {
			errs = append(errs, fmt.Errorf("%w: %q", errReceiverNoSelector, name))
		}

		for _, listenerName := range network.Receivers {
			if _, ok := cfg.OpenTelemetry.NetworkListeners[listenerName]; !ok {
				errs = append(errs, fmt.Errorf(
					"%w: log.opentelemetry.receivers.%s.network.receivers references %q",
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
