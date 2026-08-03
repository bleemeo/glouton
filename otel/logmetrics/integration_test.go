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
	"os"
	"testing"
	"time"

	"github.com/bleemeo/glouton/agent/state"
	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/otel/logsource"
	"github.com/bleemeo/glouton/prometheus/registry"

	"github.com/prometheus/prometheus/model/exemplar"
	"github.com/prometheus/prometheus/model/histogram"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/model/metadata"
	"github.com/prometheus/prometheus/storage"
)

// recordingAppender is a minimal storage.Appender test double recording only Append calls.
type recordingAppender struct {
	points []recordedPoint
}

type recordedPoint struct {
	labels labels.Labels
	value  float64
}

func (a *recordingAppender) Append(_ storage.SeriesRef, l labels.Labels, _ int64, v float64) (storage.SeriesRef, error) {
	a.points = append(a.points, recordedPoint{labels: l, value: v})

	return 0, nil
}

func (a *recordingAppender) SetOptions(*storage.AppendOptions) {}

func (a *recordingAppender) AppendExemplar(_ storage.SeriesRef, _ labels.Labels, _ exemplar.Exemplar) (storage.SeriesRef, error) {
	return 0, nil
}

func (a *recordingAppender) AppendHistogram(_ storage.SeriesRef, _ labels.Labels, _ int64, _ *histogram.Histogram, _ *histogram.FloatHistogram) (storage.SeriesRef, error) {
	return 0, nil
}

func (a *recordingAppender) AppendHistogramSTZeroSample(_ storage.SeriesRef, _ labels.Labels, _, _ int64, _ *histogram.Histogram, _ *histogram.FloatHistogram) (storage.SeriesRef, error) {
	return 0, nil
}

func (a *recordingAppender) UpdateMetadata(_ storage.SeriesRef, _ labels.Labels, _ metadata.Metadata) (storage.SeriesRef, error) {
	return 0, nil
}

func (a *recordingAppender) AppendSTZeroSample(_ storage.SeriesRef, _ labels.Labels, _, _ int64) (storage.SeriesRef, error) {
	return 0, nil
}

func (a *recordingAppender) Commit() error   { return nil }
func (a *recordingAppender) Rollback() error { return nil }

// Test a container matched via an explicit receiver's container_selectors through a real ReceiverManager, reproducing a production bug where the metric stayed stuck at zero.
func TestReceiverMatchedContainerCountsThroughRealReceiverManager(t *testing.T) {
	t.Parallel()

	logFile, err := os.CreateTemp(t.TempDir(), "web-*.log")
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer logFile.Close()

	ctr := facts.FakeContainer{
		FakeID:            "id-web-1",
		FakeContainerName: "web-1",
		FakeLogPath:       logFile.Name(),
		FakeLabels:        map[string]string{"app": "web"},
	}

	cfg := config.OpenTelemetry{
		Receivers: map[string]config.LogReceiver{
			"web_app": map[string]any{
				"container_selectors": map[string]string{"app": "web"},
				"metrics": []any{
					map[string]any{"metric": "web_errors_count", "conditions": []any{`IsMatch(body, "ERROR")`}},
				},
			},
		},
	}

	st, err := state.LoadReadOnly("", "")
	if err != nil {
		t.Fatal("Can't instantiate state:", err)
	}

	rm, err := logsource.NewReceiverManager(cfg, "/", st, nil)
	if err != nil {
		t.Fatal("NewReceiverManager failed:", err)
	}

	t.Cleanup(func() { rm.Shutdown(t.Context()) })

	man := New(cfg, nil)

	t.Cleanup(func() { _ = man.Shutdown(t.Context()) })

	rm.RegisterSinkProvider(man)

	rm.UpdateContainers(t.Context(), []facts.Container{ctr})

	time.Sleep(500 * time.Millisecond)

	dockerLine := `{"log":"2026-08-03 ERROR oops\n","stream":"stdout","time":"2024-01-15T10:23:45.123Z"}` + "\n"

	if _, err := logFile.WriteString(dockerLine); err != nil {
		t.Fatal("Failed to write log line:", err)
	}

	if err := logFile.Sync(); err != nil {
		t.Fatal("Failed to sync log file:", err)
	}

	time.Sleep(2 * time.Second)

	got := countsFor(man, "web_errors_count")
	if got["web_app"] == 0 {
		t.Fatalf("expected a non-zero count for web_errors_count under item %q, got %v", "web_app", got)
	}

	// Also drive the same path /metrics goes through, not just the registry's internal counter.
	app := &recordingAppender{}
	if err := man.EmitMetrics(t.Context(), registry.GatherState{}, app); err != nil {
		t.Fatal("EmitMetrics failed:", err)
	}

	found := false

	for _, p := range app.points {
		if p.labels.Get("__name__") == "web_errors_count" && p.labels.Get("item") == "web_app" {
			found = true

			if p.value <= 0 {
				t.Fatalf("expected a non-zero rate for web_errors_count{item=\"web_app\"} via EmitMetrics, got %v", p.value)
			}
		}
	}

	if !found {
		t.Fatalf("expected EmitMetrics to append a web_errors_count{item=\"web_app\"} point, got %+v", app.points)
	}
}
