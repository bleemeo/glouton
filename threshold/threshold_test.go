// Copyright 2015-2025 Bleemeo
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

//nolint:dupl
package threshold

import (
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/types"

	"github.com/google/go-cmp/cmp"
)

type mockState struct {
	jsonList []jsonState
}

func (m mockState) Get(key string, result any) error {
	if key == statusCacheKey {
		res, ok := result.(*[]jsonState)
		if ok && res != nil {
			*res = m.jsonList
		}
	}

	return nil
}

func (m mockState) Set(key string, object any) error {
	_ = key
	_ = object

	return nil
}

func TestStateUpdate(t *testing.T) {
	cases := [][]struct {
		timeOffsetSecond int
		status           types.Status
		want             types.Status
	}{
		{
			{-10, types.StatusOk, types.StatusOk},
			{0, types.StatusWarning, types.StatusOk},
			{10, types.StatusWarning, types.StatusOk},
			{280, types.StatusWarning, types.StatusOk},
			{290, types.StatusWarning, types.StatusOk},
			{300, types.StatusWarning, types.StatusWarning},
		},
		{
			{-10, types.StatusOk, types.StatusOk},
			{0, types.StatusWarning, types.StatusOk},
			{10, types.StatusWarning, types.StatusOk},
			{200, types.StatusCritical, types.StatusOk},
			{290, types.StatusCritical, types.StatusOk},
			{300, types.StatusCritical, types.StatusWarning},
			{310, types.StatusCritical, types.StatusWarning},
			{490, types.StatusCritical, types.StatusWarning},
			{500, types.StatusCritical, types.StatusCritical},
			{510, types.StatusCritical, types.StatusCritical},
			{520, types.StatusWarning, types.StatusWarning},
			{530, types.StatusCritical, types.StatusWarning},
			{820, types.StatusCritical, types.StatusWarning},
			{830, types.StatusCritical, types.StatusCritical},
			{840, types.StatusOk, types.StatusOk},
		},
	}
	now := time.Now()

	for i, c := range cases {
		state := statusState{}

		for _, step := range c {
			state = state.Update(step.status, 300*time.Second, 300*time.Second, now.Add(time.Duration(step.timeOffsetSecond)*time.Second))
			if state.CurrentStatus != step.want {
				t.Errorf("case #%d offset %d: state.CurrentStatus == %v, want %v", i, step.timeOffsetSecond, state.CurrentStatus, step.want)

				break
			}
		}
	}
}

func TestStateUpdatePeriodChange(t *testing.T) {
	cases := [][]struct {
		period           int
		timeOffsetSecond int
		status           types.Status
		want             types.Status
	}{
		{
			{0, -10, types.StatusOk, types.StatusOk},
			{0, 0, types.StatusWarning, types.StatusWarning},
			{0, 10, types.StatusCritical, types.StatusCritical},
			{300, 90, types.StatusOk, types.StatusOk},
			{300, 100, types.StatusWarning, types.StatusOk},
			{300, 400, types.StatusWarning, types.StatusWarning},
			{500, 410, types.StatusWarning, types.StatusWarning},
		},
	}
	now := time.Now()

	for i, c := range cases {
		state := statusState{}

		for _, step := range c {
			state = state.Update(step.status, time.Duration(step.period)*time.Second, time.Duration(step.period)*time.Second, now.Add(time.Duration(step.timeOffsetSecond)*time.Second))
			if state.CurrentStatus != step.want {
				t.Errorf("case #%d offset %d: state.CurrentStatus == %v, want %v", i, step.timeOffsetSecond, state.CurrentStatus, step.want)

				break
			}
		}
	}
}

