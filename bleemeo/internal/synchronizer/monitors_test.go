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

package synchronizer

import (
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bleemeo/bleemeo-go"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/google/go-cmp/cmp"
)

func Test_applyJitterToMonitorCreationDate_fixedProbe(t *testing.T) {
	// This test ensure that some probes set yield enough randomization.
	// Those ID should be ID of real probes.
	tests := []struct {
		name     string
		probeIDs []string
		// remember than wantMinDelta is the smaller delta between two probes.
		// So the more their is probeIDs, the smaller this delta will be.
		// Theoretically, the best value should be spreadRange / (len(probeIDs)-1)
		wantMinDelta time.Duration
	}{
		// Those are an examples with random uuid (and low wantDelta).
		// Real case should use true agent ID and high wantDelta.
		{
			name: "default-europe",
			probeIDs: []string{
				"7c6ab4b8-fc54-4615-8e9e-d83750666b90",
				"cf173500-530b-473b-a45f-39d85ef148f9",
				"4cf13deb-94d5-40a8-a279-df0e7bfd4146",
			},
			wantMinDelta: 0,
		},
		{
			name: "old-default",
			probeIDs: []string{
				"7c6ab4b8-fc54-4615-8e9e-d83750666b90",
				"61680075-9af4-4318-824d-c5ae87ace0d5",
				"68ac67fe-ad18-40ee-88fc-5debc553562d",
			},
			wantMinDelta: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for range 10 {
				good := true
				probesRuns := make([]time.Time, 0, len(tt.probeIDs))

				createDate := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(rand.Int63n(int64(365 * 24 * time.Hour)))) //nolint:gosec
				monitor := bleemeoTypes.Monitor{
					CreationDate: createDate.Format(time.RFC3339),
				}

				for _, id := range tt.probeIDs {
					agentHash := hashAgentID(id)

					got, err := applyJitterToMonitorCreationDate(monitor, agentHash)
					if err != nil {
						t.Fatal(err)
					}

					probesRuns = append(probesRuns, got)
				}

				for i, firstTime := range probesRuns {
					for j, secondTime := range probesRuns[i+1:] {
						delta := secondTime.Sub(firstTime)
						if delta < 0 {
							delta = -delta
						}

						if delta < tt.wantMinDelta {
							t.Errorf("applyJitterToMonitorCreationDate() delta = %s, want >= %s (i=%d j=%d)", delta, tt.wantMinDelta, i, j+i+1)

							good = false
						}
					}
				}

				if !good {
					break
				}
			}
		})
	}
}

func Test_applyJitterToMonitorCreationDate_different(t *testing.T) {
	// For one probe, not all monitor run at the same time
	monitor1 := bleemeoTypes.Monitor{
		CreationDate: time.Date(2020, 1, 1, 12, 54, 7, 4, time.UTC).Format(time.RFC3339),
	}
	monitor2 := bleemeoTypes.Monitor{
		CreationDate: time.Date(2020, 1, 1, 12, 54, 30, 0, time.UTC).Format(time.RFC3339),
	}
	agentID := "0943c46b-c4e3-4234-b99d-a6548296056e"

	got1, err := applyJitterToMonitorCreationDate(monitor1, hashAgentID(agentID))
	if err != nil {
		t.Fatal(err)
	}

	got2, err := applyJitterToMonitorCreationDate(monitor2, hashAgentID(agentID))
	if err != nil {
		t.Fatal(err)
	}

	delta := got1.Sub(got2)
	if delta < 0 {
		delta = -delta
	}

	// 20s should be reached, the two monitor create date are 23s apart.
	if delta < 20*time.Second {
		t.Fatalf("applyJitterToMonitorCreationDate() delta = %s, want >= 20s", delta)
	}
}

