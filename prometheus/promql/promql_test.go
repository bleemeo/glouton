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

package promql

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bleemeo/glouton/store"
	"github.com/bleemeo/glouton/types"

	"github.com/go-chi/chi/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/storage"
	"github.com/prometheus/prometheus/util/annotations"
)

const (
	testCPUUsed   = "cpu_used"
	testDiskUsed  = "disk_used"
	testInstance  = "host1"
	testItemHome  = "/home"
	testItemSrv   = "/srv"
	testLabelItem = "item"
	testLabelInst = "instance"
)

// testTime is the reference timestamp of the test fixtures. It is a whole
// number of seconds, so the JSON encoding of the timestamps has no fractional
// part and the expected envelopes stay readable.
var testTime = time.Unix(1700000000, 0) //nolint:gochecknoglobals

// newTestStore returns an in-memory store holding three series, all with a
// single point at the given time:
//
//	cpu_used{instance="host1"}               42
//	disk_used{instance="host1",item="/home"} 10
//	disk_used{instance="host1",item="/srv"}  20
func newTestStore(t *testing.T, ts time.Time) *store.Store {
	t.Helper()

	st := store.New("test", time.Hour, time.Hour)

	st.PushPoints(context.Background(), []types.MetricPoint{
		{
			Time: ts, Value: 42,
			Labels: map[string]string{types.LabelName: testCPUUsed, testLabelInst: testInstance},
		},
		{
			Time: ts, Value: 10,
			Labels: map[string]string{types.LabelName: testDiskUsed, testLabelInst: testInstance, testLabelItem: testItemHome},
		},
		{
			Time: ts, Value: 20,
			Labels: map[string]string{types.LabelName: testDiskUsed, testLabelInst: testInstance, testLabelItem: testItemSrv},
		},
	})

	return st
}

// newTestHandler returns the API mounted exactly like the local API does, so the
// tests exercise the routing and the /api/v1 prefix too.
func newTestHandler(st storage.Queryable) http.Handler {
	promQL := PromQL{}

	router := chi.NewRouter()
	router.Mount("/api/v1", promQL.Register(st))

	return router
}

// doGet runs a GET against the API and returns the status code and the raw body.
func doGet(t *testing.T, handler http.Handler, target string) (int, string) {
	t.Helper()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil))

	return recorder.Code, strings.TrimSpace(recorder.Body.String())
}

// doPostForm runs a POST with a form-encoded body, the other calling convention
// Prometheus clients use for long queries.
func doPostForm(t *testing.T, handler http.Handler, target string, form url.Values) (int, string) {
	t.Helper()

	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, target, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	return recorder.Code, strings.TrimSpace(recorder.Body.String())
}

// TestQueryEnvelope checks the full JSON envelope of an instant query against
// one known series. The whole point of the endpoint is to be
// byte-for-byte compatible with a real Prometheus server, so this compares the
// raw body rather than a decoded structure.
func TestQueryEnvelope(t *testing.T) {
	handler := newTestHandler(newTestStore(t, testTime))

	target := fmt.Sprintf("/api/v1/query?query=cpu_used&time=%d", testTime.Unix())
	want := `{"status":"success","data":{"resultType":"vector","result":` +
		`[{"metric":{"__name__":"cpu_used","instance":"host1"},"value":[1700000000,"42"]}]}}`

	code, body := doGet(t, handler, target)
	if code != http.StatusOK {
		t.Fatalf("status code = %d, want %d (body: %s)", code, http.StatusOK, body)
	}

	if body != want {
		t.Errorf("body =\n%s\nwant\n%s", body, want)
	}
}

