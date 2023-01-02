// Copyright 2015-2022 Bleemeo
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
	"testing"

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
			Key: "blackbox.targets",
			Value: []interface{}{
				map[string]interface{}{
					"module": "mymodule",
					"name":   "myname",
					"url":    "https://bleemeo.com",
				},
			},
			Type:     TypeListUnknown,
			Source:   SourceFile,
			Path:     path,
			Priority: 1,
		},
		{
			Key: "blackbox.modules.mymodule.http.valid_status_codes",
			Value: []interface{}{
				200.0,
			},
			Type:     TypeListInt,
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
			Value: []interface{}{
				"sda",
			},
			Type:     TypeListString,
			Source:   SourceFile,
			Path:     path,
			Priority: 1,
		},
		{
			Key:      "influxdb.port",
			Value:    8086.0,
			Type:     TypeInt,
			Source:   SourceFile,
			Path:     path,
			Priority: 1,
		},
		{
			Key: "metric.softstatus_period",
			Value: map[string]interface{}{
				"cpu_used": 60.0,
			},
			Type:     TypeMapStrInt,
			Source:   SourceFile,
			Path:     path,
			Priority: 1,
		},
		{
			Key: "service",
			Value: []interface{}{
				map[string]interface{}{
					"address":             "",
					"ca_file":             "",
					"http_host":           "",
					"nagios_nrpe_name":    "",
					"password":            "",
					"ssl":                 false,
					"ssl_insecure":        false,
					"included_items":      nil,
					"jmx_metrics":         []interface{}{},
					"match_process":       "",
					"stack":               "",
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
					"id":                  "service1",
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
				},
			},
			Type:     TypeListUnknown,
			Source:   SourceFile,
			Path:     path,
			Priority: 1,
		},
		{
			Key: "thresholds",
			Value: map[string]interface{}{
				"cpu_used": map[string]interface{}{
					"high_critical": 90.0,
					"high_warning":  nil,
					"low_critical":  nil,
					"low_warning":   nil,
				},
			},
			Type:     TypeMapStrUnknown,
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
