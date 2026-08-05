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

package logsource

import (
	"strconv"
	"strings"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/logger"
)

// ContainerLabelPrefix is the prefix for every glouton.* container label/annotation.
const ContainerLabelPrefix = "glouton."

const (
	labelLogEnable  = ContainerLabelPrefix + "log_enable"
	labelSendLogs   = ContainerLabelPrefix + "send_logs"
	labelLogMetrics = ContainerLabelPrefix + "log_metrics"
	labelLogFormat  = ContainerLabelPrefix + "log_format"
	labelLogFilter  = ContainerLabelPrefix + "log_filter"
)

// containerLabels is what ReceiverManager derives from a container's own
// glouton.* labels/annotations.
type containerLabels struct {
	// LogEnable is nil if unset; false vetoes the container, true implies SendLogs unless set explicitly.
	LogEnable *bool
	SendLogs  *bool
	// LogMetrics is the glouton.log_metrics value, a Log.MetricsRules name; "" means unset.
	LogMetrics string
	// LogFormat is the glouton.log_format value, a KnownLogFormats name; "" means unset.
	LogFormat string
	// LogFilter is the glouton.log_filter value, a KnownLogFilters name (shipping-only).
	LogFilter string
}

func parseContainerLabels(ctr facts.Container) containerLabels {
	raw := facts.LabelsAndAnnotations(ctr)

	return containerLabels{
		LogEnable:  parseBoolLabel(ctr, raw, labelLogEnable),
		SendLogs:   parseBoolLabel(ctr, raw, labelSendLogs),
		LogMetrics: raw[labelLogMetrics],
		LogFormat:  raw[labelLogFormat],
		LogFilter:  raw[labelLogFilter],
	}
}

// parseBoolLabel reads a boolean container label, warning and returning nil if malformed.
func parseBoolLabel(ctr facts.Container, raw map[string]string, key string) *bool {
	str, found := raw[key]
	if !found {
		return nil
	}

	v, err := strconv.ParseBool(strings.ToLower(str))
	if err != nil {
		logger.V(1).Printf("logsource: container %s (%s): invalid boolean value %q for label %q: %v", ctr.ContainerName(), ctr.ID(), str, key, err)

		return nil
	}

	return &v
}

// resolveSendLogs computes this container's effective shipping decision: explicit send_logs, else log_enable=true, else fallbackDefault.
func (labels containerLabels) resolveSendLogs(fallbackDefault bool) bool {
	if labels.SendLogs != nil {
		return *labels.SendLogs
	}

	if labels.LogEnable != nil && *labels.LogEnable {
		return true
	}

	return fallbackDefault
}

// isExcluded reports this container's own veto: glouton.log_enable=false.
func (labels containerLabels) isExcluded() bool {
	return labels.LogEnable != nil && !*labels.LogEnable
}

// equal reports whether labels and other resolve to the same values. Every field but the two *bool ones is
// directly comparable; LogEnable/SendLogs need a dedicated comparison since parseContainerLabels allocates a
// fresh *bool each call, so two calls parsing the identical underlying value would otherwise never be ==.
func (labels containerLabels) equal(other containerLabels) bool {
	return boolPtrEqual(labels.LogEnable, other.LogEnable) &&
		boolPtrEqual(labels.SendLogs, other.SendLogs) &&
		labels.LogMetrics == other.LogMetrics &&
		labels.LogFormat == other.LogFormat &&
		labels.LogFilter == other.LogFilter
}

func boolPtrEqual(a, b *bool) bool {
	if a == nil || b == nil {
		return a == b
	}

	return *a == *b
}

// isConfigExcluded reports whether ctr matches an OpenTelemetry.ContainerExclude rule.
func isConfigExcluded(cfg config.OpenTelemetry, ctr facts.Container) bool {
	for _, rule := range cfg.ContainerExclude {
		if MatchesContainerRule(ctr, rule.ContainerName, rule.Selectors) {
			return true
		}
	}

	return false
}

// IsContainerConfigExcluded reports whether ctr matches an OpenTelemetry.ContainerExclude rule, ignoring the glouton.log_enable label.
// Use it when a receiver's container_name/container_selectors already opted the container in explicitly.
func IsContainerConfigExcluded(cfg config.OpenTelemetry, ctr facts.Container) bool {
	return isConfigExcluded(cfg, ctr)
}

// IsContainerExcluded reports whether ctr is vetoed from auto-discovery: its own glouton.log_enable=false label, or a matching
// OpenTelemetry.ContainerExclude rule. Containers explicitly matched by a receiver use IsContainerConfigExcluded instead.
func IsContainerExcluded(cfg config.OpenTelemetry, ctr facts.Container) bool {
	if parseContainerLabels(ctr).isExcluded() {
		return true
	}

	return isConfigExcluded(cfg, ctr)
}

// resolveContainerLogFormat resolves a container's log format operators: its glouton.log_format label if known, else the
// ContainerFormat fallback, else nil. Warns on an unknown format name.
func resolveContainerLogFormat(
	containerName string,
	labelFormat string,
	containerFormat map[string]string,
	knownLogFormats map[string][]config.OTELOperator,
) []config.OTELOperator {
	if labelFormat != "" {
		if ops, found := knownLogFormats[labelFormat]; found {
			return ops
		}

		logger.V(1).Printf("logsource: container %q requires an unknown log format %q", containerName, labelFormat)
	}

	if fallback, found := containerFormat[containerName]; found {
		if ops, found := knownLogFormats[fallback]; found {
			return ops
		}

		logger.V(1).Printf("logsource: container %q requires an unknown log format %q", containerName, fallback)
	}

	return nil
}
