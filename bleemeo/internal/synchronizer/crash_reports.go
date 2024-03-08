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

package synchronizer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"glouton/crashreport"
	"glouton/logger"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
)

type diagnostic struct {
	filename string
	archive  io.Reader
}

type RemoteCrashReport struct {
	Name string `json:"name"`
}

// sliceDiff returns elements of s1 that are absent from s2.
func sliceDiff(s1, s2 []string) []string {
	var d []string

S1:
	for _, e1 := range s1 {
		for _, e2 := range s2 {
			if e1 == e2 {
				continue S1
			}
		}
		// e1 was not found in s2
		d = append(d, e1)
	}

	return d
}

func (s *Synchronizer) syncCrashReports(
	ctx context.Context,
	_ bool,
	_ bool,
) (updateThresholds bool, err error) {
	err = s.syncOnDemandDiagnostic(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to synchronize on-demand diagnostic: %w", err)
	}

	stateDir := s.option.Config.Agent.StateDirectory
	if crashreport.IsWriteInProgress(stateDir) {
		return false, nil
	}

	remoteCrashReports, err := s.listRemoteCrashReports(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to list remote crash reports: %w", err)
	}

	localCrashReports := crashreport.ListCrashReports(stateDir)

	reportPaths := make([]string, len(remoteCrashReports))

	for i, report := range remoteCrashReports {
		reportPaths[i] = filepath.Join(stateDir, report.Name)
	}

	notUploadedYet := sliceDiff(localCrashReports, reportPaths)

	if err = s.uploadCrashReports(ctx, notUploadedYet); err != nil {
		// We "ignore" error from crash report upload because:
		// * they are not essential
		// * by "ignoring" the error, it will be re-tried on next full sync instead of after a short delay,
		//   which seems better since it could send a rather large payload.
		logger.V(1).Printf("Upload crash report: %v", err)
	}

	return false, nil
}

func (s *Synchronizer) listRemoteCrashReports(ctx context.Context) ([]RemoteCrashReport, error) {
	result, err := s.client.Iter(ctx, "gloutoncrashreport", nil)
	if err != nil {
		return nil, fmt.Errorf("client iter: %w", err)
	}

	crashReports := make([]RemoteCrashReport, 0, len(result))

	for _, jsonMessage := range result {
		var report RemoteCrashReport

		if err = json.Unmarshal(jsonMessage, &report); err != nil {
			logger.V(2).Printf("Failed to unmarshal crash report: %v", err)

			continue
		}

		crashReports = append(crashReports, report)
	}

	return crashReports, nil
}

func (s *Synchronizer) uploadCrashReports(ctx context.Context, reports []string) error {
	for _, report := range reports {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}

		err := s.uploadCrashReport(ctx, report)
		if err != nil {
			return fmt.Errorf("failed to upload crash report %q: %w", report, err)
		}
	}

	return nil
}

func (s *Synchronizer) uploadCrashReport(ctx context.Context, reportPath string) error {
	reportFile, err := os.Open(reportPath)
	if err != nil {
		return err
	}

	defer reportFile.Close()

	stat, err := reportFile.Stat()
	if err != nil {
		return err
	}

	if stat.Size() > crashreport.MaxReportSize {
		logger.V(2).Printf("Skipping crash report %q which is too big.", reportPath)

		return nil
	}

	return s.uploadDiagnostic(ctx, filepath.Base(reportPath), reportFile)
}

func (s *Synchronizer) uploadDiagnostic(ctx context.Context, filename string, r io.Reader) error {
	buf := new(bytes.Buffer)
	multipartWriter := multipart.NewWriter(buf)

	formFile, err := multipartWriter.CreateFormFile("report_archive", filename)
	if err != nil {
		return err
	}

	_, err = io.Copy(formFile, r)
	if err != nil {
		return err
	}

	multipartWriter.Close()

	contentType := multipartWriter.FormDataContentType()

	statusCode, reqErr := s.client.DoWithBody(ctx, "v1/gloutoncrashreport/", contentType, buf)
	if reqErr != nil {
		return reqErr
	}

	if statusCode != http.StatusCreated {
		logger.V(1).Printf("Crash report upload returned status %d %s", statusCode, http.StatusText(statusCode))
	}

	return nil
}

func (s *Synchronizer) syncOnDemandDiagnostic(ctx context.Context) error {
	s.onDemandDiagnosticLock.Lock()
	defer s.onDemandDiagnosticLock.Unlock()

	if s.onDemandDiagnostic == nil {
		return nil
	}

	err := s.uploadDiagnostic(ctx, s.onDemandDiagnostic.filename, s.onDemandDiagnostic.archive)
	if err != nil {
		return err
	}

	s.onDemandDiagnostic = nil

	return nil
}

// ScheduleDiagnosticUpload stores the given diagnostic until the next synchronization,
// where it will be uploaded to the API.
// If another call to this method is made before the next synchronization,
// only the latest diagnostic will be uploaded.
func (s *Synchronizer) ScheduleDiagnosticUpload(filename string, r io.Reader) {
	s.onDemandDiagnosticLock.Lock()
	defer s.onDemandDiagnosticLock.Unlock()

	s.onDemandDiagnostic = &diagnostic{filename, r}
}
