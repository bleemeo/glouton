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

package archivewriter

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
)

// readTar reads the whole archive, failing the test if it isn't a valid tar.
func readTar(t *testing.T, content []byte) map[string]string {
	t.Helper()

	files := make(map[string]string)
	reader := tar.NewReader(bytes.NewReader(content))

	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			t.Fatal("Failed to read tar header:", err)
		}

		fileContent, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("Failed to read content of %s: %v", header.Name, err)
		}

		if int64(len(fileContent)) != header.Size {
			t.Fatalf("File %s: read %d bytes, header says %d", header.Name, len(fileContent), header.Size)
		}

		files[header.Name] = string(fileContent)
	}

	return files
}

// TestTarArchiveStaleWriter ensures the writer of a file can't write to the
// archive once another file has been created: its content would end up in the
// wrong file, or worse corrupt the tar.
func TestTarArchiveStaleWriter(t *testing.T) {
	t.Parallel()

	var buffer bytes.Buffer

	archive := NewTarWriter(&buffer)

	firstFile, err := archive.Create("first.txt")
	if err != nil {
		t.Fatal("Failed to create first.txt:", err)
	}

	if _, err := firstFile.Write([]byte("first content")); err != nil {
		t.Fatal("Failed to write to first.txt:", err)
	}

	secondFile, err := archive.Create("second.txt")
	if err != nil {
		t.Fatal("Failed to create second.txt:", err)
	}

	if _, err := firstFile.Write([]byte("late content")); !errors.Is(err, ErrStaleFileWriter) {
		t.Fatalf("Write to a stale file writer returned %v, want %v", err, ErrStaleFileWriter)
	}

	if _, err := secondFile.Write([]byte("second content")); err != nil {
		t.Fatal("Failed to write to second.txt:", err)
	}

	if err := archive.Close(); err != nil {
		t.Fatal("Failed to close the archive:", err)
	}

	if _, err := secondFile.Write([]byte("content after close")); !errors.Is(err, ErrArchiveClosed) {
		t.Fatalf("Write to a closed archive returned %v, want %v", err, ErrArchiveClosed)
	}

	files := readTar(t, buffer.Bytes())

	if got, want := files["first.txt"], "first content"; got != want {
		t.Errorf("first.txt = %q, want %q", got, want)
	}

	if got, want := files["second.txt"], "second content"; got != want {
		t.Errorf("second.txt = %q, want %q", got, want)
	}
}

// TestTarArchiveCloseDuringWrite ensures the archive stays a valid tar when it
// is closed (e.g. because the diagnostic timed out) while another goroutine is
// still writing to it.
func TestTarArchiveCloseDuringWrite(t *testing.T) {
	t.Parallel()

	const (
		fileCount = 100
		content   = "the content of this file"
	)

	var buffer bytes.Buffer

	archive := NewTarWriter(&buffer)
	writing := make(chan struct{})

	var wg sync.WaitGroup

	wg.Go(func() {
		for n := range fileCount {
			file, err := archive.Create("file" + strings.Repeat("0", n%3) + ".txt")
			if err != nil {
				return // The archive is closed, like a diagnostic module giving up.
			}

			if n == 0 {
				close(writing)
			}

			if _, err := file.Write([]byte(content)); err != nil {
				return
			}
		}
	})

	<-writing

	if err := archive.Close(); err != nil {
		t.Fatal("Failed to close the archive:", err)
	}

	wg.Wait()

	// Whatever the interleaving, the archive must be readable and no file may
	// hold anything else than what was written to it.
	for name, fileContent := range readTar(t, buffer.Bytes()) {
		if fileContent != content && fileContent != "" {
			t.Errorf("%s = %q, want %q or an empty content", name, fileContent, content)
		}
	}
}
