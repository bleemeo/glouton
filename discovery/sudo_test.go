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

package discovery

import (
	"context"
	"errors"
	"os"
	"testing"
)

// TestSudoFileReaderNeedsHostRoot pins that the reader refuses to work without a hostroot:
// a Glouton in a container has no view of the machine's filesystem without one, and reading
// its own instead would answer about the wrong machine.
func TestSudoFileReaderNeedsHostRoot(t *testing.T) {
	reader := SudoFileReader{HostRootPath: "", Runner: nil}

	if _, err := reader.ReadFile(context.Background(), "/etc/mysql/debian.cnf"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("ReadFile() with no hostroot = %v, want %v", err, os.ErrNotExist)
	}
}
