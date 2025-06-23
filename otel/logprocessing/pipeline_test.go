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

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"go.opentelemetry.io/collector/pdata/plog"
)

//nolint:gochecknoglobals,gofmt,goimports,gofumpt
var (
	epochTS  = time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)
	erasedTS = time.Date(2025, 04, 24, 17, 28, 37, 0, time.UTC)
)

// makeTimeEraserOpt provides a cmp.Option that makes logs comparison easier,
// by erasing the timestamps from the logRecord objects. It works the following way:
// - if the Timestamp field is defined (not zero or epoch), it is replaced by erasedTS (an arbitrarily chosen value)
// - it replaces all the occurrences of patterns matching the given timeRe with the string "<time erased>".
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
		KnownLogFormats: config.DefaultKnownLogFormats(),
		Receivers: map[string]config.OTLPReceiver{
			"custom-receiver": {
				Include: []string{customLogFile.Name()},
				Operators: []config.OTELOperator{
					{
						"field": "resource['service.name']",
						"type":  "add",
						"value": "custom-svc",
					},
				},
				LogFormat: "custom-format",
			},
			"filelog/later": {
				Include:   []string{jsonLogFile.Name()},
				LogFormat: "json_golang_slog",
				Filters: config.OTELFilters{
					"exclude": map[string]any{
						"match_type": "strict",
						"record_attributes": []map[string]any{
							{
								"key":   "dyn",
								"value": 2.,
							},
						},
					},
				},
			},
		},
	}
	cfg.KnownLogFormats["custom-format"] = []config.OTELOperator{
		{
			"type":  "add",
			"field": "resource.key",
			"value": "custom-res",
		},
	}

	st, err := state.LoadReadOnly("not", "used")
	if err != nil {
		t.Fatal("Can't instantiate state:", err)
	}

	persister, err := newPersistHost(st)
	if err != nil {
		t.Fatal("Can't instantiate persist host:", err)
	}

	logBuf := logBuffer{
		buf: make([]plog.Logs, 0, 2), // we plan to write 2 log lines (at a time)
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
		getLastFileSizesFromCache(st),
		pipelineOptions{
			batcherTimeout:           100 * time.Millisecond,
			logsAvailabilityCacheTTL: 100 * time.Millisecond,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		shutdownAll(pipeline.startedComponents)
	}()

	t.Log("Setting up fileconsumers ...")
	time.Sleep(time.Second)

	_, err = customLogFile.WriteString("This is a custom log line.")
	if err != nil {
		t.Fatal("Failed to write log line:", err)
	}

	slogger := slog.New(slog.NewJSONHandler(jsonLogFile, &slog.HandlerOptions{Level: slog.LevelInfo}))

	slogger.Info("This is a json log line.")

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
				"log.file.name": filepath.Base(jsonLogFile.Name()),
				"log.file.path": jsonLogFile.Name(),
			},
			Resource: map[string]any{"host.name": "myhostname"},
			Severity: 9, // info
		},
		{
			Timestamp: epochTS,
			Body:      "This is a custom log line.",
			Attributes: map[string]any{
				"log.file.name": filepath.Base(customLogFile.Name()),
				"log.file.path": customLogFile.Name(),
			},
			Resource: map[string]any{
				"host.name":    "myhostname",
				"key":          "custom-res",
				"service.name": "custom-svc",
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
				"log.file.name": filepath.Base(customLogFile.Name()),
				"log.file.path": customLogFile.Name(),
			},
			Resource: map[string]any{
				"host.name":    "myhostname",
				"key":          "custom-res",
				"service.name": "custom-svc",
			},
		},
	}
	if diff := cmp.Diff(expectedLogLines, logBuf.getAllRecords(), timeEraserOpt); diff != "" {
		t.Fatalf("Unexpected logs (-want +got):\n%s", diff)
	}

	logBuf.reset()

	for dyn := 1; dyn <= 3; dyn++ {
		slogger.Warn("With dyn value "+strconv.Itoa(dyn), "dyn", dyn)
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
				"dyn":           1.,
				"log.file.name": filepath.Base(jsonLogFile.Name()),
				"log.file.path": jsonLogFile.Name(),
			},
			Resource: map[string]any{"host.name": "myhostname"},
			Severity: 13, // warn
		},
		// Log record with dyn=2 is filtered
		{
			Timestamp: erasedTS,
			Body:      `{"time":"<time erased>","level":"WARN","msg":"With dyn value 3","dyn":3}`,
			Attributes: map[string]any{
				"dyn":           3.,
				"log.file.name": filepath.Base(jsonLogFile.Name()),
				"log.file.path": jsonLogFile.Name(),
			},
			Resource: map[string]any{"host.name": "myhostname"},
			Severity: 13, // warn
		},
	}
	if diff := cmp.Diff(expectedLogLines, logBuf.getAllRecords(), timeEraserOpt); diff != "" {
		t.Fatalf("Unexpected logs (-want +got):\n%s", diff)
	}
}
