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

package syncregistrationstatus

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/discovery"

	"github.com/google/go-cmp/cmp"
)

func service(i int, errorText string) ServiceState {
	return ServiceState{
		Name:        fmt.Sprintf("svc%05d", i),
		ServiceType: discovery.CustomService,
		Error:       errorText,
	}
}

func encodedSize(t *testing.T, v any) int {
	t.Helper()

	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}

	return len(data)
}

// TestLimitServicesUnderTheLimits checks a list within the limits is left as it is.
func TestLimitServicesUnderTheLimits(t *testing.T) {
	states := []ServiceState{service(1, ""), service(2, "connection refused"), service(3, "")}

	got, _, dropped := limitServices(states)

	if dropped != 0 {
		t.Errorf("dropped %d services, want none", dropped)
	}

	if diff := cmp.Diff(states, got); diff != "" {
		t.Errorf("limitServices() changed a list within the limits (-want +got):\n%s", diff)
	}
}

// TestLimitServicesKeepsErrorsFirst checks that when there are too many services, those
// with an error are the ones kept, and the result keeps its order.
func TestLimitServicesKeepsErrorsFirst(t *testing.T) {
	states := make([]ServiceState, 0, maxServices+500)

	for i := range maxServices + 500 {
		errorText := ""
		// The services in error are at the end, where a plain cut would drop them.
		if i >= maxServices+400 {
			errorText = "connection refused"
		}

		states = append(states, service(i, errorText))
	}

	got, _, dropped := limitServices(states)

	if len(got) != maxServices || dropped != 500 {
		t.Fatalf("kept %d and dropped %d, want %d and 500", len(got), dropped, maxServices)
	}

	withError := 0

	for _, state := range got {
		if state.Error != "" {
			withError++
		}
	}

	if withError != 100 {
		t.Errorf("kept %d of the 100 services in error, want all of them", withError)
	}

	if !slices.IsSortedFunc(got, func(a, b ServiceState) int { return strings.Compare(a.Name, b.Name) }) {
		t.Error("the services kept are no longer in their original order")
	}
}

// TestLimitServicesBytes checks the encoded list fits its byte budget when every row is
// large.
func TestLimitServicesBytes(t *testing.T) {
	long := strings.Repeat("i", 4000)
	states := make([]ServiceState, 0, 500)

	for i := range 500 {
		state := service(i, "")
		state.Instance = long
		states = append(states, state)
	}

	got, _, dropped := limitServices(states)

	if dropped == 0 {
		t.Fatal("nothing was dropped, the test does not exercise the byte budget")
	}

	if size := encodedSize(t, got); size > maxServicesBytes {
		t.Errorf("encoded services take %d bytes, over the %d budget", size, maxServicesBytes)
	}
}

// TestLimitServicesSkipsOversizedRow checks a row too large to fit does not stop the rows
// after it from being kept.
func TestLimitServicesSkipsOversizedRow(t *testing.T) {
	huge := service(0, "")
	huge.Instance = strings.Repeat("i", maxServicesBytes)

	states := []ServiceState{huge, service(1, ""), service(2, "")}

	got, _, dropped := limitServices(states)

	if dropped != 1 {
		t.Errorf("dropped %d services, want only the oversized one", dropped)
	}

	if diff := cmp.Diff(states[1:], got); diff != "" {
		t.Errorf("limitServices() mismatch (-want +got):\n%s", diff)
	}
}

// TestLimitMetricsKeepsLargestCounts checks that when there are too many rows, those
// covering the most metrics are kept, and the result keeps its order.
func TestLimitMetricsKeepsLargestCounts(t *testing.T) {
	states := make([]bleemeoTypes.MetricState, 0, maxMetrics+500)

	for i := range maxMetrics + 500 {
		states = append(states, bleemeoTypes.MetricState{
			Name:   fmt.Sprintf("metric%05d", i),
			Reason: bleemeoTypes.ReasonNotAvailableInPlan,
			Count:  i + 1,
		})
	}

	got, _, dropped := limitMetrics(states)

	if len(got) != maxMetrics || dropped != 500 {
		t.Fatalf("kept %d and dropped %d, want %d and 500", len(got), dropped, maxMetrics)
	}

	// The 500 smallest counts, 1 to 500, are the ones left out.
	if got[0].Count != 501 {
		t.Errorf("smallest count kept is %d, want 501", got[0].Count)
	}

	if !slices.IsSortedFunc(got, func(a, b bleemeoTypes.MetricState) int { return strings.Compare(a.Name, b.Name) }) {
		t.Error("the rows kept are no longer in their original order")
	}

	if size := encodedSize(t, got); size > maxMetricsBytes {
		t.Errorf("encoded rows take %d bytes, over the %d budget", size, maxMetricsBytes)
	}
}

