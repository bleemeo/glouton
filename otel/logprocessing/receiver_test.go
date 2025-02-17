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
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/bleemeo/glouton/agent/state"
	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/utils/gloutonexec"
	"github.com/bleemeo/glouton/version"

	"github.com/go-viper/mapstructure/v2"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/adapter"
	"go.opentelemetry.io/collector/component"
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

	result := make([]string, 0, len(logBuf.buf)) // - there may be more than 1 log message per plog.Logs object

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

type dummyRunner struct {
	run            func(ctx context.Context, option gloutonexec.Option, cmd string, args ...string) ([]byte, error)
	startWithPipes func(ctx context.Context, option gloutonexec.Option, cmd string, args ...string) (stdoutPipe io.ReadCloser, stderrPipe io.ReadCloser, wait func() error, err error)
}

func (dr dummyRunner) Run(ctx context.Context, option gloutonexec.Option, cmd string, args ...string) ([]byte, error) {
	return dr.run(ctx, option, cmd, args...)
}

func (dr dummyRunner) StartWithPipes(ctx context.Context, option gloutonexec.Option, cmd string, args ...string) (stdoutPipe io.ReadCloser, stderrPipe io.ReadCloser, wait func() error, err error) {
	return dr.startWithPipes(ctx, option, cmd, args...)
}

// mustNewPersistHost is a shorthand to instantiate both a state and a persist host.
func mustNewPersistHost(t *testing.T) *persistHost {
	t.Helper()

	st, err := state.LoadReadOnly("not", "used")
	if err != nil {
		t.Fatal("Can't instantiate state:", err)
	}

	host, err := newPersistHost(st)
	if err != nil {
		t.Fatal("Can't instantiate persist host:", err)
	}

	return host
}

