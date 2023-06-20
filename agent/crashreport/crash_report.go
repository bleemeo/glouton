package crashreport

import (
	"glouton/logger"
	"os"
	"path/filepath"
	"sort"
	"io"
	"bytes"
	"time"
	"errors"
)

const (
	stderrFileName    = "stderr.log"
	oldStderrFileName = "stderr.old.log"

	crashReportDirFormat  = "crashreport_20060102-150405"
	crashReportDirPattern = "crashreport_[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]-[0-9][0-9][0-9][0-9][0-9][0-9]"

	writeInProgressFlag = ".WRITE_IN_PROGRESS"
)

// SetupStderrRedirection creates a file that will receive stderr output.
// If such a file already exists, it is moved to a '.old' version and
// a new and empty file takes it place.
func SetupStderrRedirection(stateDir string) {
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
func PurgeCrashReportDirs(stateDir string, maxDirCount int) {
	existingCrashReportDirs, err := filepath.Glob(filepath.Join(stateDir, crashReportDirPattern))
	if err != nil {
		logger.V(0).Println("Failed to parse crash report glob pattern:", err)
	}

	// Remove the oldest excess crash report dirs
	sort.Strings(existingCrashReportDirs)

	for i, dir := range existingCrashReportDirs {
		if i == len(existingCrashReportDirs)-maxDirCount {
			break
		}

		err = os.RemoveAll(dir)
		if err != nil {
			logger.V(1).Printf("Failed to remove old crash report dir %q: %v", dir, err)
		}
	}
}

// BundleCrashReportFiles creates a new directory with the current datetime in its name,
// then moves the previous stderr log file to it, and creates a flag file to mark it as
// not complete (the flag will be removed once the diagnostic is created).
func BundleCrashReportFiles(stateDir string, maxDirCount int) (reportDir string) {
	if maxDirCount <= 0 {
		// Crash reports are apparently disabled in config.
		return
	}

	lastStderrFilePath := filepath.Join(stateDir, oldStderrFileName)
	lastStderrFile, err := os.Open(lastStderrFilePath)
	if err != nil {
		return "" // No previous stderr file
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

				return ""
			}

			err = os.Rename(lastStderrFilePath, filepath.Join(reportDir, stderrFileName))
			if err != nil {
				logger.V(1).Printf("Failed to move crash report to folder %q: %v", reportDir, err)

				return ""
			}

			// Mark this crash report dir as not complete, because we haven't generated a diagnostic yet.
			_, err = os.Create(filepath.Join(reportDir, writeInProgressFlag))
			if err != nil {
				logger.V(1).Printf("Failed to flag crash report dir %q as write in progress", reportDir)
			}

			logger.V(0).Printf("Created a crash-report dir at %q", reportDir)

			return reportDir
		}
	}

	return "" // No crash report dir created
}
