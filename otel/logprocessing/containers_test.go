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
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/facts"
	crTypes "github.com/bleemeo/glouton/facts/container-runtime/types"
	"github.com/bleemeo/glouton/otel/logsource"

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
		Stream: testStreamStdout,
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
		testAttrKeyResAttr: {
			{
				testFieldType:  testFieldAdd,
				testFieldName:  testResourceKey,
				testFieldValue: "val from op",
			},
		},
	}

	containerOperators := map[string]string{
		testContainerCtr1: testAttrKeyResAttr,
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

	logBuf := logBuffer{
		buf: make([]plog.Logs, 0, 2),
	}

	pipeline := pipelineContext{
		hostroot:      string(os.PathSeparator),
		lastFileSizes: make(map[string]int64),
		telemetry:     telSet,
		inputConsumer: makeBufferConsumer(t, &logBuf),
		commandRunner: noExecRunner(t),
		persister:     mustNewPersistHost(t),
	}

	containerRecv := newContainerReceiver(&pipeline)

	defer containerRecv.stop()

	ctrs := []facts.Container{
		facts.FakeContainer{
			FakeID:            "id-1",
			FakeContainerName: testContainerCtr1,
			FakeImageID:       "img-id-1",
			FakeImageName:     "img-1",
			FakeImageTags:     []string{"v1.2.3", "latest"},
			FakeLogPath:       f1.Name(),
			FakeRuntimeName:   crTypes.DockerRuntime,
		},
		facts.FakeContainer{
			FakeID:            "id-2",
			FakeContainerName: testContainerCtr2,
			FakeImageID:       "img-id-2",
			FakeImageName:     "img-2",
			FakeImageTags:     []string{"latest"},
			FakeLogPath:       f2.Name(),
			FakePodName:       "pod",
			FakePodNamespace:  "ns",
			FakeRuntimeName:   crTypes.ContainerDRuntime, // runtime shouldn't affect processing setup
		},
	}

	for _, ctr := range ctrs {
		ops, err := logsource.BuildOperators(knownOperators[containerOperators[ctr.ContainerName()]])
		if err != nil {
			t.Fatalf("Failed to build operators for container %s: %v", ctr.ContainerName(), err)
		}

		_, err = containerRecv.handleContainerLogs(ctx, ctr, ops, nil)
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
				attrContainerName:      testContainerCtr1,
				attrContainerRuntime:   crTypes.DockerRuntime,
				attrs.LogFileName:      "ctr-1.log",
				attrs.LogFilePath:      f1.Name(),
				testAttrLogIOStream:    testStreamStdout,
			},
			Resource: map[string]any{
				testFieldKey: "val from op",
			},
		},
		{
			Timestamp: tsLog2,
			Body:      "f2 log 1",
			Attributes: map[string]any{
				attrContainerID:        "id-2",
				attrContainerImageName: "img-2",
				attrContainerImageTags: `["latest"]`,
				attrContainerName:      testContainerCtr2,
				attrContainerRuntime:   crTypes.ContainerDRuntime,
				attrContainerNamespace: "ns",
				attrContainerPod:       "pod",
				attrs.LogFileName:      "ctr-2.log",
				attrs.LogFilePath:      f2.Name(),
				testAttrLogIOStream:    testStreamStdout,
			},
		},
	}
	if diff := cmp.Diff(expectedLogLines, logBuf.getAllRecords(), sortLogsOpt); diff != "" {
		t.Fatalf("Unexpected log lines (-want, +got):\n%s", diff)
	}
}

