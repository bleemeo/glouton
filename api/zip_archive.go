package api

import (
	"archive/zip"
	"io"
	"time"
)

type zipArchive struct {
	w               *zip.Writer
	currentFilename string
}

func newZipWriter(w io.Writer) *zipArchive {
	return &zipArchive{
		w: zip.NewWriter(w),
	}
}

func (a *zipArchive) CurrentFileName() string {
	return a.currentFilename
}

func (a *zipArchive) Create(filename string) (io.Writer, error) {
	if err := a.w.Flush(); err != nil {
		return nil, err
	}

	a.currentFilename = filename

	return a.w.CreateHeader(&zip.FileHeader{
		Name:     filename,
		Modified: time.Now(),
		Method:   zip.Deflate,
	})
}

func (a *zipArchive) Close() error {
	return a.w.Close()
}
