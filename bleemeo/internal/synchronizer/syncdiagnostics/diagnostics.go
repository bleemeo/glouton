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

// Package syncdiagnostics uploads to Bleemeo the diagnostics asked from Bleemeo (on-demand)
// and the crash reports.
package syncdiagnostics

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/bleemeo/bleemeo-go"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/bleemeoapi"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/crashreport"
	"github.com/bleemeo/glouton/logger"
	gloutonTypes "github.com/bleemeo/glouton/types"
)

// disableCrashReportUploadCacheKey is the State key of the date until which crash reports
// aren't uploaded, as the API asked.
const disableCrashReportUploadCacheKey = "DisableCrashReportUploadUntil"

type diagnosticWithBleemeoInfo struct {
	gloutonTypes.DiagnosticFile

	diagnosticType bleemeo.GloutonDiagnostic
	requestToken   string
}

// SyncDiagnostics uploads the on-demand diagnostic and the crash reports.
type SyncDiagnostics struct {
	state    bleemeoTypes.State
	stateDir string
	now      func() time.Time

	l sync.Mutex
	// onDemand is the diagnostic Bleemeo asked for, waiting to be uploaded.
	// Its filename is empty when there is none.
	onDemand                      onDemandDiagnostic
	disableCrashReportUploadUntil time.Time
}

// New returns the entity. stateDir is where the crash reports are written. now is the
// synchronizer's clock.
func New(state bleemeoTypes.State, stateDir string, now func() time.Time) *SyncDiagnostics {
	s := &SyncDiagnostics{
		state:    state,
		stateDir: stateDir,
		now:      now,
	}

	if state != nil {
		if err := state.Get(disableCrashReportUploadCacheKey, &s.disableCrashReportUploadUntil); err != nil {
			logger.V(1).Printf("Failed to load crash report upload throttle info from cache: %v", err)
		} else if s.disableCrashReportUploadUntil.Before(now()) {
			s.disableCrashReportUploadUntil = time.Time{}
		}
	}

	return s
}

func (s *SyncDiagnostics) Name() types.EntityName {
	return types.EntityDiagnostics
}

func (s *SyncDiagnostics) EnabledInMaintenance() bool {
	return false
}

func (s *SyncDiagnostics) EnabledInSuspendedMode() bool {
	return false
}

// ScheduleOnDemand stores the given diagnostic until the next synchronization of the
// diagnostics. Only the latest scheduled diagnostic is uploaded.
func (s *SyncDiagnostics) ScheduleOnDemand(filename, requestToken string, contents []byte) {
	s.l.Lock()
	defer s.l.Unlock()

	s.onDemand = onDemandDiagnostic{
		filename:     filepath.Base(filename),
		archive:      contents,
		requestToken: requestToken,
		parent:       s,
	}
}

func (s *SyncDiagnostics) PrepareExecution(_ context.Context, execution types.SynchronizationExecution) (types.EntitySynchronizerExecution, error) {
	return &syncDiagnosticsExecution{
		parent:    s,
		execution: execution,
	}, nil
}

// DiagnosticArchive writes the throttle of the crash reports and the on-demand diagnostic
// waiting to be uploaded.
func (s *SyncDiagnostics) DiagnosticArchive(_ context.Context, archive gloutonTypes.ArchiveWriter) error {
	file, err := archive.Create("bleemeo-sync-diagnostics.json")
	if err != nil {
		return err
	}

	s.l.Lock()
	defer s.l.Unlock()

	obj := struct {
		DisableCrashReportUploadUntil time.Time
		// PendingOnDemand is the file name of the on-demand diagnostic waiting to be uploaded.
		PendingOnDemand string
	}{
		DisableCrashReportUploadUntil: s.disableCrashReportUploadUntil,
		PendingOnDemand:               s.onDemand.filename,
	}

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")

	return enc.Encode(obj)
}

func (s *SyncDiagnostics) onDemandDiagnostics() []diagnosticWithBleemeoInfo {
	s.l.Lock()
	defer s.l.Unlock()

	if s.onDemand.filename == "" {
		return nil
	}

	return []diagnosticWithBleemeoInfo{
		{
			diagnosticType: bleemeo.GloutonDiagnostic_OnDemand,
			requestToken:   s.onDemand.requestToken,
			DiagnosticFile: s.onDemand,
		},
	}
}

func (s *SyncDiagnostics) canUploadCrashReports() bool {
	s.l.Lock()
	defer s.l.Unlock()

	return s.disableCrashReportUploadUntil.Before(s.now())
}

func (s *SyncDiagnostics) disableCrashReportUpload(duration time.Duration) {
	until := s.now().Add(duration)

	s.l.Lock()
	s.disableCrashReportUploadUntil = until
	s.l.Unlock()

	if err := s.state.Set(disableCrashReportUploadCacheKey, until); err != nil {
		logger.V(1).Printf("Failed to save crash report upload throttle info to cache: %v", err)
	}
}

