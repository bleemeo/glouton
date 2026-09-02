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
	"sync"
	"time"
)

var (
	// ErrArchiveClosed is returned when writing to an archive that is already closed.
	ErrArchiveClosed = errors.New("archive is closed")
	// ErrStaleFileWriter is returned when writing to the writer of a file that is no
	// longer the current file of the archive.
	ErrStaleFileWriter = errors.New("write to a file that is no longer the current file of the archive")
)

// TarArchive writes files to a tar archive. Its methods, and the writers returned
// by Create(), are safe for concurrent use: the crash diagnostic may be closed
// (on timeout) while the goroutine generating it is still writing.
type TarArchive struct {
	l                  sync.Mutex
	w                  *tar.Writer
	currentFileContent *bytes.Buffer
	currentFileHeader  tar.Header
	// generation is incremented on every Create() and on Close(), which
	// invalidates the writer returned by the previous Create().
	generation uint64
	closed     bool
}

func NewTarWriter(w io.Writer) *TarArchive {
	return &TarArchive{
		w: tar.NewWriter(w),
	}
}

func (a *TarArchive) CurrentFileName() string {
	a.l.Lock()
	defer a.l.Unlock()

	return a.currentFileHeader.Name
}

// flushPending writes the pending file to the tar. a.l must be held.
func (a *TarArchive) flushPending() error {
	if a.currentFileHeader.Name == "" {
		return nil
	}

	a.currentFileHeader.Size = int64(a.currentFileContent.Len())

	if err := a.w.WriteHeader(&a.currentFileHeader); err != nil {
		return err
	}

	_, err := a.w.Write(a.currentFileContent.Bytes())

	return err
}

func (a *TarArchive) Create(filename string) (io.Writer, error) {
	a.l.Lock()
	defer a.l.Unlock()

	if a.closed {
		return nil, ErrArchiveClosed
	}

	if err := a.flushPending(); err != nil {
		return nil, err
	}

	a.generation++

	a.currentFileHeader = tar.Header{
		Name:    filename,
		ModTime: time.Now(),
		Mode:    0o644,
	}

	if a.currentFileContent == nil {
		a.currentFileContent = &bytes.Buffer{}
	}

	a.currentFileContent.Reset()

	return &tarFileWriter{archive: a, generation: a.generation}, nil
}

func (a *TarArchive) Close() error {
	a.l.Lock()
	defer a.l.Unlock()

	if a.closed {
		return nil
	}

	// Any writer handed out by Create() is invalidated: writing to the archive
	// once it is closed would produce a corrupted tar.
	a.closed = true
	a.generation++

	if err := a.flushPending(); err != nil {
		return err
	}

	return a.w.Close()
}

// write appends content to the pending file, if it's still the current one.
func (a *TarArchive) write(generation uint64, content []byte) (int, error) {
	a.l.Lock()
	defer a.l.Unlock()

	if a.closed {
		return 0, ErrArchiveClosed
	}

	if generation != a.generation {
		return 0, ErrStaleFileWriter
	}

	return a.currentFileContent.Write(content)
}

// tarFileWriter is the writer of one file of a TarArchive. It only writes to the
// archive while its file is the current one, so a late write (e.g. from a
// diagnostic goroutine that outlived its timeout) can't end up in another file.
type tarFileWriter struct {
	archive    *TarArchive
	generation uint64
}

func (w *tarFileWriter) Write(content []byte) (int, error) {
	return w.archive.write(w.generation, content)
}
