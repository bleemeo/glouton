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
	"bytes"
	"errors"
	"glouton/logger"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"k8s.io/utils/strings/slices"
)

const (
	stderrFileName    = "stderr.log"
	oldStderrFileName = "stderr.old.log"

	crashReportDirFormat  = "crashreport_20060102-150405"
	crashReportDirPattern = "crashreport_[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]-[0-9][0-9][0-9][0-9][0-9][0-9]"

	writeInProgressFlag = ".WRITE_IN_PROGRESS"
)

//nolint:gochecknoglobals
var (
	skipReporting bool
	stateDir      string
)

// SetStateDir defines 'dir' as the directory where any crash report related file will go.
func SetStateDir(dir string) {
	stateDir = dir
}

// SetupStderrRedirection creates a file that will receive stderr output.
// If such a file already exists, it is moved to a '.old' version and
// a new and empty file takes it place.
func SetupStderrRedirection() {
	if _, err := os.Stat(filepath.Join(stateDir, writeInProgressFlag)); err == nil {
		// If the flag has not been deleted the last run, it may be because the crash reporting process crashed.
		// So to try not to crash again, we skip the crash reporting this time.
		// We will try to report the next time, so we delete the flag.
		skipReporting = true

		MarkAsDone()

		return
	}

	stderrFilePath := filepath.Join(stateDir, stderrFileName)
	oldStderrFilePath := filepath.Join(stateDir, oldStderrFileName)

	if _, err := os.Stat(stderrFilePath); err == nil {
		err = os.Rename(stderrFilePath, oldStderrFilePath)
		if err != nil {
			logger.V(1).Println("Failed to mark stderr log file as old:", err)

			return
		}
	}

	newStderrFile, err := os.Create(stderrFileName)
	if err != nil {
		logger.V(1).Println("Failed to create a new stderr log file:", err)

		return
	}

	defer newStderrFile.Close()

	err = redirectOSSpecificStderrToFile(newStderrFile.Fd())
	if err != nil {
		logger.V(1).Println("Failed to redirect stderr to log file:", err)
	}
}

// PurgeCrashReportDirs deletes oldest crash report directories present
// in 'stateDir' and only keeps the 'maxDirCount' most recent ones.
// Directories specified as 'except' won't be deleted.
func PurgeCrashReportDirs(maxDirCount int, preserve ...string) {
	existingCrashReportDirs, err := filepath.Glob(filepath.Join(stateDir, crashReportDirPattern))
	if err != nil {
		logger.V(0).Println("Failed to parse crash report glob pattern:", err)
	}

	crashReportDirCount := len(existingCrashReportDirs)

	// Prevent preserved report dirs from being purged
	for _, dir := range preserve {
		// A slices package will be included in the standard library from Go 1.21.
		// For now, using the one from the k8s API.
		if idx := slices.Index(existingCrashReportDirs, dir); idx >= 0 {
			// Remove dir from the list of report directories
			existingCrashReportDirs = append(existingCrashReportDirs[:idx], existingCrashReportDirs[idx+1:]...)
		}
	}

	if crashReportDirCount <= maxDirCount {
		return
	}

	// Remove the oldest excess crash report dirs
	sort.Strings(existingCrashReportDirs)

	for i, dir := range existingCrashReportDirs {
		if i == crashReportDirCount-maxDirCount {
			break
		}

		err = os.RemoveAll(dir)
		if err != nil {
			logger.V(1).Printf("Failed to remove old crash report dir %q: %v", dir, err)
		}
	}
}

// BundleCrashReportFiles creates a new directory with the current datetime in its name,
// then moves the previous stderr log file to it, and creates a flag file to prevent any upload,
// as the crash report is not complete (the flag will be removed once the diagnostic is created).
// It returns the path to the crash report directory (or an empty string if not created),
// along with an ArchiveWriter to generate a diagnostic (which can be nil).
func BundleCrashReportFiles(disabled bool, maxCrashDirCount int) (reportDir string, archiveWriter ReportArchiveWriter) {
	if skipReporting {
		return "", nil
	}

	if disabled || maxCrashDirCount <= 0 {
		// Crash reports are apparently disabled in config.
		return
	}

	lastStderrFilePath := filepath.Join(stateDir, oldStderrFileName)

	lastStderrFile, err := os.Open(lastStderrFilePath)
	if err != nil {
		return "", nil // No previous stderr file
	}

	// Only the first 4KiB of the file are needed
	// to know whether it contains a stacktrace or not.
	lastStderrFileContent := make([]byte, 4096)

	_, err = lastStderrFile.Read(lastStderrFileContent)
	if err == nil || errors.Is(err, io.EOF) {
		if bytes.Contains(lastStderrFileContent, []byte("panic")) {
			reportDir = filepath.Join(stateDir, time.Now().Format(crashReportDirFormat))

			err = os.Mkdir(reportDir, 0o740)
			if err != nil {
				logger.V(1).Printf("Failed to create crash report folder %q: %v", reportDir, err)

				return "", nil
			}

			err = os.Rename(lastStderrFilePath, filepath.Join(reportDir, stderrFileName))
			if err != nil {
				logger.V(1).Printf("Failed to move crash report to folder %q: %v", reportDir, err)

				return "", nil
			}

			// Create a file to flag that the crash report is not complete because we haven't generated a diagnostic yet.
			_, err = os.Create(filepath.Join(stateDir, writeInProgressFlag))
			if err != nil {
				logger.V(1).Printf("Failed to flag crash report dir %q as write in progress", reportDir)
			}

			logger.V(0).Printf("Created a crash-report dir at %q", reportDir) // TODO: remove

			archive, err := os.Create(filepath.Join(reportDir, "diagnostic.tar"))
			if err != nil {
				logger.V(1).Println("Failed to create diagnostic archive:", err)

				return reportDir, nil
			}

			return reportDir, newTarWriter(archive)
		}
	}

	return "", nil // No crash report dir created
}

// MarkAsDone deletes the file implying that crash report writing isn't done yet,
// which means that crash reports are now ready for upload.
func MarkAsDone() {
	err := os.Remove(filepath.Join(stateDir, writeInProgressFlag))
	if err != nil {
		logger.V(1).Println("Failed to delete write-in-progress flag:", err)
	}
}
