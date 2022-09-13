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

func TestIsCheckIgnored(t *testing.T) { //nolint:maintidx
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
		service        Service
		expectedResult bool
	}{
		{
			service: Service{
				Name:          "rabbitmq",
				Instance:      "",
				ContainerName: "",
			},
			expectedResult: false,
		},
		{
			service: Service{
				Name:          "rabbitmq",
				Instance:      "random-value",
				ContainerName: "random-value",
			},
			expectedResult: false,
		},
		{
			service: Service{
				Name:          "mysql",
				Instance:      "",
				ContainerName: "",
			},
			expectedResult: true,
		},
		{
			service: Service{
				Name:          "mysql",
				Instance:      "container-name",
				ContainerName: "container-name",
			},
			expectedResult: true,
		},
		{
			service: Service{
				Name:          "mysql",
				Instance:      "something",
				ContainerName: "something",
			},
			expectedResult: true,
		},
		{
			service: Service{
				Name:          "postgres",
				Instance:      "",
				ContainerName: "",
			},
			expectedResult: true,
		},
		{
			service: Service{
				Name:          "postgres",
				Instance:      "random-value",
				ContainerName: "random-value",
			},
			expectedResult: true,
		},
		{
			service: Service{
				Name:          "apache",
				Instance:      "",
				ContainerName: "",
			},
			expectedResult: false,
		},
		{
			service: Service{
				Name:          "apache",
				Instance:      "container-name",
				ContainerName: "container-name",
			},
			expectedResult: false,
		},
		{
			service: Service{
				Name:          "apache",
				Instance:      "container-integration",
				ContainerName: "container-integration",
			},
			expectedResult: true,
		},
		{
			service: Service{
				Name:          "apache",
				Instance:      "integration-container",
				ContainerName: "integration-container",
			},
			expectedResult: true,
		},
		{
			service: Service{
				Name:          "apache",
				Instance:      "test-integration-container",
				ContainerName: "test-integration-container",
			},
			expectedResult: true,
		},
		{
			service: Service{
				Name:          "nginx",
				Instance:      "",
				ContainerName: "",
			},
			expectedResult: false,
		},
		{
			service: Service{
				Name:          "nginx",
				Instance:      "random-value",
				ContainerName: "random-value",
			},
			expectedResult: true,
		},
		{
			service: Service{
				Name:          "redis",
				Instance:      "",
				ContainerName: "",
			},
			expectedResult: true,
		},
		{
			service: Service{
				Name:          "redis",
				Instance:      "random-value",
				ContainerName: "random-value",
			},
			expectedResult: false,
		},
		{
			service: Service{
				Name:          "influxdb",
				Instance:      "influxdb",
				ContainerName: "influxdb",
			},
			expectedResult: false,
		},
		{
			service: Service{
				Name:          "prefix",
				Instance:      "name-prefix",
				ContainerName: "name-prefix",
			},
			expectedResult: true,
		},
		{
			service: Service{
				Name:          "prefix",
				Instance:      "name-prefixSomething",
				ContainerName: "name-prefixSomething",
			},
			expectedResult: true,
		},
		{
			service: Service{
				Name:          "prefix",
				Instance:      "Something-name-prefix",
				ContainerName: "Something-name-prefix",
			},
			expectedResult: false,
		},
		{
			service: Service{
				Name:          "suffix",
				Instance:      "123-name-suffix",
				ContainerName: "123-name-suffix",
			},
			expectedResult: true,
		},
		{
			service: Service{
				Name:          "suffix",
				Instance:      "name-suffix",
				ContainerName: "name-suffix",
			},
			expectedResult: true,
		},
		{
			service: Service{
				Name:          "suffix",
				Instance:      "name-suffix123",
				ContainerName: "name-suffix123",
			},
			expectedResult: false,
		},
		{
			service: Service{
				Name:          "prefix-suffix",
				Instance:      "starts-with-###-end-withs",
				ContainerName: "starts-with-###-end-withs",
			},
			expectedResult: true,
		},
		{
			service: Service{
				Name:          "prefix-suffix",
				Instance:      "starts-with--end-withs",
				ContainerName: "starts-with--end-withs",
			},
			expectedResult: true,
		},
		{
			service: Service{
				Name:          "prefix-suffix",
				Instance:      "Astarts-with-###-end-withs",
				ContainerName: "Astarts-with-###-end-withs",
			},
			expectedResult: false,
		},
		{
			service: Service{
				Name:          "prefix-suffix",
				Instance:      "starts-with-###-end-withsB",
				ContainerName: "starts-with-###-end-withsB",
			},
			expectedResult: false,
		},
		{
			service: Service{
				Name:          "two-placeholder",
				Instance:      "web-",
				ContainerName: "web-",
			},
			expectedResult: false,
		},
		{
			service: Service{
				Name:          "two-placeholder",
				Instance:      "web-1",
				ContainerName: "web-1",
			},
			expectedResult: false,
		},
		{
			service: Service{
				Name:          "two-placeholder",
				Instance:      "web-01",
				ContainerName: "web-01",
			},
			expectedResult: true,
		},
		{
			service: Service{
				Name:          "two-placeholder",
				Instance:      "web-001",
				ContainerName: "web-001",
			},
			expectedResult: false,
		},
		{
			service: Service{
				Name:          "not-in-the-list",
				Instance:      "does-matter",
				ContainerName: "does-matter",
			},
			expectedResult: false,
		},
		{
			service: Service{
				Name:          "fixed-hostname",
				Instance:      "web.example.com",
				ContainerName: "web.example.com",
			},
			expectedResult: true,
		},
		{
			service: Service{
				Name:          "fixed-hostname",
				Instance:      "web-example-com",
				ContainerName: "web-example-com",
			},
			expectedResult: false,
		},
	}

	for i, c := range cases {
		result := ignoredChecks.IsServiceIgnored(c.service)
		if result != c.expectedResult {
			t.Errorf("%v ignoredChecks.IsCheckIgnored(%v) == '%v', want '%v'", i, c.service, result, c.expectedResult)
		}
	}
}
