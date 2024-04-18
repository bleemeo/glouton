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
	"io"

	"github.com/bleemeo/glouton/types"
)

type singlefileWriter struct {
	filename string
	writer   io.Writer
}

// NewSingleFileWriter returns an ArchiveWriter that write unmodified only filename that exactly match given filename.
// Other files data are discarded.
func NewSingleFileWriter(filename string, writer io.Writer) types.ArchiveWriter {
	return &singlefileWriter{
		filename: filename,
		writer:   writer,
	}
}

func (sw *singlefileWriter) Create(filename string) (io.Writer, error) {
	if filename == sw.filename {
		return sw.writer, nil
	}

	return io.Discard, nil
}

func (sw *singlefileWriter) CurrentFileName() string {
	return sw.filename
}
