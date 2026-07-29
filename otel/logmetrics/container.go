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
	"github.com/bleemeo/glouton/otel/logsource"
	"github.com/bleemeo/glouton/utils/hostrootsymlink"
)

// hasContainerCounters reports whether container watching could match
// anything at all: a metric must be defined, and a selection mechanism must
// be configured.
func hasContainerCounters(cfg config.Log) bool {
	return len(cfg.Metrics.Count) > 0 &&
		(len(cfg.Metrics.ContainerCounters) > 0 || len(cfg.Metrics.ContainerSelectorCounters) > 0)
}

// isContainerWatched reports whether ctr should be watched for log-to-metric,
// unless it matches a ContainerExclude rule first. Only explicit config
// (ContainerCounters/ContainerSelectorCounters) can opt a container in --
// unlike log.opentelemetry, there is no container-label auto-detection here.
func isContainerWatched(cfg config.Log, ctr facts.Container) bool {
	for _, ex := range cfg.Metrics.ContainerExclude {
		if logsource.MatchesContainerRule(ctr, ex.ContainerName, ex.Selectors) {
			return false
		}
	}

	if slices.Contains(cfg.Metrics.ContainerCounters, ctr.ContainerName()) {
		return true
	}

	for _, sel := range cfg.Metrics.ContainerSelectorCounters {
		if logsource.MatchesContainerRule(ctr, sel.ContainerName, sel.Selectors) {
			return true
		}
	}

	return false
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