// TestQueryRangeEnvelope is the matrix counterpart, kept as a guard that adding
// the instant endpoint didn't change the range one.
func TestQueryRangeEnvelope(t *testing.T) {
	handler := newTestHandler(newTestStore(t, testTime))

	target := fmt.Sprintf(
		"/api/v1/query_range?query=cpu_used&start=%d&end=%d&step=10",
		testTime.Unix(), testTime.Unix(),
	)
	want := `{"status":"success","data":{"resultType":"matrix","result":` +
		`[{"metric":{"__name__":"cpu_used","instance":"host1"},"values":[[1700000000,"42"]]}]}}`

	code, body := doGet(t, handler, target)
	if code != http.StatusOK {
		t.Fatalf("status code = %d, want %d (body: %s)", code, http.StatusOK, body)
	}

	if body != want {
		t.Errorf("body =\n%s\nwant\n%s", body, want)
	}
}

// apiEnvelope is the decoded response, used by the tests that only care about a
// part of it.
type apiEnvelope struct {
	Status    string          `json:"status"`
	Data      json.RawMessage `json:"data"`
	ErrorType string          `json:"errorType"`
	Error     string          `json:"error"`
	Warnings  []string        `json:"warnings"`
}

func decodeEnvelope(t *testing.T, body string) apiEnvelope {
	t.Helper()

	var envelope apiEnvelope

	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("can't decode response %q: %v", body, err)
	}

	return envelope
}

func TestQuery(t *testing.T) {
	handler := newTestHandler(newTestStore(t, testTime))

	cases := []struct {
		name          string
		target        string
		wantCode      int
		wantStatus    string
		wantErrorType string
		// wantResultType is only checked on success.
		wantResultType string
		wantSeries     int
	}{
		{
			name:           "success",
			target:         fmt.Sprintf("/api/v1/query?query=disk_used&time=%d", testTime.Unix()),
			wantCode:       http.StatusOK,
			wantStatus:     "success",
			wantResultType: "vector",
			wantSeries:     2,
		},
		{
			name:           "aggregation",
			target:         fmt.Sprintf("/api/v1/query?query=sum(disk_used)&time=%d", testTime.Unix()),
			wantCode:       http.StatusOK,
			wantStatus:     "success",
			wantResultType: "vector",
			wantSeries:     1,
		},
		{
			name:           "scalar expression",
			target:         "/api/v1/query?query=1%2B1",
			wantCode:       http.StatusOK,
			wantStatus:     "success",
			wantResultType: "scalar",
		},
		{
			name:           "rfc3339 time",
			target:         "/api/v1/query?query=cpu_used&time=2023-11-14T22%3A13%3A20Z",
			wantCode:       http.StatusOK,
			wantStatus:     "success",
			wantResultType: "vector",
			wantSeries:     1,
		},
		{
			name:           "no match is an empty vector, not an error",
			target:         "/api/v1/query?query=does_not_exist",
			wantCode:       http.StatusOK,
			wantStatus:     "success",
			wantResultType: "vector",
			wantSeries:     0,
		},
		{
			name:          "bad query",
			target:        "/api/v1/query?query=this%28is%28not%28promql",
			wantCode:      http.StatusBadRequest,
			wantStatus:    "error",
			wantErrorType: "bad_data",
		},
		{
			name:          "empty query",
			target:        "/api/v1/query?query=",
			wantCode:      http.StatusBadRequest,
			wantStatus:    "error",
			wantErrorType: "bad_data",
		},
		{
			name:          "bad time",
			target:        "/api/v1/query?query=cpu_used&time=not-a-time",
			wantCode:      http.StatusBadRequest,
			wantStatus:    "error",
			wantErrorType: "bad_data",
		},
		{
			name:          "bad timeout",
			target:        "/api/v1/query?query=cpu_used&timeout=not-a-duration",
			wantCode:      http.StatusBadRequest,
			wantStatus:    "error",
			wantErrorType: "bad_data",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := doGet(t, handler, tc.target)
			if code != tc.wantCode {
				t.Fatalf("status code = %d, want %d (body: %s)", code, tc.wantCode, body)
			}

			envelope := decodeEnvelope(t, body)
			if envelope.Status != tc.wantStatus {
				t.Errorf("status = %q, want %q", envelope.Status, tc.wantStatus)
			}

			if envelope.ErrorType != tc.wantErrorType {
				t.Errorf("errorType = %q, want %q", envelope.ErrorType, tc.wantErrorType)
			}

			if tc.wantStatus != "success" {
				if envelope.Error == "" {
					t.Error("error message is empty")
				}

				return
			}

			var data struct {
				ResultType string            `json:"resultType"`
				Result     []json.RawMessage `json:"result"`
			}

			if err := json.Unmarshal(envelope.Data, &data); err != nil {
				t.Fatalf("can't decode data %q: %v", envelope.Data, err)
			}

			if data.ResultType != tc.wantResultType {
				t.Errorf("resultType = %q, want %q", data.ResultType, tc.wantResultType)
			}

			if tc.wantResultType == "vector" && len(data.Result) != tc.wantSeries {
				t.Errorf("len(result) = %d, want %d", len(data.Result), tc.wantSeries)
			}
		})
	}
}

