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
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestSubDirZipWriter(t *testing.T) {
	// Ending slash will be removed by NewSubDirZipWriter()
	// then re-added in front of each zipped file name.
	const baseFolder = "sub/"

	files := map[string]string{
		"text.txt":  "This is some text content.",
		"data.json": `{"key": "value"}`,
		"oh no":     "It should work, even with whitespaces ...",
	}

	buf := new(bytes.Buffer)
	zipWriter := zip.NewWriter(buf)
	subDirWriter := NewSubDirZipWriter(baseFolder, zipWriter)

	for file, content := range files {
		writer, err := subDirWriter.Create(file)
		if err != nil {
			t.Fatal("Unexpected error while creating a zip entry:", err)
		}

		_, err = fmt.Fprint(writer, content)
		if err != nil {
			t.Fatal("Failed to write to zip entry:", err)
		}
	}

	err := zipWriter.Close()
	if err != nil {
		t.Fatal("Failed to close zip writer:", err)
	}

	// Reopen the zipped buffer and check its content
	bufReader := bytes.NewReader(buf.Bytes())

	zipReader, err := zip.NewReader(bufReader, bufReader.Size())
	if err != nil {
		t.Fatal("Failed to open zip:", err)
	}

	for _, zipFile := range zipReader.File {
		zipName := zipFile.Name
		if !strings.HasPrefix(zipName, baseFolder) {
			t.Fatalf("Zipped file %q has not expected base folder prefix", zipName)
		}

		zipName = strings.TrimPrefix(zipName, baseFolder)

		fileContent, found := files[zipName]
		if !found {
			t.Fatalf("Unexpected file %q in zip", zipName)
		}

		zipFileReader, err := zipFile.Open()
		if err != nil {
			t.Fatalf("Failed to read zip file %q: %v", zipName, err)
		}

		zipContent, err := io.ReadAll(zipFileReader)
		if err != nil {
			t.Fatalf("Failed to read zip file %q content: %v", zipName, err)
		}

		strZipContent := string(zipContent)
		if strZipContent != fileContent {
			t.Fatalf("Unexpected content for zip file %q:\nwant: %q\n got: %q", zipName, fileContent, strZipContent)
		}

		// Everything is ok for this file
		delete(files, zipName)
	}

	// Files are deleted when found in zip, so the map should be empty.
	if len(files) != 0 {
		remaining := make([]string, 0, len(files))

		for file := range files {
			remaining = append(remaining, file)
		}

		plural := ""
		if len(remaining) > 1 {
			plural = "s"
		}

		t.Fatalf("%d file%s have not been found in zip: %s", len(remaining), plural, strings.Join(remaining, ", "))
	}
}
