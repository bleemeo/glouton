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
	"errors"
	"fmt"
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

func newTestMonitor(id string) bleemeoTypes.Monitor {
	return bleemeoTypes.Monitor{
		ID:           id,
		Account:      accountID,
		CreationDate: "2020-01-03T04:05:06Z",
		Active:       true,
		URL:          "https://example.com/" + id,
	}
}

// TestSyncMonitorHeadRequest ensures head_request is given to the monitor manager, both on a full
// synchronization and on a monitor update notification.
func TestSyncMonitorHeadRequest(t *testing.T) {
	monitorHeadRequest := newTestMonitor("2b0ab0a5-9b6c-4a1a-8a3c-5c8b0d6f1e01")
	monitorHeadRequest.HeadRequest = true
	monitorGet := newTestMonitor("2b0ab0a5-9b6c-4a1a-8a3c-5c8b0d6f1e02")

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

	// The monitor manager identifies the monitors by their ID.
	checkHeadRequest(t, map[string]bool{monitorHeadRequest.ID: true, monitorGet.ID: false})

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
	checkHeadRequest(t, map[string]bool{monitorHeadRequest.ID: true, monitorGet.ID: true})
}

// TestSyncMonitorWithoutService ensures monitors are probed under their own ID: they have no
// Service, and their metrics are only attached to their agent.
func TestSyncMonitorWithoutService(t *testing.T) {
	monitor := newTestMonitor("2b0ab0a5-9b6c-4a1a-8a3c-5c8b0d6f1e01")

	helper := newHelper(t)
	defer helper.Close()

	helper.preregisterAgent(t)
	helper.wrapperClientMock.resources.monitors.add(monitor)
	helper.initSynchronizer(t)

	if err := helper.runOnce(t); err != nil {
		t.Fatal(err)
	}

	gotMonitors := helper.monitorManager.Monitors()
	if len(gotMonitors) != 1 || gotMonitors[0].ID != monitor.ID || gotMonitors[0].URL != monitor.URL {
		t.Errorf("got monitors %+v, want the monitor with ID %s and URL %s", gotMonitors, monitor.ID, monitor.URL)
	}
}

// TestMonitorNotificationRemovesMonitor ensures a notified monitor stops being probed when it's deleted,
// no longer found or deactivated, matching it by its ID.
func TestMonitorNotificationRemovesMonitor(t *testing.T) {
	monitorRemoved := newTestMonitor("2b0ab0a5-9b6c-4a1a-8a3c-5c8b0d6f1e01")
	monitorKept := newTestMonitor("2b0ab0a5-9b6c-4a1a-8a3c-5c8b0d6f1e02")

	tests := []struct {
		name string
		op   string
		// updateAPI changes the removed monitor on the API before the notification.
		updateAPI func(helper *syncTestHelper)
	}{
		{
			name:      "delete",
			op:        "delete",
			updateAPI: func(helper *syncTestHelper) { helper.wrapperClientMock.resources.monitors.dropByID(monitorRemoved.ID) },
		},
		{
			name:      "not found",
			op:        "change",
			updateAPI: func(helper *syncTestHelper) { helper.wrapperClientMock.resources.monitors.dropByID(monitorRemoved.ID) },
		},
		{
			name: "deactivated",
			op:   "change",
			updateAPI: func(helper *syncTestHelper) {
				monitor := helper.wrapperClientMock.resources.monitors.findResource(func(m bleemeoTypes.Monitor) bool {
					return m.ID == monitorRemoved.ID
				})
				monitor.Active = false
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := newHelper(t)
			defer helper.Close()

			helper.preregisterAgent(t)
			helper.wrapperClientMock.resources.monitors.add(monitorRemoved, monitorKept)
			helper.initSynchronizer(t)

			if err := helper.runOnce(t); err != nil {
				t.Fatal(err)
			}

			if got := helper.monitorManager.Monitors(); len(got) != 2 {
				t.Fatalf("got %d monitors, want 2: %+v", len(got), got)
			}

			tt.updateAPI(helper)
			helper.s.UpdateMonitor(tt.op, monitorRemoved.ID)
			helper.AddTime(time.Minute)

			if err := helper.runOnce(t); err != nil {
				t.Fatal(err)
			}

			gotMonitors := helper.monitorManager.Monitors()
			if len(gotMonitors) != 1 || gotMonitors[0].ID != monitorKept.ID {
				t.Errorf("got monitors %+v, want only the monitor with ID %s", gotMonitors, monitorKept.ID)
			}

			if diff := cmp.Diff([]bleemeoTypes.Monitor{monitorKept}, helper.s.option.Cache.Monitors()); diff != "" {
				t.Errorf("cached monitors mismatch (-want +got)\n%s", diff)
			}
		})
	}
}

