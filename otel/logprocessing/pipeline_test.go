package logprocessing

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/bleemeo/glouton/agent/state"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/config"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"go.opentelemetry.io/collector/pdata/plog"
)

//nolint: gochecknoglobals,gofmt,goimports,gofumpt
var (
	epochTS    = time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)
	erasedTime = time.Date(2025, 04, 24, 17, 28, 37, 0, time.UTC)
)

// eraseTimeOpt provides a cmp.Option that makes logs comparison easier,
// by erasing the timestamps from the logRecord objects. It works the following way:
// - if the Timestamp field is defined (not zero or epoch), it is replaced by erasedTime (an arbitrarily chosen value)
// - it replaces all the occurrences of patterns matching the given timeRe with the string "<time erased>".
func eraseTimeOpt(timeRe string) cmp.Option {
	eraseTimeRe := regexp.MustCompile(timeRe)

	filter := func(x, y logRecord) bool {
		return true
	}

	transformer := cmpopts.AcyclicTransformer("TimeEraser", func(v logRecord) logRecord {
		if !v.Timestamp.IsZero() && !v.Timestamp.Equal(epochTS) {
			v.Timestamp = erasedTime
		}

		v.Body = eraseTimeRe.ReplaceAllString(v.Body, "<time erased>")

		return v
	})

	return cmp.FilterValues(filter, transformer)
}

func TestPipeline(t *testing.T) {
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
		buf: make([]plog.Logs, 0, 2), // we plan to write 2 log lines
	}

	pipeline, err := makePipeline(
		t.Context(),
		cfg,
		"/",
		noExecRunner(t),
		func(_ context.Context, b []byte) error {
			logs, err := new(plog.ProtoUnmarshaler).UnmarshalLogs(b)
			if err != nil {
				t.Fatal("Failed to unmarshal logs:", err)
			}

			logBuf.add(logs)

			return nil
		},
		func() bleemeoTypes.LogsAvailability {
			return bleemeoTypes.LogsAvailabilityOk
		},
		persister,
		func(errs ...error) {
			t.Errorf("Warnings were reported: %v", errs)
		},
		getLastFileSizesFromCache(st),
	)
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		shutdownAll(pipeline.startedComponents)
	}()

	time.Sleep(time.Second)

	_, err = customLogFile.WriteString("This is a custom log line.")
	if err != nil {
		t.Fatal("Failed to write log line:", err)
	}

	slogger := slog.NewLogLogger(slog.NewJSONHandler(jsonLogFile, nil), slog.LevelInfo)

	slogger.Println("This is a json log line.")

	if err = customLogFile.Sync(); err != nil {
		t.Fatal("Failed to sync log file:", err)
	}

	if err = jsonLogFile.Sync(); err != nil {
		t.Fatal("Failed to sync log file:", err)
	}

	time.Sleep(10 * time.Second) // batcher timeout ...

	if throughput := pipeline.logThroughputMeter.Total(); throughput != 2 {
		t.Errorf("Expected a throughput of 2 logs/min, got %d", throughput)
	}

	expectedLogLines := []logRecord{
		{
			Timestamp: erasedTime,
			Body:      `{"time":"<time erased>","level":"INFO","msg":"This is a json log line."}`,
			Attributes: map[string]any{
				"log.file.name": filepath.Base(jsonLogFile.Name()),
				"log.file.path": jsonLogFile.Name(),
			},
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
				"key":          "custom-res",
				"service.name": "custom-svc",
			},
		},
	}

	const jsonSlogTimeRe = `\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}.\d+\+\d{2}:\d{2}`

	if diff := cmp.Diff(expectedLogLines, logBuf.getAllRecords(), eraseTimeOpt(jsonSlogTimeRe)); diff != "" {
		t.Fatalf("Unexpected logs (-want +got):\n%s", diff)
	}
}
