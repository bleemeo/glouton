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
			Value:    defaultHTTP,
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
					keyName:  "myname",
					keyURL:   "https://bleemeo.com",
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
				testCPUUsed: 60.0,
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
					keyName:         "my_app",
					keyURL:          "http://localhost:8080/metrics",
				},
			},
			Type:     TypePrometheusTargets,
			Source:   SourceFile,
			Path:     path,
			Priority: 1,
		},
		{
			Key:      "metric.snmp.exporter_address",
			Value:    DefaultLocalhost,
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
					"target":       DefaultLoopback,
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
					keyPassword:           "",
					"ssl":                 false,
					"ssl_insecure":        false,
					"included_items":      nil,
					"jmx_metrics":         []any{},
					"match_process":       "",
					"starttls":            false,
					"stats_url":           "",
					"cert_file":           "",
					keyDetailedItems:      nil,
					"http_status_code":    0.0,
					"interval":            0.0,
					"jmx_port":            0.0,
					"metrics_unix_socket": "",
					"stats_protocol":      "",
					"check_type":          "",
					"ignore_ports":        nil,
					keyType:               "service1",
					testInstance:          "instance1",
					"port":                0.0,
					keyStatsPort:          0.0,
					keyCheckCommand:       "",
					"jmx_password":        "",
					"excluded_items":      nil,
					"http_path":           "",
					"jmx_username":        "",
					"key_file":            "",
					"username":            "",
					"variant":             "",
					"log_files":           []any{},
					"log_filter":          "",
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
					testInstance: "host:* container:*",
					keyName:      "postgresql",
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
					testInstance: "host:*",
					keyName:      "redis",
				},
			},
			Type:     TypeNameInstances,
			Source:   SourceFile,
			Path:     path,
			Priority: 1,
		},
		{
			Key: keyThresholds,
			Value: map[string]any{
				testCPUUsed: map[string]any{
					"high_critical": 90.0,
				},
			},
			Type:     TypeThresholds,
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

// TestMergeRecursesIntoNestedMaps guards against a regression where merge()'s map case only shallow-merged
// (maps.Copy): when the same sub-key (e.g. a receiver name) existed on both sides, src's whole value
// replaced dst's instead of recursing, silently dropping whichever fields dst had that src didn't repeat.
func TestMergeRecursesIntoNestedMaps(t *testing.T) {
	t.Parallel()

	dst := map[string]any{
		"myrecv": map[string]any{"include": []any{"/var/log/app.log"}},
	}
	src := map[string]any{
		"myrecv": map[string]any{"send_logs": false},
	}

	got, err := merge(dst, src)
	if err != nil {
		t.Fatalf("merge returned an error: %v", err)
	}

	want := map[string]any{
		"myrecv": map[string]any{
			"include":   []any{"/var/log/app.log"},
			"send_logs": false,
		},
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("Unexpected merge result (-want +got):\n%s", diff)
	}
}

// TestMergeAppendsSlicesForSameSubKey guards against a regression where merge()'s map case let src's
// slice replace dst's whole slice for a sub-key present on both sides (e.g. two files each contributing
// entries to the same log.metrics_rules.<name> list, or globs to one receiver's include), silently
// dropping whichever entries dst had. A nested leaf list merges the same way a top-level list key does.
func TestMergeAppendsSlicesForSameSubKey(t *testing.T) {
	t.Parallel()

	dst := map[string]any{
		"apache_to_metrics": []any{map[string]any{"metric": "log_common_total"}},
	}
	src := map[string]any{
		"apache_to_metrics": []any{map[string]any{"metric": "log_common_code"}},
	}

	got, err := merge(dst, src)
	if err != nil {
		t.Fatalf("merge returned an error: %v", err)
	}

	want := map[string]any{
		"apache_to_metrics": []any{
			map[string]any{"metric": "log_common_total"},
			map[string]any{"metric": "log_common_code"},
		},
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("Unexpected merge result (-want +got):\n%s", diff)
	}
}

// TestDedupeFromListeners tests that a receiver's from_listeners list drops repeated names -- appending
// leaf lists across files can produce them, and naming one listener twice means nothing more than naming
// it once -- while leaving distinct names, their order, and invalid non-string entries alone.
func TestDedupeFromListeners(t *testing.T) {
	t.Parallel()

	config := map[string]any{
		"log.opentelemetry.receivers": map[string]any{
			"repeats":  map[string]any{"from_listeners": []any{"otlp", "otlp", "other", "otlp"}},
			"distinct": map[string]any{"from_listeners": []any{"a", "b"}},
			"invalid":  map[string]any{"from_listeners": []any{"a", map[string]any{"not": "a name"}, "a"}},
			"none":     map[string]any{"container_name": "app"},
		},
	}

	dedupeFromListeners(config)

	receivers, ok := config["log.opentelemetry.receivers"].(map[string]any)
	if !ok {
		t.Fatal("receivers key lost its shape")
	}

	fromListeners := func(name string) any {
		t.Helper()

		receiver, ok := receivers[name].(map[string]any)
		if !ok {
			t.Fatalf("receiver %q lost its shape", name)
		}

		return receiver["from_listeners"]
	}

	if diff := cmp.Diff([]any{"otlp", "other"}, fromListeners("repeats")); diff != "" {
		t.Errorf("Unexpected dedupe result (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff([]any{"a", "b"}, fromListeners("distinct")); diff != "" {
		t.Errorf("Distinct names must be left alone (-want +got):\n%s", diff)
	}

	// The non-string entry is invalid config, reported by validation later: it must be passed through
	// rather than silently dropped here (and must never be used as a map key).
	if diff := cmp.Diff([]any{"a", map[string]any{"not": "a name"}}, fromListeners("invalid")); diff != "" {
		t.Errorf("Unexpected handling of a non-string entry (-want +got):\n%s", diff)
	}

	if got := fromListeners("none"); got != nil {
		t.Errorf("a receiver without from_listeners must not gain one, got %v", got)
	}
}

// Test_loadMergesSplitMetricsRuleEntriesAcrossFiles is the end-to-end version of
// TestMergeAppendsSlicesForSameSubKey: two conf.d-style files each contribute one entry to the same
// log.metrics_rules.<name> list; both must survive instead of the second file's list silently replacing
// the first's.
func Test_loadMergesSplitMetricsRuleEntriesAcrossFiles(t *testing.T) {
	t.Parallel()

	config, _, err := load(&configLoader{}, false, false, "testdata/split-metrics-rule-a.conf", "testdata/split-metrics-rule-b.conf")
	if err != nil {
		t.Fatalf("Failed to load config: %s", err)
	}

	entries, ok := config.Log.MetricsRules["apache_to_metrics"]
	if !ok {
		t.Fatalf("Expected metrics_rules entry %q to exist, got %v", "apache_to_metrics", config.Log.MetricsRules)
	}

	if len(entries) != 2 {
		t.Fatalf("Expected 2 entries in %q, got %d: %v", "apache_to_metrics", len(entries), entries)
	}

	if got := entries[0]["metric"]; got != "log_common_total" {
		t.Errorf("Expected fileA's entry to survive, got metric=%v", got)
	}

	if got := entries[1]["metric"]; got != "log_common_code" {
		t.Errorf("Expected fileB's entry to survive, got metric=%v", got)
	}
}

// Test_loadMergesSplitReceiverFieldsAcrossFiles is the end-to-end version of
// TestMergeRecursesIntoNestedMaps: two conf.d-style files each set a different field on the same named
// log receiver; both must survive instead of validateLogReceivers rejecting the receiver as having "no
// source selector" -- a confusing secondary symptom of the real merge bug.
func Test_loadMergesSplitReceiverFieldsAcrossFiles(t *testing.T) {
	t.Parallel()

	config, _, err := load(&configLoader{}, false, false, "testdata/split-receiver-fields-a.conf", "testdata/split-receiver-fields-b.conf")
	if err != nil {
		t.Fatalf("Failed to load config: %s", err)
	}

	recv, ok := config.Log.OpenTelemetry.Receivers["myrecv"]
	if !ok {
		t.Fatalf("Expected receiver %q to exist, got %v", "myrecv", config.Log.OpenTelemetry.Receivers)
	}

	if diff := cmp.Diff([]any{"/var/log/app.log"}, recv["include"]); diff != "" {
		t.Errorf("Expected fileA's include to survive (-want +got):\n%s", diff)
	}

	if got, ok := recv["send_logs"].(bool); !ok || got {
		t.Errorf("Expected fileB's send_logs=false to survive, got %v", recv["send_logs"])
	}
}
