// Copyright 2015-2025 Bleemeo
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

package logprocessing

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/bleemeo/glouton/config"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/config/configtelemetry"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	noopM "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace/noop"
	"go.uber.org/zap"
)

type logBuffer struct {
	l   sync.Mutex
	buf []plog.Logs
}

func (logBuf *logBuffer) add(ld plog.Logs) {
	logBuf.l.Lock()
	defer logBuf.l.Unlock()

	logBuf.buf = append(logBuf.buf, ld)
}

func (logBuf *logBuffer) getAllAsStrings() []string {
	logBuf.l.Lock()
	defer logBuf.l.Unlock()

	result := make([]string, 0, len(logBuf.buf)) // There may be more than 1 log message per plog.Logs object

	for _, ld := range logBuf.buf {
		for i := range ld.ResourceLogs().Len() {
			resourceLog := ld.ResourceLogs().At(i)
			scopeLogs := resourceLog.ScopeLogs()

			for j := range scopeLogs.Len() {
				scopeLog := scopeLogs.At(j)
				logRecords := scopeLog.LogRecords()

				for k := range logRecords.Len() {
					logRecord := logRecords.At(k)
					result = append(result, logRecord.Body().AsString())
				}
			}
		}
	}

	return result
}

func makeBufferConsumer(t *testing.T, buf *logBuffer) consumer.Logs {
	t.Helper()

	cnsmr, err := consumer.NewLogs(func(_ context.Context, ld plog.Logs) error {
		buf.add(ld)

		return nil
	})
	if err != nil {
		t.Fatal("Failed to create log consumer:", err)
	}

	return cnsmr
}

var sortStringsOpt = cmpopts.SortSlices(func(x, y string) bool { return x < y }) //nolint:gochecknoglobals

func TestFileLogReceiver(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	f1, err := os.Create(filepath.Join(tmpDir, "f1.log"))
	if err != nil {
		t.Fatal("Can't create log file n°1:", err)
	}

	defer f1.Close()

	cfg := config.OTLPReceiver{
		Include: []string{
			filepath.Join(tmpDir, "*.log"),
		},
		OperatorsYAML: `- type: add
  field: resource['service.name']
  value: 'test'`,
	}

	logger, err := zap.NewDevelopment(zap.IncreaseLevel(zap.InfoLevel))
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}

	telSet := component.TelemetrySettings{
		Logger:         logger,
		TracerProvider: noop.NewTracerProvider(),
		MeterProvider:  noopM.NewMeterProvider(),
		MetricsLevel:   configtelemetry.LevelBasic,
		Resource:       pcommon.NewResource(),
	}

	logBuf := logBuffer{
		buf: make([]plog.Logs, 0, 2), // we plan to write 2 log lines
	}

	recv, err := newLogReceiver(cfg, makeBufferConsumer(t, &logBuf))
	if err != nil {
		t.Fatal("Failed to initialize log receiver:", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pipeline := pipelineContext{
		lastFileSizes:     make(map[string]int64),
		telemetry:         telSet,
		startedComponents: []component.Component{},
	}

	defer func() {
		shutdownAll(pipeline.startedComponents)
	}()

	err = recv.update(ctx, &pipeline)
	if err != nil {
		t.Fatal("Failed to update pipeline:", err)
	}

	if diff := cmp.Diff([]string{f1.Name()}, recv.currentlyWatching(), sortStringsOpt); diff != "" {
		t.Error("Unexpected watched log files (-want, +got):", diff)
	}

	f2, err := os.Create(filepath.Join(tmpDir, "f2.log"))
	if err != nil {
		t.Fatal("Can't create log file n°2:", err)
	}

	defer f2.Close()

	err = recv.update(ctx, &pipeline)
	if err != nil {
		t.Fatal("Failed to update pipeline:", err)
	}

	if diff := cmp.Diff([]string{f1.Name(), f2.Name()}, recv.currentlyWatching(), sortStringsOpt); diff != "" {
		t.Error("Unexpected watched log files (-want, +got):", diff)
	}

	time.Sleep(time.Second)

	_, err = f1.WriteString("f1 log 1")
	if err != nil {
		t.Fatal("Failed to write to log file n°1:", err)
	}

	_, err = f2.WriteString("f2 log 1")
	if err != nil {
		t.Fatal("Failed to write to log file n°2:", err)
	}

	time.Sleep(2 * time.Second)

	expectedLogLines := []string{
		"f1 log 1",
		"f2 log 1",
	}
	if diff := cmp.Diff(expectedLogLines, logBuf.getAllAsStrings(), sortStringsOpt); diff != "" {
		t.Fatal("Unexpected log lines (-want, +got):", diff)
	}

	expectedDiagnosticInfo := receiverDiagnosticInformation{
		LogProcessedCount:      2,
		LogThroughputPerMinute: 2,
		FileLogReceiverPaths: []string{
			f1.Name(),
			f2.Name(),
		},
		ExecLogReceiverPaths: []string{},
		IgnoredFilePaths:     []string{},
	}
	if diff := cmp.Diff(expectedDiagnosticInfo, recv.diagnosticInfo(), sortStringsOpt); diff != "" {
		t.Fatal("Unexpected diagnostic information (-want, +got):", diff)
	}
}
