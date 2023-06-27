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
	"context"
	"errors"
	"fmt"
	"glouton/types"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

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

func TestCrashReportArchivePattern(t *testing.T) {
	if _, err := filepath.Match(crashReportArchivePattern, ""); err != nil {
		t.Fatal("`crashReportArchivePattern` is invalid:", err)
	}
}

func TestWorkDirCreation(t *testing.T) {
	wrapper := func(t *testing.T, testDir string) {
		t.Helper()

		SetOptions(false, testDir, nil)

		err := createWorkDirIfNotExist(testDir)
		if err != nil {
			t.Fatal("Failed to create work dir:", err)
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

	t.Run("Work dir not existing", func(t *testing.T) {
		testDir, delTmpDir := setupTestDir(t)
		defer delTmpDir()

		wrapper(t, testDir)
	})

	t.Run("Work dir already existing", func(t *testing.T) {
		testDir, delTmpDir := setupTestDir(t)
		defer delTmpDir()

		err := os.Mkdir(filepath.Join(testDir, crashReportWorkDir), 0o740)
		if err != nil && !os.IsExist(err) {
			t.Fatal("Failed to pre-create crash report work dir:", err)
		}

		wrapper(t, testDir)
	})
}

func TestIsWriteInProgress(t *testing.T) {
	t.Run("In progress", func(t *testing.T) {
		testDir, delTmpDir := setupTestDir(t)
		defer delTmpDir()

		f, err := os.Create(filepath.Join(testDir, writeInProgressFlag))
		if err != nil {
			t.Fatal("Failed to create write-in-progress flag file:", err)
		}

		f.Close()

		if !isWriteInProgress(testDir) {
			t.Fatal("Write is in progress but was not considered as such.")
		}
	})

	t.Run("Not in progress", func(t *testing.T) {
		testDir, delTmpDir := setupTestDir(t)
		defer delTmpDir()

		if isWriteInProgress(testDir) {
			t.Fatal("Write is not in progress but was considered to be in progress.")
		}
	})
}

func TestStderrRedirection(t *testing.T) {
	testDir, delTmpDir := setupTestDir(t)
	defer delTmpDir()

	SetOptions(true, testDir, nil)

	SetupStderrRedirection()

	stderrFilePath := filepath.Join(testDir, stderrFileName)

	info, err := os.Stat(stderrFilePath)
	if err != nil {
		t.Fatal("Failed to", err)
	}

	if info.Size() != 0 {
		t.Fatal("Stderr log file should be empty until someone writes to stderr")
	}

	const logContent = "This is a message written on stderr."

	_, err = fmt.Fprint(os.Stderr, logContent)
	if err != nil {
		t.Fatal("Failed to write to stderr:", err)
	}

	stderrContent, err := os.ReadFile(stderrFilePath)
	if err != nil {
		t.Fatal("Failed to", err)
	}

	strStderr := string(stderrContent)
	if strStderr != logContent {
		t.Fatalf("Unexpected content from stderr:\nwant: %q\n got: %q", logContent, strStderr)
	}
}

func TestPurgeCrashReports(t *testing.T) {
	t.Run("Keep the last reports", func(t *testing.T) {
		testDir, delTmpDir := setupTestDir(t)
		defer delTmpDir()

		const keep = 2
		mostRecentReports := make([]string, keep)

		for i := 0; i < 5; i++ {
			reportTime := time.Now().Add(time.Duration(-i) * time.Hour).Add(time.Duration(i*7) * time.Second)
			crashReportPath := filepath.Join(testDir, reportTime.Format(crashReportArchiveFormat))

			if i < keep {
				mostRecentReports[i] = crashReportPath // Save it for later check
			}

			f, err := os.Create(crashReportPath)
			if err != nil {
				t.Fatal("Failed to create fake crash report:", err)
			}

			f.Close()
		}

		SetOptions(true, testDir, nil)

		PurgeCrashReports(keep)

		allReports, err := filepath.Glob(filepath.Join(testDir, crashReportArchivePattern))
		if err != nil {
			t.Fatal("Invalid pattern for `crashReportArchivePattern`:", err)
		}

		sort.Strings(mostRecentReports)

		if diff := cmp.Diff(allReports, mostRecentReports); diff != "" {
			t.Log(diff)
			t.Fatal("Unexpected crash report purge result.")
		}
	})

	t.Run("Keep preserved reports", func(t *testing.T) {
		testDir, delTmpDir := setupTestDir(t)
		defer delTmpDir()

		var reportsToKeep []string

		for i := 0; i < 10; i++ {
			reportTime := time.Now().Add(time.Duration(-i) * time.Hour).Add(time.Duration(i*7) * time.Second)
			crashReportPath := filepath.Join(testDir, reportTime.Format(crashReportArchiveFormat))

			if i%3 == 0 {
				reportsToKeep = append(reportsToKeep, crashReportPath) // Save it for later check
			}

			f, err := os.Create(crashReportPath)
			if err != nil {
				t.Fatal("Failed to create fake crash report:", err)
			}

			f.Close()
		}

		SetOptions(true, testDir, nil)

		// The max report count doesn't matter when some reports should be preserved.
		PurgeCrashReports(0, reportsToKeep...)

		allReports, err := filepath.Glob(filepath.Join(testDir, crashReportArchivePattern))
		if err != nil {
			t.Fatal("Invalid pattern for `crashReportArchivePattern`:", err)
		}

		sort.Strings(reportsToKeep)

		if diff := cmp.Diff(allReports, reportsToKeep); diff != "" {
			t.Log(diff)
			t.Fatal("Unexpected crash report purge result.")
		}
	})
}

func TestMarkAsDone(t *testing.T) {
	testDir, delTmpDir := setupTestDir(t)
	defer delTmpDir()

	err := createWorkDirIfNotExist(testDir)
	if err != nil {
		t.Fatal("Failed to create work dir:", err)
	}

	flagFilePath := filepath.Join(testDir, writeInProgressFlag)

	f, err := os.Create(flagFilePath)
	if err != nil {
		t.Fatal("Failed to create write-in-progress flag:", err)
	}

	f.Close()

	errs := markAsDone(testDir)
	if errs != nil {
		t.Fatal("Some errors logs have been written by markAsDone():", errs.Error())
	}

	_, err = os.Stat(filepath.Join(testDir, crashReportWorkDir))
	if !os.IsNotExist(err) {
		t.Fatal("markAsDone() did not delete crash report work dir.")
	}

	_, err = os.Stat(flagFilePath)
	if !os.IsNotExist(err) {
		t.Fatal("markAsDone() did not delete the write-in-progress flag.")
	}
}

func TestGenerateDiagnostic(t *testing.T) {
	cases := []struct {
		name               string
		ctxTimeout         time.Duration
		diagnosticDuration time.Duration
		diagnosticError    error
		shouldPanic        bool
		expectedError      error
	}{
		{
			name:               "Errorless behavior",
			ctxTimeout:         time.Second,
			diagnosticDuration: time.Millisecond,
			diagnosticError:    nil,
			expectedError:      nil,
		},
		{
			name:               "Context timeout",
			ctxTimeout:         time.Millisecond,
			diagnosticDuration: 10 * time.Millisecond,
			expectedError:      context.DeadlineExceeded,
		},
		{
			name:               "Panic",
			ctxTimeout:         time.Second,
			diagnosticDuration: time.Millisecond,
			shouldPanic:        true,
			expectedError:      errFailedToDiagnostic,
		},
	}

	for _, testCase := range cases {
		tc := testCase

		t.Run(tc.name, func(t *testing.T) {
			diagnosticFn := func(context.Context, types.ArchiveWriter) error {
				time.Sleep(tc.diagnosticDuration) // Simulates processing

				if tc.shouldPanic {
					panic("Panicking")
				}

				return tc.diagnosticError
			}

			ctx, cancel := context.WithTimeout(context.Background(), tc.ctxTimeout)
			defer cancel()

			err := generateDiagnostic(ctx, nil, diagnosticFn)
			if !errors.Is(err, tc.expectedError) {
				t.Fatalf("Unexpected error: want %q, got %q", tc.expectedError, err)
			}
		})
	}
}
