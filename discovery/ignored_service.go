// Copyright 2015-2024 Bleemeo
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

package discovery

import (
	"path/filepath"
	"strings"

	"github.com/bleemeo/glouton/config"
)

// IgnoredService saves the ignored services (checks/metrics/everything) imported from the configuration file.
type IgnoredService struct {
	ignoredNamesInstances []config.NameInstance
}

// NewIgnoredService initializes IgnoredService struct.
func NewIgnoredService(ignoredNamesInstances []config.NameInstance) IgnoredService {
	return IgnoredService{
		ignoredNamesInstances: ignoredNamesInstances,
	}
}

// IsServiceIgnored returns whether the given service should be ignored or not.
func (ic IgnoredService) IsServiceIgnored(srv Service) bool {
	for _, ignoredNameInstance := range ic.ignoredNamesInstances {
		if ignoredNameInstance.Name == srv.Name {
			instances := strings.Split(ignoredNameInstance.Instance, " ")
			if len(instances) == 1 && instances[0] == "" {
				return true
			}

			for _, instance := range instances {
				hasMatched := matchInstance(instance, srv.ContainerName)
				if hasMatched {
					return true
				}
			}
		}
	}

	return false
}

func matchInstance(instance, containerName string) bool {
	instanceDetails := strings.Split(instance, ":")
	if len(instanceDetails) != 2 {
		return false
	}

	instanceName := instanceDetails[0]
	instancePattern := instanceDetails[1]

	if instanceName == "host" && containerName == "" {
		return true
	}

	if instanceName == "container" && containerName != "" {
		matched, err := filepath.Match(instancePattern, containerName)
		if err == nil {
			return matched
		}
	}

	return false
}
