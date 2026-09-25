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

//go:build linux

package facts

import (
	"errors"
	"io/fs"
	"path/filepath"
	"testing"
)

// TestStatLockInodeMissing checks that a missing lock file gives an error matching
// fs.ErrNotExist, which lockState relies on.
func TestStatLockInodeMissing(t *testing.T) {
	_, err := statLockInode(filepath.Join(t.TempDir(), "lock-frontend"))
	if err == nil {
		t.Fatal("statLockInode() on a missing file returned no error")
	}

	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("errors.Is(err, fs.ErrNotExist) = false for %v", err)
	}
}