func TestFormatValue(t *testing.T) {
	cases := []struct {
		value float64
		unit  Unit
		want  string
	}{
		{
			value: 0.,
			unit:  Unit{},
			want:  "0.00",
		},
		{
			value: 0.,
			unit:  Unit{UnitType: UnitTypeUnit, UnitText: "No unit"},
			want:  "0.00",
		},
		{
			value: 0.,
			// 42 is a unknown value for UnitType
			unit: Unit{UnitType: 42, UnitText: "%"},
			want: "0.00 %",
		},
		{
			value: 0.,
			// 42 is a unknown value for UnitType
			unit: Unit{UnitType: 42, UnitText: "thing"},
			want: "0.00 thing",
		},
		{
			value: 0.,
			unit:  Unit{UnitType: UnitTypeByte, UnitText: "Byte"},
			want:  "0.00 Bytes",
		},
		{
			value: 1024,
			unit:  Unit{UnitType: UnitTypeByte, UnitText: "Byte"},
			want:  "1.00 KBytes",
		},
		{
			value: 1 << 30,
			unit:  Unit{UnitType: UnitTypeByte, UnitText: "Byte"},
			want:  "1.00 GBytes",
		},
		{
			value: 1 << 60,
			unit:  Unit{UnitType: UnitTypeByte, UnitText: "Byte"},
			want:  "1.00 EBytes",
		},
		{
			value: 1 << 70,
			unit:  Unit{UnitType: UnitTypeByte, UnitText: "Byte"},
			want:  "1024.00 EBytes",
		},
		{
			value: -1024,
			unit:  Unit{UnitType: UnitTypeByte, UnitText: "Byte"},
			want:  "-1.00 KBytes",
		},
		{
			value: -1 << 30,
			unit:  Unit{UnitType: UnitTypeByte, UnitText: "Byte"},
			want:  "-1.00 GBytes",
		},
		{
			value: 1 << 70,
			unit:  Unit{UnitType: UnitTypeBytesPS, UnitText: "Bytes/s"},
			want:  "1024.00 EBytes/s",
		},
		{
			value: -1 << 60,
			unit:  Unit{UnitType: UnitTypeBitsPS, UnitText: "Bits/s"},
			want:  "-1.00 EBits/s",
		},
	}
	for _, c := range cases {
		got := FormatValue(c.value, c.unit)
		if got != c.want {
			t.Errorf("formatValue(%v, %v) == %v, want %v", c.value, c.unit, got, c.want)
		}
	}
}

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		value time.Duration
		want  string
	}{
		{
			value: 300 * time.Second,
			want:  "5 minutes",
		},
		{
			value: 24 * time.Hour,
			want:  "1 day",
		},
		{
			value: 24*time.Hour + 100*time.Second,
			want:  "1 day",
		},
		{
			value: 24*time.Hour - 100*time.Second,
			want:  "1 day",
		},
		// less than 10% from 1 day is ignored
		{
			value: 26 * time.Hour,
			want:  "1 day",
		},
		// but more than 10% is counted and rounded
		{
			value: 26*time.Hour + 31*time.Minute,
			want:  "1 day 3 hours",
		},
		// Same apply for 1 day minus less than 10%
		{
			value: 22 * time.Hour,
			want:  "1 day",
		},
		// Same apply for 1 day minus more than 10%
		{
			value: 22*time.Hour - 25*time.Minute,
			want:  "22 hours",
		},
		{
			value: 89 * time.Second,
			want:  "1 minute 29 seconds",
		},
		{
			value: 91 * time.Second,
			want:  "1 minute 31 seconds",
		},
		{
			value: 12 * time.Second,
			want:  "12 seconds",
		},
		{
			value: 0,
			want:  "0 second",
		},
		{
			value: 90 * 24 * time.Hour,
			want:  "90 days",
		},
		{
			value: 90*24*time.Hour + time.Hour,
			want:  "90 days",
		},
		{
			value: 365 * 24 * time.Hour,
			want:  "365 days",
		},
	}
	for _, c := range cases {
		got := formatDuration(c.value)
		if got != c.want {
			t.Errorf("formatDuration(%v) == %v, want %v", c.value, got, c.want)
		}
	}
}

func TestThresholdEqual(t *testing.T) {
	cases := []struct {
		left  Threshold
		right Threshold
		want  bool
	}{
		{
			left:  Threshold{},
			right: Threshold{},
			want:  true,
		},
		{
			left:  Threshold{LowCritical: 1},
			right: Threshold{},
			want:  false,
		},
		{
			left:  Threshold{LowCritical: math.NaN()},
			right: Threshold{},
			want:  false,
		},
		{
			left:  Threshold{LowCritical: math.NaN()},
			right: Threshold{LowCritical: math.NaN()},
			want:  true,
		},
		{
			left:  Threshold{LowCritical: math.NaN(), LowWarning: math.NaN(), HighWarning: math.NaN(), HighCritical: math.NaN()},
			right: Threshold{LowCritical: math.NaN(), LowWarning: math.NaN(), HighWarning: math.NaN(), HighCritical: math.NaN()},
			want:  true,
		},
		{
			left:  Threshold{LowCritical: 5, LowWarning: math.NaN(), HighWarning: math.NaN(), HighCritical: math.NaN()},
			right: Threshold{LowCritical: 5, LowWarning: math.NaN(), HighWarning: math.NaN(), HighCritical: math.NaN()},
			want:  true,
		},
		{
			left:  Threshold{LowCritical: 5, LowWarning: math.NaN(), HighWarning: math.NaN(), HighCritical: math.NaN()},
			right: Threshold{LowCritical: 6, LowWarning: math.NaN(), HighWarning: math.NaN(), HighCritical: math.NaN()},
			want:  false,
		},
	}
	for i, c := range cases {
		got := c.left.Equal(c.right)
		if got != c.want {
			t.Errorf("case %d: left.Equal(right) == %v, want %v", i, got, c.want)
		}

		got = c.right.Equal(c.left)
		if got != c.want {
			t.Errorf("case %d: right.Equal(left) == %v, want %v", i, got, c.want)
		}
	}
}

