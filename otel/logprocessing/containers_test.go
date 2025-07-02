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
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/facts"
	crTypes "github.com/bleemeo/glouton/facts/container-runtime/types"

	"github.com/google/go-cmp/cmp"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/fileconsumer/attrs"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	noopM "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace/noop"
	"go.uber.org/zap"
)

func makeCtrLog(t *testing.T, ts time.Time, body string) []byte {
	t.Helper()

	var ctrLog = struct { //nolint:gofumpt
		Log    string `json:"log"`
		Stream string `json:"stream"`
		Time   string `json:"time"`
	}{
		Log:    body,
		Stream: "stdout",
		Time:   ts.Format("2006-01-02T15:04:05.999999999Z"),
	}

	jsonLog, err := json.Marshal(ctrLog)
	if err != nil {
		t.Fatal("Can't marshal container log:", err)
	}

	return jsonLog
}

func TestHandleContainerLogs(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	f1, err := os.Create(filepath.Join(tmpDir, "ctr-1.log"))
	if err != nil {
		t.Fatal("Can't create log file n°1:", err)
	}

	defer f1.Close()

	f2, err := os.Create(filepath.Join(tmpDir, "ctr-2.log"))
	if err != nil {
		t.Fatal("Can't create log file n°2:", err)
	}

	defer f2.Close()

	knownOperators := map[string][]config.OTELOperator{
		"key_res_attr": {
			{
				"type":  "add",
				"field": "resource.key",
				"value": "val from op",
			},
		},
	}

	containerOperators := map[string]string{
		"ctr-1": "key_res_attr",
	}

	knownFilters := map[string]config.OTELFilters{}
	containerFilter := map[string]string{}

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

	logBuf := logBuffer{
		buf: make([]plog.Logs, 0, 2), // we plan to write 2 log lines
	}

	pipeline := pipelineContext{
		hostroot:      string(os.PathSeparator),
		lastFileSizes: make(map[string]int64),
		telemetry:     telSet,
		inputConsumer: makeBufferConsumer(t, &logBuf),
		commandRunner: noExecRunner(t),
		persister:     mustNewPersistHost(t),
	}

	containerRecv := newContainerReceiver(&pipeline, containerOperators, knownOperators, containerFilter, knownFilters)

	defer containerRecv.stop()

	ctrs := []facts.Container{
		facts.FakeContainer{
			FakeID:            "id-1",
			FakeContainerName: "ctr-1",
			FakeImageID:       "img-id-1",
			FakeImageName:     "img-1",
			FakeImageTags:     []string{"v1.2.3", "latest"},
			FakeLogPath:       f1.Name(),
			FakeRuntimeName:   crTypes.DockerRuntime,
		},
		facts.FakeContainer{
			FakeID:            "id-2",
			FakeContainerName: "ctr-2",
			FakeImageID:       "img-id-2",
			FakeImageName:     "img-2",
			FakeImageTags:     []string{"latest"},
			FakeLogPath:       f2.Name(),
			FakePodName:       "pod",
			FakePodNamespace:  "ns",
			FakeRuntimeName:   crTypes.ContainerDRuntime, // the runtime shouldn't have any impact on how we set up the processing
		},
	}

	for _, ctr := range ctrs {
		ops, err := buildOperators(knownOperators[containerOperators[ctr.ContainerName()]])
		if err != nil {
			t.Fatalf("Failed to build operators for container %s: %v", ctr.ContainerName(), err)
		}

		_, err = containerRecv.handleContainerLogs(ctx, ctr, ops, knownFilters[containerFilter[ctr.ContainerName()]])
		if err != nil {
			t.Fatalf("Failed to handle logs for container %s: %v", ctr.ContainerName(), err)
		}
	}

	time.Sleep(time.Second)

	now := time.Now().In(time.UTC)
	tsLog1 := now.Add(1 * time.Second)
	tsLog2 := now.Add(2 * time.Second)

	_, err = f1.Write(makeCtrLog(t, tsLog1, "f1 log 1"))
	if err != nil {
		t.Fatal("Failed to write to log file n°1:", err)
	}

	_, err = f2.Write(makeCtrLog(t, tsLog2, "f2 log 1"))
	if err != nil {
		t.Fatal("Failed to write to log file n°2:", err)
	}

	time.Sleep(2 * time.Second)

	expectedLogLines := []logRecord{
		{
			Timestamp: tsLog1,
			Body:      "f1 log 1",
			Attributes: map[string]any{
				attrContainerID:        "id-1",
				attrContainerImageName: "img-1",
				attrContainerImageTags: `["v1.2.3","latest"]`,
				attrContainerName:      "ctr-1",
				attrContainerRuntime:   crTypes.DockerRuntime,
				attrs.LogFileName:      "ctr-1.log",
				attrs.LogFilePath:      f1.Name(),
				"log.iostream":         "stdout",
			},
			Resource: map[string]any{
				"key": "val from op",
			},
		},
		{
			Timestamp: tsLog2,
			Body:      "f2 log 1",
			Attributes: map[string]any{
				attrContainerID:        "id-2",
				attrContainerImageName: "img-2",
				attrContainerImageTags: `["latest"]`,
				attrContainerName:      "ctr-2",
				attrContainerRuntime:   crTypes.ContainerDRuntime,
				attrContainerNamespace: "ns",
				attrContainerPod:       "pod",
				attrs.LogFileName:      "ctr-2.log",
				attrs.LogFilePath:      f2.Name(),
				"log.iostream":         "stdout",
			},
		},
	}
	if diff := cmp.Diff(expectedLogLines, logBuf.getAllRecords(), sortLogsOpt); diff != "" {
		t.Fatalf("Unexpected log lines (-want, +got):\n%s", diff)
	}
}
