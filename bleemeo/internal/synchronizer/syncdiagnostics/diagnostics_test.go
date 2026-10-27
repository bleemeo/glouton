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

package syncdiagnostics

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"testing"
	"time"

	"github.com/bleemeo/bleemeo-go"
	"github.com/bleemeo/glouton/agent/state"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/bleemeoapi"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	gloutonTypes "github.com/bleemeo/glouton/types"

	"github.com/google/go-cmp/cmp"
)

var (
	errTestAPI   = errors.New("test API error")
	errNoArchive = errors.New("no archive in the uploaded diagnostic")
)

var t0 = time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC) //nolint:gochecknoglobals

// fakeClient records the file names uploaded, and answers each upload with the next delay of
// delays, then with no delay. Any other call panics.
type fakeClient struct {
	types.Client

	remote   []bleemeoapi.RemoteDiagnostic
	delays   []time.Duration
	err      error
	uploaded []string
}

func (c *fakeClient) ListDiagnostics(context.Context) ([]bleemeoapi.RemoteDiagnostic, error) {
	return c.remote, c.err
}

func (c *fakeClient) UploadDiagnostic(_ context.Context, contentType string, content io.Reader) (time.Duration, error) {
	if c.err != nil {
		return 0, c.err
	}

	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return 0, err
	}

	reader := multipart.NewReader(content, params["boundary"])

	for {
		part, err := reader.NextPart()
		if err != nil {
			return 0, errNoArchive
		}

		if part.FormName() == "archive" {
			c.uploaded = append(c.uploaded, part.FileName())

			break
		}
	}

	if len(c.delays) == 0 {
		return 0, nil
	}

	delay := c.delays[0]
	c.delays = c.delays[1:]

	return delay, nil
}

// fakeExecution gives the client to the entity. Any other call panics.
type fakeExecution struct {
	types.SynchronizationExecution

	client *fakeClient
}

func (e *fakeExecution) BleemeoAPIClient() types.Client { return e.client }

// fakeFile is a crash report, which records whether it was marked uploaded.
type fakeFile struct {
	name     string
	uploaded bool
}

func (f *fakeFile) Filename() string { return f.name }

func (f *fakeFile) Reader() (gloutonTypes.ReaderWithLen, error) {
	return readerWithLen{Reader: bytes.NewReader([]byte("the crash report"))}, nil
}

func (f *fakeFile) MarkUploaded() error {
	f.uploaded = true

	return nil
}

func crashReports(files ...*fakeFile) []diagnosticWithBleemeoInfo {
	diagnostics := make([]gloutonTypes.DiagnosticFile, 0, len(files))

	for _, f := range files {
		diagnostics = append(diagnostics, f)
	}

	return addType(diagnostics, bleemeo.GloutonDiagnostic_Crash)
}

// Test_uploadMissing checks only the diagnostics the API doesn't have are uploaded, that the
// others are marked uploaded, and that uploads stop once the API asks to wait.
func Test_uploadMissing(t *testing.T) {
	t.Parallel()

	t.Run("already on the API", func(t *testing.T) {
		t.Parallel()

		onAPI, missing := &fakeFile{name: "crash-1.zip"}, &fakeFile{name: "crash-2.zip"}
		client := &fakeClient{}

		delay, err := uploadMissing(t.Context(), client, []bleemeoapi.RemoteDiagnostic{{Name: "crash-1.zip"}}, crashReports(onAPI, missing))
		if err != nil || delay != 0 {
			t.Fatalf("uploadMissing() = %v, %v, want 0, nil", delay, err)
		}

		if diff := cmp.Diff([]string{"crash-2.zip"}, client.uploaded); diff != "" {
			t.Errorf("uploaded mismatch (-want +got)\n%s", diff)
		}

		if !onAPI.uploaded || !missing.uploaded {
			t.Errorf("uploaded marks = %v, %v, want both true", onAPI.uploaded, missing.uploaded)
		}
	})

	t.Run("API asks to wait", func(t *testing.T) {
		t.Parallel()

		first, second := &fakeFile{name: "crash-1.zip"}, &fakeFile{name: "crash-2.zip"}
		client := &fakeClient{delays: []time.Duration{time.Hour}}

		delay, err := uploadMissing(t.Context(), client, nil, crashReports(first, second))
		if err != nil || delay != time.Hour {
			t.Fatalf("uploadMissing() = %v, %v, want 1h, nil", delay, err)
		}

		if diff := cmp.Diff([]string{"crash-1.zip"}, client.uploaded); diff != "" {
			t.Errorf("uploaded mismatch (-want +got)\n%s", diff)
		}

		if second.uploaded {
			t.Error("the crash report after the delay was marked uploaded")
		}
	})

	t.Run("upload fails", func(t *testing.T) {
		t.Parallel()

		file := &fakeFile{name: "crash-1.zip"}
		client := &fakeClient{err: errTestAPI}

		if _, err := uploadMissing(t.Context(), client, nil, crashReports(file)); !errors.Is(err, errTestAPI) {
			t.Errorf("uploadMissing() error = %v, want %v", err, errTestAPI)
		}

		if file.uploaded {
			t.Error("a crash report which failed to upload was marked uploaded")
		}
	})
}