func TestAccumulatorThreshold(t *testing.T) {
	threshold := New(mockState{})
	threshold.SetThresholds(
		"fake_id",
		nil,
		map[string]Threshold{"cpu_used": {
			HighWarning:   80,
			HighCritical:  90,
			WarningDelay:  5 * time.Minute,
			CriticalDelay: 5 * time.Minute,
		}},
	)

	t0 := time.Date(2020, 2, 24, 15, 1, 0, 0, time.UTC)
	wantPoints := map[string]types.MetricPoint{
		`__name__="cpu_idle"`: {
			Labels: map[string]string{types.LabelName: "cpu_idle"},
			Point: types.Point{
				Time:  t0,
				Value: 20.0,
			},
		},
		`__name__="cpu_used"`: {
			Annotations: types.MetricAnnotations{
				Status: types.StatusDescription{
					CurrentStatus:     types.StatusWarning,
					StatusDescription: "Current value: 88.00 threshold (80.00) exceeded over last 5 minutes",
				},
			},
			Labels: map[string]string{types.LabelName: "cpu_used"},
			Point: types.Point{
				Time:  t0,
				Value: 88.0,
			},
		},
		`__name__="cpu_used_status"`: {
			Annotations: types.MetricAnnotations{
				Status: types.StatusDescription{
					CurrentStatus:     types.StatusWarning,
					StatusDescription: "Current value: 88.00 threshold (80.00) exceeded over last 5 minutes",
				},
				StatusOf: "cpu_used",
			},
			Labels: map[string]string{types.LabelName: "cpu_used_status"},
			Point: types.Point{
				Time:  t0,
				Value: 1.0,
			},
		},
	}

	points := []types.MetricPoint{
		{
			Labels: map[string]string{
				"__name__": "cpu_used",
			},
			Point: types.Point{Time: t0, Value: 88.0},
		},
		{
			Labels: map[string]string{
				"__name__": "cpu_idle",
			},
			Point: types.Point{Time: t0, Value: 20.0},
		},
	}

	newPoints, statusPoints := threshold.ApplyThresholds(points)
	newPoints = append(newPoints, statusPoints...)

	if len(newPoints) != 3 {
		t.Errorf("len(points) == %d, want 3", len(newPoints))
	}

	for i, got := range newPoints {
		labelsText := types.LabelsToText(got.Labels)
		want := wantPoints[labelsText]

		delete(wantPoints, labelsText)

		if !reflect.DeepEqual(got, want) {
			t.Errorf("points[%d] = %v, want %v", i, got, want)
		}
	}
}