type syncDiagnosticsExecution struct {
	parent    *SyncDiagnostics
	execution types.SynchronizationExecution

	// remote is the diagnostics already uploaded, as read by RefreshCache.
	remote []bleemeoapi.RemoteDiagnostic
}

// NeedSynchronization returns false: the diagnostics are uploaded when one is scheduled, and
// on full synchronizations for the crash reports.
func (e *syncDiagnosticsExecution) NeedSynchronization(_ context.Context) (bool, error) {
	return false, nil
}

func (e *syncDiagnosticsExecution) RefreshCache(ctx context.Context, _ types.SyncType) error {
	remote, err := e.execution.BleemeoAPIClient().ListDiagnostics(ctx)
	if err != nil {
		return fmt.Errorf("failed to list remote diagnostics: %w", err)
	}

	e.remote = remote

	return nil
}

func (e *syncDiagnosticsExecution) SyncRemoteAndLocal(ctx context.Context, _ types.SyncType) error {
	if crashreport.IsWriteInProgress(e.parent.stateDir) {
		return nil
	}

	local := selectDiagnostics(
		e.parent.onDemandDiagnostics(),
		crashreport.ListUnUploadedCrashReports(e.parent.stateDir),
		e.parent.canUploadCrashReports(),
	)

	disableDelay, err := uploadMissing(ctx, e.execution.BleemeoAPIClient(), e.remote, local)
	if disableDelay > 0 {
		e.parent.disableCrashReportUpload(disableDelay)

		logger.V(2).Printf("Crash reports upload is disabled for %s", disableDelay)
	}

	if err != nil {
		// We "ignore" error from diagnostics upload because:
		// * they aren't essential
		// * by "ignoring" the error, it will be re-tried on next full sync instead of after a short delay,
		//   which seems better since it could send a rather large payload.
		logger.V(1).Printf("Upload crash diagnostic: %v", err)
	}

	return nil
}

func (e *syncDiagnosticsExecution) FinishExecution(_ context.Context) {
	e.execution = nil
}

// selectDiagnostics returns the diagnostics to upload: the on-demand ones, then the crash
// reports when their upload isn't disabled. Otherwise, the crash reports are discarded.
func selectDiagnostics(
	onDemand []diagnosticWithBleemeoInfo,
	crashReports []gloutonTypes.DiagnosticFile,
	canUploadCrashReports bool,
) []diagnosticWithBleemeoInfo {
	if canUploadCrashReports {
		return append(onDemand, addType(crashReports, bleemeo.GloutonDiagnostic_Crash)...)
	}

	// Discard all crash diagnostics generated before the throttle deadline
	for _, crashDiag := range crashReports {
		if err := crashDiag.MarkUploaded(); err != nil {
			logger.V(2).Printf("Failed to discard crash diagnostic: %v", err)
		}
	}

	return onDemand
}

// uploadMissing uploads the local diagnostics the API doesn't have yet, and marks the others
// as uploaded. It stops once the API asks to wait before uploading more crash reports, and
// returns that delay.
func uploadMissing(
	ctx context.Context,
	apiClient types.DiagnosticClient,
	remote []bleemeoapi.RemoteDiagnostic,
	local []diagnosticWithBleemeoInfo,
) (time.Duration, error) {
	for _, diagnostic := range local {
		needUpload := true

		for _, remoteDiagnostic := range remote {
			if remoteDiagnostic.Name == diagnostic.Filename() {
				needUpload = false

				break
			}
		}

		if !needUpload {
			if err := diagnostic.MarkUploaded(); err != nil {
				logger.V(1).Printf("Failed to mark diagnostic uploaded: %v", err)
			}

			continue
		}

		disableDelay, err := uploadDiagnostic(ctx, apiClient, diagnostic)
		if err != nil {
			return 0, fmt.Errorf("failed to upload crash diagnostic %s: %w", diagnostic.Filename(), err)
		}

		if disableDelay > 0 {
			return disableDelay, nil // crash reports are expected to be the last diagnostics from the list
		}
	}

	return 0, nil
}

func uploadDiagnostic(ctx context.Context, apiClient types.DiagnosticClient, diagnostic diagnosticWithBleemeoInfo) (disableDelay time.Duration, err error) {
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

// onDemandDiagnostic is a diagnostic Bleemeo asked for. It's kept in memory until uploaded.
type onDemandDiagnostic struct {
	filename     string
	archive      []byte
	requestToken string
	parent       *SyncDiagnostics
}

func (diag onDemandDiagnostic) Filename() string {
	return diag.filename
}

func (diag onDemandDiagnostic) Reader() (gloutonTypes.ReaderWithLen, error) {
	return readerWithLen{Reader: bytes.NewReader(diag.archive)}, nil
}

func (diag onDemandDiagnostic) MarkUploaded() error {
	diag.parent.l.Lock()
	defer diag.parent.l.Unlock()

	if diag.filename != diag.parent.onDemand.filename {
		// Another diagnostic replaced ourself in the synchronizer. Don't remove it
		return nil
	}

	diag.parent.onDemand = onDemandDiagnostic{}

	return nil
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
