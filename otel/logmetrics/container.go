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
	"github.com/bleemeo/glouton/utils/hostrootsymlink"
)

// resolveContainerFilters returns the concatenation of every filter source that matches ctr.
func resolveContainerFilters(cfg config.Log, ctr facts.Container) []config.LogFilter {
	var filters []config.LogFilter

	for _, input := range cfg.Inputs {
		if input.Path != "" {
			continue // path-based -> not container, handled statically, see resolvePathSources
		}

		matchName := input.ContainerName != "" && ctr.ContainerName() == input.ContainerName
		matchSelectors := len(input.Selectors) > 0 && containerMatchesSelectors(ctr, input.Selectors)

		matches := (matchName && matchSelectors) || (len(input.Selectors) > 0 && matchName) || (input.ContainerName == "" && matchSelectors)

		if matches {
			filters = append(filters, input.Filters...)
		}
	}

	if knownName, found := cfg.Metrics.ContainerFilters[ctr.ContainerName()]; found {
		filters = append(filters, cfg.Metrics.KnownFilters[knownName]...)
	}

	return filters
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
