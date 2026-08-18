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

package logprocessing

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/bleemeo/glouton/agent/state"
	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/otel/logsource"
	"github.com/bleemeo/glouton/utils/gloutonexec"
	"github.com/bleemeo/glouton/version"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/fileconsumer/attrs"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	noopM "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace/noop"
	"go.uber.org/zap"
)

var errSimulatedStartFailure = errors.New("simulated start failure")

type logRecord struct {
	Timestamp  time.Time
	Body       string
	Attributes map[string]any
	Resource   map[string]any
	Severity   int32
}

type logBuffer struct {
	l   sync.Mutex
	buf []plog.Logs
}

func (logBuf *logBuffer) add(ld plog.Logs) {
	logBuf.l.Lock()
	defer logBuf.l.Unlock()

	logBuf.buf = append(logBuf.buf, ld)
}

func (logBuf *logBuffer) getAllRecords() []logRecord {
	logBuf.l.Lock()
	defer logBuf.l.Unlock()

	result := make([]logRecord, 0, len(logBuf.buf)) // a plog.Logs may hold more than one log message

	for _, ld := range logBuf.buf {
		for i := range ld.ResourceLogs().Len() {
			resourceLog := ld.ResourceLogs().At(i)
			scopeLogs := resourceLog.ScopeLogs()

			// nil (not an empty map) avoids noise in diff expectations when there are no attributes.
			var resourceAttrs map[string]any

			if resourceLog.Resource().Attributes().Len() > 0 {
				resourceAttrs = resourceLog.Resource().Attributes().AsRaw()
			}

			for j := range scopeLogs.Len() {
				scopeLog := scopeLogs.At(j)
				logRecords := scopeLog.LogRecords()

				for k := range logRecords.Len() {
					logRec := logRecords.At(k)
					result = append(result, logRecord{
						Timestamp:  logRec.Timestamp().AsTime(),
						Body:       logRec.Body().Str(),
						Attributes: logRec.Attributes().AsRaw(),
						Resource:   resourceAttrs,
						Severity:   int32(logRec.SeverityNumber()),
					})
				}
			}
		}
	}

	return result
}

