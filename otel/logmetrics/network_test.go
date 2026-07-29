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

	reg, _ := testRegistry()

	netCfg := config.LogMetricsNetworkReceiver{}
	count := map[string]config.LogMetricsCount{"pushed_errors_count": {"conditions": []any{`IsMatch(body, "\\[error\\]")`}}}

	src, err := newNetworkSource(t.Context(), testTelemetrySettings(), netCfg, count, specsForCount(count), reg)
	if err != nil {
		t.Fatal("Expected no error when no receivers are referenced, got:", err)
	}

	if src != nil {
		t.Fatal("Expected a nil source when no receivers are referenced")
	}
}

func TestNewNetworkSourceNoCounters(t *testing.T) {
	t.Parallel()

	reg, _ := testRegistry()

	netCfg := config.LogMetricsNetworkReceiver{
		Receivers: []string{"otlp"},
	}

	src, err := newNetworkSource(t.Context(), testTelemetrySettings(), netCfg, nil, nil, reg)
	if err != nil {
		t.Fatal("Expected no error when no counters are configured, got:", err)
	}

	if src != nil {
		t.Fatal("Expected a nil source when no counters are configured")
	}
}

// TestNewNetworkSourceBuildsConsumer checks that newNetworkSource builds a
// usable entry consumer once a receiver is referenced with counters
// configured, and that it no longer starts an OTLP receiver itself (that's
// shared with otel/logprocessing and tested there).
func TestNewNetworkSourceBuildsConsumer(t *testing.T) {
	t.Parallel()

	reg, _ := testRegistry()

	netCfg := config.LogMetricsNetworkReceiver{
		Receivers: []string{"otlp"},
	}
	count := map[string]config.LogMetricsCount{"pushed_errors_count": {"conditions": []any{`IsMatch(body, "\\[error\\]")`}}}

	src, err := newNetworkSource(t.Context(), testTelemetrySettings(), netCfg, count, specsForCount(count), reg)
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

// TestNewNetworkSourceSimpleEnableBuildsConsumer checks that
// newNetworkSource builds a consumer with no Receivers named, as long as
// Enable is set -- resolving the actual receiver is agent.go's job.
func TestNewNetworkSourceSimpleEnableBuildsConsumer(t *testing.T) {
	t.Parallel()

	reg, _ := testRegistry()

	netCfg := config.LogMetricsNetworkReceiver{
		Enable: true,
	}
	count := map[string]config.LogMetricsCount{"pushed_errors_count": {"conditions": []any{`IsMatch(body, "\\[error\\]")`}}}

	src, err := newNetworkSource(t.Context(), testTelemetrySettings(), netCfg, count, specsForCount(count), reg)
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
