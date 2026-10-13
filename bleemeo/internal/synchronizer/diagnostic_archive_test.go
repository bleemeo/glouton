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

package synchronizer

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/bleemeo/glouton/bleemeo/internal/cache"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
)

var errArchiveFull = errors.New("archive full")

// failingArchive records the files written, and fails to create the one named failOn.
type failingArchive struct {
	files  map[string]*bytes.Buffer
	failOn string
}

func (a *failingArchive) Create(filename string) (io.Writer, error) {
	if filename == a.failOn {
		return nil, errArchiveFull
	}

	a.files[filename] = &bytes.Buffer{}

	return a.files[filename], nil
}

func (a *failingArchive) CurrentFileName() string { return "" }

// TestDiagnosticArchiveFiles checks the synchronizer writes its sync state and the registration
// status, and that failing to write the registration status keeps neither the sync state out
// of the archive nor the error from the caller.
func TestDiagnosticArchiveFiles(t *testing.T) {
	state := newStateMock()
	s := newWithNow(types.Option{Cache: cache.Load(state), State: state}, time.Now)

	archive := &failingArchive{files: make(map[string]*bytes.Buffer)}

	if err := s.DiagnosticArchive(t.Context(), archive); err != nil {
		t.Fatalf("DiagnosticArchive() failed: %v", err)
	}

	for _, name := range []string{"bleemeo-sync-state.json", "bleemeo-registration-status.json"} {
		if archive.files[name] == nil || archive.files[name].Len() == 0 {
			t.Errorf("%s is missing from the archive", name)
		}
	}

	archive = &failingArchive{files: make(map[string]*bytes.Buffer), failOn: "bleemeo-registration-status.json"}

	if err := s.DiagnosticArchive(t.Context(), archive); !errors.Is(err, errArchiveFull) {
		t.Errorf("DiagnosticArchive() = %v, want %v", err, errArchiveFull)
	}

	if archive.files["bleemeo-sync-state.json"] == nil {
		t.Error("failing to write the registration status left the sync state out of the archive")
	}
}
