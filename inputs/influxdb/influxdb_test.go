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

package influxdb

import (
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bleemeo/glouton/inputs/internal"

	"github.com/google/go-cmp/cmp"
)

// fixedNow is the instant every gather in this file happens at, so an uptime is a value
// that can be asserted rather than one that moves.
//
//nolint:gochecknoglobals
var fixedNow = time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

// serveLine answers what a server of one line really answers: the recorded /debug/vars of
// a 1.12.4, or the recorded /metrics of a 2.9.1 or a 3.11.4 Core, plus the version header
// each of them sets on /ping. What the mapping is checked against is therefore what a
// server says, not what it ought to say.
func serveLine(t *testing.T, l line) *httptest.Server {
	t.Helper()

	var (
		fixture string
		header  string
		status  int
	)

	switch l {
	case lineV1:
		fixture, header, status = "testdata/influxdb1-debug-vars.json", "1.12.4", http.StatusNoContent
	case lineV2:
		fixture, header, status = "testdata/influxdb2-metrics.txt", "v2.9.1", http.StatusNoContent
	case lineV3:
		fixture, header, status = "testdata/influxdb3-metrics.txt", "3.11.4", http.StatusOK
	case lineUnknown:
		t.Fatal("no fixture for an unknown line")
	}

	body, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ping" {
			w.Header().Set(versionHeader, header)
			w.WriteHeader(status)

			return
		}

		_, _ = w.Write(body)
	}))

	t.Cleanup(server.Close)

	return server
}

// gatherOnce runs the detection and one gather, without the accumulator that wraps them,
// so the values asserted are the ones read rather than rates needing a second sample.
func gatherOnce(t *testing.T, url string) *internal.StoreAccumulator {
	t.Helper()

	store := &internal.StoreAccumulator{}

	input, _, err := New(url, "", "", "")
	if err != nil {
		t.Fatalf("New() = %v", err)
	}

	inner, ok := input.(*internal.Input)
	if !ok {
		t.Fatalf("New() returned %T, want *internal.Input", input)
	}

	raw, ok := inner.Input.(*metricsInput)
	if !ok {
		t.Fatalf("inner input is %T, want *metricsInput", inner.Input)
	}

	raw.now = func() time.Time { return fixedNow }

	if err := raw.Gather(store); err != nil {
		t.Fatalf("Gather() = %v", err)
	}

	return store
}

// fieldsOf collects the fields one gather produced, with the labels kept on each.
func fieldsOf(t *testing.T, store *internal.StoreAccumulator) map[string][]string {
	t.Helper()

	got := make(map[string][]string)

	for _, m := range store.Measurement {
		if m.Name != measurement {
			t.Errorf("measurement = %q, want %q", m.Name, measurement)
		}

		labels := make([]string, 0, len(m.Tags))
		for name := range m.Tags {
			labels = append(labels, name)
		}

		sort.Strings(labels)

		for field := range m.Fields {
			got[field] = labels
		}
	}

	return got
}

// TestFieldsPerLine pins what each line publishes. It is the table in the package comment,
// asserted: the shared core everywhere, the write half on 1.x and 2.x, the query half on
// 1.x and 3.x, and each line's own extras.
//
// It fails when a source family is renamed upstream, which would otherwise show up as a
// metric quietly ceasing to exist.
func TestFieldsPerLine(t *testing.T) {
	core := map[string][]string{
		fieldRequests:           {},
		fieldClientErrors:       {},
		fieldServerErrors:       {},
		fieldRequestDurationSum: {},
		fieldRequestCount:       {},
	}

	write := map[string][]string{
		fieldPointsWritten:      {},
		fieldPointsWriteFailed:  {},
		fieldPointsWriteDropped: {},
		fieldWriteTimeouts:      {},
	}

	query := map[string][]string{
		fieldQueries:          {},
		fieldQueryDurationSum: {},
		fieldQueryCount:       {},
	}

	cases := []struct {
		testName string
		line     line
		want     map[string][]string
	}{
		{
			testName: "1.x publishes the core, the write half, the query half and its own extras",
			line:     lineV1,
			want: merge(core, write, query, map[string][]string{
				// Only 1.x reports these: cardinality has no equivalent in either newer
				// line, and neither has an authentication-failure counter.
				fieldSeries:        {"database"},
				fieldAuthFailures:  {},
				fieldQueriesActive: {},
			}),
		},
		{
			testName: "2.x publishes the core and the write half, and no query metric at all",
			line:     lineV2,
			want:     merge(core, write, map[string][]string{fieldUptime: {}}),
		},
		{
			testName: "3.x publishes the core, the query half and the engine's own",
			line:     lineV3,
			want: merge(core, query, map[string][]string{
				fieldUptime:              {},
				fieldQueriesFailed:       {},
				fieldQueryOOMs:           {},
				fieldParquetCacheSize:    {},
				fieldParquetCacheFiles:   {},
				fieldParquetCacheAccess:  {"status"},
				fieldObjectStoreTransfer: {"result"},
				fieldMemPool:             {"state"},
				fieldMemory:              {"stat"},
				fieldThreadPanics:        {"type"},
			}),
		},
	}

	for _, c := range cases {
		t.Run(c.testName, func(t *testing.T) {
			server := serveLine(t, c.line)

			if diff := cmp.Diff(c.want, fieldsOf(t, gatherOnce(t, server.URL))); diff != "" {
				t.Errorf("fields (-want +got):\n%s", diff)
			}
		})
	}
}

