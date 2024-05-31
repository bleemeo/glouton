// Copyright 2015-2024 Bleemeo
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
	"io"

	"github.com/bleemeo/glouton/types"
)

type prefixWriter struct {
	prefix string
	writer types.ArchiveWriter
}

// NewPrefixWriter returns an ArchiveWriter that write to underlying writer but add a fixed prefix to all filename.
// The prefix could end with "/" making write being in a sub-folder.
func NewPrefixWriter(prefix string, writer types.ArchiveWriter) types.ArchiveWriter {
	return &prefixWriter{
		prefix: prefix,
		writer: writer,
	}
}

func (pw *prefixWriter) Create(filename string) (io.Writer, error) {
	currentFileName := pw.prefix + filename

	return pw.writer.Create(currentFileName)
}

func (pw *prefixWriter) CurrentFileName() string {
	return pw.writer.CurrentFileName()
}
