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
	"strings"
	"time"

	"github.com/bleemeo/glouton/types"
)

type subDirZipWriter struct {
	baseFolder      string
	zipWriter       *zip.Writer
	currentFileName string
}

// NewSubDirZipWriter returns an ArchiveWriter able to write directly into the given zip archive.
// Every call to Create will create a file in the given base folder.
// It's up to the caller to close the given zip.Writer once everything is done.
func NewSubDirZipWriter(baseFolder string, zipWriter *zip.Writer) types.ArchiveWriter {
	return &subDirZipWriter{
		// Zip entries must not start with a slash.
		baseFolder: strings.Trim(baseFolder, "/"),
		zipWriter:  zipWriter,
	}
}

func (sd *subDirZipWriter) Create(filename string) (io.Writer, error) {
	sd.currentFileName = sd.baseFolder + "/" + filename

	return sd.zipWriter.CreateHeader(&zip.FileHeader{
		Name:     sd.currentFileName,
		Modified: time.Now(),
		Method:   zip.Deflate,
	})
}

func (sd *subDirZipWriter) CurrentFileName() string {
	return sd.currentFileName
}
