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

//nolint:scopelint
package agent

import (
	"glouton/config"
	"reflect"
	"testing"
)

func Test_confFieldToSliceMap(t *testing.T) {
	var confInterface1 interface{} = []interface{}{
		map[interface{}]interface{}{
			"name": "mysql",
		},
		map[interface{}]interface{}{
			"name":     "postgres",
			"instance": "host:* container:*",
		},
		map[interface{}]interface{}{
			"id":            "myapplication",
			"port":          "8080",
			"check_type":    "nagios",
			"check_command": "command-to-run",
		},
		map[interface{}]interface{}{
			"id":         "custom_webserver",
			"port":       "8181",
			"check_type": "http",
		},
	}

	var confInterface2 interface{} = []interface{}{
		map[interface{}]interface{}{
			"name":     "postgres",
			"instance": "host:* container:*",
		},
		"wrong type",
		42,
	}

	var confInterfaceWithNested interface{} = []interface{}{
		map[interface{}]interface{}{
			"id":       "cassandra",
			"instance": "squirreldb-cassandra",
			"jmx_port": 7999,
			"cassandra_detailed_tables": []string{
				"squirreldb.data",
			},
			"jmx_metrics": []map[string]interface{}{
				{
					"name":  "heap_size_mb",
					"mbean": "java.lang:type=Memory",
					"scale": 42,
				},
			},
		},
	}

	type args struct {
		input    interface{}
		confType string
	}

	tests := []struct {
		name string
		args args
		want []map[string]string
	}{
		{
			name: "test1",
			args: args{
				input:    confInterface1,
				confType: "service_ignore_check",
			},
			want: []map[string]string{
				{
					"name": "mysql",
				},
				{
					"name":     "postgres",
					"instance": "host:* container:*",
				},
				{
					"id":            "myapplication",
					"port":          "8080",
					"check_type":    "nagios",
					"check_command": "command-to-run",
				},
				{
					"id":         "custom_webserver",
					"port":       "8181",
					"check_type": "http",
				},
			},
		},
		{
			name: "test2",
			args: args{
				input:    confInterface2,
				confType: "service_ignore_check",
			},
			want: []map[string]string{
				{
					"name":     "postgres",
					"instance": "host:* container:*",
				},
			},
		},
		{
			name: "test-nested",
			args: args{
				input:    confInterfaceWithNested,
				confType: "service",
			},
			want: []map[string]string{
				{
					"id":                        "cassandra",
					"instance":                  "squirreldb-cassandra",
					"jmx_port":                  "7999",
					"cassandra_detailed_tables": "[\"squirreldb.data\"]",
					"jmx_metrics":               `[{"mbean":"java.lang:type=Memory","name":"heap_size_mb","scale":42}]`,
				},
			},
		},
		{
			name: "test3",
			args: args{
				input:    "failed test",
				confType: "",
			},
			want: nil,
		},
		{
			name: "test4",
			args: args{
				input:    []interface{}{"first", "second"},
				confType: "",
			},
			want: []map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			//nolint:scopelint
			if got := confFieldToSliceMap(tt.args.input, tt.args.confType); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("confFieldToSliceMap() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_migrate(t *testing.T) {
	tests := []struct {
		name        string
		cfgFilename string
		wantKeys    map[string]interface{}
		absentKeys  []string
	}{
		{
			name:        "new-prometheus-targets",
			cfgFilename: "testdata/new-prometheus-targets.conf",
			wantKeys: map[string]interface{}{
				"metric.prometheus.targets": []interface{}{
					map[string]interface{}{
						"name": "test1",
						"url":  "http://localhost:9090/metrics",
					},
				},
			},
		},
		{
			name:        "old-prometheus-targets",
			cfgFilename: "testdata/old-prometheus-targets.conf",
			wantKeys: map[string]interface{}{
				"metric.prometheus.targets": []interface{}{
					map[string]interface{}{
						"name": "test1",
						"url":  "http://localhost:9090/metrics",
					},
				},
			},
			absentKeys: []string{"metric.prometheus.test1"},
		},
		{
			name:        "both-prometheus-targets",
			cfgFilename: "testdata/both-prometheus-targets.conf",
			wantKeys: map[string]interface{}{
				"metric.prometheus.targets": []interface{}{
					map[string]interface{}{
						"name": "new",
						"url":  "http://new:9090/metrics",
					},
					map[string]interface{}{
						"name": "old",
						"url":  "http://old:9090/metrics",
					},
				},
			},
			absentKeys: []string{"metric.prometheus.old"},
		},
		{
			name:        "old-prometheus-allow/deny_metrics",
			cfgFilename: "testdata/old-prometheus-metrics.conf",
			wantKeys: map[string]interface{}{
				"metric.allow_metrics": []interface{}{
					"test4",
					"test1",
					"test2",
				},
				"metric.deny_metrics": []interface{}{
					"test5",
					"test3",
				},
			},
			absentKeys: []string{"metric.prometheus.allow_metrics", "metric.prometheus.deny_metrics"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Configuration{}

			if err := configLoadFile(tt.cfgFilename, cfg); err != nil {
				t.Error(err)
			}

			for _, key := range tt.absentKeys {
				if _, ok := cfg.Get(key); !ok {
					t.Errorf("Get(%v) = nil, want to exists before migrate()", key)
				}
			}

			_ = migrate(cfg)

			for _, key := range tt.absentKeys {
				if v, ok := cfg.Get(key); ok {
					t.Errorf("Get(%v) = %v, want absent", key, v)
				}
			}

			for key, want := range tt.wantKeys {
				if got, ok := cfg.Get(key); !ok {
					t.Errorf("Get(%v) = nil, want present", key)
				} else if !reflect.DeepEqual(got, want) {
					t.Errorf("Get(%v) = %#v, want %#v", key, got, want)
				}
			}
		})
	}
}
