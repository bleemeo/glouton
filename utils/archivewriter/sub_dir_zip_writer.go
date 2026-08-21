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
	"archive/zip"
	"io"
	"strings"
	"sync"
	"time"
)

// SubDirZipWriter writes files into a sub-folder of a zip archive.
//
// Its methods, and the writers returned by Create(), are safe for concurrent
// use: the crash report may be closed (on timeout) while the goroutine
// generating the diagnostic is still writing.
type SubDirZipWriter struct {
	baseFolder string

	l               sync.Mutex
	zipWriter       *zip.Writer
	currentFileName string
	currentWriter   io.Writer
	// generation is incremented on every Create() and on Close(), which
	// invalidates the writer returned by the previous Create().
	generation uint64
	closed     bool
}

// NewSubDirZipWriter returns an ArchiveWriter able to write directly into the given zip archive.
// Every call to Create will create a file in the given base folder.
// The given zip.Writer is closed by Close(), which must be used instead of closing
// it directly: it's the only way to guarantee no file is written to the zip once
// its central directory is written.
func NewSubDirZipWriter(baseFolder string, zipWriter *zip.Writer) *SubDirZipWriter {
	return &SubDirZipWriter{
		// Zip entries must not start with a slash.
		baseFolder: strings.Trim(baseFolder, "/"),
		zipWriter:  zipWriter,
	}
}

func (sd *SubDirZipWriter) Create(filename string) (io.Writer, error) {
	sd.l.Lock()
	defer sd.l.Unlock()

	if sd.closed {
		return nil, ErrArchiveClosed
	}

	sd.currentFileName = sd.baseFolder + "/" + filename

	writer, err := sd.zipWriter.CreateHeader(&zip.FileHeader{
		Name:     sd.currentFileName,
		Modified: time.Now(),
		Method:   zip.Deflate,
	})
	if err != nil {
		return nil, err
	}

	sd.generation++
	sd.currentWriter = writer

	return &subDirZipFileWriter{archive: sd, generation: sd.generation}, nil
}

func (sd *SubDirZipWriter) CurrentFileName() string {
	sd.l.Lock()
	defer sd.l.Unlock()

	return sd.currentFileName
}

// Close closes the underlying zip.Writer, writing its central directory.
func (sd *SubDirZipWriter) Close() error {
	sd.l.Lock()
	defer sd.l.Unlock()

	if sd.closed {
		return nil
	}

	// Any writer handed out by Create() is invalidated: zip.Writer.CreateHeader()
	// doesn't check whether the zip is closed, so a late write would add a file
	// after the central directory.
	sd.closed = true
	sd.generation++

	return sd.zipWriter.Close()
}

// write appends content to the pending file, if it's still the current one.
func (sd *SubDirZipWriter) write(generation uint64, content []byte) (int, error) {
	sd.l.Lock()
	defer sd.l.Unlock()

	if sd.closed {
		return 0, ErrArchiveClosed
	}

	if generation != sd.generation {
		return 0, ErrStaleFileWriter
	}

	return sd.currentWriter.Write(content)
}

// subDirZipFileWriter is the writer of one file of a SubDirZipWriter. It only
// writes to the archive while its file is the current one, so a late write (e.g.
// from a diagnostic goroutine that outlived its timeout) can't corrupt the zip.
type subDirZipFileWriter struct {
	archive    *SubDirZipWriter
	generation uint64
}

func (w *subDirZipFileWriter) Write(content []byte) (int, error) {
	return w.archive.write(w.generation, content)
}