func TestThreshold(t *testing.T) { //nolint: maintidx
	threshold := New(mockState{})

	t0 := time.Date(2020, 2, 24, 15, 1, 0, 0, time.UTC)
	stepDelay := 10 * time.Second

	type setThresholdsArgs struct {
		thresholdWithItem map[string]Threshold
		thresholdAllItem  map[string]Threshold
	}

	steps := []struct {
		AddedToT0   time.Duration
		PushedValue map[string]float64
		// WantedPoints will be processed to add an _status version of any points with CurrentStatus != StatusUnset
		WantedPoints  map[string]types.StatusDescription
		SetThresholds *setThresholdsArgs
	}{
		{
			AddedToT0: 0 * stepDelay,
			SetThresholds: &setThresholdsArgs{
				thresholdWithItem: map[string]Threshold{
					`__name__="disk_used_perc",item="/home"`: {
						HighWarning:   80,
						HighCritical:  math.NaN(),
						LowCritical:   math.NaN(),
						LowWarning:    math.NaN(),
						WarningDelay:  60 * time.Second,
						CriticalDelay: 60 * time.Second,
					},
				},
				thresholdAllItem: map[string]Threshold{"cpu_used": {
					HighWarning:   80,
					HighCritical:  90,
					LowCritical:   math.NaN(),
					LowWarning:    math.NaN(),
					WarningDelay:  60 * time.Second,
					CriticalDelay: 60 * time.Second,
				}},
			},
			PushedValue: map[string]float64{
				`__name__="cpu_used"`:                    20,
				`__name__="disk_used_perc",item="/home"`: 60,
				`__name__="disk_used_perc",item="/srv"`:  60,
			},
			WantedPoints: map[string]types.StatusDescription{
				`__name__="cpu_used"`:                    {CurrentStatus: types.StatusOk, StatusDescription: "Current value: 20.00"},
				`__name__="disk_used_perc",item="/home"`: {CurrentStatus: types.StatusOk, StatusDescription: "Current value: 60.00"},
				`__name__="disk_used_perc",item="/srv"`:  {CurrentStatus: types.StatusUnset},
			},
		},
		{
			AddedToT0: 1 * stepDelay,
			PushedValue: map[string]float64{
				`__name__="cpu_used"`:                    99,
				`__name__="disk_used_perc",item="/home"`: 99,
				`__name__="disk_used_perc",item="/srv"`:  99,
			},
			WantedPoints: map[string]types.StatusDescription{
				`__name__="cpu_used"`:                    {CurrentStatus: types.StatusOk, StatusDescription: "Current value: 99.00"},
				`__name__="disk_used_perc",item="/home"`: {CurrentStatus: types.StatusOk, StatusDescription: "Current value: 99.00"},
				`__name__="disk_used_perc",item="/srv"`:  {CurrentStatus: types.StatusUnset},
			},
		},
		{
			AddedToT0: 6 * stepDelay,
			PushedValue: map[string]float64{
				`__name__="cpu_used"`:                    91,
				`__name__="disk_used_perc",item="/home"`: 91,
				`__name__="disk_used_perc",item="/srv"`:  91,
			},
			WantedPoints: map[string]types.StatusDescription{
				`__name__="cpu_used"`:                    {CurrentStatus: types.StatusOk, StatusDescription: "Current value: 91.00"},
				`__name__="disk_used_perc",item="/home"`: {CurrentStatus: types.StatusOk, StatusDescription: "Current value: 91.00"},
				`__name__="disk_used_perc",item="/srv"`:  {CurrentStatus: types.StatusUnset},
			},
		},
		{
			AddedToT0: 7 * stepDelay,
			PushedValue: map[string]float64{
				`__name__="cpu_used"`:                    97,
				`__name__="disk_used_perc",item="/home"`: 97,
				`__name__="disk_used_perc",item="/srv"`:  97,
			},
			WantedPoints: map[string]types.StatusDescription{
				`__name__="cpu_used"`:                    {CurrentStatus: types.StatusCritical, StatusDescription: "Current value: 97.00 threshold (90.00) exceeded over last 1 minute"},
				`__name__="disk_used_perc",item="/home"`: {CurrentStatus: types.StatusWarning, StatusDescription: "Current value: 97.00 threshold (80.00) exceeded over last 1 minute"},
				`__name__="disk_used_perc",item="/srv"`:  {CurrentStatus: types.StatusUnset},
			},
		},
		{
			AddedToT0: 8 * stepDelay,
			PushedValue: map[string]float64{
				`__name__="cpu_used"`: 5,
			},
			WantedPoints: map[string]types.StatusDescription{
				`__name__="cpu_used"`: {CurrentStatus: types.StatusOk, StatusDescription: "Current value: 5.00"},
			},
		},
		{
			AddedToT0: 10 * stepDelay,
			PushedValue: map[string]float64{
				`__name__="cpu_used"`: 5,
			},
			WantedPoints: map[string]types.StatusDescription{
				`__name__="cpu_used"`: {CurrentStatus: types.StatusOk, StatusDescription: "Current value: 5.00"},
			},
		},
		{
			AddedToT0: 11 * stepDelay,
			PushedValue: map[string]float64{
				`__name__="cpu_used"`: 85,
			},
			WantedPoints: map[string]types.StatusDescription{
				`__name__="cpu_used"`: {CurrentStatus: types.StatusOk, StatusDescription: "Current value: 85.00"},
			},
		},
		{
			AddedToT0: 12 * stepDelay,
			PushedValue: map[string]float64{
				`__name__="cpu_used"`: 95,
			},
			WantedPoints: map[string]types.StatusDescription{
				`__name__="cpu_used"`: {CurrentStatus: types.StatusOk, StatusDescription: "Current value: 95.00"},
			},
		},
		{
			AddedToT0: 16 * stepDelay,
			PushedValue: map[string]float64{
				`__name__="cpu_used"`: 95,
			},
			WantedPoints: map[string]types.StatusDescription{
				`__name__="cpu_used"`: {CurrentStatus: types.StatusOk, StatusDescription: "Current value: 95.00"},
			},
		},
		{
			AddedToT0: 17 * stepDelay,
			PushedValue: map[string]float64{
				`__name__="cpu_used"`: 95,
			},
			WantedPoints: map[string]types.StatusDescription{
				`__name__="cpu_used"`: {CurrentStatus: types.StatusWarning, StatusDescription: "Current value: 95.00 threshold (80.00) exceeded over last 1 minute"},
			},
		},
		{
			AddedToT0: 18 * stepDelay,
			PushedValue: map[string]float64{
				`__name__="cpu_used"`: 95,
			},
			WantedPoints: map[string]types.StatusDescription{
				`__name__="cpu_used"`: {CurrentStatus: types.StatusCritical, StatusDescription: "Current value: 95.00 threshold (90.00) exceeded over last 1 minute"},
			},
		},
		{
			AddedToT0: 30 * stepDelay,
			PushedValue: map[string]float64{
				`__name__="cpu_used"`:                    95,
				`__name__="mem_used"`:                    95,
				`__name__="net_used",item="eth0"`:        95,
				`__name__="disk_used_perc",item="/home"`: 95,
				`__name__="disk_used_perc",item="/srv"`:  95,
			},
			WantedPoints: map[string]types.StatusDescription{
				`__name__="cpu_used"`:                    {CurrentStatus: types.StatusCritical, StatusDescription: "Current value: 95.00 threshold (90.00) exceeded over last 1 minute"},
				`__name__="disk_used_perc",item="/home"`: {CurrentStatus: types.StatusWarning, StatusDescription: "Current value: 95.00 threshold (80.00) exceeded over last 1 minute"},
				`__name__="disk_used_perc",item="/srv"`:  {CurrentStatus: types.StatusUnset},
				`__name__="mem_used"`:                    {CurrentStatus: types.StatusUnset},
				`__name__="net_used",item="eth0"`:        {CurrentStatus: types.StatusUnset},
			},
		},
		{
			AddedToT0: 40 * stepDelay,
			PushedValue: map[string]float64{
				`__name__="cpu_used"`:                    95,
				`__name__="mem_used"`:                    95,
				`__name__="net_used",item="eth0"`:        95,
				`__name__="disk_used_perc",item="/home"`: 95,
				`__name__="disk_used_perc",item="/srv"`:  95,
			},
			WantedPoints: map[string]types.StatusDescription{
				`__name__="cpu_used"`:                    {CurrentStatus: types.StatusCritical, StatusDescription: "Current value: 95.00 threshold (90.00) exceeded over last 1 minute"},
				`__name__="disk_used_perc",item="/home"`: {CurrentStatus: types.StatusWarning, StatusDescription: "Current value: 95.00 threshold (80.00) exceeded over last 1 minute"},
				`__name__="disk_used_perc",item="/srv"`:  {CurrentStatus: types.StatusUnset},
				`__name__="mem_used"`:                    {CurrentStatus: types.StatusUnset},
				`__name__="net_used",item="eth0"`:        {CurrentStatus: types.StatusUnset},
			},
		},
		{
			AddedToT0: 41 * stepDelay,
			SetThresholds: &setThresholdsArgs{
				thresholdWithItem: map[string]Threshold{
					`__name__="disk_used_perc",item="/home"`: {
						HighWarning:   80,
						HighCritical:  90,
						LowCritical:   math.NaN(),
						LowWarning:    math.NaN(),
						WarningDelay:  60 * time.Second,
						CriticalDelay: 60 * time.Second,
					},
				},
				thresholdAllItem: map[string]Threshold{
					"cpu_used": {
						HighWarning:   80,
						HighCritical:  99,
						LowCritical:   math.NaN(),
						LowWarning:    math.NaN(),
						WarningDelay:  60 * time.Second,
						CriticalDelay: 60 * time.Second,
					},
					"net_used": {
						HighWarning:   80,
						HighCritical:  99,
						LowCritical:   math.NaN(),
						LowWarning:    math.NaN(),
						WarningDelay:  60 * time.Second,
						CriticalDelay: 60 * time.Second,
					},
				},
			},
			PushedValue: map[string]float64{
				`__name__="cpu_used"`:                    95,
				`__name__="mem_used"`:                    95,
				`__name__="net_used",item="eth0"`:        95,
				`__name__="disk_used_perc",item="/home"`: 95,
				`__name__="disk_used_perc",item="/srv"`:  95,
			},
			WantedPoints: map[string]types.StatusDescription{
				`__name__="cpu_used"`:                    {CurrentStatus: types.StatusWarning, StatusDescription: "Current value: 95.00 threshold (80.00) exceeded over last 1 minute"},
				`__name__="disk_used_perc",item="/home"`: {CurrentStatus: types.StatusCritical, StatusDescription: "Current value: 95.00 threshold (90.00) exceeded over last 1 minute"},
				`__name__="disk_used_perc",item="/srv"`:  {CurrentStatus: types.StatusUnset},
				`__name__="mem_used"`:                    {CurrentStatus: types.StatusUnset},
				`__name__="net_used",item="eth0"`:        {CurrentStatus: types.StatusWarning, StatusDescription: "Current value: 95.00 threshold (80.00) exceeded over last 1 minute"},
			},
		},
		{
			AddedToT0: 42 * stepDelay,
			SetThresholds: &setThresholdsArgs{
				thresholdWithItem: map[string]Threshold{
					`__name__="disk_used_perc",item="/home"`: {
						HighWarning:   80,
						HighCritical:  97,
						LowCritical:   math.NaN(),
						LowWarning:    math.NaN(),
						WarningDelay:  60 * time.Second,
						CriticalDelay: 60 * time.Second,
					},
				},
				thresholdAllItem: map[string]Threshold{
					"net_used": {
						HighWarning:   math.NaN(),
						HighCritical:  99,
						LowCritical:   math.NaN(),
						LowWarning:    math.NaN(),
						WarningDelay:  60 * time.Second,
						CriticalDelay: 60 * time.Second,
					},
				},
			},
			PushedValue: map[string]float64{
				`__name__="cpu_used"`:                    95,
				`__name__="mem_used"`:                    95,
				`__name__="net_used",item="eth0"`:        95,
				`__name__="disk_used_perc",item="/home"`: 95,
				`__name__="disk_used_perc",item="/srv"`:  95,
			},
			WantedPoints: map[string]types.StatusDescription{
				`__name__="cpu_used"`:                    {CurrentStatus: types.StatusUnset},
				`__name__="disk_used_perc",item="/home"`: {CurrentStatus: types.StatusWarning, StatusDescription: "Current value: 95.00 threshold (80.00) exceeded over last 1 minute"},
				`__name__="disk_used_perc",item="/srv"`:  {CurrentStatus: types.StatusUnset},
				`__name__="mem_used"`:                    {CurrentStatus: types.StatusUnset},
				`__name__="net_used",item="eth0"`:        {CurrentStatus: types.StatusOk, StatusDescription: "Current value: 95.00"},
			},
		},
	}

	for _, step := range steps {
		currentTime := t0.Add(step.AddedToT0)

		if step.SetThresholds != nil {
			threshold.SetThresholds("fake_id", step.SetThresholds.thresholdWithItem, step.SetThresholds.thresholdAllItem)
		}

		threshold.nowFunc = func() time.Time { return currentTime }

		points := make([]types.MetricPoint, 0, len(step.PushedValue))

		for name, value := range step.PushedValue {
			lbls := types.TextToLabels(name)

			points = append(points, types.MetricPoint{
				Labels: lbls,
				Point:  types.Point{Time: currentTime, Value: value},
			})
		}

		newPoints, statusPoints := threshold.ApplyThresholds(points)
		newPoints = append(newPoints, statusPoints...)

		moreWant := make(map[string]types.StatusDescription)
		for name, pts := range step.WantedPoints {
			moreWant[name] = pts

			if !pts.CurrentStatus.IsSet() {
				continue
			}

			lbls := types.TextToLabels(name)
			lbls[types.LabelName] += statusMetricSuffix

			moreWant[types.LabelsToText(lbls)] = pts
		}

		for _, pts := range newPoints {
			want, ok := moreWant[types.LabelsToText(pts.Labels)]
			if !ok {
				t.Errorf("At %d * stepDelay: got point %v, expected not present", step.AddedToT0/stepDelay, pts.Labels)

				continue
			}

			if diff := cmp.Diff(want, pts.Annotations.Status); diff != "" {
				t.Errorf("At %d * stepDelay: points %v mismatch: (-want +got)\n%s", step.AddedToT0/stepDelay, pts.Labels, diff)
			}
		}

		if len(newPoints) != len(moreWant) {
			t.Errorf("At %v * stepDelay: got %d points, want %d", step.AddedToT0/stepDelay, len(newPoints), len(moreWant))
		}
	}
}