// TestSyncMonitorRetry ensures a monitor synchronization which failed is retried later, without
// losing the notified updates, and that a failed full synchronization is retried as a full one.
func TestSyncMonitorRetry(t *testing.T) {
	monitorNotified := newTestMonitor("2b0ab0a5-9b6c-4a1a-8a3c-5c8b0d6f1e01")
	monitorNotNotified := newTestMonitor("2b0ab0a5-9b6c-4a1a-8a3c-5c8b0d6f1e02")

	tests := []struct {
		name string
		// notify requests the synchronization which fails.
		notify func(helper *syncTestHelper)
		// want is the monitors probed after the retry, by ID.
		want []string
	}{
		{
			name:   "notification",
			notify: func(helper *syncTestHelper) { helper.s.UpdateMonitor("change", monitorNotified.ID) },
			want:   []string{monitorNotified.ID},
		},
		{
			name:   "full synchronization",
			notify: func(helper *syncTestHelper) { helper.s.UpdateMonitors() },
			want:   []string{monitorNotified.ID, monitorNotNotified.ID},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := newHelper(t)
			defer helper.Close()

			helper.preregisterAgent(t)
			helper.initSynchronizer(t)

			if err := helper.runOnce(t); err != nil {
				t.Fatal(err)
			}

			probedIDs := func() []string {
				ids := make([]string, 0, len(helper.monitorManager.Monitors()))
				for _, m := range helper.monitorManager.Monitors() {
					ids = append(ids, m.ID)
				}

				slices.Sort(ids)

				return ids
			}

			helper.wrapperClientMock.resources.monitors.add(monitorNotified, monitorNotNotified)
			helper.wrapperClientMock.monitorsErr = errClientError
			tt.notify(helper)
			helper.AddTime(time.Minute)

			if result := helper.runOnceWithResult(t); !errors.Is(result.err, errClientError) {
				t.Fatalf("runOnce error = %v, want %v", result.err, errClientError)
			}

			if ids := probedIDs(); len(ids) != 0 {
				t.Fatalf("monitors %v probed while the API failed", ids)
			}

			// Without a new request, the synchronization is retried once the retry delay has passed.
			helper.wrapperClientMock.monitorsErr = nil
			helper.AddTime(2 * time.Minute) // The monitors are retried after a minute.

			if err := helper.runOnce(t); err != nil {
				t.Fatal(err)
			}

			if diff := cmp.Diff(tt.want, probedIDs()); diff != "" {
				t.Errorf("probed monitors mismatch (-want +got)\n%s", diff)
			}
		})
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
		"active": true,
		"url": "https://example.com",
		"expected_content": "welcome",
		"expected_response_code": 200,
		"unexpected_content": "maintenance",
		"ca_file": "/etc/ssl/ca.pem",
		"headers": {"X-Test": "value"},
		"head_request": true
	}`

	// The API returns a null service when the monitor's agent has no Service in its account.
	const monitorMinimalJSON = `{
		"id": "5c4d3e2f-1a0b-4c9d-8e7f-6a5b4c3d2e1f",
		"account": "9da59f53-1d90-4441-ae58-42c661cc1b9f",
		"agent": "7a1b2c3d-4e5f-4a6b-9c8d-7e6f5a4b3c2d",
		"created_at": "2020-01-03T04:05:06Z",
		"active": true,
		"url": "https://minimal.example.com",
		"expected_content": "",
		"expected_response_code": null,
		"unexpected_content": "",
		"ca_file": "",
		"headers": {},
		"head_request": false
	}`

	wantMinimal := bleemeoTypes.Monitor{
		ID:           "5c4d3e2f-1a0b-4c9d-8e7f-6a5b4c3d2e1f",
		Account:      "9da59f53-1d90-4441-ae58-42c661cc1b9f",
		AgentID:      "7a1b2c3d-4e5f-4a6b-9c8d-7e6f5a4b3c2d",
		URL:          "https://minimal.example.com",
		CreationDate: "2020-01-03T04:05:06Z",
		Active:       true,
		Headers:      map[string]string{},
	}

	want := bleemeoTypes.Monitor{
		ID:                   "3b2e1f3a-8c0d-4f6e-9a57-1d2c3b4a5e6f",
		Account:              "9da59f53-1d90-4441-ae58-42c661cc1b9f",
		AgentID:              "6b0ba586-0111-4a72-9cc7-f19d4b6558ed",
		URL:                  "https://example.com",
		CreationDate:         "2020-01-03T04:05:06Z",
		Active:               true,
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

			fmt.Fprintf(w, `{"next": null, "results": [%s, %s]}`, monitorJSON, monitorMinimalJSON)
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

	if diff := cmp.Diff([]bleemeoTypes.Monitor{want, wantMinimal}, monitors); diff != "" {
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
