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
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/bleemeo/glouton/inputs/internal"
)

// TestServersShareOnlyTheName is the guarantee that the shared metric names buy nothing
// more than a name: two InfluxDB servers read by one agent must report their own values,
// with nothing summed, averaged or carried between them.
//
// It reads a 1.x and a 3.x in the same process, which is the arrangement most likely to
// leak -- they share the package, the field names and the accumulator's machinery, and
// differ only in the input instance.
func TestServersShareOnlyTheName(t *testing.T) {
	v1 := serveLine(t, lineV1)
	v3 := serveLine(t, lineV3)

	// Read the two in an interleaved order, so a value left behind by one would be picked
	// up by the other rather than merely overwritten.
	storeV1 := gatherOnce(t, v1.URL)
	storeV3 := gatherOnce(t, v3.URL)
	storeV1Again := gatherOnce(t, v1.URL)

	requestsOf := func(store *internal.StoreAccumulator) float64 {
		for _, m := range store.Measurement {
			if v, ok := m.Fields[fieldRequests]; ok {
				value, _ := v.(float64)

				return value
			}
		}

		t.Fatalf("no %s field", fieldRequests)

		return 0
	}

	// httpd.req of the 1.x recording, against the sum of http_requests_total in the 3.x
	// one. Two servers, one metric name, two numbers.
	wantV1 := 12.0
	wantV3 := sumPromFixture(t, "testdata/influxdb3-metrics.txt", "http_requests_total")

	if wantV1 == wantV3 {
		t.Fatal("the two fixtures happen to agree, so this test would prove nothing")
	}

	if got := requestsOf(storeV1); got != wantV1 {
		t.Errorf("1.x %s = %v, want %v", fieldRequests, got, wantV1)
	}

	if got := requestsOf(storeV3); got != wantV3 {
		t.Errorf("3.x %s = %v, want %v", fieldRequests, got, wantV3)
	}

	// And reading the 1.x again after the 3.x gives the 1.x number, not the other's and
	// not a sum of both.
	if got := requestsOf(storeV1Again); got != wantV1 {
		t.Errorf("1.x %s after reading a 3.x = %v, want %v", fieldRequests, got, wantV1)
	}
}

// TestVersionIsPerServer checks the detected line is remembered on the input and not
// anywhere shared: one server being a 1.x must not make the next one be read as one, which
// would point it at an endpoint holding none of its metrics.
func TestVersionIsPerServer(t *testing.T) {
	inputFor := func(url string) *metricsInput {
		in, _, err := New(url, "", "", "")
		if err != nil {
			t.Fatalf("New() = %v", err)
		}

		inner, ok := in.(*internal.Input)
		if !ok {
			t.Fatalf("New() returned %T", in)
		}

		raw, ok := inner.Input.(*metricsInput)
		if !ok {
			t.Fatalf("inner input is %T", inner.Input)
		}

		return raw
	}

	cases := []struct {
		line line
		want line
	}{{lineV1, lineV1}, {lineV2, lineV2}, {lineV3, lineV3}}

	inputs := make([]*metricsInput, 0, len(cases))

	for _, c := range cases {
		server := serveLine(t, c.line)
		in := inputFor(server.URL)

		if err := in.Gather(&internal.StoreAccumulator{}); err != nil {
			t.Fatalf("Gather() = %v", err)
		}

		inputs = append(inputs, in)
	}

	for i, c := range cases {
		if got := inputs[i].line; got != c.want {
			t.Errorf("input %d remembered line %v, want %v", i, got, c.want)
		}
	}
}

// TestConcurrentServersDoNotCrossTalk reads several servers at once, since discovery
// gathers them in parallel and a shared value would show up as a race or a wrong number.
func TestConcurrentServersDoNotCrossTalk(t *testing.T) {
	servers := map[line]*httptest.Server{
		lineV1: serveLine(t, lineV1),
		lineV2: serveLine(t, lineV2),
		lineV3: serveLine(t, lineV3),
	}

	type result struct {
		line  line
		names int
	}

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results []result
	)

	// Several rounds, so an interleaving that would carry a value across has a chance to
	// happen rather than depending on one lucky schedule.
	for range 5 {
		for l, server := range servers {
			wg.Add(1)

			go func(l line, url string) {
				defer wg.Done()

				store := &internal.StoreAccumulator{}

				in, _, err := New(url, "", "", "")
				if err != nil {
					return
				}

				inner, _ := in.(*internal.Input)
				raw, _ := inner.Input.(*metricsInput)
				raw.now = func() time.Time { return fixedNow }

				if err := raw.Gather(store); err != nil {
					return
				}

				names := map[string]bool{}

				for _, m := range store.Measurement {
					for f := range m.Fields {
						names[f] = true
					}
				}

				mu.Lock()

				results = append(results, result{line: l, names: len(names)})
				mu.Unlock()
			}(l, server.URL)
		}
	}

	wg.Wait()

	// The field count is the signature of a line: 1.x publishes the core plus the write
	// half, the query half and its own three; 2.x has no query metric; 3.x no write half.
	want := map[line]int{lineV1: 15, lineV2: 10, lineV3: 18}

	if len(results) != 15 {
		t.Fatalf("%d results, want 15", len(results))
	}

	for _, r := range results {
		if r.names != want[r.line] {
			t.Errorf("%v published %d fields, want %d: a value carried from another server",
				r.line, r.names, want[r.line])
		}
	}
}
