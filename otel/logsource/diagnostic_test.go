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
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/facts"

	"github.com/google/go-cmp/cmp"
	"go.opentelemetry.io/collector/consumer"
)

// summarizeInputs maps each source name to its status and one "kind status name" line per input.
func summarizeInputs(sources []SourceDiagnostic) map[string][]string {
	out := make(map[string][]string, len(sources))

	for _, src := range sources {
		lines := []string{"source " + string(src.Status)}

		for _, input := range src.Inputs {
			lines = append(lines, fmt.Sprintf("%s %s %s", input.Kind, input.Status, input.Name))
		}

		slices.Sort(lines[1:])
		out[src.Name] = lines
	}

	return out
}

// Test that every configured file, listener and container match of a receiver shows up as an input with
// its own status, whatever the status of the others.
func TestReceiverManagerDiagnosticSources(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	existing := filepath.Join(dir, "myapp.log")
	missing := filepath.Join(dir, "myapp-err.log")

	if err := os.WriteFile(existing, []byte("line\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := config.OpenTelemetry{
		ReceiversDefaultSendLogs: true,
		Receivers: map[string]config.LogReceiver{
			"both":         {"include": []string{existing, missing}},
			"none":         {"include": []string{existing, missing}, "send_logs": false},
			"no-exists":    {"include": []string{missing, filepath.Join(dir, "*.missing")}},
			"bad-fmt":      {"include": []string{existing}, "log_format": "does-not-exist"},
			"net-ok":       {"from_listeners": []string{"otlp/ok"}},
			"net-bad":      {"from_listeners": []string{"otlp/not_exist"}},
			"net-conflict": {"from_listeners": []string{"otlp/conflict"}},
			"net-partial":  {"from_listeners": []string{"otlp/not_exist", "otlp/ok"}},
			"all-in-one": {
				"include":        []string{existing, missing},
				"from_listeners": []string{"otlp/not_exist", "otlp/ok"},
				"container_name": "redis",
			},
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

	rm.UpdateContainers(t.Context(), nil, nil, true)
	rm.SetNetworkListenerStates(map[string]string{
		"otlp/ok":       "",
		"otlp/conflict": "listen tcp 0.0.0.0:1236: bind: address already in use",
	})

	want := map[string][]string{
		"receiver:both": {
			"source active",
			"file active " + existing,
			"file waiting " + missing,
		},
		"receiver:none": {
			"source unused",
			"file unused " + missing,
			"file unused " + existing,
		},
		"receiver:no-exists": {
			"source waiting",
			"file waiting " + filepath.Join(dir, "*.missing"),
			"file waiting " + missing,
		},
		"receiver:bad-fmt": {
			"source active",
			"file active " + existing,
		},
		"receiver:net-ok": {
			"source active",
			"listener active otlp/ok",
		},
		"receiver:net-bad": {
			"source failed",
			"listener failed otlp/not_exist",
		},
		"receiver:net-conflict": {
			"source failed",
			"listener failed otlp/conflict",
		},
		"receiver:net-partial": {
			"source active",
			"listener active otlp/ok",
			"listener failed otlp/not_exist",
		},
		"receiver:all-in-one": {
			"source active",
			"container waiting name=redis",
			"file active " + existing,
			"file waiting " + missing,
			"listener active otlp/ok",
			"listener failed otlp/not_exist",
		},
	}

	got := rm.DiagnosticSources()

	if diff := cmp.Diff(want, summarizeInputs(got)); diff != "" {
		t.Errorf("DiagnosticSources mismatch (-want +got):\n%s", diff)
	}

	for _, src := range got {
		switch src.Name {
		case "receiver:bad-fmt":
			if diff := cmp.Diff([]string{`unknown log format "does-not-exist"`}, src.Errors); diff != "" {
				t.Errorf("Unexpected errors for %s (-want +got):\n%s", src.Name, diff)
			}
		case "receiver:net-conflict":
			if want := "failed to start: listen tcp 0.0.0.0:1236: bind: address already in use"; src.Inputs[0].Reason != want {
				t.Errorf("Unexpected reason for %s: got %q, want %q", src.Name, src.Inputs[0].Reason, want)
			}
		}
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
// file has one line per input then one line per source.
func TestWriteSourcesDiagnostic(t *testing.T) {
	t.Parallel()

	sources := []SourceDiagnostic{
		{
			Name:   "receiver:app",
			Origin: "receiver",
			Status: SourceActive,
			Inputs: []InputDiagnostic{
				{Kind: InputFile, Name: "/var/log/app/a.log", Status: SourceActive, Mode: "direct"},
				{Kind: InputFile, Name: "/var/log/app/b.log", Status: SourceWaiting, Reason: "file doesn't exist or can't be read"},
			},
			key: ReceiverSourceKey("app"),
		},
		{
			Name:   "service:myapp",
			Origin: "service",
			Status: SourceExcluded,
			Reason: "container_and_service_enable is false",
			Inputs: []InputDiagnostic{
				{Kind: InputFile, Name: "/var/log/myapp/myapp.log", Status: SourceExcluded, Reason: "container_and_service_enable is false"},
			},
		},
		{
			Name:   "receiver:aaa",
			Origin: "receiver",
			Status: SourceFailed,
			Errors: []string{"decoding config: boom"},
		},
		{
			Name:   "receiver:zzz",
			Origin: "receiver",
			Status: SourceActive,
			Inputs: []InputDiagnostic{{Kind: InputListener, Name: "otlp/ok", Status: SourceActive}},
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
		t.Errorf("Expected service:myapp to not be shipped, got %+v", got[2].Shipping)
	}

	var gotLines [][]string

	for line := range strings.SplitSeq(writer.files[sourcesTextArchivePath].String(), "\n") {
		if fields := strings.Fields(line); len(fields) > 0 && strings.Contains(fields[0], ":") && !strings.HasSuffix(fields[0], ":") {
			gotLines = append(gotLines, fields)
		}
	}

	wantLines := [][]string{
		// Inputs, sorted by status, source, then input.
		{"receiver:app", "active", "file", "/var/log/app/a.log", "direct"},
		{"receiver:zzz", "active", "listener", "otlp/ok", "-"},
		{"service:myapp", "excluded", "file", "/var/log/myapp/myapp.log", "-", "container_and_service_enable", "is", "false"},
		{"receiver:aaa", "failed", "-", "-", "-"},
		{"receiver:app", "waiting", "file", "/var/log/app/b.log", "-", "file", "doesn't", "exist", "or", "can't", "be", "read"},
		// Sources.
		{"receiver:app", "active", "42", "7", "1"},
		{"receiver:zzz", "active", "-", "-", "-"},
		{"service:myapp", "excluded", "-", "-", "-", "container_and_service_enable", "is", "false"},
		{"receiver:aaa", "failed", "-", "-", "-", "decoding", "config:", "boom"},
	}

	if diff := cmp.Diff(wantLines, gotLines); diff != "" {
		t.Errorf("Unexpected sources.txt lines (-want +got):\n%s\n%s", diff, writer.files[sourcesTextArchivePath].String())
	}
}
