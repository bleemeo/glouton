// Copyright 2015-2025 Bleemeo
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
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"strconv"
	"time"

	"github.com/bleemeo/bleemeo-go"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/bleemeoapi"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	"github.com/bleemeo/glouton/crashreport"
	"github.com/bleemeo/glouton/logger"
	gloutonTypes "github.com/bleemeo/glouton/types"
)

var errUploadFailed = errors.New("upload failed")

type diagnosticWithBleemeoInfo struct {
	gloutonTypes.DiagnosticFile

	diagnosticType bleemeo.GloutonDiagnostic
	requestToken   string
}

func (s *Synchronizer) syncDiagnostics(ctx context.Context, syncType types.SyncType, execution types.SynchronizationExecution) (updateThresholds bool, err error) {
	_ = syncType

	apiClient := execution.BleemeoAPIClient()

	remoteDiagnostics, err := apiClient.ListDiagnostics(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to list remote diagnostics: %w", err)
	}

	stateDir := s.option.Config.Agent.StateDirectory
	if crashreport.IsWriteInProgress(stateDir) {
		return false, nil
	}

	localDiagnostics := s.listOnDemandDiagnostics()
	crashDiagnostics := crashreport.ListUnUploadedCrashReports(stateDir)

	if s.canUploadCrashReports() {
		localDiagnostics = append(localDiagnostics, addType(crashDiagnostics, bleemeo.GloutonDiagnostic_Crash)...)
	} else {
		// Discard all crash diagnostics generated before the throttle deadline
		for _, crashDiag := range crashDiagnostics {
			err = crashDiag.MarkUploaded()
			if err != nil {
				logger.V(2).Printf("Failed to discard crash diagnostic: %v", err)
			}
		}
	}

	diagnosticsToUpload := make([]diagnosticWithBleemeoInfo, 0, len(localDiagnostics))

	for _, diagnostic := range localDiagnostics {
		needUpload := true

		for _, remoteDiagnostic := range remoteDiagnostics {
			if remoteDiagnostic.Name == diagnostic.Filename() {
				needUpload = false

				break
			}
		}

		if !needUpload {
			if err := diagnostic.MarkUploaded(); err != nil {
				logger.V(1).Printf("Failed to mark diagnostic uploaded: %v", err)
			}
		} else {
			diagnosticsToUpload = append(diagnosticsToUpload, diagnostic)
		}
	}

	if err = s.uploadDiagnostics(ctx, apiClient, diagnosticsToUpload); err != nil {
		// We "ignore" error from diagnostics upload because:
		// * they aren't essential
		// * by "ignoring" the error, it will be re-tried on next full sync instead of after a short delay,
		//   which seems better since it could send a rather large payload.
		logger.V(1).Printf("Upload crash diagnostic: %v", err)
	}

	return false, nil
}

func (s *Synchronizer) listOnDemandDiagnostics() []diagnosticWithBleemeoInfo {
	s.state.l.Lock()
	defer s.state.l.Unlock()

	if s.state.onDemandDiagnostic.filename != "" {
		return []diagnosticWithBleemeoInfo{
			{
				diagnosticType: bleemeo.GloutonDiagnostic_OnDemand,
				requestToken:   s.state.onDemandDiagnostic.requestToken,
				DiagnosticFile: s.state.onDemandDiagnostic,
			},
		}
	}

	return nil
}

func (s *Synchronizer) uploadDiagnostics(ctx context.Context, apiClient types.DiagnosticClient, diagnostics []diagnosticWithBleemeoInfo) error {
	for _, diagnostic := range diagnostics {
		disabledUntil, err := s.uploadDiagnostic(ctx, apiClient, diagnostic)
		if err != nil {
			return fmt.Errorf("failed to upload crash diagnostic %s: %w", diagnostic.Filename(), err)
		}

		if disabledUntil > 0 {
			s.disableCrashReportUpload(disabledUntil)

			logger.V(2).Printf("Crash reports upload is disabled for %s", disabledUntil)

			break // crash reports are expected to be the last diagnostics from the list
		}
	}

	return nil
}

func (s *Synchronizer) uploadDiagnostic(ctx context.Context, apiClient types.DiagnosticClient, diagnostic diagnosticWithBleemeoInfo) (disableDelay time.Duration, err error) {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return 0, ctxErr
	}

	reader, err := diagnostic.Reader()
	if err != nil {
		return 0, err
	}

	defer reader.Close()

	if reader.Len() > bleemeoapi.DiagnosticMaxSize {
		logger.V(2).Printf("Skipping crash diagnostic %s which is too big.", diagnostic.Filename())

		return 0, diagnostic.MarkUploaded()
	}

	buf := new(bytes.Buffer)
	multipartWriter := multipart.NewWriter(buf)

	err = multipartWriter.WriteField("type", strconv.Itoa(int(diagnostic.diagnosticType)))
	if err != nil {
		return 0, err
	}

	if diagnostic.requestToken != "" {
		err = multipartWriter.WriteField("request_token", diagnostic.requestToken)
		if err != nil {
			return 0, err
		}
	}

	formFile, err := multipartWriter.CreateFormFile("archive", diagnostic.Filename())
	if err != nil {
		return 0, err
	}

	_, err = io.Copy(formFile, reader)
	if err != nil {
		return 0, err
	}

	_ = multipartWriter.Close()

	disabledDelay, err := apiClient.UploadDiagnostic(ctx, multipartWriter.FormDataContentType(), buf)
	if err != nil {
		return 0, err
	}

	return disabledDelay, diagnostic.MarkUploaded()
}

type synchronizerOnDemandDiagnostic struct {
	filename     string
	archive      []byte
	requestToken string
	s            *Synchronizer
}

func (diag synchronizerOnDemandDiagnostic) Filename() string {
	return diag.filename
}

func (diag synchronizerOnDemandDiagnostic) Reader() (gloutonTypes.ReaderWithLen, error) {
	return readerWithLen{Reader: bytes.NewReader(diag.archive)}, nil
}

type readerWithLen struct {
	*bytes.Reader
}

func (r readerWithLen) Len() int {
	return r.Reader.Len()
}

func (r readerWithLen) Close() error {
	return nil
}

func (diag synchronizerOnDemandDiagnostic) MarkUploaded() error {
	diag.s.state.l.Lock()
	defer diag.s.state.l.Unlock()

	if diag.filename != diag.s.state.onDemandDiagnostic.filename {
		// Another diagnostic replaced ourself in the synchronizer. Don't remove it
		return nil
	}

	diag.s.state.onDemandDiagnostic = synchronizerOnDemandDiagnostic{}

	return nil
}

func addType(diagnostics []gloutonTypes.DiagnosticFile, fixedType bleemeo.GloutonDiagnostic) []diagnosticWithBleemeoInfo {
	result := make([]diagnosticWithBleemeoInfo, 0, len(diagnostics))

	for _, diagnostic := range diagnostics {
		result = append(result, diagnosticWithBleemeoInfo{
			DiagnosticFile: diagnostic,
			diagnosticType: fixedType,
			requestToken:   "",
		})
	}

	return result
}
