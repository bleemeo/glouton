// Copyright 2015-2024 Bleemeo
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
	"testing"
	"time"

	"github.com/bleemeo/glouton/config"

	"github.com/google/go-cmp/cmp"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/config/configtelemetry"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/processor"
	"go.opentelemetry.io/collector/processor/processorhelper"
	noopM "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace/noop"
	"go.uber.org/zap"
)

func makeBufferConsumer(t *testing.T, telSet component.TelemetrySettings, output *[]plog.Logs) processor.Logs {
	t.Helper()

	set := processor.Settings{
		TelemetrySettings: telSet,
	}

	cnsmr, err := consumer.NewLogs(func(_ context.Context, ld plog.Logs) error {
		*output = append(*output, ld)

		return nil
	})
	if err != nil {
		t.Fatal("Failed to create log consumer:", err)
	}

	prcsr, err := processorhelper.NewLogs(context.Background(), set, nil, cnsmr, func(_ context.Context, logs plog.Logs) (plog.Logs, error) {
		return logs, nil
	})
	if err != nil {
		t.Fatal("Failed to create log processor:", err)
	}

	return prcsr
}

func TestFileLogReceiver(t *testing.T) {
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

	var output []plog.Logs

	recv, err := newLogReceiver(cfg, makeBufferConsumer(t, telSet, &output))
	if err != nil {
		t.Fatal("Failed to initialize log receiver:", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pipeline := pipelineContext{
		lastFileSizes:     make(map[string]int64),
		telemetry:         telSet,
		startedComponents: []component.Component{},
		// logThroughputMeter: newRingCounter(1),
	}

	err = recv.update(ctx, &pipeline)
	if err != nil {
		t.Fatal("Failed to update pipeline:", err)
	}

	if diff := cmp.Diff([]string{f1.Name()}, recv.currentlyWatching()); diff != "" {
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

	if diff := cmp.Diff([]string{f1.Name(), f2.Name()}, recv.currentlyWatching()); diff != "" {
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

	if len(output) != 2 { // FIXME: improve check
		t.Errorf("Unexpected logs: want %d, got %d", 2, len(output))
	}
}