// TestQueryTimeDefaultsToNow checks that omitting "time" evaluates at the
// current time — which is what the local panel relies on, since it sends no
// "time" parameter at all.
func TestQueryTimeDefaultsToNow(t *testing.T) {
	handler := newTestHandler(newTestStore(t, time.Now()))

	code, body := doGet(t, handler, "/api/v1/query?query=cpu_used")
	if code != http.StatusOK {
		t.Fatalf("status code = %d, want %d (body: %s)", code, http.StatusOK, body)
	}

	var data struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Value [2]json.RawMessage `json:"value"`
		} `json:"result"`
	}

	if err := json.Unmarshal(decodeEnvelope(t, body).Data, &data); err != nil {
		t.Fatalf("can't decode data: %v", err)
	}

	if data.ResultType != "vector" {
		t.Fatalf("resultType = %q, want %q", data.ResultType, "vector")
	}

	if len(data.Result) != 1 {
		t.Fatalf("len(result) = %d, want 1 — an instant query without 'time' must evaluate at now", len(data.Result))
	}

	if got := string(data.Result[0].Value[1]); got != `"42"` {
		t.Errorf("value = %s, want %q", got, "42")
	}
}

// TestQueryPost checks the POST calling convention, which Grafana uses for long
// queries.
func TestQueryPost(t *testing.T) {
	handler := newTestHandler(newTestStore(t, testTime))

	form := url.Values{
		"query": []string{"cpu_used"},
		"time":  []string{strconv.FormatInt(testTime.Unix(), 10)},
	}

	code, body := doPostForm(t, handler, "/api/v1/query", form)
	if code != http.StatusOK {
		t.Fatalf("status code = %d, want %d (body: %s)", code, http.StatusOK, body)
	}

	want := `{"status":"success","data":{"resultType":"vector","result":` +
		`[{"metric":{"__name__":"cpu_used","instance":"host1"},"value":[1700000000,"42"]}]}}`

	if body != want {
		t.Errorf("body =\n%s\nwant\n%s", body, want)
	}
}

// blockingQueryable is a storage.Queryable whose Select never returns until the
// query's context is done. It is used to check that the "timeout" parameter is
// actually applied to the query context instead of the engine's own two-minute
// default.
type blockingQueryable struct{}

func (blockingQueryable) Querier(int64, int64) (storage.Querier, error) {
	return blockingQuerier{}, nil
}

type blockingQuerier struct{}

func (blockingQuerier) Select(ctx context.Context, _ bool, _ *storage.SelectHints, _ ...*labels.Matcher) storage.SeriesSet {
	<-ctx.Done()

	return storage.ErrSeriesSet(ctx.Err())
}

func (blockingQuerier) LabelValues(
	context.Context, string, *storage.LabelHints, ...*labels.Matcher,
) ([]string, annotations.Annotations, error) {
	return nil, nil, nil
}

func (blockingQuerier) LabelNames(
	context.Context, *storage.LabelHints, ...*labels.Matcher,
) ([]string, annotations.Annotations, error) {
	return nil, nil, nil
}

func (blockingQuerier) Close() error {
	return nil
}