func Test_applyJitterToMonitorCreationDate(t *testing.T) {
	for range 10 {
		createDate := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(rand.Int63n(int64(365 * 24 * time.Hour)))) //nolint:gosec
		monitor := bleemeoTypes.Monitor{
			CreationDate: createDate.Format(time.RFC3339),
		}

		const runCount = 5000

		maxCreateDate := createDate
		minCreateDate := createDate
		countPerTimestamp := make(map[int64]int, runCount)

		wantDelta := 16 * time.Second

		// Due to jitter being randomized, having delta being the full theoretical range would require too
		// much tries. Let's just check that delta is not way too small. This seems
		// enough to avoid flaky test.
		wantDelta -= time.Second

		for range runCount {
			got, err := applyJitterToMonitorCreationDate(monitor, uint64(rand.Int63())) //nolint:gosec
			if err != nil {
				t.Fatal(err)
			}

			// The jitterCreateDate must be within the same minute as original createDate.
			// We want to kept the minute unchanged. This is required for monitor with
			// resolution of 5 minutes because for the quorum Bleemeo assume that the monitor metrics are
			// send at the beginning of the minute after creationDate + N * 5 minutes.
			createDateMinute := createDate.Truncate(time.Minute)
			gotMinute := got.Truncate(time.Minute)

			if !createDateMinute.Equal(gotMinute) {
				t.Fatalf("applyJitterToMonitorCreationDate().Truncate(Minute) = %s, want %s", gotMinute, createDateMinute)
			}

			// The jitterCreateDate must be within the first 45 seconds of the minutes
			// The API task to compute quorum of probes starts at the beginning of every minute,
			// if we run the probe too late in the minute (e.g. 8h20m55s), the new points may
			// not be received by the API on the next quorum (e.g. 8h21m00s). This means the API
			// could use points from the last run (e.g. 8h15m55s), which are more than 5 minutes old.
			// To avoid this problem, we don't run the probes on the last 15 seconds of every minute.
			if got.Second() > 45 {
				t.Fatalf("applyJitterToMonitorCreationDate().Second() = %d, want <= 45", got.Second())
			}

			if got.After(maxCreateDate) {
				maxCreateDate = got
			}

			if got.Before(minCreateDate) {
				minCreateDate = got
			}

			countPerTimestamp[got.Unix()]++
		}

		gotDelta := maxCreateDate.Sub(minCreateDate)

		// applyJitterToMonitorCreationDate should spread creationDate enough
		if gotDelta < wantDelta {
			t.Fatalf("applyJitterToMonitorCreationDate() delta = %s, want >= %s", gotDelta, wantDelta)
		}

		var (
			maxPerBucket int
			minPerBucket int
		)

		for _, count := range countPerTimestamp {
			if maxPerBucket < count {
				maxPerBucket = count
			}

			if minPerBucket == 0 || minPerBucket > count {
				minPerBucket = count
			}
		}

		// applyJitterToMonitorCreationDate should have uniform distribution
		if maxPerBucket-minPerBucket > runCount/10 {
			t.Fatalf("maxPerBucket - minPerBucket = %d, want <= %d\ncountPerTimestamp: %v", maxPerBucket-minPerBucket, runCount/10, countPerTimestamp)
		}
	}
}

func Test_applyJitterToMonitorCreationDateFixedValue(t *testing.T) {
	createDate := time.Date(2024, 4, 30, 16, 32, 47, 123456, time.UTC)
	monitor := bleemeoTypes.Monitor{
		CreationDate: createDate.Format(time.RFC3339),
	}

	got, err := applyJitterToMonitorCreationDate(monitor, 42)
	if err != nil {
		t.Fatal(err)
	}

	// What this test check is that wanted value don't change too often, especially between
	// different run and when time.Now() changes.
	// If applyJitterToMonitorCreationDate need to be updated, the wanted value could change.
	want := time.Date(2024, 4, 30, 16, 32, 32, 42000000, time.UTC)

	if !got.Equal(want) {
		t.Errorf("applyJitterToMonitorCreationDate() = %s, want %s", got, want)
	}
}

