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
	"context"
	"maps"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/logger"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	noopM "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace/noop"
)

func testTelemetrySettings() component.TelemetrySettings {
	return component.TelemetrySettings{
		Logger:         logger.ZapLogger(),
		TracerProvider: noop.NewTracerProvider(),
		MeterProvider:  noopM.NewMeterProvider(),
		Resource:       pcommon.NewResource(),
	}
}

// collectingSink returns a consumer.Metrics recording every Sum data point it sees,
// keyed by metric name, and a function to read the accumulated totals. The consumer
// callback runs on the OTel pipeline's own goroutine, concurrently with the test
// reading the totals, hence the mutex.
func collectingSink() (consumer.Metrics, func() map[string]int64) {
	var l sync.Mutex

	totals := make(map[string]int64)

	sink, err := consumer.NewMetrics(func(_ context.Context, md pmetric.Metrics) error {
		l.Lock()
		defer l.Unlock()

		for i := range md.ResourceMetrics().Len() {
			sms := md.ResourceMetrics().At(i).ScopeMetrics()
			for j := range sms.Len() {
				ms := sms.At(j).Metrics()
				for k := range ms.Len() {
					m := ms.At(k)
					if m.Type() != pmetric.MetricTypeSum {
						continue
					}

					dps := m.Sum().DataPoints()
					for d := range dps.Len() {
						totals[m.Name()] += dps.At(d).IntValue()
					}
				}
			}
		}

		return nil
	})
	if err != nil {
		panic(err)
	}

	return sink, func() map[string]int64 {
		l.Lock()
		defer l.Unlock()

		return maps.Clone(totals)
	}
}

// TestSourceCountsRealFile is an end-to-end test of a real OTel
// filelogreceiver+countconnector source: it must count matching lines via OTTL.
func TestSourceCountsRealFile(t *testing.T) {
	t.Parallel()

	logFile, err := os.CreateTemp(t.TempDir(), "app-*.log")
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer logFile.Close()

	sink, totals := collectingSink()

	src, err := newSource(t.Context(), testTelemetrySettings(), []string{logFile.Name()}, false, []config.LogFilter{
		{Metric: "app_errors_count", Regex: `\[error\]`},
		{Metric: "app_requests_count", Regex: "GET /"},
	}, sink)
	if err != nil {
		t.Fatal("Failed to build source:", err)
	}

	defer src.stop(t.Context()) //nolint:errcheck

	time.Sleep(500 * time.Millisecond)

	lines := []string{
		"127.0.0.1 GET / 200\n",
		"[error] something broke\n",
		"127.0.0.1 GET / [error] weird combo\n",
		"just a normal line\n",
	}

	for _, line := range lines {
		if _, err := logFile.WriteString(line); err != nil {
			t.Fatal("Failed to write log line:", err)
		}
	}

	if err := logFile.Sync(); err != nil {
		t.Fatal("Failed to sync log file:", err)
	}

	time.Sleep(time.Second)

	got := totals()

	if got["app_errors_count"] != 2 {
		t.Errorf("Expected 2 matches for app_errors_count, got %d", got["app_errors_count"])
	}

	if got["app_requests_count"] != 2 {
		t.Errorf("Expected 2 matches for app_requests_count, got %d", got["app_requests_count"])
	}
}

// TestSourceUnwrapsContainerEnvelope is the regression test for the original
// RabbitMQ bug: a regex that only matches the unwrapped message, fed through a
// Docker-JSON-wrapped raw line, via a real filelogreceiver+countconnector pipeline
// with the container envelope operator enabled.
func TestSourceUnwrapsContainerEnvelope(t *testing.T) {
	t.Parallel()

	logFile, err := os.CreateTemp(t.TempDir(), "container-*.log")
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer logFile.Close()

	sink, totals := collectingSink()

	// The container parser preserves the trailing newline embedded in Docker's JSON
	// "log" field value, so the body is "[error] something broke\n", not "...broke".
	src, err := newSource(t.Context(), testTelemetrySettings(), []string{logFile.Name()}, true, []config.LogFilter{
		{Metric: "container_errors_count", Regex: `^\[error\] something broke\n?$`},
	}, sink)
	if err != nil {
		t.Fatal("Failed to build source:", err)
	}

	defer src.stop(t.Context()) //nolint:errcheck

	time.Sleep(500 * time.Millisecond)

	dockerLine := `{"log":"[error] something broke\n","stream":"stdout","time":"2024-01-15T10:23:45.123Z"}` + "\n"

	if _, err := logFile.WriteString(dockerLine); err != nil {
		t.Fatal("Failed to write log line:", err)
	}

	if err := logFile.Sync(); err != nil {
		t.Fatal("Failed to sync log file:", err)
	}

	time.Sleep(time.Second)

	if got := totals()["container_errors_count"]; got != 1 {
		t.Errorf("Expected 1 match for container_errors_count, got %d", got)
	}
}

