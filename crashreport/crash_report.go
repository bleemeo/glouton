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
	"archive/tar"
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"glouton/logger"
	"glouton/types"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"k8s.io/utils/strings/slices"
)

const (
	crashReportWorkDir = "crash_report"

	stderrFileName    = "stderr.log"
	oldStderrFileName = crashReportWorkDir + string(os.PathSeparator) + stderrFileName

	panicDiagnosticArchive = "crash_diagnostic.tar"

	crashReportArchiveFormat  = "crashreport_20060102-150405.zip"
	crashReportArchivePattern = "crashreport_[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]-[0-9][0-9][0-9][0-9][0-9][0-9].zip"

	writeInProgressFlag = "crash_report_in_progress"
)

var errFailedToDiagnostic = errors.New("failed to generate a diagnostic")

//nolint:gochecknoglobals
var (
	skipReporting     bool
	enabled           bool
	stateDir          string
	maxReportDirCount int
	diagnostic        func(context.Context, types.ArchiveWriter) error
)

// SetOptions defines 'dir' as the directory where any crash report related file will go.
func SetOptions(disabled bool, dir string, maxDirCount int, diagnosticFn func(context.Context, types.ArchiveWriter) error) {
	enabled = !disabled
	stateDir = dir
	maxReportDirCount = maxDirCount
	diagnostic = diagnosticFn
}

