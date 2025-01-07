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

package archivewriter

import (
	"archive/zip"
	"io"
	"time"
)

type ZipArchive struct {
	w               *zip.Writer
	currentFilename string
}

func NewZipWriter(w io.Writer) *ZipArchive {
	return &ZipArchive{
		w: zip.NewWriter(w),
	}
}

func (a *ZipArchive) CurrentFileName() string {
	return a.currentFilename
}

func (a *ZipArchive) Create(filename string) (io.Writer, error) {
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

func (a *ZipArchive) Close() error {
	return a.w.Close()
}
