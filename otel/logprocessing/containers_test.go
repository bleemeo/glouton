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
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/facts"
	crTypes "github.com/bleemeo/glouton/facts/container-runtime/types"
	"github.com/bleemeo/glouton/utils/gloutonexec"

	"github.com/google/go-cmp/cmp"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/fileconsumer/attrs"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	noopM "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace/noop"
	"go.uber.org/zap"
)

var errNotFound = errors.New("not found")

type dummyRuntime struct {
	crTypes.RuntimeInterface

	imageTags map[string][]string
}

func (r dummyRuntime) ImageTags(_ context.Context, _, imageName string) ([]string, error) {
	tags, ok := r.imageTags[imageName]
	if !ok {
		return nil, fmt.Errorf("image %q %w", imageName, errNotFound)
	}

	return tags, nil
}

type dummyContainer struct {
	facts.Container

	id           string
	name         string
	imageID      string
	imageName    string
	logPath      string
	podName      string
	podNamespace string
	runtimeName  string
}

func (c dummyContainer) ID() string {
	return c.id
}

func (c dummyContainer) ContainerName() string {
	return c.name
}

func (c dummyContainer) ImageID() string {
	return c.imageID
}

func (c dummyContainer) ImageName() string {
	return c.imageName
}

func (c dummyContainer) LogPath() string {
	return c.logPath
}

func (c dummyContainer) PodName() string {
	return c.podName
}

func (c dummyContainer) PodNamespace() string {
	return c.podNamespace
}

func (c dummyContainer) RuntimeName() string {
	return c.runtimeName
}

func makeCtrLog(t *testing.T, body string) []byte {
	t.Helper()

	var ctrLog = struct { //nolint:gofumpt
		Log    string `json:"log"`
		Stream string `json:"stream"`
		Time   string `json:"time"`
	}{
		Log:    body,
		Stream: "stdout",
		Time:   time.Now().Format("2006-01-02T15:04:05.999999999Z"),
	}

	jsonLog, err := json.Marshal(ctrLog)
	if err != nil {
		t.Fatal("Can't marshal container log:", err)
	}

	return jsonLog
}

func TestHandleContainersLogs(t *testing.T) {
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

	globalOperators := map[string][]config.OTELOperator{
		"key_res_attr": {
			{
				"type":  "add",
				"field": "resource.key",
				"value": "key attribute value",
			},
		},
	}

	containerOperators := map[string]string{
		"ctr-1": "key_res_attr",
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

	logBuf := logBuffer{
		buf: make([]plog.Logs, 0, 2), // we plan to write 2 log lines
	}

	pipeline := pipelineContext{
		hostroot:      string(os.PathSeparator),
		lastFileSizes: make(map[string]int64),
		telemetry:     telSet,
		inputConsumer: makeBufferConsumer(t, &logBuf),
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

	crRuntime := dummyRuntime{
		imageTags: map[string][]string{
			"img-1": {"v1.2.3", "latest"},
			"img-2": {"latest"},
		},
	}

	containerRecv := newContainerReceiver(&pipeline, containerOperators, globalOperators)

	defer containerRecv.stop()

	ctrs := []facts.Container{
		dummyContainer{
			id:          "id-1",
			name:        "ctr-1",
			imageID:     "img-id-1",
			imageName:   "img-1",
			logPath:     f1.Name(),
			runtimeName: crTypes.DockerRuntime,
		},
		dummyContainer{
			id:           "id-2",
			name:         "ctr-2",
			imageID:      "img-id-2",
			imageName:    "img-2",
			logPath:      f2.Name(),
			podName:      "pod",
			podNamespace: "ns",
			runtimeName:  crTypes.ContainerDRuntime, // the runtime shouldn't have any impact on how we set up the processing
		},
	}

	containerRecv.HandleContainersLogs(ctx, crRuntime, ctrs)

	time.Sleep(time.Second)

	_, err = f1.Write(makeCtrLog(t, "f1 log 1"))
	if err != nil {
		t.Fatal("Failed to write to log file n°1:", err)
	}

	_, err = f2.Write(makeCtrLog(t, "f2 log 1"))
	if err != nil {
		t.Fatal("Failed to write to log file n°2:", err)
	}

	time.Sleep(2 * time.Second)

	expectedLogLines := []logRecord{
		{
			Body: "f1 log 1",
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
				"key": "key attribute value",
			},
		},
		{
			Body: "f2 log 1",
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
