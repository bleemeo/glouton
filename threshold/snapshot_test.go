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

package threshold

import (
	"math"
	"testing"
	"time"

	"github.com/bleemeo/glouton/types"
)

// floatEqual compares two float64, treating NaN as equal to NaN so the
// "unset bound" sentinel can be asserted directly.
func floatEqual(a, b float64) bool {
	if math.IsNaN(a) && math.IsNaN(b) {
		return true
	}

	return a == b
}

func TestAllThresholds(t *testing.T) {
	reg := New(mockState{})
	reg.SetThresholds(
		testMainAgentID,
		// thresholdWithItem: Bleemeo-pushed, keyed by canonical labels text.
		map[string]Threshold{
			testLabelsDiskUsedPerc: {
				LowCritical:   math.NaN(),
				LowWarning:    math.NaN(),
				HighWarning:   80,
				HighCritical:  90,
				WarningDelay:  60 * time.Second,
				CriticalDelay: 5 * time.Minute,
			},
			// A fully-unset (NaN) threshold must be skipped.
			testLabelsNetUsedEth0: {
				LowCritical:  math.NaN(),
				LowWarning:   math.NaN(),
				HighWarning:  math.NaN(),
				HighCritical: math.NaN(),
			},
		},
		// thresholdAllItem: config-defined, keyed by metric name.
		map[string]Threshold{
			testCPUUsed: {
				LowCritical:   math.NaN(),
				LowWarning:    math.NaN(),
				HighWarning:   70,
				HighCritical:  95,
				WarningDelay:  30 * time.Second,
				CriticalDelay: 2 * time.Minute,
			},
			// A zero-value threshold must be skipped.
			"mem_used": {},
		},
	)

	got := reg.AllThresholds()

	// Two non-zero thresholds expected; the NaN-only and zero-value ones
	// are filtered out.
	if len(got) != 2 {
		t.Fatalf("AllThresholds() returned %d entries, want 2: %+v", len(got), got)
	}

	byKey := make(map[string]Entry, len(got))
	for _, e := range got {
		key := e.Source + "|" + e.MetricName + "|" + e.LabelsText
		byKey[key] = e
	}

	wantConfig := Entry{
		MetricName:    testCPUUsed,
		LabelsText:    "",
		Source:        SourceConfig,
		LowCritical:   math.NaN(),
		LowWarning:    math.NaN(),
		HighWarning:   70,
		HighCritical:  95,
		WarningDelay:  30 * time.Second,
		CriticalDelay: 2 * time.Minute,
	}
	gotConfig, ok := byKey["config|"+testCPUUsed+"|"]

	if !ok {
		t.Fatalf("AllThresholds() missing config entry for %q", testCPUUsed)
	}

	assertEntryEqual(t, "config", gotConfig, wantConfig)

	wantBleemeo := Entry{
		MetricName:    "disk_used_perc",
		LabelsText:    testLabelsDiskUsedPerc,
		Source:        SourceBleemeo,
		LowCritical:   math.NaN(),
		LowWarning:    math.NaN(),
		HighWarning:   80,
		HighCritical:  90,
		WarningDelay:  60 * time.Second,
		CriticalDelay: 5 * time.Minute,
	}
	gotBleemeo, ok := byKey["bleemeo|disk_used_perc|"+testLabelsDiskUsedPerc]

	if !ok {
		t.Fatalf("AllThresholds() missing bleemeo entry for %q", testLabelsDiskUsedPerc)
	}

	assertEntryEqual(t, "bleemeo", gotBleemeo, wantBleemeo)
}

func assertEntryEqual(t *testing.T, name string, got, want Entry) {
	t.Helper()

	if got.MetricName != want.MetricName {
		t.Errorf("%s: MetricName = %q, want %q", name, got.MetricName, want.MetricName)
	}

	if got.LabelsText != want.LabelsText {
		t.Errorf("%s: LabelsText = %q, want %q", name, got.LabelsText, want.LabelsText)
	}

	if got.Source != want.Source {
		t.Errorf("%s: Source = %q, want %q", name, got.Source, want.Source)
	}

	if !floatEqual(got.LowCritical, want.LowCritical) {
		t.Errorf("%s: LowCritical = %v, want %v", name, got.LowCritical, want.LowCritical)
	}

	if !floatEqual(got.LowWarning, want.LowWarning) {
		t.Errorf("%s: LowWarning = %v, want %v", name, got.LowWarning, want.LowWarning)
	}

	if !floatEqual(got.HighWarning, want.HighWarning) {
		t.Errorf("%s: HighWarning = %v, want %v", name, got.HighWarning, want.HighWarning)
	}

	if !floatEqual(got.HighCritical, want.HighCritical) {
		t.Errorf("%s: HighCritical = %v, want %v", name, got.HighCritical, want.HighCritical)
	}

	if got.WarningDelay != want.WarningDelay {
		t.Errorf("%s: WarningDelay = %v, want %v", name, got.WarningDelay, want.WarningDelay)
	}

	if got.CriticalDelay != want.CriticalDelay {
		t.Errorf("%s: CriticalDelay = %v, want %v", name, got.CriticalDelay, want.CriticalDelay)
	}
}

func TestAllThresholdsEmpty(t *testing.T) {
	reg := New(mockState{})

	if got := reg.AllThresholds(); len(got) != 0 {
		t.Errorf("AllThresholds() on empty registry = %+v, want empty", got)
	}
}

func TestAllStates(t *testing.T) {
	reg := New(mockState{})

	t0 := time.Date(2020, 2, 24, 15, 1, 0, 0, time.UTC)
	reg.nowFunc = func() time.Time { return t0 }

	// Immediate delays so the status flips on the first point.
	reg.SetThresholds(
		"",
		nil,
		map[string]Threshold{
			testCPUUsed: {
				LowCritical:  math.NaN(),
				LowWarning:   math.NaN(),
				HighWarning:  80,
				HighCritical: 90,
			},
		},
	)

	// Before any point is evaluated, there are no states.
	if got := reg.AllStates(); len(got) != 0 {
		t.Fatalf("AllStates() before any point = %+v, want empty", got)
	}

	points := []types.MetricPoint{
		{
			Labels: map[string]string{types.LabelName: testCPUUsed},
			Point:  types.Point{Time: t0, Value: 95},
		},
	}
	reg.ApplyThresholds(points)

	got := reg.AllStates()
	if len(got) != 1 {
		t.Fatalf("AllStates() returned %d entries, want 1: %+v", len(got), got)
	}

	state := got[0]

	if state.MetricName != testCPUUsed {
		t.Errorf("MetricName = %q, want %q", state.MetricName, testCPUUsed)
	}

	if state.LabelsText != NameCPUUsed {
		t.Errorf("LabelsText = %q, want %q", state.LabelsText, NameCPUUsed)
	}

	if state.CurrentStatus != types.StatusCritical {
		t.Errorf("CurrentStatus = %v, want %v", state.CurrentStatus, types.StatusCritical)
	}

	if state.LastValue != 95 {
		t.Errorf("LastValue = %v, want 95", state.LastValue)
	}

	if !state.LastUpdate.Equal(t0) {
		t.Errorf("LastUpdate = %v, want %v", state.LastUpdate, t0)
	}

	if !state.CriticalSince.Equal(t0) {
		t.Errorf("CriticalSince = %v, want %v", state.CriticalSince, t0)
	}
}
