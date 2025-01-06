// Copyright 2015-2025 Bleemeo
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

package testutil

import (
	"errors"
	"fmt"

	"github.com/bleemeo/glouton/facts"
)

var errDuplicatedID = errors.New("duplicated ID")

// ContainersToMap convert a list of containers to a map of container with ID as key.
// Return error if there is a ID conflict.
func ContainersToMap(containers []facts.Container) (map[string]facts.Container, error) {
	results := make(map[string]facts.Container, len(containers))

	for _, c := range containers {
		if results[c.ID()] != nil {
			return nil, fmt.Errorf("%w %v", errDuplicatedID, c.ID())
		}

		results[c.ID()] = c
	}

	return results, nil
}
