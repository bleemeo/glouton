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

package logmetrics

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/facts"
	crTypes "github.com/bleemeo/glouton/facts/container-runtime/types"
	glmodel "github.com/bleemeo/glouton/prometheus/model"
	"github.com/bleemeo/glouton/prometheus/registry"
)

// fakeRuntime implements crTypes.RuntimeInterface by embedding a nil interface and
// overriding only Containers, the sole method the Manager under test calls. Calling
// any other method would panic, which is fine: this test never does.
type fakeRuntime struct {
	crTypes.RuntimeInterface

	containers []facts.Container
}

func (f *fakeRuntime) Containers(context.Context, time.Duration, bool) ([]facts.Container, error) {
	return f.containers, nil
}

// TestManagerStaticSource is an end-to-end test of a legacy path-based log.inputs
// entry: a real file, a real OTel filelogreceiver+countconnector pipeline, through
// the Manager.
func TestManagerStaticSource(t *testing.T) {
	t.Parallel()

	logFile, err := os.CreateTemp(t.TempDir(), "app-*.log")
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer logFile.Close()

	cfg := config.Log{
		Inputs: []config.LogInput{
			{Path: logFile.Name(), Filters: []config.LogFilter{
				{Metric: "app_errors_count", Regex: `\[error\]`},
			}},
		},
	}

	man := New(cfg, "/", &fakeRuntime{})

	// Metric names must be known immediately, before any matching line was seen.
	if names := man.MetricNames(); len(names) != 1 || names[0] != "app_errors_count" {
		t.Fatalf("Expected metric %q to be pre-registered, got %v", "app_errors_count", names)
	}

	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan error, 1)

	go func() {
		done <- man.Run(ctx)
	}()

	time.Sleep(500 * time.Millisecond)

	lines := []string{
		"normal request line\n",
		"[error] something broke\n",
		"another normal line\n",
		"[error] something else broke\n",
	}

	for _, line := range lines {
		if _, err := logFile.WriteString(line); err != nil {
			t.Fatal("Failed to write log line:", err)
		}
	}

	if err := logFile.Sync(); err != nil {
		t.Fatal("Failed to sync log file:", err)
	}

	time.Sleep(time.Second)

	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Manager.Run did not return after context cancellation")
	}

	appender := glmodel.NewBufferAppender()

	if err := man.EmitMetrics(t.Context(), registry.GatherState{}, appender); err != nil {
		t.Fatal("EmitMetrics returned an error:", err)
	}

	mfs, err := appender.AsMF()
	if err != nil {
		t.Fatal("AsMF returned an error:", err)
	}

	if len(mfs) != 1 || mfs[0].GetName() != "app_errors_count" {
		t.Fatalf("Expected exactly 1 metric family named app_errors_count, got %v", mfs)
	}

	if got := mfs[0].GetMetric()[0].GetUntyped().GetValue(); got != 2.0/windowSecs {
		t.Errorf("Expected rate %v, got %v", 2.0/windowSecs, got)
	}
}

// TestManagerContainerSource is an end-to-end test of a legacy container_name-based
// log.inputs entry: a fake container whose log file is a real, Docker-JSON-wrapped
// temp file, resolved and processed dynamically by the Manager's container polling
// loop. This is the regression test for the original RabbitMQ bug: the log line is
// wrapped exactly like a real Docker container log, and the filter only matches the
// unwrapped message, so it also verifies the "container" envelope operator runs
// before OTTL matching.
func TestManagerContainerSource(t *testing.T) {
	t.Parallel()

	logFile, err := os.CreateTemp(t.TempDir(), "rabbitmq-*.log")
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer logFile.Close()

	ctr := facts.FakeContainer{
		FakeContainerName: "bleemeo_minimal-rabbitmq-1",
		FakeLogPath:       logFile.Name(),
	}

	// The container parser preserves the trailing newline embedded in Docker's JSON
	// "log" field value, so the body is "[error] something broke\n", not "...broke".
	cfg := config.Log{
		Inputs: []config.LogInput{
			{ContainerName: ctr.ContainerName(), Filters: []config.LogFilter{
				{Metric: "rabbitmq_errors", Regex: `^\[error\] something broke\n?$`},
			}},
		},
	}

	man := New(cfg, "/", &fakeRuntime{containers: []facts.Container{ctr}})

	man.startStaticSources(t.Context())
	man.updateContainerSources(t.Context())

	defer man.stopAll(t.Context())

	time.Sleep(500 * time.Millisecond)

	dockerLine := `{"log":"[error] something broke\n","stream":"stdout","time":"2024-01-15T10:23:45.123Z"}` + "\n"

	if _, err := logFile.WriteString(dockerLine); err != nil {
		t.Fatal("Failed to write log line:", err)
	}

	if err := logFile.Sync(); err != nil {
		t.Fatal("Failed to sync log file:", err)
	}

	time.Sleep(time.Second)

	man.l.Lock()
	counter, found := man.reg.counters["rabbitmq_errors"]
	man.l.Unlock()

	if !found {
		t.Fatal("Expected a counter for rabbitmq_errors")
	}

	if got := counter.counter.Total(); got != 1 {
		t.Errorf("Expected 1 match for rabbitmq_errors, got %d", got)
	}
}

func TestManagerContainerRemoved(t *testing.T) {
	t.Parallel()

	logFile, err := os.CreateTemp(t.TempDir(), "app-*.log")
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer logFile.Close()

	ctr := facts.FakeContainer{FakeContainerName: "app-1", FakeLogPath: logFile.Name()}

	cfg := config.Log{
		Inputs: []config.LogInput{
			{ContainerName: ctr.ContainerName(), Filters: []config.LogFilter{{Metric: "app_errors", Regex: "ERROR"}}},
		},
	}

	runtime := &fakeRuntime{containers: []facts.Container{ctr}}
	man := New(cfg, "/", runtime)

	man.updateContainerSources(t.Context())

	man.l.Lock()
	_, watching := man.containerSources[ctr.ID()]
	man.l.Unlock()

	if !watching {
		t.Fatal("Expected the container to be watched after the first update")
	}

	runtime.containers = nil

	man.updateContainerSources(t.Context())

	man.l.Lock()
	_, stillWatching := man.containerSources[ctr.ID()]
	man.l.Unlock()

	if stillWatching {
		t.Fatal("Expected the container's source to be stopped once the container disappeared")
	}
}
