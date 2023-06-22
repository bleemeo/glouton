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

package crashreport

import (
	"glouton/logger"
	"os"
	"path/filepath"
	"testing"
)

func TestCrashReportArchivePattern(t *testing.T) {
	if _, err := filepath.Match(crashReportArchivePattern, ""); err != nil {
		t.Fatal("`crashReportArchivePattern` is invalid:", err)
	}
}

func setupTestDir(t *testing.T) (testDir string, delTestDir func()) {
	t.Helper()

	testDir, err := os.MkdirTemp("", "testworkdir_")
	if err != nil {
		t.Skip("Could not create test directory:", err)
	}

	delTestDir = func() {
		err := os.RemoveAll(testDir)
		if err != nil {
			t.Logf("Failed to remove test dir %q", testDir)
		}
	}

	if tmpInfo, err := os.Stat(testDir); err != nil {
		delTestDir()
		t.Skip("Failed to", err)
	} else if tmpInfo.Mode().Perm()&0o200 == 0 {
		delTestDir()
		t.Skipf("Missing write permission for temp dir %q", testDir)
	}

	return testDir, delTestDir
}

func TestWorkDirCreation(t *testing.T) {
	testDir, delTmpDir := setupTestDir(t)
	defer delTmpDir()

	SetOptions(false, testDir, nil)

	ok := createWorkDirIfNotExist(testDir)
	if !ok {
		// The error has been given to the logger
		t.Fatal(string(logger.Buffer()))
	}

	workDirPath := filepath.Join(testDir, crashReportWorkDir)

	info, err := os.Stat(workDirPath)
	if err != nil {
		t.Fatal("Failed to", err)
	}

	if !info.IsDir() {
		t.Fatalf("Work dir %q is not a directory ...", workDirPath)
	}

	perm := info.Mode().Perm()
	if perm != 480 {
		t.Fatalf("Did not create work dir with expected permissions:\nwant: -rwxr-----\n got: %s", perm)
	}
}