func (logBuf *logBuffer) reset() {
	logBuf.l.Lock()
	defer logBuf.l.Unlock()

	logBuf.buf = logBuf.buf[:0]
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

// noExecRunner returns a CommandRunner that marks the given test as fail if any command is executed.
func noExecRunner(t *testing.T) dummyRunner {
	t.Helper()

	return dummyRunner{
		run: func(_ context.Context, _ gloutonexec.Option, cmd string, args ...string) ([]byte, error) {
			t.Errorf("No command should have been executed during this test, but: %s %s", cmd, args)

			return nil, nil
		},
		startWithPipes: func(_ context.Context, _ gloutonexec.Option, cmd string, args ...string) (io.ReadCloser, io.ReadCloser, func() error, error) {
			t.Errorf("No command should have been executed during this test, but: %s %s", cmd, args)

			return nil, nil, nil, nil
		},
	}
}

type dummyFacter struct{}

func (dummyFacter) Facts(ctx context.Context, maxAge time.Duration) (facts map[string]string, err error) {
	return map[string]string{"hostname": testHostname}, nil
}

// fakeFacter returns a Facter than use hard coded facts (with just "hostname"="myhosname").
func fakeFacter() Facter {
	return dummyFacter{}
}

// mustNewPersistHost is a shorthand to instantiate both a state and a persist host.
func mustNewPersistHost(t *testing.T) *logsource.PersistHost {
	t.Helper()

	st, err := state.LoadReadOnly("not", "used")
	if err != nil {
		t.Fatal("Can't instantiate state:", err)
	}

	host, err := logsource.NewPersistHost(st, logsource.PersistConfig{
		StorageType:  logsource.PersistStorageType,
		CacheKey:     logsource.LogFileMetadataCacheKey,
		ArchivePath:  "log-processing/persister.json",
		SaveThrottle: saveFileSizesToCachePeriod,
	})
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

//nolint:gochecknoglobals
var (
	sortLogsOpt  = cmpopts.SortSlices(func(x, y logRecord) bool { return x.Body < y.Body })
	sortFilesOpt = cmpopts.SortSlices(func(x, y string) bool { return x < y })
)

func TestFileLogReceiver(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	f1, err := os.Create(filepath.Join(tmpDir, "f1.log"))
	if err != nil {
		t.Fatal("Can't create log file n°1:", err)
	}

	defer f1.Close()

	knownLogFormats := map[string][]config.OTELOperator{
		testAttrKeyResAttr: {
			{
				testFieldType:  testFieldAdd,
				testFieldName:  testResourceKey,
				testFieldValue: testKeyAttrValue,
			},
		},
	}

	cfg := config.LogReceiver{
		"include": []string{
			filepath.Join(tmpDir, "*.log"),
		},
		"operators": []config.OTELOperator{
			{
				testFieldType:  testFieldAdd,
				testFieldName:  testRouteServiceName,
				testFieldValue: testServiceApache,
			},
		},
		"log_format": testAttrKeyResAttr,
		"filters": config.OTELFilters{
			testFieldInclude: map[string]any{
				testFilterMatchType: testRegexp,
				testFilterBodies: []string{
					"log [13579]",
				},
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

	pipeline := pipelineContext{
		hostroot:          string(os.PathSeparator),
		lastFileSizes:     make(map[string]int64),
		telemetry:         telSet,
		startedComponents: []component.Component{},
		commandRunner:     noExecRunner(t),
		persister:         mustNewPersistHost(t),
	}

	defer pipeline.shutdownAll()

	logBuf := logBuffer{
		buf: make([]plog.Logs, 0, 2), // half the written lines are expected to be filtered out
	}

	recv, warn, err := newLogReceiver("filelog/recv", cfg, false, makeBufferConsumer(t, &logBuf), knownLogFormats, logsource.StatFile)
	if err != nil {
		t.Fatal("Failed to initialize log receiver:", err)
	}

	if warn != nil {
		t.Fatal("Got a warning during log receiver initialization:", warn)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	err = recv.update(ctx, &pipeline, addWarningsFn(t))
	if err != nil {
		t.Fatal("Failed to update pipeline:", err)
	}

	if diff := cmp.Diff([]string{f1.Name()}, recv.currentlyWatching(), sortFilesOpt); diff != "" {
		t.Errorf("Unexpected watched log files (-want, +got):\n%s", diff)
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

	if diff := cmp.Diff([]string{f1.Name(), f2.Name()}, recv.currentlyWatching(), sortFilesOpt); diff != "" {
		t.Errorf("Unexpected watched log files (-want, +got):\n%s", diff)
	}

	time.Sleep(time.Second)

	for i := 1; i <= 2; i++ {
		_, err = fmt.Fprintf(f1, "f1 log %d\n", i)
		if err != nil {
			t.Fatal("Failed to write to log file n°1:", err)
		}

		_, err = fmt.Fprintf(f2, "f2 log %d\n", i)
		if err != nil {
			t.Fatal("Failed to write to log file n°2:", err)
		}
	}

	time.Sleep(2 * time.Second)

	expectedLogLines := []logRecord{
		{
			Timestamp: time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC),
			Body:      "f1 log 1",
			Attributes: map[string]any{
				attrs.LogFileName: "f1.log",
				attrs.LogFilePath: f1.Name(),
			},
			Resource: map[string]any{
				testAttrServiceName: testServiceApache,
				testFieldKey:        testKeyAttrValue,
			},
		},
		{
			Timestamp: time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC),
			Body:      "f2 log 1",
			Attributes: map[string]any{
				attrs.LogFileName: "f2.log",
				attrs.LogFilePath: f2.Name(),
			},
			Resource: map[string]any{
				testAttrServiceName: testServiceApache,
				testFieldKey:        testKeyAttrValue,
			},
		},
	}
	if diff := cmp.Diff(expectedLogLines, logBuf.getAllRecords(), sortLogsOpt); diff != "" {
		t.Fatalf("Unexpected log lines (-want, +got):\n%s", diff)
	}

	fileSizes, err := recv.SizesByFile()
	if err != nil {
		t.Fatal("Failed to get file sizes:", err)
	}

	expectedFileSizes := map[string]int64{
		f1.Name(): 18,
		f2.Name(): 18,
	}
	if diff := cmp.Diff(expectedFileSizes, fileSizes); diff != "" {
		t.Fatalf("Unexpected file sizes (-want, +got):\n%s", diff)
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
	if diff := cmp.Diff(expectedDiagnosticInfo, recv.diagnosticInfo(), sortFilesOpt); diff != "" {
		t.Fatalf("Unexpected diagnostic information (-want, +got):\n%s", diff)
	}
}

// TestLogReceiverRetriesFilterSetupAfterFailure checks that update() only marks filter setup done once
// setupFilters has actually succeeded, so a receiver whose filters: config fails to build on the first
// attempt retries it later instead of shipping every matched file unfiltered forever. The invalid regex
// used here is only rejected at filterprocessor.CreateLogs time, not at decode time (same trick as
// TestSetupContainerLogReceiverRollsBackExtensionOnFilterFailure).
func TestLogReceiverRetriesFilterSetupAfterFailure(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	f1, err := os.Create(filepath.Join(tmpDir, "f1.log"))
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer f1.Close()

	cfg := config.LogReceiver{
		"include": []string{
			filepath.Join(tmpDir, "*.log"),
		},
		"filters": config.OTELFilters{
			testFieldInclude: map[string]any{
				testFilterMatchType: testRegexp,
				testFilterBodies: []string{
					"[unclosed",
				},
			},
		},
	}

	logger, err := zap.NewDevelopment(zap.IncreaseLevel(zap.InfoLevel))
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}

	pipeline := pipelineContext{
		hostroot:      string(os.PathSeparator),
		lastFileSizes: make(map[string]int64),
		telemetry: component.TelemetrySettings{
			Logger:         logger,
			TracerProvider: noop.NewTracerProvider(),
			MeterProvider:  noopM.NewMeterProvider(),
			Resource:       pcommon.NewResource(),
		},
		startedComponents: []component.Component{},
		commandRunner:     noExecRunner(t),
		persister:         mustNewPersistHost(t),
	}

	defer pipeline.shutdownAll()

	logBuf := logBuffer{buf: make([]plog.Logs, 0, 1)}

	recv, warn, err := newLogReceiver("filelog/recv", cfg, false, makeBufferConsumer(t, &logBuf), nil, logsource.StatFile)
	if err != nil {
		t.Fatal("Failed to initialize log receiver:", err)
	}

	if warn != nil {
		t.Fatal("Got a warning during log receiver initialization:", warn)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	if err := recv.update(ctx, &pipeline, addWarningsFn(t)); err == nil {
		t.Fatal("Expected update() to fail on the malformed filter")
	}

	if recv.setupFilterDone {
		t.Fatal("Expected setupFilterDone to stay false after a failed setupFilters, so the next update() retries it")
	}

	if got := len(recv.currentlyWatching()); got != 0 {
		t.Fatalf("Expected no file to be watched after a failed update(), got %d: %v", got, recv.currentlyWatching())
	}

	// Swap in a valid filter config, as a corrected regex would, then retry: this must re-run setupFilters
	// rather than no-op on a setupFilterDone left over from the failed attempt.
	validFilterCfg, warn, err := buildLogFilterConfig(config.OTELFilters{
		testFieldInclude: map[string]any{
			testFilterMatchType: testRegexp,
			testFilterBodies: []string{
				"valid",
			},
		},
	})
	if err != nil || warn != nil {
		t.Fatalf("Failed to build the valid filter config: err=%v warn=%v", err, warn)
	}

	recv.filterCfg = validFilterCfg

	if err := recv.update(ctx, &pipeline, addWarningsFn(t)); err != nil {
		t.Fatal("Expected the retried update() to succeed once the filter config is valid:", err)
	}

	if !recv.setupFilterDone {
		t.Error("Expected setupFilterDone to be true after a successful setupFilters")
	}

	if diff := cmp.Diff([]string{f1.Name()}, recv.currentlyWatching(), sortFilesOpt); diff != "" {
		t.Errorf("Unexpected watched log files after the retry (-want, +got):\n%s", diff)
	}
}

// TestNginxBothDefaultRouteDoesNotDropUnmatchedLines guards against a regression where the "nginx_both"
// composite log format's router had no "default" route (unlike "haproxy", which does): a line matching
// neither the access nor the error sub-pattern was silently dropped by the stanza router transformer
// instead of degrading through the access parser like haproxy's equivalent case.
func TestNginxBothDefaultRouteDoesNotDropUnmatchedLines(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	f, err := os.Create(filepath.Join(tmpDir, "nginx.log"))
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer f.Close()

	knownLogFormats, err := logsource.ExpandLogFormats(config.DefaultKnownLogFormats())
	if err != nil {
		t.Fatalf("Failed to expand default known log formats: %v", err)
	}

	cfg := config.LogReceiver{
		"include":    []string{f.Name()},
		"log_format": "nginx_both",
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

	pipeline := pipelineContext{
		hostroot:          string(os.PathSeparator),
		lastFileSizes:     make(map[string]int64),
		telemetry:         telSet,
		startedComponents: []component.Component{},
		commandRunner:     noExecRunner(t),
		persister:         mustNewPersistHost(t),
	}

	defer pipeline.shutdownAll()

	logBuf := logBuffer{buf: make([]plog.Logs, 0, 1)}

	recv, warn, err := newLogReceiver("filelog/nginx", cfg, false, makeBufferConsumer(t, &logBuf), knownLogFormats, logsource.StatFile)
	if err != nil {
		t.Fatal("Failed to initialize log receiver:", err)
	}

	if warn != nil {
		t.Fatal("Got a warning during log receiver initialization:", warn)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	if err := recv.update(ctx, &pipeline, addWarningsFn(t)); err != nil {
		t.Fatal("Failed to update pipeline:", err)
	}

	time.Sleep(time.Second)

	// Matches neither nginx_both's access pattern (starts with an IP) nor its error pattern (starts with
	// a "YYYY/MM/DD ... [error]" timestamp).
	if _, err := f.WriteString("this line matches neither the nginx access nor error pattern\n"); err != nil {
		t.Fatal("Failed to write to log file:", err)
	}

	time.Sleep(2 * time.Second)

	if got := len(logBuf.getAllRecords()); got != 1 {
		t.Fatalf("Expected the unmatched line to still be shipped (not dropped) via the router's default route, got %d records: %v", got, logBuf.getAllRecords())
	}
}

func TestFileLogReceiverWithHostroot(t *testing.T) {
	t.Parallel()

	// Test that hostroot stays internal and never leaks into watched/reported paths.
	const watchedFile = "/file.log"
	// hostRootPath acts as the mountpoint of the host filesystem.
	hostRootPath := t.TempDir()

	file, err := os.Create(filepath.Join(hostRootPath, watchedFile))
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer file.Close()

	cfg := config.LogReceiver{
		"include": []string{
			watchedFile,
		},
		"operators": []config.OTELOperator{
			{
				testFieldType:  testFieldAdd,
				testFieldName:  testRouteServiceName,
				testFieldValue: testServiceApache,
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
		buf: make([]plog.Logs, 0, 1),
	}

	recv, warn, err := newLogReceiver("recv-from-container", cfg, false, makeBufferConsumer(t, &logBuf), map[string][]config.OTELOperator{}, logsource.StatFile)
	if err != nil {
		t.Fatal("Failed to initialize log receiver:", err)
	}

	if warn != nil {
		t.Fatal("Got a warning during log receiver initialization:", warn)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	pipeline := pipelineContext{
		hostroot:          hostRootPath,
		lastFileSizes:     make(map[string]int64),
		telemetry:         telSet,
		startedComponents: []component.Component{},
		commandRunner:     noExecRunner(t),
		persister:         mustNewPersistHost(t),
	}

	defer pipeline.shutdownAll()

	err = recv.update(ctx, &pipeline, addWarningsFn(t))
	if err != nil {
		t.Fatal("Failed to update pipeline:", err)
	}

	if diff := cmp.Diff([]string{watchedFile}, recv.currentlyWatching(), sortFilesOpt); diff != "" {
		t.Errorf("Unexpected watched log files (-want, +got):\n%s", diff)
	}

	time.Sleep(time.Second)

	const logLine = "file log 1"

	_, err = file.WriteString(logLine)
	if err != nil {
		t.Fatal("Failed to write to log file:", err)
	}

	time.Sleep(2 * time.Second)

	expectedLogLines := []logRecord{
		{
			Timestamp: time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC),
			Body:      logLine,
			Attributes: map[string]any{
				attrs.LogFileName: "file.log",  // base name
				attrs.LogFilePath: watchedFile, // absolute path
			},
			Resource: map[string]any{
				testAttrServiceName: testServiceApache,
			},
		},
	}
	if diff := cmp.Diff(expectedLogLines, logBuf.getAllRecords(), sortLogsOpt); diff != "" {
		t.Fatalf("Unexpected log lines (-want, +got):\n%s", diff)
	}

	fileSizes, err := recv.SizesByFile()
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
	if diff := cmp.Diff(expectedDiagnosticInfo, recv.diagnosticInfo(), sortFilesOpt); diff != "" {
		t.Fatalf("Unexpected diagnostic information (-want, +got):\n%s", diff)
	}
}

func TestExecLogReceiver(t *testing.T) {
	if version.IsWindows() {
		t.Skip("We currently don't support accessing protected files on Windows.")
	}

	t.Parallel()

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
			expectedTailArgs: []string{testFollowName, "--bytes=0", file.Name()},
		},
		{
			name:             "file has not changed",
			previousFileSize: 7,
			currentFileSize:  7,
			expectedTailArgs: []string{testFollowName, "--bytes=+7", file.Name()},
		},
		{
			name:             "file has grown",
			previousFileSize: 7,
			currentFileSize:  10,
			expectedTailArgs: []string{testFollowName, "--bytes=+7", file.Name()},
		},
		{
			name:             "file has been truncated",
			previousFileSize: 10,
			currentFileSize:  3,
			expectedTailArgs: []string{testFollowName, "--bytes=+0", file.Name()},
		},
	}

	cfg := config.LogReceiver{
		"include": []string{file.Name()},
		"operators": []config.OTELOperator{
			{
				testFieldType:  testFieldAdd,
				testFieldName:  testRouteServiceName,
				testFieldValue: testServiceApache,
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

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Replacing the statFile function with a mock to force the use of "sudo".
			statFile := func(string, string, CommandRunner) (ignore, needSudo bool, sizeFn func() (int64, error)) {
				return false, true, func() (int64, error) {
					return tc.currentFileSize, nil
				}
			}

			var startCmdCallsCount int

			pipeline := pipelineContext{
				hostroot:          string(os.PathSeparator),
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
							t.Errorf("Unexpected tail args (-want, +got):\n%s", diff)
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

			defer pipeline.shutdownAll()

			recv, warn, err := newLogReceiver("root_files", cfg, false, makeBufferConsumer(t, &logBuffer{buf: []plog.Logs{}}), map[string][]config.OTELOperator{}, statFile)
			if err != nil {
				t.Fatal("Failed to initialize log receiver:", err)
			}

			if warn != nil {
				t.Fatal("Got a warning during log receiver initialization:", warn)
			}

			err = recv.update(ctx, &pipeline, addWarningsFn(t))
			if err != nil {
				t.Fatal("Failed to update pipeline:", err)
			}

			if diff := cmp.Diff([]string{file.Name()}, recv.currentlyWatching(), sortFilesOpt); diff != "" {
				t.Errorf("Unexpected watched log files (-want, +got):\n%s", diff)
			}

			if startCmdCallsCount != 1 {
				t.Fatalf("Starting command should have been called once, but has been %d times.", startCmdCallsCount)
			}
		})
	}
}

// TestFileLogReceiverPartialBatchFailureDoesNotDuplicate guards against a regression where update()
// resolved a whole batch of new files through one SetupLogReceiverFactories call and only recorded
// r.watching after every file in the batch started successfully: if a later file in the batch failed to
// start, an earlier file's already-started receiver was left out of r.watching, so the next update() call
// started a second, duplicate receiver tailing (and shipping) that same file. Since update() now starts
// files one at a time (startFile), a failing file must not affect any other file in the same or a later call.
func TestFileLogReceiverPartialBatchFailureDoesNotDuplicate(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	fileOK, err := os.Create(filepath.Join(tmpDir, "ok.log"))
	if err != nil {
		t.Fatal("Can't create fileOK:", err)
	}

	defer fileOK.Close()

	fileFail, err := os.Create(filepath.Join(tmpDir, "fail.log"))
	if err != nil {
		t.Fatal("Can't create fileFail:", err)
	}

	defer fileFail.Close()

	// Force sudo/exec mode for both files, so Start() goes through commandRunner.StartWithPipes, which
	// we can make fail deterministically for one specific file.
	statFile := func(string, string, CommandRunner) (ignore, needSudo bool, sizeFn func() (int64, error)) {
		return false, true, func() (int64, error) { return 0, nil }
	}

	startCallsByFile := map[string]int{}

	testLogger, err := zap.NewDevelopment(zap.IncreaseLevel(zap.InfoLevel))
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}

	telSet := component.TelemetrySettings{
		Logger:         testLogger,
		TracerProvider: noop.NewTracerProvider(),
		MeterProvider:  noopM.NewMeterProvider(),
		Resource:       pcommon.NewResource(),
	}

	pipeline := pipelineContext{
		hostroot:          string(os.PathSeparator),
		lastFileSizes:     make(map[string]int64),
		telemetry:         telSet,
		startedComponents: []component.Component{},
		commandRunner: dummyRunner{
			run: func(_ context.Context, _ gloutonexec.Option, cmd string, args ...string) ([]byte, error) {
				t.Errorf("No command should have been executed using this method, but: %s %s", cmd, args)

				return nil, nil
			},
			startWithPipes: func(_ context.Context, _ gloutonexec.Option, _ string, args ...string) (io.ReadCloser, io.ReadCloser, func() error, error) {
				file := args[len(args)-1]
				startCallsByFile[file]++

				if file == fileFail.Name() {
					return nil, nil, nil, fmt.Errorf("%w: %s", errSimulatedStartFailure, file)
				}

				nopReadCloser := io.NopCloser(bytes.NewReader(nil))

				return nopReadCloser, nopReadCloser, func() error { return nil }, nil
			},
		},
		persister: mustNewPersistHost(t),
	}

	defer pipeline.shutdownAll()

	cfg := config.LogReceiver{
		"include": []string{fileOK.Name(), fileFail.Name()},
	}

	recv, warn, err := newLogReceiver("root_files", cfg, false, makeBufferConsumer(t, &logBuffer{buf: []plog.Logs{}}), map[string][]config.OTELOperator{}, statFile)
	if err != nil {
		t.Fatal("Failed to initialize log receiver:", err)
	}

	if warn != nil {
		t.Fatal("Got a warning during log receiver initialization:", warn)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	err = recv.update(ctx, &pipeline, addWarningsFn(t))
	if err == nil {
		t.Fatal("Expected update() to return an error for the failing file")
	}

	if diff := cmp.Diff([]string{fileOK.Name()}, recv.currentlyWatching(), sortFilesOpt); diff != "" {
		t.Errorf("Unexpected watched log files after the first update() (-want, +got):\n%s", diff)
	}

	// Second call: fileOK must not be restarted (it's already watched), fileFail is retried since it
	// never got marked as watched.
	err = recv.update(ctx, &pipeline, addWarningsFn(t))
	if err == nil {
		t.Fatal("Expected update() to return an error for the still-failing file")
	}

	if diff := cmp.Diff([]string{fileOK.Name()}, recv.currentlyWatching(), sortFilesOpt); diff != "" {
		t.Errorf("Unexpected watched log files after the second update() (-want, +got):\n%s", diff)
	}

	if got := startCallsByFile[fileOK.Name()]; got != 1 {
		t.Errorf("Expected fileOK to be started exactly once across both update() calls, got %d", got)
	}

	if got := startCallsByFile[fileFail.Name()]; got != 2 {
		t.Errorf("Expected fileFail to be retried on both update() calls, got %d", got)
	}
}
