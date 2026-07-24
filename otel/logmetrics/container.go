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

func hasContainerCounters(cfg config.Log) bool {
	for _, input := range cfg.Inputs {
		if input.Path == "" && (input.ContainerName != "" || len(input.Selectors) > 0) {
			return true
		}
	}

	return len(cfg.Metrics.ContainerCounters) > 0
}

// resolveContainerCounters returns the concatenation of every counter source that matches ctr.
func resolveContainerCounters(cfg config.Log, ctr facts.Container) []config.LogCounter {
	var counters []config.LogCounter

	for _, input := range cfg.Inputs {
		if input.Path != "" {
			continue // path-based, not container
		}

		matchName := input.ContainerName != "" && ctr.ContainerName() == input.ContainerName
		matchSelectors := len(input.Selectors) > 0 && containerMatchesSelectors(ctr, input.Selectors)

		matches := (matchName && matchSelectors) || (len(input.Selectors) == 0 && matchName) || (input.ContainerName == "" && matchSelectors)

		if matches {
			counters = append(counters, input.Counters...)
		}
	}

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

	return counters
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
