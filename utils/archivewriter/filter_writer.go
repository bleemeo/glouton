// Copyright 2015-2023 Bleemeo
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
	"github.com/bleemeo/glouton/types"
	"io"
	"path"
)

type filterWriter struct {
	glob            string
	writer          types.ArchiveWriter
	currentFileName string
}

// NewFilterWriter returns an ArchiveWriter that write to underlying writer but only filename matching given glob.
// For file not matching the prefix, data written are discarded.
func NewFilterWriter(glob string, writer types.ArchiveWriter) types.ArchiveWriter {
	return &filterWriter{
		glob:   glob,
		writer: writer,
	}
}

func (fw *filterWriter) Create(filename string) (io.Writer, error) {
	fw.currentFileName = filename

	ok, err := path.Match(fw.glob, filename)
	if err != nil {
		return nil, err
	}

	if ok {
		return fw.writer.Create(filename)
	}

	return io.Discard, nil
}

func (fw *filterWriter) CurrentFileName() string {
	return fw.currentFileName
}
