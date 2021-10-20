package api

import (
	"archive/tar"
	"bytes"
	"io"
	"time"
)

type tarArchive struct {
	w                  *tar.Writer
	currentFileContent *bytes.Buffer
	currentFileHeader  tar.Header
}

func newTarWriter(w io.Writer) *tarArchive {
	return &tarArchive{
		w: tar.NewWriter(w),
	}
}

func (a *tarArchive) CurrentFileName() string {
	return a.currentFileHeader.Name
}

func (a *tarArchive) flushPending() error {
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

func (a *tarArchive) Create(filename string) (io.Writer, error) {
	if err := a.flushPending(); err != nil {
		return nil, err
	}

	a.currentFileHeader = tar.Header{
		Name:    filename,
		ModTime: time.Now(),
		Mode:    0o644,
	}

	if a.currentFileContent == nil {
		a.currentFileContent = &bytes.Buffer{}
	}

	a.currentFileContent.Reset()

	return a.currentFileContent, nil
}

func (a *tarArchive) Close() error {
	if err := a.flushPending(); err != nil {
		return err
	}

	return a.w.Close()
}