// TestSetupContainerLogReceiverRollsBackExtensionOnFilterFailure guards against a regression where
// makeStorageFn registered a persistent extension before the log filter was built, but no error path
// after that point removed it -- leaking a stale extension on every failed setup attempt (e.g. retried on
// the next container scan). A malformed filter (a broken regex, decoded by buildLogFilterConfig but never
// validated: it's fed straight to setupContainerLogReceiver, bypassing the normal handleContainerLogs
// gate) makes CreateLogs fail on the log filter step, which is reached only after the receiver factories
// -- and their persistent extension -- were already set up successfully.
func TestSetupContainerLogReceiverRollsBackExtensionOnFilterFailure(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	f, err := os.Create(filepath.Join(tmpDir, "ctr.log"))
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer f.Close()

	filtersCfg, _, _ := buildLogFilterConfig(config.OTELFilters{
		testFieldInclude: map[string]any{
			testFilterMatchType: testRegexp,
			testFilterBodies:    []string{"[unclosed"},
		},
	})

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
		commandRunner: noExecRunner(t),
		persister:     mustNewPersistHost(t),
	}

	defer pipeline.shutdownAll()

	containerRecv := newContainerReceiver(&pipeline)
	defer containerRecv.stop()

	ctr := facts.FakeContainer{FakeID: "id-1", FakeContainerName: testContainerCtr1, FakeLogPath: f.Name()}
	logCtr := makeLogContainer(t.Context(), ctr, f.Name())

	err = containerRecv.setupContainerLogReceiver(t.Context(), logCtr, nil, filtersCfg)
	if err == nil {
		t.Fatal("Expected setupContainerLogReceiver to fail on the malformed filter")
	}

	if got := len(containerRecv.registeredExtensions); got != 0 {
		t.Errorf("Expected no leaked registeredExtensions entry after a failed setup, got %d: %v", got, containerRecv.registeredExtensions)
	}

	if got := len(containerRecv.startedComponents); got != 0 {
		t.Errorf("Expected no leaked startedComponents entry after a failed setup, got %d: %v", got, containerRecv.startedComponents)
	}
}

// TestStopWatchingForContainersCleansUpEvenWithoutStartedComponents guards against a regression where a
// container with a registeredExtensions entry but no startedComponents entry (e.g. setup failed before
// startedComponents was ever populated) had its extension permanently leaked: the old code's early
// continue, taken whenever startedComponents[ctrID] was absent, skipped the RemovePersistentExtsAndForget
// call and the map deletions entirely.
func TestStopWatchingForContainersCleansUpEvenWithoutStartedComponents(t *testing.T) {
	t.Parallel()

	pipeline := pipelineContext{persister: mustNewPersistHost(t)}
	containerRecv := newContainerReceiver(&pipeline)

	const ctrID = "orphaned-id"

	extID := pipeline.persister.NewPersistentExt("container/" + ctrID + "/some.log")
	containerRecv.registeredExtensions[ctrID] = []component.ID{extID}
	containerRecv.containers[ctrID] = Container{LogFilePath: "some.log"}
	// Deliberately no containerRecv.startedComponents[ctrID] entry.

	containerRecv.stopWatchingForContainers(t.Context(), []string{ctrID})

	if _, found := pipeline.persister.GetExtensions()[extID]; found {
		t.Error("Expected the orphaned container's extension to be removed from the persister")
	}

	if _, found := containerRecv.registeredExtensions[ctrID]; found {
		t.Error("Expected registeredExtensions to be cleaned up even without a startedComponents entry")
	}
}

// TestContainerReceiverSizesByFileSkipsOnlyTheFailingFile guards against a regression where one file's
// non-ErrNotExist stat error aborted SizesByFile entirely, discarding every other file's
// already-successfully-read size (same bug shape and fix as otel/logsource's managedSource.SizesByFile).
func TestContainerReceiverSizesByFileSkipsOnlyTheFailingFile(t *testing.T) {
	t.Parallel()

	pipeline := pipelineContext{persister: mustNewPersistHost(t)}
	cr := newContainerReceiver(&pipeline)

	cr.sizeFnByFile["good.log"] = func() (int64, error) { return 42, nil }
	cr.sizeFnByFile["bad.log"] = func() (int64, error) { return 0, errors.New("permission denied") } //nolint:err113
	cr.sizeFnByFile["gone.log"] = func() (int64, error) { return 0, fs.ErrNotExist }

	sizes, err := cr.SizesByFile()
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if diff := cmp.Diff(map[string]int64{containerFileSizePrefix + "good.log": 42}, sizes); diff != "" {
		t.Fatalf("Unexpected sizes (-want +got):\n%s", diff)
	}
}