func TestSourceInvalidRegex(t *testing.T) {
	t.Parallel()

	sink, _ := collectingSink()

	_, err := newSource(t.Context(), testTelemetrySettings(), []string{"/nonexistent"}, false, []config.LogFilter{
		{Metric: "bad", Regex: "("},
	}, sink)
	if err == nil {
		t.Fatal("Expected an error for an invalid regex")
	}
}

// TestSourceFastPathSingleConnector locks in the optimization: when every filter
// is valid, buildConnectors uses one combined connector instead of one per filter.
func TestSourceFastPathSingleConnector(t *testing.T) {
	t.Parallel()

	sink, _ := collectingSink()

	src, err := newSource(t.Context(), testTelemetrySettings(), []string{"/nonexistent"}, false, []config.LogFilter{
		{Metric: "a_count", Regex: "a"},
		{Metric: "b_count", Regex: "b"},
		{Metric: "c_count", Regex: "c"},
	}, sink)
	if err != nil {
		t.Fatal("Failed to build source:", err)
	}

	defer src.stop(t.Context()) //nolint:errcheck

	if len(src.conns) != 1 {
		t.Errorf("Expected exactly 1 connector on the fast path, got %d", len(src.conns))
	}
}

// TestSourceIsolatesInvalidFilter is the regression test for the fallback path in
// buildConnectors: when a source has both valid and invalid filters, the combined
// fast path fails validation, so it falls back to one connector per valid filter --
// the invalid one is disabled but its siblings keep counting normally.
func TestSourceIsolatesInvalidFilter(t *testing.T) {
	t.Parallel()

	logFile, err := os.CreateTemp(t.TempDir(), "app-*.log")
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer logFile.Close()

	sink, totals := collectingSink()

	src, err := newSource(t.Context(), testTelemetrySettings(), []string{logFile.Name()}, false, []config.LogFilter{
		{Metric: "app_errors_count", Regex: `\[error\]`},
		{Metric: "app_requests_count", Regex: "GET /"},
		{Metric: "app_broken_count", Regex: "("},
	}, sink)
	if err != nil {
		t.Fatal("Failed to build source despite one invalid filter:", err)
	}

	defer src.stop(t.Context()) //nolint:errcheck

	if len(src.conns) != 2 {
		t.Errorf("Expected exactly 2 connectors (valid filters only, isolated per-filter), got %d", len(src.conns))
	}

	time.Sleep(500 * time.Millisecond)

	lines := []string{
		"[error] something broke\n",
		"127.0.0.1 GET / 200\n",
	}

	for _, line := range lines {
		if _, err := logFile.WriteString(line); err != nil {
			t.Fatal("Failed to write log line:", err)
		}
	}

	if err := logFile.Sync(); err != nil {
		t.Fatal("Failed to sync log file:", err)
	}

	time.Sleep(time.Second)

	got := totals()

	if got["app_errors_count"] != 1 {
		t.Errorf("Expected 1 match for app_errors_count, got %d", got["app_errors_count"])
	}

	if got["app_requests_count"] != 1 {
		t.Errorf("Expected 1 match for app_requests_count, got %d", got["app_requests_count"])
	}

	if _, found := got["app_broken_count"]; found {
		t.Errorf("app_broken_count should never receive any data, got %d", got["app_broken_count"])
	}
}
