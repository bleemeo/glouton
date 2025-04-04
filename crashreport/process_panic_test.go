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

package crashreport

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bleemeo/glouton/types"
)

func TestTryToGenerateDiagnostic(t *testing.T) {
	t.Run("No diagnostic", func(t *testing.T) {
		testDir := t.TempDir()

		tryToGenerateDiagnostic(time.Second, testDir, nil)

		diagnosticPath := filepath.Join(testDir, panicDiagnosticArchive)

		_, err := os.Stat(diagnosticPath)
		if !os.IsNotExist(err) {
			t.Fatal("tryToGenerateDiagnostic() should not have generated a diagnostic archive.")
		}
	})

	t.Run("Normal diagnostic", func(t *testing.T) {
		const (
			timeout     = time.Second
			fileName    = "file.txt"
			fileContent = "Some diagnostic written done to a file in the archive"
		)

		diagnosticFn := func(ctx context.Context, writer types.ArchiveWriter) error {
			deadline, ok := ctx.Deadline()
			if !ok {
				t.Fatalf("A deadline should have been set (%v).", timeout)
			}

			maxExpected := time.Now().Add(timeout)
			if deadline.After(maxExpected) {
				want := maxExpected.Format("15:04:05.000")
				got := deadline.Format("15:04:05.000")
				t.Fatalf("The deadline was incorrectly defined:\nwant at most: %s\n         got: %s", want, got)
			}

			w, err := writer.Create(fileName)
			if err != nil {
				t.Fatal("Failed to create file in diagnostic archive:", err)
			}

			_, err = fmt.Fprint(w, fileContent)
			if err != nil {
				t.Fatal("Failed to write to file in diagnostic archive:", err)
			}

			return nil
		}

		testDir := t.TempDir()

		tryToGenerateDiagnostic(timeout, testDir, diagnosticFn)

		diagnosticPath := filepath.Join(testDir, panicDiagnosticArchive)

		f, err := os.Open(diagnosticPath)
		if pathErr := new(os.PathError); errors.As(err, &pathErr) {
			t.Fatal("tryToGenerateDiagnostic() should have generated a diagnostic archive, but", pathErr.Unwrap())
		} else if err != nil {
			t.Fatal("Unexpected error while opening diagnostic archive:", err)
		}

		defer f.Close()

		tarReader := tar.NewReader(f)

		h, err := tarReader.Next()
		if err != nil {
			t.Fatal("Failed to read diagnostic archive:", err)
		}

		if h.Name != fileName {
			t.Fatalf("Unexpected file name: want %q, got %q.", fileName, h.Name)
		}

		buf := make([]byte, h.Size)

		_, err = tarReader.Read(buf)
		if err != nil && !errors.Is(err, io.EOF) {
			t.Fatal("Failed to read diagnostic archive content:", err)
		}

		content := string(buf)
		if content != fileContent {
			t.Fatalf("Unexpected file content:\nwant: %q\n got: %q", fileContent, content)
		}
	})

	t.Run("Diagnostic error", func(t *testing.T) {
		diagnosticErr := errors.New("some diagnostic generation error") //nolint:err113
		diagnosticFn := func(_ context.Context, _ types.ArchiveWriter) error {
			return diagnosticErr
		}

		testDir := t.TempDir()

		setupStderrRedirection(testDir) // For reading errors produced by tryToGenerateDiagnostic()

		tryToGenerateDiagnostic(time.Second, testDir, diagnosticFn)

		stderrContent, err := os.ReadFile(filepath.Join(testDir, stderrFileName))
		if err != nil {
			t.Fatal("Failed to read stderr content:", err)
		}

		errLogs := strings.TrimRight(string(stderrContent), "\n")
		if !strings.HasSuffix(errLogs, diagnosticErr.Error()) {
			t.Fatalf("Unexpected error message: %q", errLogs)
		}
	})
}
