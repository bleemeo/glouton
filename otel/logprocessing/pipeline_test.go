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
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bleemeo/glouton/agent/state"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/otel/logsource"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/pdata/plog"
)

//nolint:gochecknoglobals
var (
	epochTS  = time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)
	erasedTS = time.Date(2025, 4, 24, 17, 28, 37, 0, time.UTC)
)

// makeTimeEraserOpt returns a cmp.Option that erases timestamps from logRecord objects for easier comparison.
func makeTimeEraserOpt(timeRe string) cmp.Option {
	eraseTimeRe := regexp.MustCompile(timeRe)

	filter := func(x, y logRecord) bool {
		return true
	}

	transformer := cmpopts.AcyclicTransformer("TimeEraser", func(v logRecord) logRecord {
		if !v.Timestamp.IsZero() && !v.Timestamp.Equal(epochTS) {
			v.Timestamp = erasedTS
		}

		v.Body = eraseTimeRe.ReplaceAllString(v.Body, "<time erased>")

		return v
	})

	return cmp.FilterValues(filter, transformer)
}

func TestPipeline(t *testing.T) { //nolint: maintidx
	t.Parallel()

	tmpDir := t.TempDir()

	customLogFile, err := os.Create(filepath.Join(tmpDir, "custom.log"))
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer customLogFile.Close()

	jsonLogFile, err := os.Create(filepath.Join(tmpDir, "json.log"))
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer jsonLogFile.Close()

	cfg := config.OpenTelemetry{
		ReceiversDefaultSendLogs: true, // none of the receivers below override this
		KnownLogFormats:          config.DefaultKnownLogFormats(),
		Receivers: map[string]config.LogReceiver{
			"custom-receiver": {
				"include": []string{customLogFile.Name()},
				"operators": []config.OTELOperator{
					{
						testFieldName:  testRouteServiceName,
						testFieldType:  testFieldAdd,
						testFieldValue: testCustomSvc,
					},
				},
				"log_format": "custom-format",
			},
			"filelog/later": {
				"include":    []string{jsonLogFile.Name()},
				"log_format": "json_golang_slog",
				"filters": config.OTELFilters{
					testFilterExclude: map[string]any{
						testFilterMatchType: "strict",
						"record_attributes": []map[string]any{
							{
								testFieldKey:   testDyn,
								testFieldValue: 2.,
							},
						},
					},
				},
			},
		},
	}
	cfg.KnownLogFormats["custom-format"] = []config.OTELOperator{
		{
			testFieldType:  testFieldAdd,
			testFieldName:  testResourceKey,
			testFieldValue: testCustomRes,
		},
	}

	st, err := state.LoadReadOnly("not", "used")
	if err != nil {
		t.Fatal("Can't instantiate state:", err)
	}

	persister, err := logsource.NewPersistHost(st, logsource.PersistConfig{
		StorageType:  logsource.PersistStorageType,
		CacheKey:     logsource.LogFileMetadataCacheKey,
		ArchivePath:  "log-processing/persister.json",
		SaveThrottle: saveFileSizesToCachePeriod,
	})
	if err != nil {
		t.Fatal("Can't instantiate persist host:", err)
	}

	logBuf := logBuffer{
		buf: make([]plog.Logs, 0, 2),
	}

	currentAvailability := new(atomic.Value)
	currentAvailability.Store(bleemeoTypes.LogsAvailabilityOk)

	pipeline, err := makePipeline(
		t.Context(),
		cfg,
		"/",
		noExecRunner(t),
		fakeFacter(),
		func(_ context.Context, b []byte) error {
			logs, err := new(plog.ProtoUnmarshaler).UnmarshalLogs(b)
			if err != nil {
				t.Fatal("Failed to unmarshal logs:", err)
			}

			logBuf.add(logs)

			return nil
		},
		func() bleemeoTypes.LogsAvailability {
			return currentAvailability.Load().(bleemeoTypes.LogsAvailability) //nolint: forcetypeassert
		},
		persister,
		func(errs ...error) {
			t.Errorf("Warnings were reported: %v", errs)
		},
		cfg.KnownLogFormats, // nothing to expand
		logsource.GetLastFileSizesFromCache(st, logsource.LogFileSizesCacheKey),
		pipelineOptions{
			batcherTimeout:           100 * time.Millisecond,
			logsAvailabilityCacheTTL: 100 * time.Millisecond,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	defer pipeline.shutdownAll()

	// Build a Manager around this pipeline directly (bypassing New(), which hardcodes slower pipelineOptions)
	// and register it as a SinkProvider, mirroring agent.go's wiring.
	man := &Manager{
		config:      cfg,
		pipeline:    pipeline,
		fanoutSinks: make(map[string]*fanoutSink),
	}

	receiverManager, err := logsource.NewReceiverManager(cfg, "/", st, noExecRunner(t))
	if err != nil {
		t.Fatal("Can't instantiate receiver manager:", err)
	}

	receiverManager.RegisterSinkProvider(man)

	if err := receiverManager.RescanReceivers(t.Context()); err != nil {
		t.Fatal("Failed to resolve configured receivers:", err)
	}

	t.Log("Setting up fileconsumers ...")
	time.Sleep(time.Second)

	_, err = customLogFile.WriteString("This is a custom log line.")
	if err != nil {
		t.Fatal("Failed to write log line:", err)
	}

	slogger := slog.New(slog.NewJSONHandler(jsonLogFile, &slog.HandlerOptions{Level: slog.LevelInfo}))

	slogger.InfoContext(t.Context(), "This is a json log line.")

	if err = customLogFile.Sync(); err != nil {
		t.Fatal("Failed to sync log file:", err)
	}

	if err = jsonLogFile.Sync(); err != nil {
		t.Fatal("Failed to sync log file:", err)
	}

	t.Log("Waiting for batcher ...")
	time.Sleep(time.Second)

	if throughput := pipeline.logThroughputMeter.Total(); throughput != 2 {
		t.Errorf("Expected a throughput of 2 logs/min, got %d", throughput)
	}

	expectedLogLines := []logRecord{
		{
			Timestamp: erasedTS,
			Body:      `{"time":"<time erased>","level":"INFO","msg":"This is a json log line."}`,
			Attributes: map[string]any{
				testAttrLogFileName: filepath.Base(jsonLogFile.Name()),
				testAttrLogFilePath: jsonLogFile.Name(),
			},
			Resource: map[string]any{testAttrHostName: testHostname},
			Severity: 9, // info
		},
		{
			Timestamp: epochTS,
			Body:      "This is a custom log line.",
			Attributes: map[string]any{
				testAttrLogFileName: filepath.Base(customLogFile.Name()),
				testAttrLogFilePath: customLogFile.Name(),
			},
			Resource: map[string]any{
				testAttrHostName:    testHostname,
				testFieldKey:        testCustomRes,
				testAttrServiceName: "custom-svc",
			},
		},
	}

	// slog JSON handler uses the RFC3339Nano layout to represent timestamps
	const jsonSlogTimeRe = `\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}.\d+((\+\d{2}:\d{2})|Z(\d{2}:\d{2})?)`

	timeEraserOpt := makeTimeEraserOpt(jsonSlogTimeRe)
	if diff := cmp.Diff(expectedLogLines, logBuf.getAllRecords(), timeEraserOpt); diff != "" {
		t.Fatalf("Unexpected logs (-want +got):\n%s", diff)
	}

	logBuf.reset()

	currentAvailability.Store(bleemeoTypes.LogsAvailabilityShouldBuffer) // temporarily block logs

	_, err = customLogFile.WriteString("This is a another log line.")
	if err != nil {
		t.Fatal("Failed to write log line:", err)
	}

	if err = customLogFile.Sync(); err != nil {
		t.Fatal("Failed to sync log file:", err)
	}

	t.Log("Waiting for batcher ...")
	time.Sleep(time.Second)

	if throughput := pipeline.logThroughputMeter.Total(); throughput != 2 { // still 2
		t.Errorf("Expected a throughput of 2 logs/min, got %d", throughput)
	}

	if diff := cmp.Diff([]logRecord{}, logBuf.getAllRecords(), timeEraserOpt); diff != "" {
		t.Fatalf("No logs should have been written, but:\n%s", diff)
	}

	currentAvailability.Store(bleemeoTypes.LogsAvailabilityOk) // re-allow logs

	t.Log("Waiting for retry ...")
	time.Sleep(5 * time.Second)

	if throughput := pipeline.logThroughputMeter.Total(); throughput != 3 {
		t.Errorf("Expected a throughput of 3 logs/min, got %d", throughput)
	}

	expectedLogLines = []logRecord{
		{
			Timestamp: epochTS,
			Body:      "This is a another log line.",
			Attributes: map[string]any{
				testAttrLogFileName: filepath.Base(customLogFile.Name()),
				testAttrLogFilePath: customLogFile.Name(),
			},
			Resource: map[string]any{
				testAttrHostName:    testHostname,
				testFieldKey:        testCustomRes,
				testAttrServiceName: "custom-svc",
			},
		},
	}
	if diff := cmp.Diff(expectedLogLines, logBuf.getAllRecords(), timeEraserOpt); diff != "" {
		t.Fatalf("Unexpected logs (-want +got):\n%s", diff)
	}

	logBuf.reset()

	for dyn := 1; dyn <= 3; dyn++ {
		slogger.WarnContext(t.Context(), "With dyn value "+strconv.Itoa(dyn), "dyn", dyn)
	}

	t.Log("Waiting for batcher ...")
	time.Sleep(time.Second)

	if throughput := pipeline.logThroughputMeter.Total(); throughput != 5 {
		t.Errorf("Expected a throughput of 5 logs/min, got %d", throughput)
	}

	if total := pipeline.logProcessedCount.Load(); total != 5 {
		t.Errorf("Expected a total of 5 logs records, got %d", total)
	}

	expectedLogLines = []logRecord{
		{
			Timestamp: erasedTS,
			Body:      `{"time":"<time erased>","level":"WARN","msg":"With dyn value 1","dyn":1}`,
			Attributes: map[string]any{
				"dyn":               1.,
				testAttrLogFileName: filepath.Base(jsonLogFile.Name()),
				testAttrLogFilePath: jsonLogFile.Name(),
			},
			Resource: map[string]any{testAttrHostName: testHostname},
			Severity: 13, // warn
		},
		// Log record with dyn=2 is filtered
		{
			Timestamp: erasedTS,
			Body:      `{"time":"<time erased>","level":"WARN","msg":"With dyn value 3","dyn":3}`,
			Attributes: map[string]any{
				"dyn":               3.,
				testAttrLogFileName: filepath.Base(jsonLogFile.Name()),
				testAttrLogFilePath: jsonLogFile.Name(),
			},
			Resource: map[string]any{testAttrHostName: testHostname},
			Severity: 13, // warn
		},
	}
	if diff := cmp.Diff(expectedLogLines, logBuf.getAllRecords(), timeEraserOpt); diff != "" {
		t.Fatalf("Unexpected logs (-want +got):\n%s", diff)
	}
}

// fakeShutdownComponent is a minimal component.Component recording whether Shutdown was called, used to
// verify shutdownAll actually reaches components started under a *logReceiver, not just p.startedComponents.
type fakeShutdownComponent struct {
	shutdownCalled atomic.Bool
}

func (c *fakeShutdownComponent) Start(context.Context, component.Host) error { return nil }

func (c *fakeShutdownComponent) Shutdown(context.Context) error {
	c.shutdownCalled.Store(true)

	return nil
}

// TestShutdownAllStopsPipelineReceivers guards against a regression where shutdownAll only stopped
// p.startedComponents, leaving journald/syslog/syslog-auth/auditd receivers (tracked in p.receivers,
// populated by setupJournald/setupSyslog/setupAuditD) running forever after a config reload or agent
// shutdown.
func TestShutdownAllStopsPipelineReceivers(t *testing.T) {
	t.Parallel()

	pipeline := pipelineContext{
		persister: mustNewPersistHost(t),
	}

	fake := &fakeShutdownComponent{}
	recv := &logReceiver{
		name:              "fake",
		watching:          map[string]logsource.ReceiverKind{},
		startedComponents: []component.Component{fake},
	}

	pipeline.receivers = append(pipeline.receivers, recv)

	pipeline.shutdownAll()

	if !fake.shutdownCalled.Load() {
		t.Error("Expected shutdownAll to shut down the receiver's started components via p.receivers")
	}
}