func merge(sets ...map[string][]string) map[string][]string {
	out := map[string][]string{}

	for _, set := range sets {
		maps.Copy(out, set)
	}

	return out
}

// TestSharedCoreValues checks the core really is the same number read three ways. Each
// line reports its request count somewhere different, and the fixtures were taken from
// servers that had served a different number of requests, so what is asserted is that
// each value matches its own fixture rather than that the three agree.
func TestSharedCoreValues(t *testing.T) {
	cases := []struct {
		testName string
		line     line
		want     float64
	}{
		// httpd.req of the recorded /debug/vars.
		{testName: "1.x reads httpd.req", line: lineV1, want: 12},
		// The sum of http_api_requests_total over every label.
		{testName: "2.x sums http_api_requests_total", line: lineV2, want: sumPromFixture(t, "testdata/influxdb2-metrics.txt", "http_api_requests_total")},
		// The sum of http_requests_total over every label.
		{testName: "3.x sums http_requests_total", line: lineV3, want: sumPromFixture(t, "testdata/influxdb3-metrics.txt", "http_requests_total")},
	}

	for _, c := range cases {
		t.Run(c.testName, func(t *testing.T) {
			server := serveLine(t, c.line)
			store := gatherOnce(t, server.URL)

			var got float64

			for _, m := range store.Measurement {
				if v, ok := m.Fields[fieldRequests]; ok {
					got, _ = v.(float64)
				}
			}

			if got != c.want {
				t.Errorf("%s = %v, want %v", fieldRequests, got, c.want)
			}
		})
	}
}

// sumPromFixture adds up a family's lines in a fixture, so the expected value is read
// from the recording rather than copied into the test.
func sumPromFixture(t *testing.T, path, family string) float64 {
	t.Helper()

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var total float64

	for l := range strings.SplitSeq(string(body), "\n") {
		if !strings.HasPrefix(l, family+"{") && !strings.HasPrefix(l, family+" ") {
			continue
		}

		// The value is the last field, not the second: a label value can hold a space, as
		// 3.x's method_path="GET /health" does.
		fields := strings.Fields(l)
		if len(fields) < 2 {
			continue
		}

		value, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err != nil {
			t.Fatalf("unreadable fixture line %q: %v", l, err)
		}

		total += value
	}

	if total == 0 {
		t.Fatalf("no %s line in %s, the test proves nothing", family, path)
	}

	return total
}

// TestDurationsAreSeconds checks the unit is normalised at the source. 1.x counts
// nanoseconds where 2.x and 3.x count seconds, so without that the average would be a
// billion times too large for a 1.x server.
func TestDurationsAreSeconds(t *testing.T) {
	server := serveLine(t, lineV1)
	store := gatherOnce(t, server.URL)

	var sum, count float64

	for _, m := range store.Measurement {
		if v, ok := m.Fields[fieldRequestDurationSum]; ok {
			sum, _ = v.(float64)
		}

		if v, ok := m.Fields[fieldRequestCount]; ok {
			count, _ = v.(float64)
		}
	}

	// The fixture's httpd.reqDurationNs is 41397831 ns over 12 requests: 0.0414 s in
	// total, and about 3.4 ms each.
	if want := 41397831.0 / 1e9; sum != want {
		t.Errorf("%s = %v, want %v (nanoseconds divided into seconds)", fieldRequestDurationSum, sum, want)
	}

	if count != 12 {
		t.Errorf("%s = %v, want 12", fieldRequestCount, count)
	}
}

// TestUptimeIsComputedFromStartTime checks 3.x's start time becomes an uptime. Its
// labels are dropped with it: process_start_time_seconds carries a uuid the server picks
// anew at every start, which as a label would begin a new series on each restart.
func TestUptimeIsComputedFromStartTime(t *testing.T) {
	server := serveLine(t, lineV3)
	store := gatherOnce(t, server.URL)

	startTime := sumPromFixture(t, "testdata/influxdb3-metrics.txt", "process_start_time_seconds")
	want := fixedNow.Sub(time.Unix(int64(startTime), 0)).Seconds()

	var got float64

	found := false

	for _, m := range store.Measurement {
		if v, ok := m.Fields[fieldUptime]; ok {
			got, _ = v.(float64)
			found = true

			if len(m.Tags) != 0 {
				t.Errorf("%s carries labels %v, want none", fieldUptime, m.Tags)
			}
		}
	}

	if !found {
		t.Fatalf("no %s field", fieldUptime)
	}

	if got != want {
		t.Errorf("%s = %v, want %v", fieldUptime, got, want)
	}
}

