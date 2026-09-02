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
	"errors"
	"testing"
)

// Test that a receiver's from_listeners entry naming a listener opentelemetry.listeners
// doesn't define is rejected at load time -- since there's no implicit/default listener to fall back to
// anymore, an undefined name is unambiguously a typo or a forgotten listeners entry, and should be
// caught immediately instead of only surfacing later as a runtime agent_config_warning.
func TestValidateLogReceiversRejectsUndefinedNetworkListener(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Log: Log{
			OpenTelemetry: OpenTelemetry{
				Receivers: map[string]LogReceiver{
					"filelog/recv": {
						"from_listeners": []any{"does_not_exist"},
					},
				},
			},
		},
	}

	err := validateLogReceivers(cfg)
	if !errors.Is(err, errReceiverNetworkListenerUndefined) {
		t.Fatalf("Expected errReceiverNetworkListenerUndefined, got: %v", err)
	}
}

// Test that a receiver's from_listeners entry naming a listener that IS defined under
// opentelemetry.listeners is accepted.
func TestValidateLogReceiversAcceptsDefinedNetworkListener(t *testing.T) {
	t.Parallel()

	cfg := Config{
		OpenTelemetry: OpenTelemetryConfig{
			NetworkListeners: map[string]NetworkListener{
				"otlp": {Protocols: NetworkProtocols{GRPC: &NetworkEndpoint{}}},
			},
		},
		Log: Log{
			OpenTelemetry: OpenTelemetry{
				Receivers: map[string]LogReceiver{
					"filelog/recv": {
						"from_listeners": []any{"otlp"},
					},
				},
			},
		},
	}

	if err := validateLogReceivers(cfg); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
}

// Test that a receiver's metrics: {include: name} entry referencing a log.metrics_rules entry that
// doesn't exist is rejected at load time, instead of otel/logmetrics silently dropping that one metric at
// runtime with nothing but a log line.
func TestValidateLogReceiversRejectsUndefinedMetricsRule(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Log: Log{
			OpenTelemetry: OpenTelemetry{
				Receivers: map[string]LogReceiver{
					"apache_access": {
						"container_name": "apache",
						"metrics":        []any{map[string]any{"include": "does_not_exist"}},
					},
				},
			},
		},
	}

	err := validateLogReceivers(cfg)
	if !errors.Is(err, errReceiverMetricsRuleUndefined) {
		t.Fatalf("Expected errReceiverMetricsRuleUndefined, got: %v", err)
	}
}

// Test that a receiver's metrics: {include: name} entry referencing a log.metrics_rules entry that DOES
// exist is accepted.
func TestValidateLogReceiversAcceptsDefinedMetricsRule(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Log: Log{
			MetricsRules: map[string][]LogMetricEntry{
				"web_rules": {{"metric": "web_errors_count"}},
			},
			OpenTelemetry: OpenTelemetry{
				Receivers: map[string]LogReceiver{
					"apache_access": {
						"container_name": "apache",
						"metrics":        []any{map[string]any{"include": "web_rules"}},
					},
				},
			},
		},
	}

	if err := validateLogReceivers(cfg); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
}

// Test that an ordinary inline metrics: entry (a "metric" name, no "include") is never flagged by the
// metrics_rules check -- guards against a false positive on the common case.
func TestValidateLogReceiversAcceptsInlineMetricsEntry(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Log: Log{
			OpenTelemetry: OpenTelemetry{
				Receivers: map[string]LogReceiver{
					"apache_access": {
						"container_name": "apache",
						"metrics":        []any{map[string]any{"metric": "web_errors_count"}},
					},
				},
			},
		},
	}

	if err := validateLogReceivers(cfg); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
}

// Test that a receiver with no source selector at all is still rejected (errReceiverNoSelector), even
// though this validator now also checks network listener names -- guards against the new check
// accidentally short-circuiting the existing one.
func TestValidateLogReceiversRejectsNoSelector(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Log: Log{
			OpenTelemetry: OpenTelemetry{
				Receivers: map[string]LogReceiver{
					"empty": {},
				},
			},
		},
	}

	err := validateLogReceivers(cfg)
	if !errors.Is(err, errReceiverNoSelector) {
		t.Fatalf("Expected errReceiverNoSelector, got: %v", err)
	}
}