// TestSyncMonitorHeadRequest ensures head_request is given to the monitor manager, both on a full
// synchronization and on a monitor update notification.
func TestSyncMonitorHeadRequest(t *testing.T) {
	newTestMonitor := func(id string, serviceID string, headRequest bool) bleemeoTypes.Monitor {
		return bleemeoTypes.Monitor{
			ID:           id,
			ServiceID:    serviceID,
			Account:      accountID,
			CreationDate: "2020-01-03T04:05:06Z",
			URL:          "https://example.com/" + id,
			HeadRequest:  headRequest,
		}
	}

	monitorHeadRequest := newTestMonitor("2b0ab0a5-9b6c-4a1a-8a3c-5c8b0d6f1e01", "7d1c2b3a-4e5f-4a6b-8c7d-9e0f1a2b3c01", true)
	monitorGet := newTestMonitor("2b0ab0a5-9b6c-4a1a-8a3c-5c8b0d6f1e02", "7d1c2b3a-4e5f-4a6b-8c7d-9e0f1a2b3c02", false)

	helper := newHelper(t)
	defer helper.Close()

	helper.preregisterAgent(t)
	helper.wrapperClientMock.resources.monitors.add(monitorHeadRequest, monitorGet)
	helper.initSynchronizer(t)

	checkHeadRequest := func(t *testing.T, wantHeadRequest map[string]bool) {
		t.Helper()

		gotMonitors := helper.monitorManager.Monitors()
		if len(gotMonitors) != len(wantHeadRequest) {
			t.Fatalf("got %d monitors, want %d: %+v", len(gotMonitors), len(wantHeadRequest), gotMonitors)
		}

		for _, monitor := range gotMonitors {
			want, ok := wantHeadRequest[monitor.ID]
			if !ok {
				t.Errorf("unexpected monitor %s", monitor.ID)

				continue
			}

			if monitor.HeadRequest != want {
				t.Errorf("monitor %s: HeadRequest = %v, want %v", monitor.URL, monitor.HeadRequest, want)
			}
		}
	}

	if err := helper.runOnce(t); err != nil {
		t.Fatal(err)
	}

	// The monitor manager identifies the monitors by their Service ID.
	checkHeadRequest(t, map[string]bool{monitorHeadRequest.ServiceID: true, monitorGet.ServiceID: false})

	// Swap head_request on the API, and notify only the change of the second monitor.
	for i, m := range helper.wrapperClientMock.resources.monitors.elems {
		helper.wrapperClientMock.resources.monitors.elems[i].HeadRequest = !m.HeadRequest
	}

	helper.s.UpdateMonitor("change", monitorGet.ID)
	helper.AddTime(time.Minute)

	if err := helper.runOnce(t); err != nil {
		t.Fatal(err)
	}

	// The first monitor wasn't notified, so it keeps the value from the previous synchronization.
	checkHeadRequest(t, map[string]bool{monitorHeadRequest.ServiceID: true, monitorGet.ServiceID: true})
}

// TestSyncMonitorWithoutService ensures a monitor without Service isn't given to the monitor manager,
// as its metrics couldn't be attached to it.
func TestSyncMonitorWithoutService(t *testing.T) {
	monitorWithService := bleemeoTypes.Monitor{
		ID:           "2b0ab0a5-9b6c-4a1a-8a3c-5c8b0d6f1e01",
		ServiceID:    "7d1c2b3a-4e5f-4a6b-8c7d-9e0f1a2b3c01",
		Account:      accountID,
		CreationDate: "2020-01-03T04:05:06Z",
		URL:          "https://example.com/with-service",
	}
	monitorWithoutService := bleemeoTypes.Monitor{
		ID:           "2b0ab0a5-9b6c-4a1a-8a3c-5c8b0d6f1e02",
		Account:      accountID,
		CreationDate: "2020-01-03T04:05:06Z",
		URL:          "https://example.com/without-service",
	}

	helper := newHelper(t)
	defer helper.Close()

	helper.preregisterAgent(t)
	helper.wrapperClientMock.resources.monitors.add(monitorWithService, monitorWithoutService)
	helper.initSynchronizer(t)

	if err := helper.runOnce(t); err != nil {
		t.Fatal(err)
	}

	gotMonitors := helper.monitorManager.Monitors()
	if len(gotMonitors) != 1 {
		t.Fatalf("got %d monitors, want 1: %+v", len(gotMonitors), gotMonitors)
	}

	if gotMonitors[0].ID != monitorWithService.ServiceID || gotMonitors[0].URL != monitorWithService.URL {
		t.Errorf("got monitor ID=%s URL=%s, want ID=%s URL=%s", gotMonitors[0].ID, gotMonitors[0].URL, monitorWithService.ServiceID, monitorWithService.URL)
	}
}

// TestUpdateMonitorWithoutID ensures a monitor notification without monitor ID reloads all the monitors.
func TestUpdateMonitorWithoutID(t *testing.T) {
	monitor := bleemeoTypes.Monitor{
		ID:           "2b0ab0a5-9b6c-4a1a-8a3c-5c8b0d6f1e01",
		ServiceID:    "7d1c2b3a-4e5f-4a6b-8c7d-9e0f1a2b3c01",
		Account:      accountID,
		CreationDate: "2020-01-03T04:05:06Z",
		URL:          "https://example.com/",
	}

	helper := newHelper(t)
	defer helper.Close()

	helper.preregisterAgent(t)
	helper.initSynchronizer(t)

	if err := helper.runOnce(t); err != nil {
		t.Fatal(err)
	}

	if got := helper.monitorManager.Monitors(); len(got) != 0 {
		t.Fatalf("got %d monitors, want 0", len(got))
	}

	helper.wrapperClientMock.resources.monitors.add(monitor)
	helper.s.UpdateMonitor("change", "")
	helper.AddTime(time.Minute)

	if err := helper.runOnce(t); err != nil {
		t.Fatal(err)
	}

	if got := helper.monitorManager.Monitors(); len(got) != 1 || got[0].ID != monitor.ServiceID {
		t.Fatalf("got monitors %+v, want the monitor with ID %s", got, monitor.ServiceID)
	}
}

