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

package logmetrics

import (
	"testing"

	"github.com/bleemeo/glouton/config"
)

func TestNewNetworkSourceDisabled(t *testing.T) {
	t.Parallel()

	sink, _ := collectingSink()

	netCfg := config.LogMetricsNetworkReceiver{}
	count := map[string]config.LogMetricsCount{"pushed_errors_count": {"conditions": []any{`IsMatch(body, "\\[error\\]")`}}}

	src, err := newNetworkSource(t.Context(), testTelemetrySettings(), netCfg, count, sink)
	if err != nil {
		t.Fatal("Expected no error when no receivers are referenced, got:", err)
	}

	if src != nil {
		t.Fatal("Expected a nil source when no receivers are referenced")
	}
}

func TestNewNetworkSourceNoCounters(t *testing.T) {
	t.Parallel()

	sink, _ := collectingSink()

	netCfg := config.LogMetricsNetworkReceiver{
		Receivers: []string{"otlp"},
	}

	src, err := newNetworkSource(t.Context(), testTelemetrySettings(), netCfg, nil, sink)
	if err != nil {
		t.Fatal("Expected no error when no counters are configured, got:", err)
	}

	if src != nil {
		t.Fatal("Expected a nil source when no counters are configured")
	}
}

// TestNewNetworkSourceBuildsConsumer verifies that, once a receiver is
// referenced with at least one log.metrics.count entry configured (global,
// see LogMetricsConfig's doc comment), newNetworkSource builds a usable
// entry consumer and its connectors stop cleanly -- it no longer starts an
// OTLP receiver itself (that's shared with otel/logprocessing, see
// logsource.SetupOTLPNetworkReceiver/FanoutLogs, and tested there).
func TestNewNetworkSourceBuildsConsumer(t *testing.T) {
	t.Parallel()

	sink, _ := collectingSink()

	netCfg := config.LogMetricsNetworkReceiver{
		Receivers: []string{"otlp"},
	}
	count := map[string]config.LogMetricsCount{"pushed_errors_count": {"conditions": []any{`IsMatch(body, "\\[error\\]")`}}}

	src, err := newNetworkSource(t.Context(), testTelemetrySettings(), netCfg, count, sink)
	if err != nil {
		t.Fatal("Failed to build network source:", err)
	}

	if src == nil || src.entryConsumer == nil {
		t.Fatal("Expected a non-nil source with a non-nil entry consumer when a receiver is referenced with counters configured")
	}

	if err := src.stop(t.Context()); err != nil {
		t.Fatal("Failed to stop network source:", err)
	}
}

// TestNewNetworkSourceSimpleEnableBuildsConsumer is the regression test for
// the simple "enable: true" shortcut (config.OTLPNetworkParticipation/
// LogMetricsNetworkReceiver.Enable): newNetworkSource must build a consumer
// even with no Receivers named, as long as Enable is set -- resolving which
// actual log.network.receivers entry that means is agent.go's job (see
// config.ResolveNetworkReceivers), not this function's.
func TestNewNetworkSourceSimpleEnableBuildsConsumer(t *testing.T) {
	t.Parallel()

	sink, _ := collectingSink()

	netCfg := config.LogMetricsNetworkReceiver{
		Enable: true,
	}
	count := map[string]config.LogMetricsCount{"pushed_errors_count": {"conditions": []any{`IsMatch(body, "\\[error\\]")`}}}

	src, err := newNetworkSource(t.Context(), testTelemetrySettings(), netCfg, count, sink)
	if err != nil {
		t.Fatal("Failed to build network source:", err)
	}

	if src == nil || src.entryConsumer == nil {
		t.Fatal("Expected a non-nil source with a non-nil entry consumer when Enable is set with counters configured")
	}

	if err := src.stop(t.Context()); err != nil {
		t.Fatal("Failed to stop network source:", err)
	}
}