func TestQueryTimeoutExceeded(t *testing.T) {
	handler := newTestHandler(blockingQueryable{})

	start := time.Now()
	code, body := doGet(t, handler, "/api/v1/query?query=cpu_used&timeout=100ms")
	elapsed := time.Since(start)

	if code == http.StatusOK {
		t.Fatalf("status code = %d, want an error status (body: %s)", code, body)
	}

	// The engine's own timeout is two minutes; the request must have been cut
	// short by the "timeout" parameter instead.
	if elapsed > 30*time.Second {
		t.Errorf("request took %s, the 'timeout' parameter was not applied", elapsed)
	}

	envelope := decodeEnvelope(t, body)
	if envelope.Status != "error" {
		t.Errorf("status = %q, want %q", envelope.Status, "error")
	}

	if envelope.Error == "" {
		t.Error("error message is empty")
	}
}

func TestLabelNames(t *testing.T) {
	handler := newTestHandler(newTestStore(t, testTime))

	cases := []struct {
		name   string
		target string
		want   []string
	}{
		{
			name:   "all names",
			target: "/api/v1/labels",
			want:   []string{"__name__", "instance", "item"},
		},
		{
			name:   "restricted by matcher",
			target: "/api/v1/labels?match%5B%5D=cpu_used",
			want:   []string{"__name__", "instance"},
		},
		{
			name:   "several matcher sets are merged",
			target: "/api/v1/labels?match%5B%5D=cpu_used&match%5B%5D=disk_used",
			want:   []string{"__name__", "instance", "item"},
		},
		{
			name:   "matcher selecting nothing",
			target: "/api/v1/labels?match%5B%5D=does_not_exist",
			want:   []string{},
		},
		{
			name:   "time range excluding the points",
			target: "/api/v1/labels?start=1000&end=2000",
			want:   []string{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := doGet(t, handler, tc.target)
			if code != http.StatusOK {
				t.Fatalf("status code = %d, want %d (body: %s)", code, http.StatusOK, body)
			}

			var got []string

			if err := json.Unmarshal(decodeEnvelope(t, body).Data, &got); err != nil {
				t.Fatalf("can't decode data: %v", err)
			}

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("label names mismatch: (-want +got)\n%s", diff)
			}
		})
	}
}

// TestLabelNamesBadMatcher checks that a match[] that would select everything is
// rejected, as Prometheus does.
func TestLabelNamesBadMatcher(t *testing.T) {
	handler := newTestHandler(newTestStore(t, testTime))

	code, body := doGet(t, handler, `/api/v1/labels?match%5B%5D=%7Bfoo%3D%22%22%7D`)
	if code != http.StatusBadRequest {
		t.Fatalf("status code = %d, want %d (body: %s)", code, http.StatusBadRequest, body)
	}

	envelope := decodeEnvelope(t, body)
	if envelope.ErrorType != "bad_data" {
		t.Errorf("errorType = %q, want %q", envelope.ErrorType, "bad_data")
	}
}

func TestLabelValues(t *testing.T) {
	handler := newTestHandler(newTestStore(t, testTime))

	cases := []struct {
		name   string
		target string
		want   []string
	}{
		{
			name:   "metric names",
			target: "/api/v1/label/__name__/values",
			want:   []string{testCPUUsed, testDiskUsed},
		},
		{
			name:   "item values are sorted and deduplicated",
			target: "/api/v1/label/item/values",
			want:   []string{testItemHome, testItemSrv},
		},
		{
			name:   "restricted by matcher",
			target: "/api/v1/label/__name__/values?match%5B%5D=disk_used",
			want:   []string{testDiskUsed},
		},
		{
			name:   "several matcher sets are merged and sorted",
			target: "/api/v1/label/__name__/values?match%5B%5D=disk_used&match%5B%5D=cpu_used",
			want:   []string{testCPUUsed, testDiskUsed},
		},
		{
			name:   "unknown label name is not an error",
			target: "/api/v1/label/no_such_label/values",
			want:   []string{},
		},
		{
			name:   "label absent from the selected series",
			target: "/api/v1/label/item/values?match%5B%5D=cpu_used",
			want:   []string{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := doGet(t, handler, tc.target)
			if code != http.StatusOK {
				t.Fatalf("status code = %d, want %d (body: %s)", code, http.StatusOK, body)
			}

			var got []string

			if err := json.Unmarshal(decodeEnvelope(t, body).Data, &got); err != nil {
				t.Fatalf("can't decode data: %v", err)
			}

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("label values mismatch: (-want +got)\n%s", diff)
			}
		})
	}
}

