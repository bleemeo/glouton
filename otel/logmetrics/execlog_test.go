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
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/otel/logsource"
	"github.com/bleemeo/glouton/utils/gloutonexec"
	"github.com/bleemeo/glouton/version"
)

// TestSourceExecLogFallback is the regression test for the gap this pass
// closes: a log-to-metric source for a file this process can't read directly
// must fall back to a sudo-tail (execlogreceiver), the same way
// otel/logprocessing already does, instead of silently producing no metric.
func TestSourceExecLogFallback(t *testing.T) {
	if version.IsWindows() {
		t.Skip("We currently don't support accessing protected files on Windows.")
	}

	t.Parallel()

	tmpDir := t.TempDir()

	file, err := os.Create(filepath.Join(tmpDir, "protected.log"))
	if err != nil {
		t.Fatal("Can't create log file:", err)
	}

	defer file.Close()

	// Force the sudo-tail fallback, regardless of the file's real permissions
	// (StatFile itself, and its real permission-detection logic, are already
	// covered by otel/logsource's own tests).
	mockStatFile := func(string, string, logsource.CommandRunner) (ignore, needSudo bool, sizeFn func() (int64, error)) {
		return false, true, func() (int64, error) { return 0, nil }
	}

	var (
		l        sync.Mutex
		tailArgs []string
	)

	runner := dummyRunner{
		run: func(_ context.Context, _ gloutonexec.Option, cmd string, args ...string) ([]byte, error) {
			t.Errorf("No Run call expected, but: %s %s", cmd, args)

			return nil, nil
		},
		startWithPipes: func(_ context.Context, _ gloutonexec.Option, _ string, args ...string) (io.ReadCloser, io.ReadCloser, func() error, error) {
			l.Lock()
			tailArgs = args
			l.Unlock()

			return io.NopCloser(bytes.NewReader(nil)), io.NopCloser(bytes.NewReader(nil)), func() error { return nil }, nil
		},
	}

	sink, _ := collectingSink()

	src, err := newSource(t.Context(), testTelemetrySettings(), []string{file.Name()}, false, false, []config.LogCounter{
		{Metric: "protected_errors_count", Regex: `\[error\]`},
	}, sink, nil, runner, mockStatFile, "")
	if err != nil {
		t.Fatal("Failed to build source:", err)
	}

	defer src.stop(t.Context()) //nolint:errcheck

	time.Sleep(500 * time.Millisecond)

	l.Lock()

	got := append([]string(nil), tailArgs...)
	l.Unlock()

	if len(got) == 0 {
		t.Fatal("Expected a sudo tail command to have been started, but StartWithPipes was never called")
	}

	if last := got[len(got)-1]; last != file.Name() {
		t.Errorf("Expected the tail command to target %q, got args %v", file.Name(), got)
	}
}
