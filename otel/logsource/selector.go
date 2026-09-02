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

import "github.com/bleemeo/glouton/facts"

// MatchesContainerRule reports whether ctr matches containerName (exact, if
// set) and selectors (every key/value must match a label or annotation, if
// set); an unset field acts as a wildcard.
func MatchesContainerRule(ctr facts.Container, containerName string, selectors map[string]string) bool {
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