// Test_selectDiagnostics checks the crash reports are discarded while their upload is disabled.
func Test_selectDiagnostics(t *testing.T) {
	t.Parallel()

	s := New(nil, t.TempDir(), func() time.Time { return t0 })
	s.ScheduleOnDemand("/tmp/on-demand.zip", "token", []byte("archive"))

	for _, canUpload := range []bool{true, false} {
		crash := &fakeFile{name: "crash-1.zip"}

		got := selectDiagnostics(s.onDemandDiagnostics(), []gloutonTypes.DiagnosticFile{crash}, canUpload)

		names := make([]string, 0, len(got))
		for _, d := range got {
			names = append(names, d.Filename())
		}

		want := []string{"on-demand.zip"}
		if canUpload {
			want = append(want, "crash-1.zip")
		}

		if diff := cmp.Diff(want, names); diff != "" {
			t.Errorf("canUpload=%v: diagnostics mismatch (-want +got)\n%s", canUpload, diff)
		}

		if crash.uploaded == canUpload {
			t.Errorf("canUpload=%v: crash report marked uploaded = %v, want %v", canUpload, crash.uploaded, !canUpload)
		}
	}
}

// TestOnDemandReplaced checks a diagnostic scheduled while another one is uploaded stays
// pending: marking the first one uploaded must not drop the second one.
func TestOnDemandReplaced(t *testing.T) {
	t.Parallel()

	s := New(nil, t.TempDir(), func() time.Time { return t0 })
	s.ScheduleOnDemand("first.zip", "token-1", []byte("first"))

	uploading := s.onDemandDiagnostics()

	s.ScheduleOnDemand("second.zip", "token-2", []byte("second"))

	if err := uploading[0].MarkUploaded(); err != nil {
		t.Fatal(err)
	}

	pending := s.onDemandDiagnostics()
	if len(pending) != 1 || pending[0].Filename() != "second.zip" || pending[0].requestToken != "token-2" {
		t.Fatalf("pending = %+v, want second.zip", pending)
	}

	if err := pending[0].MarkUploaded(); err != nil {
		t.Fatal(err)
	}

	if pending := s.onDemandDiagnostics(); len(pending) != 0 {
		t.Errorf("pending = %+v, want none", pending)
	}
}

// TestCrashReportThrottle checks the delay the API answered is saved in the State, so that it
// still applies after a restart, and is dropped once over.
func TestCrashReportThrottle(t *testing.T) {
	t.Parallel()

	now := t0
	clock := func() time.Time { return now }

	// An in-memory State, which encodes the values as the State file does.
	memoryState, err := state.LoadReadOnly("", "")
	if err != nil {
		t.Fatal(err)
	}

	s := New(memoryState, t.TempDir(), clock)
	if !s.canUploadCrashReports() {
		t.Fatal("crash reports disabled before the API asked to")
	}

	s.disableCrashReportUpload(time.Hour)

	if s.canUploadCrashReports() {
		t.Error("crash reports still enabled after the API asked to wait")
	}

	if restarted := New(memoryState, t.TempDir(), clock); restarted.canUploadCrashReports() {
		t.Error("the delay asked by the API was lost on restart")
	}

	now = t0.Add(2 * time.Hour)

	restarted := New(memoryState, t.TempDir(), clock)
	if !restarted.canUploadCrashReports() || !restarted.disableCrashReportUploadUntil.IsZero() {
		t.Errorf("after the delay, the restarted entity still has %v", restarted.disableCrashReportUploadUntil)
	}
}

// TestEntityUploadsOnDemand runs two executions: the on-demand diagnostic is uploaded by the
// first one only, though the API doesn't list it afterwards.
func TestEntityUploadsOnDemand(t *testing.T) {
	t.Parallel()

	s := New(nil, t.TempDir(), func() time.Time { return t0 })
	s.ScheduleOnDemand("on-demand.zip", "token", []byte("archive"))

	client := &fakeClient{remote: []bleemeoapi.RemoteDiagnostic{{Name: "older.zip"}}}

	for run := range 2 {
		execution, err := s.PrepareExecution(t.Context(), &fakeExecution{client: client})
		if err != nil {
			t.Fatal(err)
		}

		if err := execution.RefreshCache(t.Context(), 0); err != nil {
			t.Fatalf("run %d: RefreshCache() = %v", run, err)
		}

		if err := execution.SyncRemoteAndLocal(t.Context(), 0); err != nil {
			t.Fatalf("run %d: SyncRemoteAndLocal() = %v", run, err)
		}

		execution.FinishExecution(t.Context())
	}

	if diff := cmp.Diff([]string{"on-demand.zip"}, client.uploaded); diff != "" {
		t.Errorf("uploaded mismatch (-want +got)\n%s", diff)
	}
}

// TestEntityListFails checks a failure to list the uploaded diagnostics is the entity's error.
func TestEntityListFails(t *testing.T) {
	t.Parallel()

	s := New(nil, t.TempDir(), func() time.Time { return t0 })

	execution, err := s.PrepareExecution(t.Context(), &fakeExecution{client: &fakeClient{err: errTestAPI}})
	if err != nil {
		t.Fatal(err)
	}

	if err := execution.RefreshCache(t.Context(), 0); !errors.Is(err, errTestAPI) {
		t.Errorf("RefreshCache() = %v, want %v", err, errTestAPI)
	}
}