// TestMonitorFieldsMatchTypes ensures all the fields of the monitors are requested.
// The API mock doesn't use the fields, so a missing field would only be noticed on a real API.
func TestMonitorFieldsMatchTypes(t *testing.T) {
	t.Parallel()

	fields := strings.Split(monitorFields, ",")

	var checkType func(typ reflect.Type)

	checkType = func(typ reflect.Type) {
		for field := range typ.Fields() {
			if field.Anonymous {
				checkType(field.Type)

				continue
			}

			name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
			if !slices.Contains(fields, name) {
				t.Errorf("fields %q don't contain %q (field %s)", monitorFields, name, field.Name)
			}
		}
	}

	checkType(reflect.TypeFor[bleemeoTypes.Monitor]())
}

// TestWrapperClientMonitors ensures the monitors are read from /v1/monitor/ and decoded from its fields.
func TestWrapperClientMonitors(t *testing.T) {
	t.Parallel()

	const monitorJSON = `{
		"id": "3b2e1f3a-8c0d-4f6e-9a57-1d2c3b4a5e6f",
		"account": "9da59f53-1d90-4441-ae58-42c661cc1b9f",
		"agent": "6b0ba586-0111-4a72-9cc7-f19d4b6558ed",
		"created_at": "2020-01-03T04:05:06Z",
		"service": "fdd9d999-e2ff-45d3-af2b-6519cf8e3e70",
		"url": "https://example.com",
		"expected_content": "welcome",
		"expected_response_code": 200,
		"unexpected_content": "maintenance",
		"ca_file": "/etc/ssl/ca.pem",
		"headers": {"X-Test": "value"},
		"head_request": true
	}`

	want := bleemeoTypes.Monitor{
		ID:                   "3b2e1f3a-8c0d-4f6e-9a57-1d2c3b4a5e6f",
		Account:              "9da59f53-1d90-4441-ae58-42c661cc1b9f",
		AgentID:              "6b0ba586-0111-4a72-9cc7-f19d4b6558ed",
		ServiceID:            "fdd9d999-e2ff-45d3-af2b-6519cf8e3e70",
		URL:                  "https://example.com",
		CreationDate:         "2020-01-03T04:05:06Z",
		ExpectedContent:      "welcome",
		ExpectedResponseCode: 200,
		ForbiddenContent:     "maintenance",
		CAFile:               "/etc/ssl/ca.pem",
		Headers:              map[string]string{"X-Test": "value"},
		HeadRequest:          true,
	}

	var (
		l           sync.Mutex
		listQueries []url.Values
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/o/token/":
			fmt.Fprint(w, `{"access_token": "token", "token_type": "Bearer", "expires_in": 3600, "refresh_token": "refresh"}`)
		case "/v1/monitor/":
			l.Lock()

			listQueries = append(listQueries, r.URL.Query())

			l.Unlock()

			fmt.Fprintf(w, `{"next": null, "results": [%s]}`, monitorJSON)
		case "/v1/monitor/" + want.ID + "/":
			if got := r.URL.Query().Get(paramFields); got != monitorFields {
				t.Errorf("GET %s: fields = %q, want %q", r.URL.Path, got, monitorFields)
			}

			fmt.Fprint(w, monitorJSON)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client, err := bleemeo.NewClient(
		bleemeo.WithCredentials("user", "password"),
		bleemeo.WithEndpoint(srv.URL),
	)
	if err != nil {
		t.Fatal(err)
	}

	cl := &wrapperClient{client: client}

	monitors, err := cl.ListMonitors(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff([]bleemeoTypes.Monitor{want}, monitors); diff != "" {
		t.Errorf("ListMonitors() mismatch (-want +got)\n%s", diff)
	}

	l.Lock()
	if len(listQueries) != 1 {
		t.Fatalf("got %d list requests, want 1", len(listQueries))
	}

	if got := listQueries[0].Get(paramActive); got != queryTrue {
		t.Errorf("list request: active = %q, want %q", got, queryTrue)
	}

	if got := listQueries[0].Get(paramFields); got != monitorFields {
		t.Errorf("list request: fields = %q, want %q", got, monitorFields)
	}

	if listQueries[0].Has("monitor") {
		t.Errorf("list request has the deprecated monitor parameter: %v", listQueries[0])
	}
	l.Unlock()

	monitor, err := cl.GetMonitorByID(t.Context(), want.ID)
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(want, monitor); diff != "" {
		t.Errorf("GetMonitorByID() mismatch (-want +got)\n%s", diff)
	}
}