// TestLimitMetricsBytes checks the byte budget holds for rows with very long metric names,
// which a Prometheus target can produce.
func TestLimitMetricsBytes(t *testing.T) {
	states := make([]bleemeoTypes.MetricState, 0, 900)

	for i := range 900 {
		states = append(states, bleemeoTypes.MetricState{
			Name:   fmt.Sprintf("%05d_%s", i, strings.Repeat("n", 2000)),
			Reason: bleemeoTypes.ReasonNotAvailableInPlan,
			Count:  1,
		})
	}

	got, _, dropped := limitMetrics(states)

	if dropped == 0 {
		t.Fatal("nothing was dropped, the test does not exercise the byte budget")
	}

	if size := encodedSize(t, got); size > maxMetricsBytes {
		t.Errorf("encoded rows take %d bytes, over the %d budget", size, maxMetricsBytes)
	}
}

// TestLimitIsDeterministic checks the same list is always cut the same way: the result is
// compared to decide whether the API needs updating.
func TestLimitIsDeterministic(t *testing.T) {
	states := make([]bleemeoTypes.MetricState, 0, maxMetrics*2)

	for i := range maxMetrics * 2 {
		// Many equal counts, so that the cut falls among ties.
		states = append(states, bleemeoTypes.MetricState{
			Name:   fmt.Sprintf("metric%05d", i),
			Reason: bleemeoTypes.ReasonPending,
			Count:  i % 3,
		})
	}

	first, _, _ := limitMetrics(states)

	for range 10 {
		got, _, _ := limitMetrics(states)
		if diff := cmp.Diff(first, got); diff != "" {
			t.Fatalf("limitMetrics() cut the same list differently:\n%s", diff)
		}
	}
}

// TestLimitServicesDisabledIsNotAProblem checks a service whose metrics the user disabled is
// not kept ahead of the others: its error reports a choice, not a failure.
func TestLimitServicesDisabledIsNotAProblem(t *testing.T) {
	states := make([]ServiceState, 0, maxServices+1)

	// Sorted first, a working service; then the cap's worth of disabled ones.
	states = append(states, service(0, ""))

	for i := 1; i <= maxServices; i++ {
		disabled := service(i, "metrics disabled by configuration")
		disabled.MetricsIgnored = true
		states = append(states, disabled)
	}

	got, _, dropped := limitServices(states)

	if dropped != 1 {
		t.Fatalf("dropped %d services, want 1", dropped)
	}

	// All tie, so the order of the list decides: the last disabled one is left out, not
	// the working one sorted first.
	if got[0].Name != "svc00000" {
		t.Errorf("the working service was dropped in favour of a disabled one")
	}
}

// TestLimitReturnsEncodedRows checks the rows returned are the JSON of the rows kept, which
// the hash relies on.
func TestLimitReturnsEncodedRows(t *testing.T) {
	states := []ServiceState{service(1, ""), service(2, "connection refused")}

	got, rows, _ := limitServices(states)

	if len(rows) != len(got) {
		t.Fatalf("got %d encoded rows for %d services", len(rows), len(got))
	}

	for i := range got {
		if want := encodedSize(t, got[i]); len(rows[i]) != want {
			t.Errorf("row %d is %d bytes, want the %d of its JSON", i, len(rows[i]), want)
		}
	}
}

// TestMinEncodedSizeIsALowerBound checks no row encodes shorter than the size limit uses to
// stop early: otherwise it would stop while a row could still fit.
func TestMinEncodedSizeIsALowerBound(t *testing.T) {
	shortest := ServiceState{Dynamic: true, InputSupported: true}

	if size := encodedSize(t, shortest); size < minEncodedSize[ServiceState]() {
		t.Errorf("a service encodes in %d bytes, under the %d lower bound", size, minEncodedSize[ServiceState]())
	}

	if size := encodedSize(t, bleemeoTypes.MetricState{}); size < minEncodedSize[bleemeoTypes.MetricState]() {
		t.Errorf("a metric row encodes in %d bytes, under the %d lower bound", size, minEncodedSize[bleemeoTypes.MetricState]())
	}
}
