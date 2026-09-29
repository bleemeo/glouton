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

package logsource

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/facts"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"go.opentelemetry.io/collector/consumer"
)

// Test that ReceiverManager reports each configured receiver with the status matching what it reads.
func TestReceiverManagerDiagnosticSources(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	existing := filepath.Join(dir, "app.log")

	if err := os.WriteFile(existing, []byte("line\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := config.OpenTelemetry{
		ReceiversDefaultSendLogs: true,
		Receivers: map[string]config.LogReceiver{
			"app":     {"include": []string{existing}},
			"missing": {"include": []string{filepath.Join(dir, "missing.log")}},
			"no-send": {"include": []string{existing}, "send_logs": false},
			"bad-fmt": {"include": []string{filepath.Join(dir, "other.log")}, "log_format": "does-not-exist"},
		},
	}

	rm := newTestReceiverManager(t, cfg)

	sink, _ := recordingLogsConsumer()

	rm.RegisterSinkProvider(&fakeSinkProvider{
		want: func(src ResolvedSource) (consumer.Logs, bool) { return sink, src.SendLogs },
	})

	if err := rm.RescanReceivers(t.Context()); err != nil {
		t.Fatal("RescanReceivers failed:", err)
	}

	want := []SourceDiagnostic{
		{
			Name:    "receiver:app",
			Origin:  "receiver",
			Status:  SourceActive,
			Include: []string{existing},
			Files:   []FileDiagnostic{{Path: existing, Input: "direct"}},
		},
		{
			Name:      "receiver:bad-fmt",
			Origin:    "receiver",
			Status:    SourceWaiting,
			Reason:    "no readable file matches include",
			Errors:    []string{`unknown log format "does-not-exist"`},
			Include:   []string{filepath.Join(dir, "other.log")},
			LogFormat: "does-not-exist",
			Files:     []FileDiagnostic{},
		},
		{
			Name:    "receiver:missing",
			Origin:  "receiver",
			Status:  SourceWaiting,
			Reason:  "no readable file matches include",
			Include: []string{filepath.Join(dir, "missing.log")},
			Files:   []FileDiagnostic{},
		},
		{
			Name:    "receiver:no-send",
			Origin:  "receiver",
			Status:  SourceUnused,
			Reason:  "send_logs is false and no metrics are configured",
			Include: []string{existing},
			Files:   []FileDiagnostic{},
		},
	}

	got := rm.DiagnosticSources()

	opts := []cmp.Option{
		cmpopts.IgnoreUnexported(SourceDiagnostic{}),
		cmpopts.SortSlices(func(a, b SourceDiagnostic) bool { return a.Name < b.Name }),
	}

	if diff := cmp.Diff(want, got, opts...); diff != "" {
		t.Errorf("DiagnosticSources mismatch (-want +got):\n%s", diff)
	}
}

// Test the status of containers handled by the label fallback: only an opted-in one is read.
func TestReceiverManagerDiagnosticContainerLabels(t *testing.T) {
	t.Parallel()

	logPath := filepath.Join(t.TempDir(), "ctr.log")

	if err := os.WriteFile(logPath, []byte("line\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	makeCtr := func(name string, labels map[string]string) facts.Container {
		return facts.FakeContainer{FakeID: "id-" + name, FakeContainerName: name, FakeLogPath: logPath, FakeLabels: labels}
	}

	rm := newTestReceiverManager(t, config.OpenTelemetry{})

	sink, _ := recordingLogsConsumer()

	rm.RegisterSinkProvider(&fakeSinkProvider{
		want: func(src ResolvedSource) (consumer.Logs, bool) {
			if src.LogMetricsRule != "" {
				return nil, false // simulates an unknown rule
			}

			return sink, src.SendLogs
		},
	})

	rm.UpdateContainers(t.Context(), []facts.Container{
		makeCtr("opted-in", map[string]string{"glouton.log_enable": "true"}),
		makeCtr("no-label", nil),
		makeCtr("send-off", map[string]string{"glouton.send_logs": "false"}),
		makeCtr("bad-rule", map[string]string{"glouton.log_metrics": "unknown"}),
	}, nil, true)

	got := make(map[string]SourceStatus)

	for _, src := range rm.DiagnosticSources() {
		got[src.Name] = src.Status
	}

	want := map[string]SourceStatus{
		"container:opted-in": SourceActive,
		"container:no-label": SourceExcluded,
		"container:send-off": SourceExcluded,
		"container:bad-rule": SourceFailed,
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Unexpected statuses (-want +got):\n%s", diff)
	}
}

// Test that WriteSourcesDiagnostic attaches the consumers' info to the right source, and that the text
// file has one line per file read.
func TestWriteSourcesDiagnostic(t *testing.T) {
	t.Parallel()

	sources := []SourceDiagnostic{
		{
			Name:   "receiver:app",
			Origin: "receiver",
			Status: SourceActive,
			Files: []FileDiagnostic{
				{Path: "/var/log/app/a.log", Input: "direct"},
				{Path: "/var/log/app/b.log", Input: "sudo"},
			},
			key: ReceiverSourceKey("app"),
		},
		{
			Name:    "service:myapp",
			Origin:  "service",
			Status:  SourceExcluded,
			Reason:  "log.opentelemetry.auto_discovery.container_and_service_enable is false",
			Include: []string{"/var/log/myapp/myapp.log"},
		},
		{
			Name:    "receiver:aaa",
			Origin:  "receiver",
			Status:  SourceWaiting,
			Include: []string{"/var/log/aaa.log"},
		},
		{
			Name:   "receiver:zzz",
			Origin: "receiver",
			Status: SourceActive,
		},
	}

	writer := &fakeArchiveWriter{}

	err := WriteSourcesDiagnostic(
		writer,
		sources,
		map[SourceKey]ShippingDiagnostic{ReceiverSourceKey("app"): {LogCount: 42, LogThroughputPerMinute: 7}},
		map[SourceKey]LogToMetricsDiagnostic{ReceiverSourceKey("app"): {Metrics: []string{"app_errors"}}},
	)
	if err != nil {
		t.Fatal("WriteSourcesDiagnostic failed:", err)
	}

	var got []SourceDiagnostic

	if err := json.Unmarshal(writer.files[sourcesJSONArchivePath].Bytes(), &got); err != nil {
		t.Fatal("Can't decode sources.json:", err)
	}

	gotNames := make([]string, 0, len(got))
	for _, src := range got {
		gotNames = append(gotNames, src.Name)
	}

	// Sorted by status, then by name.
	if diff := cmp.Diff([]string{"receiver:app", "receiver:zzz", "service:myapp", "receiver:aaa"}, gotNames); diff != "" {
		t.Fatalf("Unexpected source order (-want +got):\n%s", diff)
	}

	if got[0].Shipping == nil || got[0].Shipping.LogCount != 42 {
		t.Errorf("Expected receiver:app to be shipped with 42 logs, got %+v", got[0].Shipping)
	}

	if got[0].LogToMetrics == nil || len(got[0].LogToMetrics.Metrics) != 1 {
		t.Errorf("Expected receiver:app to have one log-to-metric, got %+v", got[0].LogToMetrics)
	}

	if got[2].Shipping != nil {
		t.Errorf("Expected service:myapp to not be shipped, got %+v", got[1].Shipping)
	}

	text := writer.files[sourcesTextArchivePath].String()

	for _, wantLine := range [][]string{
		{"receiver:app", "active", "/var/log/app/a.log", "direct", "42", "7", "1"},
		{"receiver:app", "active", "/var/log/app/b.log", "sudo", "42", "7", "1"},
		{"service:myapp", "excluded", "/var/log/myapp/myapp.log", "-", "-", "-", "-", "log.opentelemetry.auto_discovery.container_and_service_enable", "is", "false"},
	} {
		found := false

		for line := range strings.SplitSeq(text, "\n") {
			if slices.Equal(strings.Fields(line), wantLine) {
				found = true

				break
			}
		}

		if !found {
			t.Errorf("Expected a line %v in sources.txt, got:\n%s", wantLine, text)
		}
	}
}