func TestSeries(t *testing.T) {
	handler := newTestHandler(newTestStore(t, testTime))

	cases := []struct {
		name     string
		target   string
		wantCode int
		want     []map[string]string
	}{
		{
			name:     "one matcher",
			target:   "/api/v1/series?match%5B%5D=disk_used",
			wantCode: http.StatusOK,
			want: []map[string]string{
				{"__name__": testDiskUsed, testLabelInst: testInstance, testLabelItem: testItemHome},
				{"__name__": testDiskUsed, testLabelInst: testInstance, testLabelItem: testItemSrv},
			},
		},
		{
			name:     "several matchers are merged",
			target:   "/api/v1/series?match%5B%5D=cpu_used&match%5B%5D=disk_used%7Bitem%3D%22%2Fsrv%22%7D",
			wantCode: http.StatusOK,
			want: []map[string]string{
				{"__name__": testCPUUsed, testLabelInst: testInstance},
				{"__name__": testDiskUsed, testLabelInst: testInstance, testLabelItem: testItemSrv},
			},
		},
		{
			name:     "no match is an empty list",
			target:   "/api/v1/series?match%5B%5D=does_not_exist",
			wantCode: http.StatusOK,
			want:     []map[string]string{},
		},
		{
			name:     "missing match[] is an error",
			target:   "/api/v1/series",
			wantCode: http.StatusBadRequest,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := doGet(t, handler, tc.target)
			if code != tc.wantCode {
				t.Fatalf("status code = %d, want %d (body: %s)", code, tc.wantCode, body)
			}

			if tc.wantCode != http.StatusOK {
				return
			}

			var got []map[string]string

			if err := json.Unmarshal(decodeEnvelope(t, body).Data, &got); err != nil {
				t.Fatalf("can't decode data: %v", err)
			}

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("series mismatch: (-want +got)\n%s", diff)
			}
		})
	}
}

// TestRoutes checks that every documented route is mounted, and that the ones we
// deliberately don't serve stay absent.
func TestRoutes(t *testing.T) {
	handler := newTestHandler(newTestStore(t, testTime))

	cases := []struct {
		method   string
		target   string
		wantCode int
	}{
		{http.MethodGet, "/api/v1/query?query=cpu_used", http.StatusOK},
		{http.MethodPost, "/api/v1/query", http.StatusBadRequest}, // no query parameter
		{http.MethodGet, "/api/v1/query_range?query=cpu_used&start=0&end=10&step=10", http.StatusOK},
		{http.MethodGet, "/api/v1/labels", http.StatusOK},
		{http.MethodPost, "/api/v1/labels", http.StatusOK},
		{http.MethodGet, "/api/v1/label/__name__/values", http.StatusOK},
		{http.MethodGet, "/api/v1/series?match%5B%5D=cpu_used", http.StatusOK},
		{http.MethodPost, "/api/v1/series?match%5B%5D=cpu_used", http.StatusOK},
		{http.MethodGet, "/api/v1/rules", http.StatusNotFound},
		{http.MethodGet, "/api/v1/admin/tsdb/snapshot", http.StatusNotFound},
	}

	for _, tc := range cases {
		t.Run(tc.method+" "+tc.target, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequestWithContext(t.Context(), tc.method, tc.target, nil)

			if tc.method == http.MethodPost {
				request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			}

			handler.ServeHTTP(recorder, request)

			if recorder.Code != tc.wantCode {
				t.Errorf(
					"status code = %d, want %d (body: %s)",
					recorder.Code, tc.wantCode, strings.TrimSpace(recorder.Body.String()),
				)
			}
		})
	}
}
