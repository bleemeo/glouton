package threshold

import (
	"agentgo/types"
	"testing"
	"time"
)

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
		got := formatValue(c.value, c.unit)
		if got != c.want {
			t.Errorf("formatValue(%v, %v) == %v, want %v", c.value, c.unit, got, c.want)
		}
	}
}