// TestThresholdRestart test behavior of threshold after a Glouton restart.
func TestThresholdRestart(t *testing.T) {
	t0 := time.Date(2020, 2, 24, 15, 1, 0, 0, time.UTC)

	threshold := New(mockState{
		jsonList: []jsonState{
			{
				statusState: statusState{
					CurrentStatus: types.StatusOk,
					CriticalSince: t0.Add(-30 * time.Second),
					WarningSince:  t0.Add(-40 * time.Second),
					LastUpdate:    t0.Add(-30 * time.Second),
				},
				LabelsText: `__name__="cpu_used"`,
			},
			{
				statusState: statusState{
					CurrentStatus: types.StatusWarning,
					CriticalSince: t0.Add(-80 * time.Second),
					WarningSince:  t0.Add(-90 * time.Second),
					LastUpdate:    t0.Add(-80 * time.Second),
				},
				LabelsText: `__name__="disk_used_perc",item="/home"`,
			},
			{
				statusState: statusState{
					CurrentStatus: types.StatusWarning,
					CriticalSince: t0.Add(-30 * time.Second),
					WarningSince:  t0.Add(-70 * time.Second),
					LastUpdate:    t0.Add(-30 * time.Second),
				},
				LabelsText: `__name__="mem_used"`,
			},
		},
	})

	stepDelay := 10 * time.Second

	type setThresholdsArgs struct {
		thresholdWithItem map[string]Threshold
		thresholdAllItem  map[string]Threshold
	}

	steps := []struct {
		AddedToT0   time.Duration
		PushedValue map[string]float64
		// WantedPoints will be processed to add an _status version of any points with CurrentStatus != StatusUnset
		WantedPoints  map[string]types.StatusDescription
		SetThresholds *setThresholdsArgs
	}{
		{
			AddedToT0: 0 * stepDelay,
			SetThresholds: &setThresholdsArgs{
				thresholdWithItem: map[string]Threshold{
					`__name__="disk_used_perc",item="/home"`: {
						HighWarning:   80,
						HighCritical:  90,
						LowCritical:   math.NaN(),
						LowWarning:    math.NaN(),
						WarningDelay:  60 * time.Second,
						CriticalDelay: 60 * time.Second,
					},
				},
				thresholdAllItem: map[string]Threshold{"cpu_used": {
					HighWarning:   80,
					HighCritical:  90,
					LowCritical:   math.NaN(),
					LowWarning:    math.NaN(),
					WarningDelay:  60 * time.Second,
					CriticalDelay: 60 * time.Second,
				}},
			},
			PushedValue: map[string]float64{
				`__name__="cpu_used"`:                    95,
				`__name__="mem_used"`:                    95,
				`__name__="disk_used_perc",item="/home"`: 95,
				`__name__="disk_used_perc",item="/srv"`:  95,
			},
			WantedPoints: map[string]types.StatusDescription{
				`__name__="cpu_used"`:                    {CurrentStatus: types.StatusOk, StatusDescription: "Current value: 95.00"},
				`__name__="disk_used_perc",item="/home"`: {CurrentStatus: types.StatusCritical, StatusDescription: "Current value: 95.00 threshold (90.00) exceeded over last 1 minute"},
				`__name__="disk_used_perc",item="/srv"`:  {CurrentStatus: types.StatusUnset},
				`__name__="mem_used"`:                    {CurrentStatus: types.StatusUnset},
			},
		},
		{
			AddedToT0: 1 * stepDelay,
			SetThresholds: &setThresholdsArgs{
				thresholdWithItem: map[string]Threshold{
					`__name__="disk_used_perc",item="/home"`: {
						HighWarning:   80,
						HighCritical:  90,
						LowCritical:   math.NaN(),
						LowWarning:    math.NaN(),
						WarningDelay:  60 * time.Second,
						CriticalDelay: 60 * time.Second,
					},
					`__name__="mem_used"`: {
						HighWarning:   80,
						HighCritical:  90,
						LowCritical:   math.NaN(),
						LowWarning:    math.NaN(),
						WarningDelay:  60 * time.Second,
						CriticalDelay: 60 * time.Second,
					},
				},
				thresholdAllItem: map[string]Threshold{"cpu_used": {
					HighWarning:   80,
					HighCritical:  90,
					LowCritical:   math.NaN(),
					LowWarning:    math.NaN(),
					WarningDelay:  60 * time.Second,
					CriticalDelay: 60 * time.Second,
				}},
			},
			PushedValue: map[string]float64{
				`__name__="cpu_used"`:                    95,
				`__name__="mem_used"`:                    95,
				`__name__="disk_used_perc",item="/home"`: 95,
				`__name__="disk_used_perc",item="/srv"`:  95,
			},
			WantedPoints: map[string]types.StatusDescription{
				`__name__="cpu_used"`:                    {CurrentStatus: types.StatusOk, StatusDescription: "Current value: 95.00"},
				`__name__="disk_used_perc",item="/home"`: {CurrentStatus: types.StatusCritical, StatusDescription: "Current value: 95.00 threshold (90.00) exceeded over last 1 minute"},
				`__name__="disk_used_perc",item="/srv"`:  {CurrentStatus: types.StatusUnset},
				`__name__="mem_used"`:                    {CurrentStatus: types.StatusWarning, StatusDescription: "Current value: 95.00 threshold (80.00) exceeded over last 1 minute"},
			},
		},
		{
			AddedToT0: 2 * stepDelay,
			PushedValue: map[string]float64{
				`__name__="cpu_used"`:                    95,
				`__name__="mem_used"`:                    95,
				`__name__="disk_used_perc",item="/home"`: 95,
				`__name__="disk_used_perc",item="/srv"`:  95,
			},
			WantedPoints: map[string]types.StatusDescription{
				`__name__="cpu_used"`:                    {CurrentStatus: types.StatusWarning, StatusDescription: "Current value: 95.00 threshold (80.00) exceeded over last 1 minute"},
				`__name__="disk_used_perc",item="/home"`: {CurrentStatus: types.StatusCritical, StatusDescription: "Current value: 95.00 threshold (90.00) exceeded over last 1 minute"},
				`__name__="disk_used_perc",item="/srv"`:  {CurrentStatus: types.StatusUnset},
				`__name__="mem_used"`:                    {CurrentStatus: types.StatusWarning, StatusDescription: "Current value: 95.00 threshold (80.00) exceeded over last 1 minute"},
			},
		},
		{
			AddedToT0: 3 * stepDelay,
			PushedValue: map[string]float64{
				`__name__="cpu_used"`:                    95,
				`__name__="mem_used"`:                    95,
				`__name__="disk_used_perc",item="/home"`: 95,
				`__name__="disk_used_perc",item="/srv"`:  95,
			},
			WantedPoints: map[string]types.StatusDescription{
				`__name__="cpu_used"`:                    {CurrentStatus: types.StatusCritical, StatusDescription: "Current value: 95.00 threshold (90.00) exceeded over last 1 minute"},
				`__name__="disk_used_perc",item="/home"`: {CurrentStatus: types.StatusCritical, StatusDescription: "Current value: 95.00 threshold (90.00) exceeded over last 1 minute"},
				`__name__="disk_used_perc",item="/srv"`:  {CurrentStatus: types.StatusUnset},
				`__name__="mem_used"`:                    {CurrentStatus: types.StatusCritical, StatusDescription: "Current value: 95.00 threshold (90.00) exceeded over last 1 minute"},
			},
		},
		{
			AddedToT0: 30 * stepDelay,
			PushedValue: map[string]float64{
				`__name__="cpu_used"`:                    95,
				`__name__="mem_used"`:                    95,
				`__name__="disk_used_perc",item="/home"`: 95,
				`__name__="disk_used_perc",item="/srv"`:  95,
			},
			WantedPoints: map[string]types.StatusDescription{
				`__name__="cpu_used"`:                    {CurrentStatus: types.StatusCritical, StatusDescription: "Current value: 95.00 threshold (90.00) exceeded over last 1 minute"},
				`__name__="disk_used_perc",item="/home"`: {CurrentStatus: types.StatusCritical, StatusDescription: "Current value: 95.00 threshold (90.00) exceeded over last 1 minute"},
				`__name__="disk_used_perc",item="/srv"`:  {CurrentStatus: types.StatusUnset},
				`__name__="mem_used"`:                    {CurrentStatus: types.StatusCritical, StatusDescription: "Current value: 95.00 threshold (90.00) exceeded over last 1 minute"},
			},
		},
	}

	for _, step := range steps {
		currentTime := t0.Add(step.AddedToT0)

		if step.SetThresholds != nil {
			threshold.SetThresholds("fake_id", step.SetThresholds.thresholdWithItem, step.SetThresholds.thresholdAllItem)
		}

		threshold.nowFunc = func() time.Time { return currentTime }

		points := make([]types.MetricPoint, 0, len(step.PushedValue))

		for name, value := range step.PushedValue {
			lbls := types.TextToLabels(name)

			points = append(points, types.MetricPoint{
				Labels: lbls,
				Point:  types.Point{Time: currentTime, Value: value},
			})
		}

		newPoints, statusPoints := threshold.ApplyThresholds(points)
		newPoints = append(newPoints, statusPoints...)

		moreWant := make(map[string]types.StatusDescription)
		for name, pts := range step.WantedPoints {
			moreWant[name] = pts

			if !pts.CurrentStatus.IsSet() {
				continue
			}

			lbls := types.TextToLabels(name)
			lbls[types.LabelName] += statusMetricSuffix

			moreWant[types.LabelsToText(lbls)] = pts
		}

		for _, pts := range newPoints {
			want, ok := moreWant[types.LabelsToText(pts.Labels)]
			if !ok {
				t.Errorf("At %d * stepDelay: got point %v, expected not present", step.AddedToT0/stepDelay, pts.Labels)

				continue
			}

			if diff := cmp.Diff(want, pts.Annotations.Status); diff != "" {
				t.Errorf("At %d * stepDelay: points %v mismatch: (-want +got)\n%s", step.AddedToT0/stepDelay, pts.Labels, diff)
			}
		}

		if len(newPoints) != len(moreWant) {
			t.Errorf("At %d * stepDelay: got %d points, want %d", step.AddedToT0/stepDelay, len(newPoints), len(moreWant))
		}
	}
}

