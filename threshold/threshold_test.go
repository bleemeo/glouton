// Copyright 2015-2019 Bleemeo
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
	"glouton/types"
	"math"
	"reflect"
	"testing"
	"time"
)

type mockState struct{}

func (m mockState) Get(key string, result interface{}) error {
	return nil
}

func (m mockState) Set(key string, object interface{}) error {
	return nil
}

type mockStore struct {
	points []types.MetricPoint
}

func (s *mockStore) PushPoints(points []types.MetricPoint) {
	s.points = append(s.points, points...)
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
			state = state.Update(step.status, 300*time.Second, now.Add(time.Duration(step.timeOffsetSecond)*time.Second))
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
			state = state.Update(step.status, time.Duration(step.period)*time.Second, now.Add(time.Duration(step.timeOffsetSecond)*time.Second))
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
	}
	for _, c := range cases {
		got := formatDuration(c.value)
		if got != c.want {
			t.Errorf("formatValue(%v) == %v, want %v", c.value, got, c.want)
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
	db := &mockStore{}
	threshold := New(mockState{})
	threshold.SetThresholds(
		nil,
		map[string]Threshold{"cpu_used": {
			HighWarning:  80,
			HighCritical: 90,
		}},
	)

	t0 := time.Date(2020, 2, 24, 15, 1, 0, 0, time.UTC)
	wantPoints := map[string]types.MetricPoint{
		`__name__="cpu_idle"`: {
			Annotations: types.MetricAnnotations{
				BleemeoItem: "some-item",
			},
			Labels: map[string]string{types.LabelName: "cpu_idle"},
			Point: types.Point{
				Time:  t0,
				Value: 20.0,
			},
		},
		`__name__="cpu_used"`: {
			Annotations: types.MetricAnnotations{
				BleemeoItem: "some-item",
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
				BleemeoItem: "some-item",
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

	pusher := threshold.WithPusher(db)
	pusher.PushPoints([]types.MetricPoint{
		{
			Labels: map[string]string{
				"__name__": "cpu_used",
			},
			Annotations: types.MetricAnnotations{BleemeoItem: "some-item"},
			Point:       types.Point{Time: t0, Value: 88.0},
		},
		{
			Labels: map[string]string{
				"__name__": "cpu_idle",
			},
			Annotations: types.MetricAnnotations{BleemeoItem: "some-item"},
			Point:       types.Point{Time: t0, Value: 20.0},
		},
	})

	if len(db.points) != 3 {
		t.Errorf("len(points) == %d, want 3", len(db.points))
	}

	for i, got := range db.points {
		labelsText := types.LabelsToText(got.Labels)
		want := wantPoints[labelsText]

		delete(wantPoints, labelsText)

		if !reflect.DeepEqual(got, want) {
			t.Errorf("points[%d] = %v, want %v", i, got, want)
		}
	}
}
