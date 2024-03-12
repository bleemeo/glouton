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
	"strconv"
)

const diagnosticMaxSize = 5 << 20 // 5MB

type RemoteDiagnostic struct {
	Name string `json:"name"`
}

type diagnostic struct {
	filename     string
	requestToken string
	archive      io.Reader
}

type diagnosticType = int

const (
	crashDiagnostic    diagnosticType = 0
	onDemandDiagnostic diagnosticType = 1
)

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

func (s *Synchronizer) syncDiagnostics(
	ctx context.Context,
	_ bool,
	_ bool,
) (updateThresholds bool, err error) {
	stateDir := s.option.Config.Agent.StateDirectory
	if crashreport.IsWriteInProgress(stateDir) {
		return false, nil
	}

	remoteDiagnostics, err := s.listRemoteDiagnostics(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to list remote diagnostics: %w", err)
	}

	err = s.syncOnDemandDiagnostic(ctx, remoteDiagnostics)
	if err != nil {
		// We "ignore" on-demand diagnostic upload errors for the same reasons as crash diagnostics (see below)
		logger.V(1).Printf("Failed to synchronize on-demand diagnostics: %v", err)
	}

	localCrashDiagnostics := crashreport.ListCrashReports(stateDir)

	diagnosticPaths := make([]string, len(remoteDiagnostics))

	for i, diagnostic := range remoteDiagnostics {
		diagnosticPaths[i] = filepath.Join(stateDir, diagnostic.Name)
	}

	notUploadedYet := sliceDiff(localCrashDiagnostics, diagnosticPaths)

	if err = s.uploadCrashDiagnostics(ctx, notUploadedYet); err != nil {
		// We "ignore" error from crash diagnostic upload because:
		// * they aren't essential
		// * by "ignoring" the error, it will be re-tried on next full sync instead of after a short delay,
		//   which seems better since it could send a rather large payload.
		logger.V(1).Printf("Upload crash diagnostic: %v", err)
	}

	return false, nil
}

func (s *Synchronizer) listRemoteDiagnostics(ctx context.Context) ([]RemoteDiagnostic, error) {
	result, err := s.client.Iter(ctx, "gloutondiagnostic", nil)
	if err != nil {
		return nil, fmt.Errorf("client iter: %w", err)
	}

	diagnostics := make([]RemoteDiagnostic, 0, len(result))

	for _, jsonMessage := range result {
		var remoteDiagnostic RemoteDiagnostic

		if err = json.Unmarshal(jsonMessage, &remoteDiagnostic); err != nil {
			logger.V(2).Printf("Failed to unmarshal diagnostic: %v", err)

			continue
		}

		diagnostics = append(diagnostics, remoteDiagnostic)
	}

	return diagnostics, nil
}

func (s *Synchronizer) uploadCrashDiagnostics(ctx context.Context, diagnostics []string) error {
	for _, diagnostic := range diagnostics {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}

		err := s.uploadCrashDiagnostic(ctx, diagnostic)
		if err != nil {
			return fmt.Errorf("failed to upload crash diagnostic %q: %w", diagnostic, err)
		}
	}

	return nil
}

func (s *Synchronizer) uploadCrashDiagnostic(ctx context.Context, diagnosticPath string) error {
	diagnosticFile, err := os.Open(diagnosticPath)
	if err != nil {
		return err
	}

	defer diagnosticFile.Close()

	stat, err := diagnosticFile.Stat()
	if err != nil {
		return err
	}

	if stat.Size() > diagnosticMaxSize {
		logger.V(2).Printf("Skipping crash diagnostic %q which is too big.", diagnosticPath)

		return nil
	}

	return s.uploadDiagnostic(ctx, filepath.Base(diagnosticPath), diagnosticFile, crashDiagnostic)
}

func (s *Synchronizer) uploadDiagnostic(ctx context.Context, filename string, r io.Reader, diagnosticType diagnosticType, requestToken ...string) error {
	buf := new(bytes.Buffer)
	multipartWriter := multipart.NewWriter(buf)

	err := multipartWriter.WriteField("diagnostic_type", strconv.Itoa(diagnosticType))
	if err != nil {
		return err
	}

	if len(requestToken) == 1 {
		err = multipartWriter.WriteField("request_token", requestToken[0])
		if err != nil {
			return err
		}
	}

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

	statusCode, reqErr := s.client.DoWithBody(ctx, "v1/gloutondiagnostic/", contentType, buf)
	if reqErr != nil {
		return reqErr
	}

	if statusCode != http.StatusCreated {
		logger.V(1).Printf("Diagnostic upload returned status %d %s", statusCode, http.StatusText(statusCode))
	}

	return nil
}

func (s *Synchronizer) syncOnDemandDiagnostic(ctx context.Context, remoteDiagnostics []RemoteDiagnostic) error {
	s.onDemandDiagnosticLock.Lock()
	defer s.onDemandDiagnosticLock.Unlock()

	if s.onDemandDiagnostic == nil {
		return nil
	}

	needUpload := true

	for _, remoteDiagnostic := range remoteDiagnostics {
		if remoteDiagnostic.Name == s.onDemandDiagnostic.filename {
			needUpload = false // already on API

			break
		}
	}

	if needUpload {
		err := s.uploadDiagnostic(ctx, s.onDemandDiagnostic.filename, s.onDemandDiagnostic.archive, onDemandDiagnostic, s.onDemandDiagnostic.requestToken)
		if err != nil {
			return err
		}
	}

	s.onDemandDiagnostic = nil

	return nil
}

// ScheduleDiagnosticUpload stores the given diagnostic until the next synchronization,
// where it will be uploaded to the API.
// If another call to this method is made before the next synchronization,
// only the latest diagnostic will be uploaded.
func (s *Synchronizer) ScheduleDiagnosticUpload(filename, requestToken string, r io.Reader) {
	s.onDemandDiagnosticLock.Lock()
	defer s.onDemandDiagnosticLock.Unlock()

	s.onDemandDiagnostic = &diagnostic{filename, requestToken, r}
}
