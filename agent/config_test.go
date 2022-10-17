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
	"net/url"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
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

func Test_loadConfiguration(t *testing.T) { //nolint:maintidx
	URLMustParse := func(raw string) *url.URL {
		u, err := url.Parse(raw)
		if err != nil {
			panic(err)
		}

		return u
	}

	tests := []struct {
		name        string
		configFiles []string
		envs        map[string]string
		wantKeys    map[string]interface{}
		absentKeys  []string
		wantErr     bool
		warnings    []string
		wantCfg     Config
	}{
		{
			name: "migration file",
			configFiles: []string{
				"testdata/old-prometheus-targets.conf",
			},
			absentKeys: []string{"metric.prometheus.test1"},
			wantKeys: map[string]interface{}{
				"metric.prometheus.targets": []interface{}{
					map[string]interface{}{
						"name": "test1",
						"url":  "http://localhost:9090/metrics",
					},
				},
			},
			warnings: []string{
				"setting is deprecated: metrics.prometheus. See https://docs.bleemeo.com/metrics-sources/prometheus",
			},
			wantCfg: Config{
				SNMP: SNMP{ExporterURL: URLMustParse("http://localhost:9116/snmp")},
				Container: Container{
					Runtime: ContainerRuntime{
						Docker: ContainerRuntimeAddresses{
							Addresses: []string{
								"",
								"unix:///run/docker.sock",
								"unix:///var/run/docker.sock",
							},
							DisablePrefixHostRoot: false,
						},
						ContainerD: ContainerRuntimeAddresses{
							Addresses: []string{
								"/run/containerd/containerd.sock",
								"/run/k3s/containerd/containerd.sock",
							},
							DisablePrefixHostRoot: false,
						},
					},
				},
			},
		},
		{
			name: "new file",
			configFiles: []string{
				"testdata/new-prometheus-targets.conf",
			},
			absentKeys: []string{"metric.prometheus.test1"},
			wantKeys: map[string]interface{}{
				"metric.prometheus.targets": []interface{}{
					map[string]interface{}{
						"name": "test1",
						"url":  "http://localhost:9090/metrics",
					},
				},
			},
			warnings: nil,
			wantCfg: Config{
				SNMP: SNMP{ExporterURL: URLMustParse("http://localhost:9116/snmp")},
				Container: Container{
					Runtime: ContainerRuntime{
						Docker: ContainerRuntimeAddresses{
							Addresses: []string{
								"",
								"unix:///run/docker.sock",
								"unix:///var/run/docker.sock",
							},
							DisablePrefixHostRoot: false,
						},
						ContainerD: ContainerRuntimeAddresses{
							Addresses: []string{
								"/run/containerd/containerd.sock",
								"/run/k3s/containerd/containerd.sock",
							},
							DisablePrefixHostRoot: false,
						},
					},
				},
			},
		},
		{
			name: "services",
			configFiles: []string{
				"testdata/services.conf.d",
			},
			warnings: []string{
				"invalid config value: a key \"id\" is missing in one of your service override",
				"invalid config value: service id \" not fixable@\" can only contains letters, digits and underscore",
				"invalid config value: service id \"custom-bad.name\" can not contains dot (.) or dash (-). Changed to \"custom_bad_name\"",
			},
			wantCfg: Config{
				Services: Services{
					{
						ID:       "apache",
						Instance: "",
						ExtraAttribute: map[string]string{
							"port":      "80",
							"address":   "127.0.0.1",
							"http_path": "/",
							"http_host": "127.0.0.1:80",
						},
					},
					{
						ID:       "apache",
						Instance: "CONTAINER_NAME",
						ExtraAttribute: map[string]string{
							"port":      "80",
							"address":   "172.17.0.2",
							"http_path": "/",
							"http_host": "127.0.0.1:80",
						},
					},
					{
						ID: "myapplication",
						ExtraAttribute: map[string]string{
							"port":          "8080",
							"check_type":    "nagios",
							"check_command": "command-to-run",
						},
					},
					{
						ID: "custom_webserver",
						ExtraAttribute: map[string]string{
							"port":       "8181",
							"check_type": "http",
						},
					},
					{
						ID: "custom_bad_name",
						ExtraAttribute: map[string]string{
							"check_type":    "nagios",
							"check_command": "azerty",
						},
					},
				},
				SNMP: SNMP{ExporterURL: URLMustParse("http://localhost:9116/snmp")},
				Container: Container{
					Runtime: ContainerRuntime{
						Docker: ContainerRuntimeAddresses{
							Addresses: []string{
								"",
								"unix:///run/docker.sock",
								"unix:///var/run/docker.sock",
							},
							DisablePrefixHostRoot: false,
						},
						ContainerD: ContainerRuntimeAddresses{
							Addresses: []string{
								"/run/containerd/containerd.sock",
								"/run/k3s/containerd/containerd.sock",
							},
							DisablePrefixHostRoot: false,
						},
					},
				},
			},
		},
		{
			name: "enabled renamed",
			configFiles: []string{
				"testdata/enabled.conf",
			},
			absentKeys: []string{"agent.windows_exporter.enabled", "telegraf.docker_metrics_enabled", "web.enabled"},
			wantKeys: map[string]interface{}{
				"agent.windows_exporter.enable":  true,
				"telegraf.docker_metrics_enable": true,
			},
			warnings: []string{
				"setting is deprecated: agent.windows_exporter.enabled. Please use agent.windows_exporter.enable",
				"setting is deprecated: telegraf.docker_metrics_enabled. Please use telegraf.docker_metrics_enable",
			},
			wantCfg: Config{
				SNMP: SNMP{ExporterURL: URLMustParse("http://localhost:9116/snmp")},
				Container: Container{
					Runtime: ContainerRuntime{
						Docker: ContainerRuntimeAddresses{
							Addresses: []string{
								"",
								"unix:///run/docker.sock",
								"unix:///var/run/docker.sock",
							},
							DisablePrefixHostRoot: false,
						},
						ContainerD: ContainerRuntimeAddresses{
							Addresses: []string{
								"/run/containerd/containerd.sock",
								"/run/k3s/containerd/containerd.sock",
							},
							DisablePrefixHostRoot: false,
						},
					},
				},
			},
		},
		{
			name: "folder",
			configFiles: []string{
				"testdata/folder1",
			},
			absentKeys: []string{"bleemeo.enabled"},
			wantKeys: map[string]interface{}{
				"bleemeo.enable":     false,
				"bleemeo.account_id": "second",
			},
			warnings: []string{
				"setting is deprecated: bleemeo.enabled. Please use bleemeo.enable",
			},
			wantCfg: Config{
				SNMP: SNMP{ExporterURL: URLMustParse("http://localhost:9116/snmp")},
				Container: Container{
					Runtime: ContainerRuntime{
						Docker: ContainerRuntimeAddresses{
							Addresses: []string{
								"",
								"unix:///run/docker.sock",
								"unix:///var/run/docker.sock",
							},
							DisablePrefixHostRoot: false,
						},
						ContainerD: ContainerRuntimeAddresses{
							Addresses: []string{
								"/run/containerd/containerd.sock",
								"/run/k3s/containerd/containerd.sock",
							},
							DisablePrefixHostRoot: false,
						},
					},
				},
			},
		},
		{
			name: "deprecated envs",
			configFiles: []string{
				"testdata/empty.conf",
			},
			envs: map[string]string{
				"BLEEMEO_AGENT_ACCOUNT":      "the-account-id",
				"GLOUTON_KUBERNETES_ENABLED": "true",
			},
			absentKeys: []string{"kubernetes.enabled"},
			wantKeys: map[string]interface{}{
				"bleemeo.account_id": "the-account-id",
				"kubernetes.enable":  true,
			},
			warnings: []string{
				"environment variable is deprecated: BLEEMEO_AGENT_ACCOUNT, use GLOUTON_BLEEMEO_ACCOUNT_ID instead",
				"environment variable is deprecated: GLOUTON_KUBERNETES_ENABLED, use GLOUTON_KUBERNETES_ENABLE instead",
			},
			wantCfg: Config{
				SNMP: SNMP{ExporterURL: URLMustParse("http://localhost:9116/snmp")},
				Container: Container{
					Runtime: ContainerRuntime{
						Docker: ContainerRuntimeAddresses{
							Addresses: []string{
								"",
								"unix:///run/docker.sock",
								"unix:///var/run/docker.sock",
							},
							DisablePrefixHostRoot: false,
						},
						ContainerD: ContainerRuntimeAddresses{
							Addresses: []string{
								"/run/containerd/containerd.sock",
								"/run/k3s/containerd/containerd.sock",
							},
							DisablePrefixHostRoot: false,
						},
					},
				},
			},
		},
		{
			name: "bleemeo-agent envs",
			configFiles: []string{
				"testdata/empty.conf",
			},
			envs: map[string]string{
				"BLEEMEO_AGENT_KUBERNETES_ENABLE": "true",
				"BLEEMEO_AGENT_BLEEMEO_ENABLED":   "true",
			},
			absentKeys: []string{"kubernetes.enabled", "bleemeo.enabled"},
			wantKeys: map[string]interface{}{
				"bleemeo.enable":    true,
				"kubernetes.enable": true,
			},
			warnings: []string{
				"environment variable is deprecated: BLEEMEO_AGENT_KUBERNETES_ENABLE, use GLOUTON_KUBERNETES_ENABLE instead",
				"environment variable is deprecated: BLEEMEO_AGENT_BLEEMEO_ENABLED, use GLOUTON_BLEEMEO_ENABLE instead",
			},
			wantCfg: Config{
				SNMP: SNMP{ExporterURL: URLMustParse("http://localhost:9116/snmp")},
				Container: Container{
					Runtime: ContainerRuntime{
						Docker: ContainerRuntimeAddresses{
							Addresses: []string{
								"",
								"unix:///run/docker.sock",
								"unix:///var/run/docker.sock",
							},
							DisablePrefixHostRoot: false,
						},
						ContainerD: ContainerRuntimeAddresses{
							Addresses: []string{
								"/run/containerd/containerd.sock",
								"/run/k3s/containerd/containerd.sock",
							},
							DisablePrefixHostRoot: false,
						},
					},
				},
			},
		},
		{
			name: "old-logging",
			configFiles: []string{
				"testdata/old-logging.conf",
			},
			wantKeys: map[string]interface{}{
				"logging.buffer.head_size_bytes": 4200,
				"logging.buffer.tail_size_bytes": 4800,
			},
			absentKeys: []string{"logging.buffer.head_size", "logging.buffer.tail_size"},
			warnings: []string{
				"setting is deprecated: logging.buffer.head_size. Please use logging.buffer.head_size_bytes",
				"setting is deprecated: logging.buffer.tail_size. Please use logging.buffer.tail_size_bytes",
			},
			wantCfg: Config{
				SNMP: SNMP{ExporterURL: URLMustParse("http://localhost:9116/snmp")},
				Container: Container{
					Runtime: ContainerRuntime{
						Docker: ContainerRuntimeAddresses{
							Addresses: []string{
								"",
								"unix:///run/docker.sock",
								"unix:///var/run/docker.sock",
							},
							DisablePrefixHostRoot: false,
						},
						ContainerD: ContainerRuntimeAddresses{
							Addresses: []string{
								"/run/containerd/containerd.sock",
								"/run/k3s/containerd/containerd.sock",
							},
							DisablePrefixHostRoot: false,
						},
					},
				},
			},
		},
		{
			name: "config-1",
			configFiles: []string{
				"testdata/config1.conf",
			},
			warnings: nil,
			wantCfg: Config{
				SNMP: SNMP{ExporterURL: URLMustParse("http://localhost:9116/snmp")},
				Container: Container{
					DisabledByDefault: true,
					AllowPatternList: []string{
						"bleemeo_*",
					},
					DenyPatternList: []string{
						"bleemeo_ephemeral",
						"bleemeo_builder",
					},
					Runtime: ContainerRuntime{
						Docker: ContainerRuntimeAddresses{
							Addresses: []string{
								"",
								"unix:///run/docker.sock",
								"unix:///var/run/docker.sock",
							},
							DisablePrefixHostRoot: false,
						},
						ContainerD: ContainerRuntimeAddresses{
							Addresses: []string{
								"/run/containerd/containerd.sock",
								"/run/k3s/containerd/containerd.sock",
							},
							DisablePrefixHostRoot: false,
						},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lookupEnv := func(envName string) (string, bool) {
				value, ok := tt.envs[envName]

				return value, ok
			}

			cfg, oldCfg, warnings, err := loadConfiguration(tt.configFiles, lookupEnv)
			if (err != nil) != tt.wantErr {
				t.Errorf("loadConfiguration() error = %v, wantErr %v", err, tt.wantErr)

				return
			}

			warningsString := make([]string, 0, len(warnings))

			for _, v := range warnings {
				warningsString = append(warningsString, v.Error())
			}

			lessString := func(x string, y string) bool {
				return x < y
			}

			if diff := cmp.Diff(warningsString, tt.warnings, cmpopts.SortSlices(lessString), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("warnings: %s", diff)
			}

			for _, key := range tt.absentKeys {
				if v, ok := oldCfg.Get(key); ok {
					t.Errorf("Get(%v) = %v, want absent", key, v)
				}
			}

			for key, want := range tt.wantKeys {
				if got, ok := oldCfg.Get(key); !ok {
					t.Errorf("Get(%v) = nil, want present", key)
				} else if !reflect.DeepEqual(got, want) {
					t.Errorf("Get(%v) = %#v, want %#v", key, got, want)
				}
			}

			if diff := cmp.Diff(tt.wantCfg, cfg, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("config mismatch (-want +got)\n%s", diff)
			}
		})
	}
}
