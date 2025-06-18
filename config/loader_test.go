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

package config

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	yamlParser "github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/file"
)

// Test that the items are loaded with the right type.
func TestLoader(t *testing.T) {
	const path = "testdata/loader.conf"

	loader := configLoader{}

	err := loader.Load(path, file.Provider(path), yamlParser.Parser())
	if err != nil {
		t.Fatalf("Failed to load config: %s", err)
	}

	expected := []Item{
		{
			Key:      "blackbox.enable",
			Value:    true,
			Type:     TypeBool,
			Source:   SourceFile,
			Path:     path,
			Priority: 1,
		},
		{
			Key:      "blackbox.modules.mymodule.prober",
			Value:    "http",
			Type:     TypeString,
			Source:   SourceFile,
			Path:     path,
			Priority: 1,
		},
		{
			Key:      "blackbox.modules.mymodule.timeout",
			Value:    float64(5 * time.Second),
			Source:   SourceFile,
			Type:     TypeInt,
			Path:     path,
			Priority: 1,
		},
		{
			Key: "blackbox.modules.mymodule.http.valid_status_codes",
			Value: []any{
				200.0,
			},
			Type:     TypeListInt,
			Source:   SourceFile,
			Path:     path,
			Priority: 1,
		},
		{
			Key: "blackbox.targets",
			Value: []any{
				map[string]any{
					"module": "mymodule",
					"name":   "myname",
					"url":    "https://bleemeo.com",
				},
			},
			Type:     TypeBlackboxTargets,
			Source:   SourceFile,
			Path:     path,
			Priority: 1,
		},
		{
			Key:      "bleemeo.enable",
			Value:    true,
			Type:     TypeBool,
			Source:   SourceFile,
			Path:     path,
			Priority: 1,
		},
		{
			Key: "disk_monitor",
			Value: []any{
				"sda",
			},
			Type:     TypeListString,
			Source:   SourceFile,
			Path:     path,
			Priority: 1,
		},
		{
			Key: "metric.softstatus_period",
			Value: map[string]any{
				"cpu_used": 60.0,
			},
			Type:     TypeMapStrInt,
			Source:   SourceFile,
			Path:     path,
			Priority: 1,
		},
		{
			Key: "metric.prometheus.targets",
			Value: []any{
				map[string]any{
					"allow_metrics": nil,
					"deny_metrics":  nil,
					"name":          "my_app",
					"url":           "http://localhost:8080/metrics",
				},
			},
			Type:     TypePrometheusTargets,
			Source:   SourceFile,
			Path:     path,
			Priority: 1,
		},
		{
			Key:      "metric.snmp.exporter_address",
			Value:    "localhost",
			Type:     TypeString,
			Source:   SourceFile,
			Path:     path,
			Priority: 1,
		},
		{
			Key: "metric.snmp.targets",
			Value: []any{
				map[string]any{
					"initial_name": "AP Wifi",
					"target":       "127.0.0.1",
				},
			},
			Type:     TypeSNMPTargets,
			Source:   SourceFile,
			Path:     path,
			Priority: 1,
		},
		{
			Key: "service",
			Value: []any{
				map[string]any{
					"address":             "",
					"tags":                nil,
					"ca_file":             "",
					"http_host":           "",
					"nagios_nrpe_name":    "",
					"password":            "",
					"ssl":                 false,
					"ssl_insecure":        false,
					"included_items":      nil,
					"jmx_metrics":         []any{},
					"match_process":       "",
					"starttls":            false,
					"stats_url":           "",
					"cert_file":           "",
					"detailed_items":      nil,
					"http_status_code":    0.0,
					"interval":            0.0,
					"jmx_port":            0.0,
					"metrics_unix_socket": "",
					"stats_protocol":      "",
					"check_type":          "",
					"ignore_ports":        nil,
					"type":                "service1",
					"instance":            "instance1",
					"port":                0.0,
					"stats_port":          0.0,
					"check_command":       "",
					"jmx_password":        "",
					"excluded_items":      nil,
					"http_path":           "",
					"jmx_username":        "",
					"key_file":            "",
					"username":            "",
					"log_files":           []any{},
					"log_format":          "",
				},
			},
			Type:     TypeServices,
			Source:   SourceFile,
			Path:     path,
			Priority: 1,
		},
		{
			Key: "service_ignore_check",
			Value: []any{
				map[string]any{
					"instance": "host:* container:*",
					"name":     "postgresql",
				},
			},
			Type:     TypeNameInstances,
			Source:   SourceFile,
			Path:     path,
			Priority: 1,
		},
		{
			Key: "service_ignore_metrics",
			Value: []any{
				map[string]any{
					"instance": "host:*",
					"name":     "redis",
				},
			},
			Type:     TypeNameInstances,
			Source:   SourceFile,
			Path:     path,
			Priority: 1,
		},
		{
			Key: "thresholds",
			Value: map[string]any{
				"cpu_used": map[string]any{
					"high_critical": 90.0,
					"high_warning":  nil,
					"low_critical":  nil,
					"low_warning":   nil,
				},
			},
			Type:     TypeThresholds,
			Source:   SourceFile,
			Path:     path,
			Priority: 1,
		},
		{
			Key: "log.inputs",
			Value: []any{
				map[string]any{
					"container_name":      "",
					"container_selectors": map[string]any{"com.docker.compose.service": "cassandra"},
					"filters": []any{
						map[string]any{
							"metric": "cassandra_logs_count",
							"regex":  ".*",
						},
					},
					"path": "",
				},
			},
			Type:     TypeLogInputs,
			Source:   SourceFile,
			Path:     path,
			Priority: 1,
		},
	}

	lessFunc := func(x Item, y Item) bool {
		return x.Key < y.Key
	}

	if diff := cmp.Diff(expected, loader.items, cmpopts.SortSlices(lessFunc)); diff != "" {
		t.Fatalf("diff:\n%s", diff)
	}
}

func TestIsNil(t *testing.T) {
	cases := []struct {
		value    any
		expected bool
	}{
		{
			value:    nil,
			expected: true,
		},
		{
			value:    any(nil),
			expected: true,
		},
		{
			value:    []string(nil),
			expected: true,
		},
		{
			value:    []string{},
			expected: false,
		},
		{
			value:    "",
			expected: false,
		},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("%#v", tc.value), func(t *testing.T) {
			t.Parallel()

			result := isNil(tc.value)
			if result != tc.expected {
				t.Fatalf("Unexpected result for isNil(%#v): want %t, got %t", tc.value, tc.expected, result)
			}
		})
	}
}