func TestMergeThresholds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		t1     Threshold
		t2     Threshold
		expect Threshold
	}{
		{
			name: "different-delays",
			t1: Threshold{
				LowCritical:   15,
				LowWarning:    20,
				WarningDelay:  2 * time.Minute,
				HighWarning:   80,
				HighCritical:  90,
				CriticalDelay: 2 * time.Minute,
			},
			t2: Threshold{
				LowCritical:   5,
				LowWarning:    10,
				WarningDelay:  1 * time.Minute,
				HighWarning:   50,
				HighCritical:  60,
				CriticalDelay: 1 * time.Minute,
			},
			expect: Threshold{
				LowCritical:   15,
				LowWarning:    20,
				WarningDelay:  1 * time.Minute,
				HighWarning:   50,
				HighCritical:  60,
				CriticalDelay: 1 * time.Minute,
			},
		},
		{
			name: "different-delays-2",
			t1: Threshold{
				LowCritical:   15,
				LowWarning:    20,
				WarningDelay:  10 * time.Minute,
				HighWarning:   80,
				HighCritical:  90,
				CriticalDelay: 20 * time.Minute,
			},
			t2: Threshold{
				LowCritical:   5,
				LowWarning:    10,
				WarningDelay:  5 * time.Minute,
				HighWarning:   81,
				HighCritical:  82,
				CriticalDelay: 2 * time.Minute,
			},
			expect: Threshold{
				LowCritical:   15,
				LowWarning:    20,
				WarningDelay:  10 * time.Minute,
				HighWarning:   80,
				HighCritical:  82,
				CriticalDelay: 2 * time.Minute,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := test.t1.Merge(test.t2); !reflect.DeepEqual(got, test.expect) {
				t.Fatalf("Merge\n%#v\nwith\n%#v\nexpected\n%#v\ngot\n%#v\n", test.t1, test.t2, test.expect, got)
			}
		})
	}
}

func TestThresholdsFromConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		Name              string
		Config            config.Threshold
		MetricName        string
		SoftPeriods       map[string]time.Duration
		DefaultSoftPeriod time.Duration
		Expected          Threshold
	}{
		{
			Name: "full",
			Config: config.Threshold{
				LowCritical:  newFloatPointer(1),
				LowWarning:   newFloatPointer(5),
				HighWarning:  newFloatPointer(70),
				HighCritical: newFloatPointer(80),
			},
			MetricName:        "",
			SoftPeriods:       nil,
			DefaultSoftPeriod: time.Second,
			Expected: Threshold{
				LowCritical:   1,
				LowWarning:    5,
				HighWarning:   70,
				HighCritical:  80,
				WarningDelay:  time.Second,
				CriticalDelay: time.Second,
			},
		},
		{
			Name: "only high",
			Config: config.Threshold{
				LowCritical:  nil,
				LowWarning:   nil,
				HighWarning:  newFloatPointer(70),
				HighCritical: newFloatPointer(80),
			},
			MetricName: "cpu_used",
			SoftPeriods: map[string]time.Duration{
				"cpu_used": time.Hour,
			},
			DefaultSoftPeriod: 300,
			Expected: Threshold{
				LowCritical:   math.NaN(),
				LowWarning:    math.NaN(),
				HighWarning:   70,
				HighCritical:  80,
				WarningDelay:  time.Hour,
				CriticalDelay: time.Hour,
			},
		},
		{
			Name: "only low",
			Config: config.Threshold{
				LowCritical:  newFloatPointer(1),
				LowWarning:   newFloatPointer(5.8),
				HighWarning:  nil,
				HighCritical: nil,
			},
			MetricName:        "cpu_used",
			SoftPeriods:       nil,
			DefaultSoftPeriod: time.Second,
			Expected: Threshold{
				LowCritical:   1,
				LowWarning:    5.8,
				HighWarning:   math.NaN(),
				HighCritical:  math.NaN(),
				WarningDelay:  time.Second,
				CriticalDelay: time.Second,
			},
		},
		{
			Name: "not set",
			Config: config.Threshold{
				LowCritical:  nil,
				LowWarning:   nil,
				HighWarning:  nil,
				HighCritical: nil,
			},
			MetricName:        "cpu_used",
			SoftPeriods:       nil,
			DefaultSoftPeriod: time.Second,
			Expected: Threshold{
				LowCritical:   math.NaN(),
				LowWarning:    math.NaN(),
				HighWarning:   math.NaN(),
				HighCritical:  math.NaN(),
				WarningDelay:  time.Second,
				CriticalDelay: time.Second,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			t.Parallel()

			got := FromConfig(test.Config, test.MetricName, test.SoftPeriods, test.DefaultSoftPeriod)
			if diff := cmp.Diff(test.Expected, got); diff != "" {
				t.Fatalf("Wrong threshold from config:\n%s", diff)
			}
		})
	}
}

func newFloatPointer(value float64) *float64 {
	p := new(float64)
	*p = value

	return p
}
