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

// ContainerLabelPrefix is the prefix for every glouton.* container
// label/annotation ReceiverManager's container-label fallback path consults
// (see parseContainerLabels), for a container matched by no configured
// receiver's container_name/container_selectors.
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
	// LogEnable is nil if unset. false vetoes the container entirely (see
	// IsContainerExcluded); true also implies SendLogs unless send_logs is
	// set explicitly (see resolveSendLogs) -- back-compat with today's live
	// opt-in behavior.
	LogEnable *bool
	SendLogs  *bool
	// LogMetrics is the glouton.log_metrics value: a Log.MetricsRules name,
	// resolved by otel/logmetrics, not here. "" means unset.
	LogMetrics string
	// LogFormat is the glouton.log_format value, a KnownLogFormats name. ""
	// means unset (see resolveContainerLogFormat).
	LogFormat string
	// LogFilter is the glouton.log_filter value, shipping-only (a
	// KnownLogFilters name). ReceiverManager doesn't resolve it -- it's
	// exposed so otel/logprocessing can, unchanged from today.
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

// parseBoolLabel reads a boolean container label, warning (not erroring) and
// returning nil if malformed -- a typo shouldn't silently enable or disable
// something.
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

// resolveSendLogs computes this container's effective shipping decision: its
// own explicit send_logs if set, else log_enable=true implying send_logs=true,
// else fallbackDefault -- the caller decides what that means (see
// ReceiverManager.updateLabelContainers: it's auto_discovery.
// container_and_service_enable, not the receiver-oriented OpenTelemetry.SendLogs).
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

// IsContainerExcluded reports whether ctr is vetoed from every log source
// (shipping and metrics alike): either its own glouton.log_enable=false
// label, or a matching OpenTelemetry.ContainerExclude rule. This check always
// runs first, before any receiver/label resolution -- even for a container
// otherwise matched by an explicit receiver's container_name/
// container_selectors.
func IsContainerExcluded(cfg config.OpenTelemetry, ctr facts.Container) bool {
	if parseContainerLabels(ctr).isExcluded() {
		return true
	}

	for _, rule := range cfg.ContainerExclude {
		if MatchesContainerRule(ctr, rule.ContainerName, rule.Selectors) {
			return true
		}
	}

	return false
}

// resolveContainerLogFormat resolves the raw operators for a container's log
// format: its own glouton.log_format label if it names a known format, else
// the OpenTelemetry.ContainerFormat[containerName] fallback, else nil. Both
// paths warn (not error) on an unknown format name. The result applies to
// BOTH this container's shipped logs and its metrics conditions/attributes --
// unlike today's otel/logprocessing-only container_format, which never
// reached otel/logmetrics.
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
