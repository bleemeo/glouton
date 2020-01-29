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
		{
			"name":     "prefix",
			"instance": "container:name-prefix*",
		},
		{
			"name":     "suffix",
			"instance": "container:*name-suffix",
		},
		{
			"name":     "prefix-suffix",
			"instance": "container:starts-with-*-end-withs",
		},
		{
			"name":     "two-placeholder",
			"instance": "container:web-??",
		},
		{
			"name":     "fixed-hostname",
			"instance": "container:web.example.com",
		},
	}

	ignoredChecks := NewIgnoredService(checksIgnored)

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
		{
			nameContainer: NameContainer{
				Name:          "prefix",
				ContainerName: "name-prefix",
			},
			expectedResult: true,
		},
		{
			nameContainer: NameContainer{
				Name:          "prefix",
				ContainerName: "name-prefixSomething",
			},
			expectedResult: true,
		},
		{
			nameContainer: NameContainer{
				Name:          "prefix",
				ContainerName: "Something-name-prefix",
			},
			expectedResult: false,
		},
		{
			nameContainer: NameContainer{
				Name:          "suffix",
				ContainerName: "123-name-suffix",
			},
			expectedResult: true,
		},
		{
			nameContainer: NameContainer{
				Name:          "suffix",
				ContainerName: "name-suffix",
			},
			expectedResult: true,
		},
		{
			nameContainer: NameContainer{
				Name:          "suffix",
				ContainerName: "name-suffix123",
			},
			expectedResult: false,
		},
		{
			nameContainer: NameContainer{
				Name:          "prefix-suffix",
				ContainerName: "starts-with-###-end-withs",
			},
			expectedResult: true,
		},
		{
			nameContainer: NameContainer{
				Name:          "prefix-suffix",
				ContainerName: "starts-with--end-withs",
			},
			expectedResult: true,
		},
		{
			nameContainer: NameContainer{
				Name:          "prefix-suffix",
				ContainerName: "Astarts-with-###-end-withs",
			},
			expectedResult: false,
		},
		{
			nameContainer: NameContainer{
				Name:          "prefix-suffix",
				ContainerName: "starts-with-###-end-withsB",
			},
			expectedResult: false,
		},
		{
			nameContainer: NameContainer{
				Name:          "two-placeholder",
				ContainerName: "web-",
			},
			expectedResult: false,
		},
		{
			nameContainer: NameContainer{
				Name:          "two-placeholder",
				ContainerName: "web-1",
			},
			expectedResult: false,
		},
		{
			nameContainer: NameContainer{
				Name:          "two-placeholder",
				ContainerName: "web-01",
			},
			expectedResult: true,
		},
		{
			nameContainer: NameContainer{
				Name:          "two-placeholder",
				ContainerName: "web-001",
			},
			expectedResult: false,
		},
		{
			nameContainer: NameContainer{
				Name:          "not-in-the-list",
				ContainerName: "does-matter",
			},
			expectedResult: false,
		},
		{
			nameContainer: NameContainer{
				Name:          "fixed-hostname",
				ContainerName: "web.example.com",
			},
			expectedResult: true,
		},
		{
			nameContainer: NameContainer{
				Name:          "fixed-hostname",
				ContainerName: "web-example-com",
			},
			expectedResult: false,
		},
	}

	for i, c := range cases {
		result := ignoredChecks.IsServiceIgnored(c.nameContainer)
		if result != c.expectedResult {
			t.Errorf("%v ignoredChecks.IsCheckIgnored(%v) == '%v', want '%v'", i, c.nameContainer, result, c.expectedResult)
		}
	}
}