// TestIgnoresTheRest checks the families the input is not interested in stay out. The 3.x
// fixture keeps 420 http_request_duration_seconds_bucket lines and a Tokio counter for
// exactly this: a 3.11 Core answers 2667 points and only a couple of dozen are wanted.
func TestIgnoresTheRest(t *testing.T) {
	for _, l := range []line{lineV1, lineV2, lineV3} {
		t.Run(l.String(), func(t *testing.T) {
			server := serveLine(t, l)
			store := gatherOnce(t, server.URL)

			for _, m := range store.Measurement {
				for field := range m.Fields {
					if strings.Contains(field, "bucket") || strings.Contains(field, "tokio") ||
						strings.Contains(field, "go_") {
						t.Errorf("field %q should not be published", field)
					}
				}
			}

			if len(store.Measurement) > 40 {
				t.Errorf("%d points from one gather, want a couple of dozen", len(store.Measurement))
			}
		})
	}
}

// TestUnauthorizedIsNamed checks the 401 a 3.x server at its default settings answers is
// reported as the missing token it is, rather than as a bare HTTP status.
func TestUnauthorizedIsNamed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))

	t.Cleanup(server.Close)

	input, _, err := New(server.URL, "", "", "")
	if err != nil {
		t.Fatalf("New() = %v", err)
	}

	// The 401 on /ping identifies a 3.x, and the one on /metrics is then the token.
	if err := input.Gather(&internal.StoreAccumulator{}); !errors.Is(err, errUnauthorized) {
		t.Errorf("Gather() = %v, want %v", err, errUnauthorized)
	}
}

// TestTokenIsSent checks the token reaches the server: without it a 3.x answers 401 and
// there are no metrics at all.
func TestTokenIsSent(t *testing.T) {
	var got string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metrics" {
			got = r.Header.Get("Authorization")
		}

		w.Header().Set(versionHeader, "3.11.4")
		_, _ = w.Write([]byte("influxdb3_parquet_cache_size_bytes 0\n"))
	}))

	t.Cleanup(server.Close)

	input, _, err := New(server.URL, "", "", "s3cret-token")
	if err != nil {
		t.Fatalf("New() = %v", err)
	}

	if err := input.Gather(&internal.StoreAccumulator{}); err != nil {
		t.Fatalf("Gather() = %v", err)
	}

	if got != "Bearer s3cret-token" {
		t.Errorf("Authorization on /metrics = %q, want %q", got, "Bearer s3cret-token")
	}
}

// TestUnknownVersionGathersNothing checks a server that is none of the three lines is left
// alone rather than read as a guess: reading the wrong endpoint would publish nothing and
// explain nothing.
func TestUnknownVersionGathersNothing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	t.Cleanup(server.Close)

	input, _, err := New(server.URL, "", "", "")
	if err != nil {
		t.Fatalf("New() = %v", err)
	}

	store := &internal.StoreAccumulator{}

	if err := input.Gather(store); err != nil {
		t.Errorf("Gather() = %v, want nil", err)
	}

	if len(store.Measurement) != 0 {
		t.Errorf("%d points from a server of no known line, want 0", len(store.Measurement))
	}
}

// TestLineIsAskedOnce checks the version probe is not repeated: a server does not change
// major version between two gathers, and paying a request for it every ten seconds would
// double the cost of the input.
func TestLineIsAskedOnce(t *testing.T) {
	var pinged int

	body, err := os.ReadFile("testdata/influxdb3-metrics.txt")
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ping" {
			pinged++

			w.Header().Set(versionHeader, "3.11.4")
			w.WriteHeader(http.StatusOK)

			return
		}

		_, _ = w.Write(body)
	}))

	t.Cleanup(server.Close)

	input, _, err := New(server.URL, "", "", "")
	if err != nil {
		t.Fatalf("New() = %v", err)
	}

	for range 3 {
		if err := input.Gather(&internal.StoreAccumulator{}); err != nil {
			t.Fatalf("Gather() = %v", err)
		}
	}

	if pinged != 1 {
		t.Errorf("/ping asked %d times over three gathers, want 1", pinged)
	}
}

// TestTransformMetrics covers the averages: the raw duration and the operation count are
// both dropped, leaving the average of the period.
func TestTransformMetrics(t *testing.T) {
	fields := map[string]float64{
		fieldRequestDurationSum: 0.5,
		fieldRequestCount:       4,
		fieldQueryDurationSum:   0.2,
		fieldQueryCount:         0,
		fieldUptime:             1234,
	}

	got := transformMetrics(internal.GatherContext{}, fields, nil) //nolint:exhaustruct

	want := map[string]float64{
		// 0.5 s of request time over 4 requests, both already per second.
		fieldRequestDuration: 0.125,
		// No query finished during the period, so there is no average to report -- and the
		// raw duration is dropped all the same, being meaningless on its own.
		fieldUptime: 1234,
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("transformMetrics() (-want +got):\n%s", diff)
	}
}

// TestNoURLIsDisabled checks a service discovery found no address for produces no input
// rather than one reading an empty URL.
func TestNoURLIsDisabled(t *testing.T) {
	input, _, err := New("", "", "", "")
	if err == nil {
		t.Error(`New("") = nil error, want ErrDisabledInput`)
	}

	if input != nil {
		t.Errorf(`New("") returned an input (%T), want nil`, input)
	}
}