func addWarningsFn(t *testing.T) func(errs ...error) {
	t.Helper()

	return func(errs ...error) {
		t.Helper()

		t.Log("Warnings:", errs)
	}
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
		Operators: []map[string]any{
			{
				"type":  "add",
				"field": "resource['service.name']",
				"value": "apache_server",
			},
		},
	}

	logger, err := zap.NewDevelopment(zap.IncreaseLevel(zap.InfoLevel))
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}

	telSet := component.TelemetrySettings{
		Logger:         logger,
		TracerProvider: noop.NewTracerProvider(),
		MeterProvider:  noopM.NewMeterProvider(),
		Resource:       pcommon.NewResource(),
	}

	logBuf := logBuffer{
		buf: make([]plog.Logs, 0, 2), // we plan to write 2 log lines
	}

	recv, err := newLogReceiver("filelog/recv", cfg, string(os.PathSeparator), makeBufferConsumer(t, &logBuf))
	if err != nil {
		t.Fatal("Failed to initialize log receiver:", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pipeline := pipelineContext{
		lastFileSizes:     make(map[string]int64),
		telemetry:         telSet,
		startedComponents: []component.Component{},
		commandRunner: dummyRunner{
			run: func(_ context.Context, _ gloutonexec.Option, cmd string, args ...string) ([]byte, error) {
				t.Errorf("No command should have been executed during this test, but: %s %s", cmd, args)

				return nil, nil
			},
			startWithPipes: func(_ context.Context, _ gloutonexec.Option, cmd string, args ...string) (io.ReadCloser, io.ReadCloser, func() error, error) {
				t.Errorf("No command should have been executed during this test, but: %s %s", cmd, args)

				return nil, nil, nil, nil
			},
		},
		persister: mustNewPersistHost(t),
	}

	defer func() {
		shutdownAll(pipeline.startedComponents)
	}()

	err = recv.update(ctx, &pipeline, addWarningsFn(t))
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

	err = recv.update(ctx, &pipeline, addWarningsFn(t))
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

	fileSizes, err := recv.sizesByFile()
	if err != nil {
		t.Fatal("Failed to get file sizes:", err)
	}

	expectedFileSizes := map[string]int64{
		f1.Name(): 8,
		f2.Name(): 8,
	}
	if diff := cmp.Diff(expectedFileSizes, fileSizes); diff != "" {
		t.Fatal("Unexpected file sizes (-want, +got):", diff)
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

func TestFileLogReceiverWithHostroot(t *testing.T) {
	t.Parallel()

	// In this test, we ensure hostroot is handled correctly.
	// It shouldn't appear anywhere, as it is internal logic.
	const watchedFile = "/file.log"
	// We'll act as if this temp dir was the mountpoint of the host filesystem,
	// and the file we watch is at its root.
	hostRootPath := t.TempDir()

	file, err := os.Create(filepath.Join(hostRootPath, watchedFile))
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer file.Close()

	cfg := config.OTLPReceiver{
		Include: []string{
			watchedFile,
		},
		Operators: []map[string]any{
			{
				"type":  "add",
				"field": "resource['service.name']",
				"value": "apache_server",
			},
		},
	}

	logger, err := zap.NewDevelopment(zap.IncreaseLevel(zap.InfoLevel))
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}

	telSet := component.TelemetrySettings{
		Logger:         logger,
		TracerProvider: noop.NewTracerProvider(),
		MeterProvider:  noopM.NewMeterProvider(),
		Resource:       pcommon.NewResource(),
	}

	logBuf := logBuffer{
		buf: make([]plog.Logs, 0, 1), // we plan to write 1 log line
	}

	recv, err := newLogReceiver("recv-from-container", cfg, hostRootPath, makeBufferConsumer(t, &logBuf))
	if err != nil {
		t.Fatal("Failed to initialize log receiver:", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pipeline := pipelineContext{
		lastFileSizes:     make(map[string]int64),
		telemetry:         telSet,
		startedComponents: []component.Component{},
		commandRunner: dummyRunner{
			run: func(_ context.Context, _ gloutonexec.Option, cmd string, args ...string) ([]byte, error) {
				t.Errorf("No command should have been executed during this test, but: %s %s", cmd, args)

				return nil, nil
			},
			startWithPipes: func(_ context.Context, _ gloutonexec.Option, cmd string, args ...string) (io.ReadCloser, io.ReadCloser, func() error, error) {
				t.Errorf("No command should have been executed during this test, but: %s %s", cmd, args)

				return nil, nil, nil, nil
			},
		},
		persister: mustNewPersistHost(t),
	}

	defer func() {
		shutdownAll(pipeline.startedComponents)
	}()

	err = recv.update(ctx, &pipeline, addWarningsFn(t))
	if err != nil {
		t.Fatal("Failed to update pipeline:", err)
	}

	if diff := cmp.Diff([]string{watchedFile}, recv.currentlyWatching(), sortStringsOpt); diff != "" {
		t.Error("Unexpected watched log files (-want, +got):", diff)
	}

	time.Sleep(time.Second)

	_, err = file.WriteString("file log 1")
	if err != nil {
		t.Fatal("Failed to write to log file:", err)
	}

	time.Sleep(2 * time.Second)

	expectedLogLines := []string{"file log 1"}
	if diff := cmp.Diff(expectedLogLines, logBuf.getAllAsStrings(), sortStringsOpt); diff != "" {
		t.Fatal("Unexpected log lines (-want, +got):", diff)
	}

	fileSizes, err := recv.sizesByFile()
	if err != nil {
		t.Fatal("Failed to get file sizes:", err)
	}

	expectedFileSizes := map[string]int64{
		watchedFile: 10,
	}
	if diff := cmp.Diff(expectedFileSizes, fileSizes); diff != "" {
		t.Fatal("Unexpected file sizes (-want, +got):", diff)
	}

	expectedDiagnosticInfo := receiverDiagnosticInformation{
		LogProcessedCount:      1,
		LogThroughputPerMinute: 1,
		FileLogReceiverPaths:   []string{watchedFile},
		ExecLogReceiverPaths:   []string{},
		IgnoredFilePaths:       []string{},
	}
	if diff := cmp.Diff(expectedDiagnosticInfo, recv.diagnosticInfo(), sortStringsOpt); diff != "" {
		t.Fatal("Unexpected diagnostic information (-want, +got):", diff)
	}
}

func TestExecLogReceiver(t *testing.T) {
	if version.IsWindows() {
		t.Skip("We currently don't support accessing protected files on Windows.")
	}

	// This test must NOT run in parallel, since it replaces the statFile function.

	t.Cleanup(func() {
		// Restoring normal statFile function for other tests
		statFile = statFileImpl
	})

	tmpDir := t.TempDir()
	// Using the same file for all subtests, we won't open it anyway.
	file, err := os.Create(filepath.Join(tmpDir, "file.log"))
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer file.Close()

	cases := []struct {
		name             string
		previousFileSize int64
		currentFileSize  int64
		expectedTailArgs []string
	}{
		{
			name:             "new file",
			previousFileSize: -1, // -1 for no history
			currentFileSize:  7,
			expectedTailArgs: []string{"--follow=name", "--bytes=0", file.Name()},
		},
		{
			name:             "file has not changed",
			previousFileSize: 7,
			currentFileSize:  7,
			expectedTailArgs: []string{"--follow=name", "--bytes=+7", file.Name()},
		},
		{
			name:             "file has grown",
			previousFileSize: 7,
			currentFileSize:  10,
			expectedTailArgs: []string{"--follow=name", "--bytes=+7", file.Name()},
		},
		{
			name:             "file has been truncated",
			previousFileSize: 10,
			currentFileSize:  3,
			expectedTailArgs: []string{"--follow=name", "--bytes=+0", file.Name()},
		},
	}

	cfg := config.OTLPReceiver{
		Include: []string{file.Name()},
		Operators: []map[string]any{
			{
				"type":  "add",
				"field": "resource['service.name']",
				"value": "apache_server",
			},
		},
	}

	logger, err := zap.NewDevelopment(zap.IncreaseLevel(zap.InfoLevel))
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}

	telSet := component.TelemetrySettings{
		Logger:         logger,
		TracerProvider: noop.NewTracerProvider(),
		MeterProvider:  noopM.NewMeterProvider(),
		Resource:       pcommon.NewResource(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Replacing the statFile function with a mock to force the use of "sudo".
			statFile = func(string, string, CommandRunner) (ignore, needSudo bool, sizeFn func() (int64, error)) {
				return false, true, func() (int64, error) {
					return tc.currentFileSize, nil
				}
			}

			var startCmdCallsCount int

			pipeline := pipelineContext{
				lastFileSizes:     make(map[string]int64),
				telemetry:         telSet,
				startedComponents: []component.Component{},
				commandRunner: dummyRunner{
					run: func(_ context.Context, _ gloutonexec.Option, cmd string, args ...string) ([]byte, error) {
						t.Errorf("No command should have been executed using this method, but: %s %s", cmd, args)

						return nil, nil
					},
					startWithPipes: func(_ context.Context, _ gloutonexec.Option, _ string, args ...string) (stdoutPipe io.ReadCloser, stderrPipe io.ReadCloser, wait func() error, err error) {
						startCmdCallsCount++

						if diff := cmp.Diff(tc.expectedTailArgs, args); diff != "" {
							t.Error("Unexpected tail args (-want, +got):", diff)
						}

						nopReadCloser := io.NopCloser(bytes.NewReader(nil))

						return nopReadCloser, nopReadCloser, func() error { return nil }, nil
					},
				},
				persister: mustNewPersistHost(t),
			}

			if tc.previousFileSize >= 0 {
				pipeline.lastFileSizes[file.Name()] = tc.previousFileSize
			}

			defer func() {
				shutdownAll(pipeline.startedComponents)
			}()

			recv, err := newLogReceiver("root_files", cfg, string(os.PathSeparator), makeBufferConsumer(t, &logBuffer{buf: []plog.Logs{}}))
			if err != nil {
				t.Fatal("Failed to initialize log receiver:", err)
			}

			err = recv.update(ctx, &pipeline, addWarningsFn(t))
			if err != nil {
				t.Fatal("Failed to update pipeline:", err)
			}

			if diff := cmp.Diff([]string{file.Name()}, recv.currentlyWatching(), sortStringsOpt); diff != "" {
				t.Error("Unexpected watched log files (-want, +got):", diff)
			}

			if startCmdCallsCount != 1 {
				t.Fatalf("Starting command should have been called once, but has been %d times.", startCmdCallsCount)
			}
		})
	}
}

func TestRetryConfigIsUpToDate(t *testing.T) {
	consumerretryConfig := adapter.BaseConfig{}.RetryOnFailure

	err := mapstructure.Decode(retryCfg, &consumerretryConfig)
	if err != nil {
		t.Fatal("Failed to define consumerretry config:", err)
	}

	// Converting both consumerretryConfig and retryCfg to maps,
	// so we can compare them easily.

	var consumerretryCfgMap, retryCfgMap map[string]any

	err = mapstructure.Decode(consumerretryConfig, &consumerretryCfgMap)
	if err != nil {
		t.Fatal("Failed to convert consumerretry config to a map:", err)
	}

	err = mapstructure.Decode(retryCfg, &retryCfgMap)
	if err != nil {
		t.Fatal("Failed to convert retry config to a map:", err)
	}

	if diff := cmp.Diff(retryCfgMap, consumerretryCfgMap); diff != "" {
		t.Fatal("Unexpected consumerretry config (-want, +got):", diff)
	}
}
