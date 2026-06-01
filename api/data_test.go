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

package api

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bleemeo/glouton/threshold"
	"github.com/bleemeo/glouton/types"
)

const (
	testLabelsDiskUsedPerc = `__name__="disk_used_perc",instance_uuid="main-agent-id",item="/home"`
	testCPUUsed            = "cpu_used"
)

// callThresholds runs the Thresholds handler against the given registry
// (which may be nil) and decodes the JSON response.
func callThresholds(t *testing.T, reg *threshold.Registry) ThresholdsResponse {
	t.Helper()

	d := Data{api: &API{Threshold: reg}}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/data/thresholds", nil)
	rec := httptest.NewRecorder()

	d.Thresholds(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Thresholds() status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp ThresholdsResponse

	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding response: %v (body: %s)", err, rec.Body.String())
	}

	return resp
}

func TestThresholdsHandlerNilRegistry(t *testing.T) {
	resp := callThresholds(t, nil)

	// Empty (non-nil) slices so the JSON is [] rather than null.
	if resp.Thresholds == nil || len(resp.Thresholds) != 0 {
		t.Errorf("Thresholds = %+v, want empty non-nil slice", resp.Thresholds)
	}

	if resp.States == nil || len(resp.States) != 0 {
		t.Errorf("States = %+v, want empty non-nil slice", resp.States)
	}
}

func TestThresholdsHandler(t *testing.T) {
	reg := threshold.New(stubState{})

	reg.SetThresholds(
		"main-agent-id",
		map[string]threshold.Threshold{
			testLabelsDiskUsedPerc: {
				LowCritical:   math.NaN(),
				LowWarning:    math.NaN(),
				HighWarning:   80,
				HighCritical:  90,
				WarningDelay:  60 * time.Second,
				CriticalDelay: 5 * time.Minute,
			},
		},
		map[string]threshold.Threshold{
			testCPUUsed: {
				LowCritical:  math.NaN(),
				LowWarning:   math.NaN(),
				HighWarning:  80,
				HighCritical: 90,
			},
		},
	)

	// Push a critical cpu_used point so a state is recorded. State times
	// come from the engine's clock (time.Now), so bracket the call to
	// assert they land within the window rather than at a fixed instant.
	before := time.Now()

	reg.ApplyThresholds([]types.MetricPoint{
		{
			Labels: map[string]string{types.LabelName: testCPUUsed},
			Point:  types.Point{Value: 95},
		},
	})

	after := time.Now()

	resp := callThresholds(t, reg)

	// --- rules ---
	if len(resp.Thresholds) != 2 {
		t.Fatalf("Thresholds returned %d rules, want 2: %+v", len(resp.Thresholds), resp.Thresholds)
	}

	// Sorted by metric name then labels: cpu_used (config) before disk_used_perc (bleemeo).
	cpu := resp.Thresholds[0]
	if cpu.MetricName != testCPUUsed || cpu.Source != threshold.SourceConfig {
		t.Errorf("rule[0] = {%q, %q}, want {%q, config}", cpu.MetricName, cpu.Source, testCPUUsed)
	}

	if cpu.LowCritical != nil || cpu.LowWarning != nil {
		t.Errorf("rule[0] low bounds = {%v, %v}, want both null (NaN)", cpu.LowCritical, cpu.LowWarning)
	}

	if cpu.HighWarning == nil || *cpu.HighWarning != 80 {
		t.Errorf("rule[0] HighWarning = %v, want 80", cpu.HighWarning)
	}

	if cpu.Item != "" {
		t.Errorf("rule[0] Item = %q, want empty", cpu.Item)
	}

	disk := resp.Thresholds[1]
	if disk.MetricName != "disk_used_perc" || disk.Source != threshold.SourceBleemeo {
		t.Errorf("rule[1] = {%q, %q}, want {disk_used_perc, bleemeo}", disk.MetricName, disk.Source)
	}

	if disk.Item != "/home" {
		t.Errorf("rule[1] Item = %q, want /home", disk.Item)
	}

	if disk.WarningDelaySec != 60 || disk.CriticalDelaySec != 300 {
		t.Errorf("rule[1] delays = {%d, %d}s, want {60, 300}", disk.WarningDelaySec, disk.CriticalDelaySec)
	}

	// --- states ---
	if len(resp.States) != 1 {
		t.Fatalf("States returned %d entries, want 1: %+v", len(resp.States), resp.States)
	}

	st := resp.States[0]
	if st.MetricName != testCPUUsed {
		t.Errorf("state MetricName = %q, want %q", st.MetricName, testCPUUsed)
	}

	if st.Status != "critical" {
		t.Errorf("state Status = %q, want critical", st.Status)
	}

	if st.LastValue == nil || *st.LastValue != 95 {
		t.Errorf("state LastValue = %v, want 95", st.LastValue)
	}

	if st.CriticalSince == nil || st.CriticalSince.Before(before) || st.CriticalSince.After(after) {
		t.Errorf("state CriticalSince = %v, want within [%v, %v]", st.CriticalSince, before, after)
	}

	if st.LastUpdate == nil || st.LastUpdate.Before(before) || st.LastUpdate.After(after) {
		t.Errorf("state LastUpdate = %v, want within [%v, %v]", st.LastUpdate, before, after)
	}
}

func TestStatusToString(t *testing.T) {
	cases := map[types.Status]string{
		types.StatusOk:       "ok",
		types.StatusWarning:  "warning",
		types.StatusCritical: "critical",
		types.StatusUnset:    "unknown",
	}

	for status, want := range cases {
		if got := statusToString(status); got != want {
			t.Errorf("statusToString(%v) = %q, want %q", status, got, want)
		}
	}
}

func TestItemFromLabelsText(t *testing.T) {
	cases := map[string]string{
		"":                     "",
		`__name__="cpu_used"`:  "",
		testLabelsDiskUsedPerc: "/home",
	}

	for labelsText, want := range cases {
		if got := itemFromLabelsText(labelsText); got != want {
			t.Errorf("itemFromLabelsText(%q) = %q, want %q", labelsText, got, want)
		}
	}
}

func TestNullableValueWhenUpdated(t *testing.T) {
	t0 := time.Date(2020, 2, 24, 15, 1, 0, 0, time.UTC)

	if got := nullableValueWhenUpdated(95, time.Time{}); got != nil {
		t.Errorf("nullableValueWhenUpdated with zero time = %v, want nil", got)
	}

	if got := nullableValueWhenUpdated(math.NaN(), t0); got != nil {
		t.Errorf("nullableValueWhenUpdated with NaN = %v, want nil", got)
	}

	if got := nullableValueWhenUpdated(95, t0); got == nil || *got != 95 {
		t.Errorf("nullableValueWhenUpdated(95, t0) = %v, want 95", got)
	}
}

// stubState is a no-op threshold.State for tests that don't need
// persisted status.
type stubState struct{}

func (stubState) Get(string, any) error { return nil }
func (stubState) Set(string, any) error { return nil }
