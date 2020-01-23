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

import "testing"

func TestIsCheckIgnored(t *testing.T) {
	checksIgnored := []map[string]string{
		{
			"name": "mysql",
		},
		{
			"name":     "postgres",
			"instance": "host:* container:*",
		},
		{
			"name":     "apache",
			"instance": "container:*integration*",
		},
		{
			"name":     "nginx",
			"instance": "container:*",
		},
		{
			"name":     "redis",
			"instance": "host:*",
		},
		{
			"name":     "influxdb",
			"instance": "host:*",
		},
	}

	ignoredChecks := NewIgnoredCheck(checksIgnored)

	cases := []struct {
		nameContainer  NameContainer
		expectedResult bool
	}{
		{
			nameContainer: NameContainer{
				Name:          "rabbitmq",
				ContainerName: "",
			},
			expectedResult: false,
		},
		{
			nameContainer: NameContainer{
				Name:          "rabbitmq",
				ContainerName: "random-value",
			},
			expectedResult: false,
		},
		{
			nameContainer: NameContainer{
				Name:          "mysql",
				ContainerName: "",
			},
			expectedResult: true,
		},
		{
			nameContainer: NameContainer{
				Name:          "mysql",
				ContainerName: "container-name",
			},
			expectedResult: true,
		},
		{
			nameContainer: NameContainer{
				Name:          "mysql",
				ContainerName: "something",
			},
			expectedResult: true,
		},
		{
			nameContainer: NameContainer{
				Name:          "postgres",
				ContainerName: "",
			},
			expectedResult: true,
		},
		{
			nameContainer: NameContainer{
				Name:          "postgres",
				ContainerName: "random-value",
			},
			expectedResult: true,
		},
		{
			nameContainer: NameContainer{
				Name:          "apache",
				ContainerName: "",
			},
			expectedResult: false,
		},
		{
			nameContainer: NameContainer{
				Name:          "apache",
				ContainerName: "container-name",
			},
			expectedResult: false,
		},
		{
			nameContainer: NameContainer{
				Name:          "apache",
				ContainerName: "container-integration",
			},
			expectedResult: true,
		},
		{
			nameContainer: NameContainer{
				Name:          "apache",
				ContainerName: "integration-container",
			},
			expectedResult: true,
		},
		{
			nameContainer: NameContainer{
				Name:          "apache",
				ContainerName: "test-integration-container",
			},
			expectedResult: true,
		},
		{
			nameContainer: NameContainer{
				Name:          "nginx",
				ContainerName: "",
			},
			expectedResult: false,
		},
		{
			nameContainer: NameContainer{
				Name:          "nginx",
				ContainerName: "random-value",
			},
			expectedResult: true,
		},
		{
			nameContainer: NameContainer{
				Name:          "redis",
				ContainerName: "",
			},
			expectedResult: true,
		},
		{
			nameContainer: NameContainer{
				Name:          "redis",
				ContainerName: "random-value",
			},
			expectedResult: false,
		},
		{
			nameContainer: NameContainer{
				Name:          "influxdb",
				ContainerName: "influxdb",
			},
			expectedResult: false,
		},
	}

	for i, c := range cases {
		result := ignoredChecks.IsCheckIgnored(c.nameContainer)
		if result != c.expectedResult {
			t.Errorf("%v ignoredChecks.IsCheckIgnored(%v) == '%v', want '%v'", i, c.nameContainer, result, c.expectedResult)
		}
	}
}
