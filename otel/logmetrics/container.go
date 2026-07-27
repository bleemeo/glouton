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

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/utils/hostrootsymlink"
)

// containerLogCounterLabel lets a single container opt into a named
// known_counters group via a Docker label/Kubernetes annotation, mirroring
// otel/logprocessing's glouton.log_filter/glouton.log_format container labels.
// It takes precedence over the static container_counters config map.
const containerLogCounterLabel = "glouton.log_counter"

// hasContainerCounters reports whether any container could possibly need
// watching: via the static container_counters/container_selector_counters
// config, or -- since any container can opt in purely via the
// glouton.log_counter label -- the mere existence of a known_counters group
// for a label to reference.
func hasContainerCounters(cfg config.Log) bool {
	return len(cfg.Metrics.ContainerCounters) > 0 ||
		len(cfg.Metrics.ContainerSelectorCounters) > 0 ||
		len(cfg.Metrics.KnownCounters) > 0
}

// resolveContainerCounters returns the concatenation of every counter source that matches ctr,
// or nil if ctr matches a ContainerExclude rule -- exclusion vetoes every other resolution
// mechanism (label, ContainerCounters, ContainerSelectorCounters) unconditionally.
func resolveContainerCounters(cfg config.Log, ctr facts.Container) []config.LogCounter {
	for _, ex := range cfg.Metrics.ContainerExclude {
		if matchesContainerRule(ctr, ex.ContainerName, ex.Selectors) {
			return nil
		}
	}

	var counters []config.LogCounter

	hasFromLabel := false

	if knownName, found := facts.LabelsAndAnnotations(ctr)[containerLogCounterLabel]; found {
		if known, ok := cfg.Metrics.KnownCounters[knownName]; ok {
			counters = append(counters, known...)
			hasFromLabel = true
		} else {
			logger.V(1).Printf("Container %s (%s) requires an unknown log counter: %q", ctr.ContainerName(), ctr.ID(), knownName)
		}
	}

	if !hasFromLabel {
		if knownName, found := cfg.Metrics.ContainerCounters[ctr.ContainerName()]; found {
			counters = append(counters, cfg.Metrics.KnownCounters[knownName]...)
		}
	}

	// Selector-based rules are additive: every matching entry contributes its
	// known_counters group on top of whatever the label/ContainerCounters
	// resolution above already picked. ContainerName, if set, is an extra
	// requirement on top of Selectors (both must match).
	for _, sel := range cfg.Metrics.ContainerSelectorCounters {
		if matchesContainerRule(ctr, sel.ContainerName, sel.Selectors) {
			counters = append(counters, cfg.Metrics.KnownCounters[sel.KnownCounters]...)
		}
	}

	return counters
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
