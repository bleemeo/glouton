package api

import (
	"fmt"
	"io"
)

type textArchive struct {
	w           io.Writer
	currentFile string
}

func newTextArchive(w io.Writer) *textArchive {
	return &textArchive{
		w: w,
	}
}

func (a *textArchive) CurrentFileName() string {
	return a.currentFile
}

func (a *textArchive) Create(filename string) (io.Writer, error) {
	if a.currentFile != "" {
		fmt.Fprintf(a.w, "-----[ end of filename: %s ]-----\n\n", a.currentFile)
	}

	a.currentFile = filename

	fmt.Fprintf(a.w, "-----[ start of filename: %s ]-----\n", filename)

	return a.w, nil
}

func (a *textArchive) Close() error {
	return nil
}
