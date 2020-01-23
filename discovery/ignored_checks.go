// Copyright 2015-2019 Bleemeo
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
	"regexp"
	"strings"
)

// IgnoredCheck save the ignored checks imported from the configuration file
type IgnoredCheck struct {
	ignoredChecks []map[string]string
}

// NewIgnoredCheck initialize ignoredCheck
func NewIgnoredCheck(ignoredChecks []map[string]string) IgnoredCheck {
	return IgnoredCheck{
		ignoredChecks: ignoredChecks,
	}
}

// IsCheckIgnored return if the check is ignored or not
func (ic IgnoredCheck) IsCheckIgnored(nameContainer NameContainer) bool {
	for _, ignoredCheck := range ic.ignoredChecks {
		if ignoredCheck["name"] == nameContainer.Name {
			instances := strings.Split(ignoredCheck["instance"], " ")
			if len(instances) == 1 && instances[0] == "" {
				return true
			}
			for _, instance := range instances {
				hasMatched := matchInstance(instance, nameContainer.ContainerName)
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
	instanceValue := instanceDetails[1]
	if instanceName == "container" {
		if instanceValue != "" && containerName != "" {
			re := buildRegex(instanceValue)
			matched := re.MatchString(containerName)
			if matched {
				return true
			}
		}
	}

	if instanceName == "host" && containerName == "" {
		return true
	}

	return false
}

func buildRegex(instanceValue string) *regexp.Regexp {
	regexRule := ""
	hasSuffix := strings.HasSuffix(instanceValue, "*")
	hasPrefix := strings.HasPrefix(instanceValue, "*")
	if hasPrefix {
		regexRule += "^.*"
	}
	values := strings.Split(instanceValue, "*")
	for _, value := range values {
		regexRule += value + ".*"
	}
	if hasSuffix {
		regexRule += "$"
	} else {
		regexRule = strings.TrimRight(regexRule, ".*") + "$"
	}
	return regexp.MustCompile(regexRule)
}
