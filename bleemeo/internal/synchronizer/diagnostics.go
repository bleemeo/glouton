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
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strconv"

	"github.com/bleemeo/bleemeo-go"
	"github.com/bleemeo/glouton/crashreport"
	"github.com/bleemeo/glouton/logger"
	gloutonTypes "github.com/bleemeo/glouton/types"
)

const diagnosticMaxSize = 5 << 20 // 5MB

var errUploadFailed = errors.New("upload failed")

type RemoteDiagnostic struct {
	Name string `json:"name"`
}

type diagnosticWithBleemeoInfo struct {
	gloutonTypes.DiagnosticFile
	diagnosticType diagnosticType
	requestToken   string
}

type diagnosticType = int

const (
	crashDiagnostic    diagnosticType = 0
	onDemandDiagnostic diagnosticType = 1
)

func (s *Synchronizer) syncDiagnostics(ctx context.Context, _, _ bool) (updateThresholds bool, err error) {
	remoteDiagnostics, err := s.listRemoteDiagnostics(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to list remote diagnostics: %w", err)
	}

	stateDir := s.option.Config.Agent.StateDirectory
	if crashreport.IsWriteInProgress(stateDir) {
		return false, nil
	}

	localDiagnostics := addType(crashreport.ListUnUploadedCrashReports(stateDir), crashDiagnostic)
	localDiagnostics = append(localDiagnostics, s.listOnDemandDiagnostics()...)
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

	if err = s.uploadDiagnostics(ctx, diagnosticsToUpload); err != nil {
		// We "ignore" error from diagnostics upload because:
		// * they aren't essential
		// * by "ignoring" the error, it will be re-tried on next full sync instead of after a short delay,
		//   which seems better since it could send a rather large payload.
		logger.V(1).Printf("Upload crash diagnostic: %v", err)
	}

	return false, nil
}

func (s *Synchronizer) listOnDemandDiagnostics() []diagnosticWithBleemeoInfo {
	s.onDemandDiagnosticLock.Lock()
	defer s.onDemandDiagnosticLock.Unlock()

	if s.onDemandDiagnostic.filename != "" {
		return []diagnosticWithBleemeoInfo{
			{
				diagnosticType: onDemandDiagnostic,
				requestToken:   s.onDemandDiagnostic.requestToken,
				DiagnosticFile: s.onDemandDiagnostic,
			},
		}
	}

	return nil
}

func (s *Synchronizer) listRemoteDiagnostics(ctx context.Context) ([]RemoteDiagnostic, error) {
	iter := s.client.Iterator(bleemeo.ResourceGloutonDiagnostic, nil)

	count, err := iter.Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("client iter count: %w", err)
	}

	diagnostics := make([]RemoteDiagnostic, 0, count)

	for iter.Next(ctx) {
		var remoteDiagnostic RemoteDiagnostic

		if err = json.Unmarshal(iter.At(), &remoteDiagnostic); err != nil {
			logger.V(2).Printf("Failed to unmarshal diagnostic: %v", err)

			continue
		}

		diagnostics = append(diagnostics, remoteDiagnostic)
	}

	if iter.Err() != nil {
		return nil, iter.Err()
	}

	return diagnostics, nil
}

func (s *Synchronizer) uploadDiagnostics(ctx context.Context, diagnostics []diagnosticWithBleemeoInfo) error {
	for _, diagnostic := range diagnostics {
		if err := s.uploadDiagnostic(ctx, diagnostic); err != nil {
			return fmt.Errorf("failed to upload crash diagnostic %s: %w", diagnostic.Filename(), err)
		}
	}

	return nil
}

func (s *Synchronizer) uploadDiagnostic(ctx context.Context, diagnostic diagnosticWithBleemeoInfo) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}

	reader, err := diagnostic.Reader()
	if err != nil {
		return err
	}

	defer reader.Close()

	if reader.Len() > diagnosticMaxSize {
		logger.V(2).Printf("Skipping crash diagnostic %s which is too big.", diagnostic.Filename())

		return diagnostic.MarkUploaded()
	}

	buf := new(bytes.Buffer)
	multipartWriter := multipart.NewWriter(buf)

	err = multipartWriter.WriteField("type", strconv.Itoa(diagnostic.diagnosticType))
	if err != nil {
		return err
	}

	if diagnostic.requestToken != "" {
		err = multipartWriter.WriteField("request_token", diagnostic.requestToken)
		if err != nil {
			return err
		}
	}

	formFile, err := multipartWriter.CreateFormFile("archive", diagnostic.Filename())
	if err != nil {
		return err
	}

	_, err = io.Copy(formFile, reader)
	if err != nil {
		return err
	}

	multipartWriter.Close()

	contentType := multipartWriter.FormDataContentType()

	statusCode, reqErr := s.client.DoWithBody(ctx, bleemeo.ResourceGloutonDiagnostic, contentType, buf)
	if reqErr != nil {
		return reqErr
	}

	if statusCode != http.StatusCreated {
		return fmt.Errorf("%w: status %d %s", errUploadFailed, statusCode, http.StatusText(statusCode))
	}

	return diagnostic.MarkUploaded()
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
	diag.s.onDemandDiagnosticLock.Lock()
	defer diag.s.onDemandDiagnosticLock.Unlock()

	if diag.filename != diag.s.onDemandDiagnostic.filename {
		// Another diagnostic replaced ourself in the synchronizer. Don't remove it
		return nil
	}

	diag.s.onDemandDiagnostic = synchronizerOnDemandDiagnostic{}

	return nil
}

// ScheduleDiagnosticUpload stores the given diagnostic until the next synchronization,
// where it will be uploaded to the API.
// If another call to this method is made before the next synchronization,
// only the latest diagnostic will be uploaded.
func (s *Synchronizer) ScheduleDiagnosticUpload(filename, requestToken string, contents []byte) {
	s.l.Lock()
	defer s.l.Unlock()
	s.onDemandDiagnosticLock.Lock()
	defer s.onDemandDiagnosticLock.Unlock()

	s.forceSync[syncMethodDiagnostics] = false
	s.onDemandDiagnostic = synchronizerOnDemandDiagnostic{
		filename:     filepath.Base(filename),
		archive:      contents,
		s:            s,
		requestToken: requestToken,
	}
}

func addType(diagnostics []gloutonTypes.DiagnosticFile, fixedType diagnosticType) []diagnosticWithBleemeoInfo {
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