func createWorkDirIfNotExist() (ok bool) {
	workDirPath := filepath.Join(stateDir, crashReportWorkDir)

	err := os.Mkdir(workDirPath, 0o740)
	if err != nil {
		if os.IsExist(err) {
			return true
		}

		logger.V(1).Println("Failed to create crash report work dir:", err)

		return false
	}

	return true
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

		markAsDone()
	}

	if !createWorkDirIfNotExist() {
		return
	}

	stderrFilePath := filepath.Join(stateDir, stderrFileName)
	oldStderrFilePath := filepath.Join(stateDir, oldStderrFileName)

	if _, err := os.Stat(stderrFilePath); err == nil {
		err = os.Rename(stderrFilePath, oldStderrFilePath)
		if err != nil {
			logger.V(1).Println("Failed to handle old stderr log file:", err)
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

// PurgeCrashReports deletes oldest crash reports present in 'stateDir'
// and only keeps the 'maxReportCount' most recent ones.
// Crash reports specified as 'preserve' won't be deleted.
func PurgeCrashReports(maxReportCount int, preserve ...string) {
	existingCrashReports, err := filepath.Glob(filepath.Join(stateDir, crashReportArchivePattern))
	if err != nil {
		// filepath.Glob's documentation states that "Glob ignores file system errors [...]".
		// The only possible returned error is ErrBadPattern, and the above pattern is known to be valid.
		return
	}

	crashReportCount := len(existingCrashReports)

	// Prevent preserved reports from being purged
	for _, report := range preserve {
		// A slices package will be included in the standard library from Go 1.21.
		// For now, using the one from the k8s API.
		if idx := slices.Index(existingCrashReports, report); idx >= 0 {
			// Remove report from the list of crash reports
			existingCrashReports = append(existingCrashReports[:idx], existingCrashReports[idx+1:]...)
		}
	}

	if crashReportCount <= maxReportCount {
		return
	}

	// Remove the oldest excess crash reports
	sort.Strings(existingCrashReports)

	for i, report := range existingCrashReports {
		if i == crashReportCount-maxReportCount {
			break
		}

		err = os.RemoveAll(report)
		if err != nil {
			logger.V(1).Printf("Failed to remove old crash report %q: %v", report, err)
		}
	}
}

// BundleCrashReportFiles creates a crash report archive with the current datetime in its name,
// then moves the previous stderr log file to it, and creates a flag file to prevent any upload,
// as the crash report is not complete (the flag will be removed once the diagnostic is created).
// It returns the path to the created crash report (or an empty string if not created).
func BundleCrashReportFiles(ctx context.Context) (reportPath string) {
	if skipReporting {
		return ""
	}

	if !enabled || maxReportDirCount <= 0 {
		// Crash reports are apparently disabled in config.
		return
	}

	var foundStderrLog, foundPanicDiagnostic bool

	lastStderrFilePath := filepath.Join(stateDir, oldStderrFileName)

	lastStderrFile, err := os.Open(lastStderrFilePath)
	if err != nil {
		return "" // No previous crash
	}

	defer lastStderrFile.Close()

	// Only the first 4KiB of the file are needed
	// to know whether it contains a stacktrace or not.
	lastStderrFileContent := make([]byte, 4096)

	_, err = lastStderrFile.Read(lastStderrFileContent)
	if err == nil || errors.Is(err, io.EOF) {
		if bytes.Contains(lastStderrFileContent, []byte("panic")) {
			foundStderrLog = true
		}
	}

	if _, err = os.Stat(filepath.Join(stateDir, panicDiagnosticArchive)); err == nil {
		foundPanicDiagnostic = true
	}

	if foundStderrLog || foundPanicDiagnostic {
		return makeBundle(ctx)
	}

	return "" // No crash report created
}

// makeBundle creates an archive with the current datetime in its name,
// containing the old stderr log file, a freshly created diagnostic
// and the diagnostic generated at the moment of the last crash, if one.
// It returns the name of the created archive if everything went well, otherwise an empty string.
func makeBundle(ctx context.Context) string {
	// Create a file to flag that the crash report is not complete because we haven't generated a diagnostic yet.
	_, err := os.Create(filepath.Join(stateDir, writeInProgressFlag))
	if err != nil {
		logger.V(1).Println("Failed to create flag to mark crash report writing as in progress")
	}

	defer markAsDone()

	crashReportPath := filepath.Join(stateDir, time.Now().Format(crashReportArchiveFormat))

	crashReportArchive, err := os.Create(crashReportPath)
	if err != nil {
		logger.V(1).Printf("Can't create crash report archive %q: %v", crashReportPath, err)

		return ""
	}

	defer crashReportArchive.Close()

	zipWriter := zip.NewWriter(crashReportArchive)

	defer zipWriter.Close()

	stderrFile, err := os.Open(filepath.Join(stateDir, oldStderrFileName))
	if err == nil { // Open stderr log file
		writer, err := zipWriter.Create(stderrFileName)
		if err == nil { // Create stderr.log entry in zip
			stderrFileContent, err := io.ReadAll(stderrFile)
			if err == nil { // Read stderr log file content
				if _, err = writer.Write(stderrFileContent); err != nil { // Write it to zip
					logger.V(1).Println("Failed to write stderr log file to zip:", err)
				}
			} else {
				logger.V(1).Println("Failed to read stderr log file:", err)
			}
		} else {
			logger.V(1).Println("Failed create stderr log file in zip:", err)
		}
	}

	// If a diagnostic has been generated when crashing, copy it into the archive
	crashDiagnosticFile, err := os.Open(filepath.Join(stateDir, panicDiagnosticArchive))
	if err == nil {
		defer func() {
			crashDiagnosticFile.Close()

			err := os.Remove(crashDiagnosticFile.Name())
			if err != nil {
				logger.V(1).Println("Failed to delete crash diagnostic archive:", err)
			}
		}()

		crashDiagnosticReader := tar.NewReader(crashDiagnosticFile)

		for {
			entry, err := crashDiagnosticReader.Next()
			if err != nil {
				break
			}

			crashDiagnosticZipWriter, err := zipWriter.Create("crash_diagnostic/" + entry.Name)
			if err != nil {
				logger.V(1).Println("Failed to create a crash diagnostic entry to crash report archive:", err)

				continue
			}

			_, err = io.Copy(crashDiagnosticZipWriter, crashDiagnosticReader) //nolint:gosec
			if err != nil {
				logger.V(1).Printf("Failed to copy crash diagnostic file %q to crash report archive: %v", entry.Name, err)
			}
		}
	} else {
		logger.V(1).Println("Failed to copy crash diagnostic to crash report archive:", err)
	}

	if diagnostic != nil {
		inSituArchiveWriter := newInSituZipWriter("diagnostic", zipWriter)

		diagnosticCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		err = generateDiagnostic(diagnosticCtx, inSituArchiveWriter)
		if err != nil {
			logger.V(1).Println("Failed to generate a diagnostic into the crash report archive:", err)
		}
	} else {
		logger.V(1).Println("Can't generate a diagnostic.")
	}

	return crashReportPath
}

// markAsDone removes the crash report working directory and
// deletes the file implying that crash report writing is not done yet,
// which means that crash reports are now ready for upload.
func markAsDone() {
	err := os.RemoveAll(filepath.Join(stateDir, crashReportWorkDir))
	if err != nil {
		logger.V(1).Println("Failed to remove the crash report work dir:", err)
	}

	err = os.Remove(filepath.Join(stateDir, writeInProgressFlag))
	if err != nil && !os.IsNotExist(err) {
		logger.V(1).Println("Failed to delete write-in-progress flag:", err)
	}
}

// generateDiagnostic writes a diagnostic to the given writer.
// The given context must have defined a timeout if the generation
// of the diagnostic should be limited in time.
// When calling generateDiagnostic, the diagnostic callback must not be nil.
func generateDiagnostic(ctx context.Context, writer types.ArchiveWriter) error {
	done := make(chan error, 1) // Buffered channel to avoid the goroutine being blocked on send

	go func() {
		defer func() {
			if recovErr := recover(); recovErr != nil {
				w, err := writer.Create("diagnostic-crash.log")
				if err != nil {
					logger.V(1).Println("Failed to write diagnostic crash stack trace:", err)

					done <- errFailedToDiagnostic

					return
				}

				_, err = fmt.Fprint(w, recovErr)
				if err != nil {
					logger.V(1).Println("Failed to write diagnostic crash stack trace:", err)
				}

				done <- errFailedToDiagnostic
			}
		}()

		done <- diagnostic(ctx, writer)
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
