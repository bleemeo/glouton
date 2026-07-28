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
	"path/filepath"
	"slices"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/utils/hostrootsymlink"
)

// containerLogCounterLabel lets a single container opt into log-to-metric via
// a Docker label/Kubernetes annotation (any value, presence is all that
// matters), mirroring otel/logprocessing's glouton.log_filter/
// glouton.log_format container labels. Since log.metrics.count is global (see
// LogMetricsConfig's doc comment), there is no group name to reference here
// anymore: the label is a plain opt-in on top of ContainerCounters/
// ContainerSelectorCounters.
const containerLogCounterLabel = "glouton.log_counter"

// hasContainerCounters reports whether container watching could possibly
// matter: only if at least one metric is defined at all (see
// LogMetricsConfig.Count) -- with none, no container (however selected) would
// ever produce anything. If Count is non-empty, watching is always
// potentially relevant, since any container can opt in purely via the
// glouton.log_counter label regardless of ContainerCounters/
// ContainerSelectorCounters.
func hasContainerCounters(cfg config.Log) bool {
	return len(cfg.Metrics.Count) > 0
}

// isContainerWatched reports whether ctr should be watched for log-to-metric:
// via the glouton.log_counter label, the static ContainerCounters list, or a
// matching ContainerSelectorCounters rule -- unless it first matches a
// ContainerExclude rule, which vetoes every other selection mechanism
// unconditionally.
func isContainerWatched(cfg config.Log, ctr facts.Container) bool {
	for _, ex := range cfg.Metrics.ContainerExclude {
		if matchesContainerRule(ctr, ex.ContainerName, ex.Selectors) {
			return false
		}
	}

	if _, found := facts.LabelsAndAnnotations(ctr)[containerLogCounterLabel]; found {
		return true
	}

	if slices.Contains(cfg.Metrics.ContainerCounters, ctr.ContainerName()) {
		return true
	}

	for _, sel := range cfg.Metrics.ContainerSelectorCounters {
		if matchesContainerRule(ctr, sel.ContainerName, sel.Selectors) {
			return true
		}
	}

	return false
}

// matchesContainerRule reports whether ctr matches a {containerName, selectors} rule:
// containerName (if set) requires an exact match, selectors (if set) require every
// key/value to match a label or annotation -- both conditions apply together when set,
// and an empty/unset one acts as a wildcard on that dimension.
func matchesContainerRule(ctr facts.Container, containerName string, selectors map[string]string) bool {
	if containerName != "" && containerName != ctr.ContainerName() {
		return false
	}

	return containerMatchesSelectors(ctr, selectors)
}

// containerMatchesSelectors returns true if the container's labels or annotations match the selectors.
func containerMatchesSelectors(container facts.Container, selectors map[string]string) bool {
	matchLabels := labelsMatchSelectors(container.Labels(), selectors)
	matchAnnotations := labelsMatchSelectors(container.Annotations(), selectors)

	return matchLabels || matchAnnotations
}

// labelsMatchSelectors returns true if the labels match all the selectors.
func labelsMatchSelectors(labels map[string]string, selectors map[string]string) bool {
	for name, value := range selectors {
		if labels[name] != value {
			return false
		}
	}

	return true
}

// resolveContainerLogPath returns the hostroot-prefixed, symlink-resolved log file
// path for ctr, or "" if the container has no log file.
func resolveContainerLogPath(ctr facts.Container, hostroot string) string {
	logPath := ctr.LogPath()
	if logPath == "" {
		return ""
	}

	if hostroot != "/" {
		logPath = hostrootsymlink.EvalSymlinks(hostroot, logPath)
	}

	return filepath.Join(hostroot, logPath)
}
