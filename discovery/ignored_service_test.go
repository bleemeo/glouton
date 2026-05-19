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

package discovery

import (
	"testing"

	"github.com/bleemeo/glouton/config"
)

const (
	testIgnMySQL          = string(MySQLService)
	testIgnPostgres       = string(PostgreSQLService)
	testIgnInfluxDB       = string(InfluxDBService)
	testIgnRabbitMQ       = string(RabbitMQService)
	testIgnPrefix         = "prefix"
	testIgnSuffix         = "suffix"
	testIgnPrefixSuffix   = "prefix-suffix"
	testIgnTwoPlaceholder = "two-placeholder"
	testIgnFixedHostname  = "fixed-hostname"
	testIgnRandomValue    = "random-value"
	testIgnContainerName  = "container-name"
)

func TestIsServiceIgnored(t *testing.T) { //nolint:maintidx
	checksIgnored := []config.NameInstance{
		{
			Name: testIgnMySQL,
		},
		{
			Name:     testIgnPostgres,
			Instance: "host:* container:*",
		},
		{
			Name:     testApache,
			Instance: "container:*integration*",
		},
		{
			Name:     testNginx,
			Instance: "container:*",
		},
		{
			Name:     testRedis,
			Instance: "host:*",
		},
		{
			Name:     testIgnInfluxDB,
			Instance: "host:*",
		},
		{
			Name:     testIgnPrefix,
			Instance: "container:name-prefix*",
		},
		{
			Name:     testIgnSuffix,
			Instance: "container:*name-suffix",
		},
		{
			Name:     testIgnPrefixSuffix,
			Instance: "container:starts-with-*-end-withs",
		},
		{
			Name:     testIgnTwoPlaceholder,
			Instance: "container:web-??",
		},
		{
			Name:     testIgnFixedHostname,
			Instance: "container:web.example.com",
		},
	}

	ignoredChecks := NewIgnoredService(checksIgnored)

	cases := []struct {
		service        Service
		expectedResult bool
	}{
		{
			service: Service{
				Name:          testIgnRabbitMQ,
				Instance:      "",
				ContainerName: "",
			},
			expectedResult: false,
		},
		{
			service: Service{
				Name:          testIgnRabbitMQ,
				Instance:      testIgnRandomValue,
				ContainerName: testIgnRandomValue,
			},
			expectedResult: false,
		},
		{
			service: Service{
				Name:          testIgnMySQL,
				Instance:      "",
				ContainerName: "",
			},
			expectedResult: true,
		},
		{
			service: Service{
				Name:          testIgnMySQL,
				Instance:      testIgnContainerName,
				ContainerName: testIgnContainerName,
			},
			expectedResult: true,
		},
		{
			service: Service{
				Name:          testIgnMySQL,
				Instance:      "something",
				ContainerName: "something",
			},
			expectedResult: true,
		},
		{
			service: Service{
				Name:          testIgnPostgres,
				Instance:      "",
				ContainerName: "",
			},
			expectedResult: true,
		},
		{
			service: Service{
				Name:          testIgnPostgres,
				Instance:      testIgnRandomValue,
				ContainerName: testIgnRandomValue,
			},
			expectedResult: true,
		},
		{
			service: Service{
				Name:          testApache,
				Instance:      "",
				ContainerName: "",
			},
			expectedResult: false,
		},
		{
			service: Service{
				Name:          testApache,
				Instance:      testIgnContainerName,
				ContainerName: testIgnContainerName,
			},
			expectedResult: false,
		},
		{
			service: Service{
				Name:          testApache,
				Instance:      "container-integration",
				ContainerName: "container-integration",
			},
			expectedResult: true,
		},
		{
			service: Service{
				Name:          testApache,
				Instance:      "integration-container",
				ContainerName: "integration-container",
			},
			expectedResult: true,
		},
		{
			service: Service{
				Name:          testApache,
				Instance:      "test-integration-container",
				ContainerName: "test-integration-container",
			},
			expectedResult: true,
		},
		{
			service: Service{
				Name:          testNginx,
				Instance:      "",
				ContainerName: "",
			},
			expectedResult: false,
		},
		{
			service: Service{
				Name:          testNginx,
				Instance:      testIgnRandomValue,
				ContainerName: testIgnRandomValue,
			},
			expectedResult: true,
		},
		{
			service: Service{
				Name:          testRedis,
				Instance:      "",
				ContainerName: "",
			},
			expectedResult: true,
		},
		{
			service: Service{
				Name:          testRedis,
				Instance:      testIgnRandomValue,
				ContainerName: testIgnRandomValue,
			},
			expectedResult: false,
		},
		{
			service: Service{
				Name:          testIgnInfluxDB,
				Instance:      testIgnInfluxDB,
				ContainerName: testIgnInfluxDB,
			},
			expectedResult: false,
		},
		{
			service: Service{
				Name:          testIgnPrefix,
				Instance:      "name-prefix",
				ContainerName: "name-prefix",
			},
			expectedResult: true,
		},
		{
			service: Service{
				Name:          testIgnPrefix,
				Instance:      "name-prefixSomething",
				ContainerName: "name-prefixSomething",
			},
			expectedResult: true,
		},
		{
			service: Service{
				Name:          testIgnPrefix,
				Instance:      "Something-name-prefix",
				ContainerName: "Something-name-prefix",
			},
			expectedResult: false,
		},
		{
			service: Service{
				Name:          testIgnSuffix,
				Instance:      "123-name-suffix",
				ContainerName: "123-name-suffix",
			},
			expectedResult: true,
		},
		{
			service: Service{
				Name:          testIgnSuffix,
				Instance:      "name-suffix",
				ContainerName: "name-suffix",
			},
			expectedResult: true,
		},
		{
			service: Service{
				Name:          testIgnSuffix,
				Instance:      "name-suffix123",
				ContainerName: "name-suffix123",
			},
			expectedResult: false,
		},
		{
			service: Service{
				Name:          testIgnPrefixSuffix,
				Instance:      "starts-with-###-end-withs",
				ContainerName: "starts-with-###-end-withs",
			},
			expectedResult: true,
		},
		{
			service: Service{
				Name:          testIgnPrefixSuffix,
				Instance:      "starts-with--end-withs",
				ContainerName: "starts-with--end-withs",
			},
			expectedResult: true,
		},
		{
			service: Service{
				Name:          testIgnPrefixSuffix,
				Instance:      "Astarts-with-###-end-withs",
				ContainerName: "Astarts-with-###-end-withs",
			},
			expectedResult: false,
		},
		{
			service: Service{
				Name:          testIgnPrefixSuffix,
				Instance:      "starts-with-###-end-withsB",
				ContainerName: "starts-with-###-end-withsB",
			},
			expectedResult: false,
		},
		{
			service: Service{
				Name:          testIgnTwoPlaceholder,
				Instance:      "web-",
				ContainerName: "web-",
			},
			expectedResult: false,
		},
		{
			service: Service{
				Name:          testIgnTwoPlaceholder,
				Instance:      "web-1",
				ContainerName: "web-1",
			},
			expectedResult: false,
		},
		{
			service: Service{
				Name:          testIgnTwoPlaceholder,
				Instance:      "web-01",
				ContainerName: "web-01",
			},
			expectedResult: true,
		},
		{
			service: Service{
				Name:          testIgnTwoPlaceholder,
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
				Name:          testIgnFixedHostname,
				Instance:      "web.example.com",
				ContainerName: "web.example.com",
			},
			expectedResult: true,
		},
		{
			service: Service{
				Name:          testIgnFixedHostname,
				Instance:      "web-example-com",
				ContainerName: "web-example-com",
			},
			expectedResult: false,
		},
	}

	for i, c := range cases {
		result := ignoredChecks.IsServiceIgnored(c.service)
		if result != c.expectedResult {
			t.Errorf("%v ignoredChecks.IsServiceIgnored(%v) == '%v', want '%v'", i, c.service, result, c.expectedResult)
		}
	}
}
