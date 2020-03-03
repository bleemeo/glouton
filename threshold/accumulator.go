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
	"context"
	"fmt"
	"glouton/agent/state"
	"glouton/logger"
	"glouton/types"
	"math"
	"reflect"
	"sync"
	"time"

	"github.com/influxdata/telegraf"
)

const statusCacheKey = "CacheStatusState"

// StatusAccumulator is the type used by Accumulator to send metrics. It's a subset of store.Accumulator
type StatusAccumulator interface {
	AddFieldsWithStatus(measurement string, fields map[string]interface{}, tags map[string]string, statuses map[string]types.StatusDescription, createStatusOf bool, t ...time.Time)
	AddFields(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time)
	AddError(err error)
}

// Accumulator implement telegraf.Accumulator (+AddFieldsWithStatus, cf store package) but check threshold and
// emit the metric points with a status
type Accumulator struct {
	acc   StatusAccumulator
	state *state.State

	l                 sync.Mutex
	states            map[MetricNameItem]statusState
	units             map[MetricNameItem]Unit
	thresholdsAllItem map[string]Threshold
	thresholds        map[MetricNameItem]Threshold
	defaultSoftPeriod time.Duration
	softPeriods       map[string]time.Duration
}

// New returns a new Accumulator
func New(acc StatusAccumulator, state *state.State) *Accumulator {
	self := &Accumulator{
		acc:               acc,
		state:             state,
		states:            make(map[MetricNameItem]statusState),
		defaultSoftPeriod: 300 * time.Second,
	}

	var jsonList []jsonState

	err := state.Get(statusCacheKey, &jsonList)
	if err != nil {
		for _, v := range jsonList {
			self.states[v.MetricNameItem] = v.statusState
		}
	}

	return self
}

// SetThresholds configure thresholds.
// The thresholdWithItem is searched first and only match if both the metric name and item match
// Then thresholdAllItem is searched and match is the metric name match regardless of the metric item.
func (a *Accumulator) SetThresholds(thresholdWithItem map[MetricNameItem]Threshold, thresholdAllItem map[string]Threshold) {
	a.l.Lock()
	defer a.l.Unlock()

	a.thresholdsAllItem = thresholdAllItem
	a.thresholds = thresholdWithItem

	logger.V(2).Printf("Thresholds contains %d definitions for specific item and %d definitions for any item", len(thresholdWithItem), len(thresholdAllItem))
}

// SetSoftPeriod configure soft status period. A metric must stay in higher status for at least this period before its status actually change.
// For example, CPU usage must be above 80% for at least 5 minutes before being alerted. The term soft-status is taken from Nagios.
func (a *Accumulator) SetSoftPeriod(defaultPeriod time.Duration, periodPerMetrics map[string]time.Duration) {
	a.l.Lock()
	defer a.l.Unlock()

	a.softPeriods = periodPerMetrics
	a.defaultSoftPeriod = defaultPeriod

	logger.V(2).Printf("SoftPeriod contains %d definitions", len(periodPerMetrics))
}

// SetUnits configure the units
func (a *Accumulator) SetUnits(units map[MetricNameItem]Unit) {
	a.l.Lock()
	defer a.l.Unlock()

	a.units = units

	logger.V(2).Printf("Units contains %d definitions", len(units))
}

// AddFields adds a metric to the accumulator with the given measurement
// name, fields, and tags (and timestamp). If a timestamp is not provided,
// then the accumulator sets it to "now".
// Create a point with a value, decorating it with tags
// NOTE: tags is expected to be owned by the caller, don't mutate
// it after passing to Add.
func (a *Accumulator) AddFields(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.addMetrics(measurement, fields, tags, t...)
}

// AddGauge is the same as AddFields, but will add the metric as a "Gauge" type
func (a *Accumulator) AddGauge(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.addMetrics(measurement, fields, tags, t...)
}

// AddCounter is the same as AddFields, but will add the metric as a "Counter" type
func (a *Accumulator) AddCounter(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.addMetrics(measurement, fields, tags, t...)
}

// AddSummary is the same as AddFields, but will add the metric as a "Summary" type
func (a *Accumulator) AddSummary(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.addMetrics(measurement, fields, tags, t...)
}

// AddHistogram is the same as AddFields, but will add the metric as a "Histogram" type
func (a *Accumulator) AddHistogram(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.addMetrics(measurement, fields, tags, t...)
}

// SetPrecision do nothing right now
func (a *Accumulator) SetPrecision(precision time.Duration) {
	a.AddError(fmt.Errorf("SetPrecision not implemented"))
}

// AddMetric is not yet implemented
func (a *Accumulator) AddMetric(telegraf.Metric) {
	a.AddError(fmt.Errorf("AddMetric not implemented"))
}

// WithTracking is not yet implemented
func (a *Accumulator) WithTracking(maxTracked int) telegraf.TrackingAccumulator {
	a.AddError(fmt.Errorf("WithTracking not implemented"))
	return nil
}

// AddError add an error to the Accumulator
func (a *Accumulator) AddError(err error) {
	if a.acc != nil {
		a.acc.AddError(err)
		return
	}

	if err != nil {
		logger.V(1).Printf("Add error called with: %v", err)
	}
}

// AddFieldsWithStatus is the same as store.Accumulator.AddFieldsWithStatus
func (a *Accumulator) AddFieldsWithStatus(measurement string, fields map[string]interface{}, tags map[string]string, statuses map[string]types.StatusDescription, createStatusOf bool, t ...time.Time) {
	a.acc.AddFieldsWithStatus(measurement, fields, tags, statuses, createStatusOf, t...)
}

// MetricNameItem is the couple Name and Item
type MetricNameItem struct {
	Name string
	Item string
}

type statusState struct {
	CurrentStatus types.Status
	CriticalSince time.Time
	WarningSince  time.Time
	LastUpdate    time.Time
}

type jsonState struct {
	MetricNameItem
	statusState
}

func (s statusState) Update(newStatus types.Status, period time.Duration, now time.Time) statusState {
	if s.CurrentStatus == types.StatusUnset {
		s.CurrentStatus = newStatus
	}

	// Make sure time didn't jump backward.
	if s.CriticalSince.After(now) {
		s.CriticalSince = time.Time{}
	}

	if s.WarningSince.After(now) {
		s.WarningSince = time.Time{}
	}

	criticalDuration := time.Duration(0)
	warningDuration := time.Duration(0)

	switch newStatus {
	case types.StatusCritical:
		if s.CriticalSince.IsZero() {
			s.CriticalSince = now
		}

		if s.WarningSince.IsZero() {
			s.WarningSince = now
		}

		criticalDuration = now.Sub(s.CriticalSince)
		warningDuration = now.Sub(s.WarningSince)
	case types.StatusWarning:
		s.CriticalSince = time.Time{}

		if s.WarningSince.IsZero() {
			s.WarningSince = now
		}

		warningDuration = now.Sub(s.WarningSince)
	default:
		s.CriticalSince = time.Time{}
		s.WarningSince = time.Time{}
	}

	switch {
	case period == 0:
		s.CurrentStatus = newStatus
	case criticalDuration >= period:
		s.CurrentStatus = types.StatusCritical
	case warningDuration >= period:
		s.CurrentStatus = types.StatusWarning
	case s.CurrentStatus == types.StatusCritical && newStatus == types.StatusWarning:
		// downgrade status immediately
		s.CurrentStatus = types.StatusWarning
	case newStatus == types.StatusOk:
		// downgrade status immediately
		s.CurrentStatus = types.StatusOk
	}

	s.LastUpdate = time.Now()

	return s
}

// Threshold define a min/max thresholds
// Use NaN to mark the limit as unset
type Threshold struct {
	LowCritical  float64
	LowWarning   float64
	HighWarning  float64
	HighCritical float64
}

// Equal test equality of threhold object
func (t Threshold) Equal(other Threshold) bool {
	if t == other {
		return true
	}
	// Need special handling for NaN
	if t.LowCritical != other.LowCritical && (!math.IsNaN(t.LowCritical) || !math.IsNaN(other.LowCritical)) {
		return false
	}

	if t.LowWarning != other.LowWarning && (!math.IsNaN(t.LowWarning) || !math.IsNaN(other.LowWarning)) {
		return false
	}

	if t.HighWarning != other.HighWarning && (!math.IsNaN(t.HighWarning) || !math.IsNaN(other.HighWarning)) {
		return false
	}

	if t.HighCritical != other.HighCritical && (!math.IsNaN(t.HighCritical) || !math.IsNaN(other.HighCritical)) {
		return false
	}

	return true
}

// Unit represent the unit of a metric
type Unit struct {
	UnitType int    `json:"unit,omitempty"`
	UnitText string `json:"unit_text,omitempty"`
}

// Possible value for UnitType
const (
	UnitTypeUnit = 0
	UnitTypeByte = 2
	UnitTypeBit  = 3
)

// FromInterfaceMap convert a map[string]interface{} to Threshold.
// It expect the key "low_critical", "low_warning", "high_critical" and "high_warning"
func FromInterfaceMap(input map[string]interface{}) (Threshold, error) {
	result := Threshold{
		LowCritical:  math.NaN(),
		LowWarning:   math.NaN(),
		HighWarning:  math.NaN(),
		HighCritical: math.NaN(),
	}

	for _, name := range []string{"low_critical", "low_warning", "high_warning", "high_critical"} {
		if raw, ok := input[name]; ok {
			var value float64

			switch v := raw.(type) {
			case float64:
				value = v
			case int:
				value = float64(v)
			default:
				return result, fmt.Errorf("%v is not a float", raw)
			}

			switch name {
			case "low_critical":
				result.LowCritical = value
			case "low_warning":
				result.LowWarning = value
			case "high_warning":
				result.HighWarning = value
			case "high_critical":
				result.HighCritical = value
			}
		}
	}

	return result, nil
}

// IsZero returns true is all threshold limit are unset (NaN)
// Is also returns true is all threshold are equal and 0 (which is the zero-value of Threshold structure
// and is an invalid threshold configuration)
func (t Threshold) IsZero() bool {
	if math.IsNaN(t.LowCritical) && math.IsNaN(t.LowWarning) && math.IsNaN(t.HighWarning) && math.IsNaN(t.HighCritical) {
		return true
	}

	return t.LowCritical == 0.0 && t.LowWarning == 0.0 && t.HighWarning == 0.0 && t.HighCritical == 0.0
}

// CurrentStatus returns the current status regarding the threshold and (if not ok) return the exceeded limit
func (t Threshold) CurrentStatus(value float64) (types.Status, float64) {
	if !math.IsNaN(t.LowCritical) && value < t.LowCritical {
		return types.StatusCritical, t.LowCritical
	}

	if !math.IsNaN(t.LowWarning) && value < t.LowWarning {
		return types.StatusWarning, t.LowWarning
	}

	if !math.IsNaN(t.HighCritical) && value > t.HighCritical {
		return types.StatusCritical, t.HighCritical
	}

	if !math.IsNaN(t.HighWarning) && value > t.HighWarning {
		return types.StatusWarning, t.HighWarning
	}

	return types.StatusOk, math.NaN()
}

// GetThreshold return the current threshold for given Metric
func (a *Accumulator) GetThreshold(key MetricNameItem) Threshold {
	a.l.Lock()
	defer a.l.Unlock()

	return a.getThreshold(key)
}

func (a *Accumulator) getThreshold(key MetricNameItem) Threshold {
	if threshold, ok := a.thresholds[key]; ok {
		return threshold
	}

	v := a.thresholdsAllItem[key.Name]
	if v.IsZero() {
		return Threshold{
			LowCritical:  math.NaN(),
			LowWarning:   math.NaN(),
			HighWarning:  math.NaN(),
			HighCritical: math.NaN(),
		}
	}

	return v
}

// convertInterface convert the interface type in float64
func convertInterface(value interface{}) (float64, error) {
	switch value := value.(type) {
	case uint64:
		return float64(value), nil
	case float64:
		return value, nil
	case int:
		return float64(value), nil
	case int64:
		return float64(value), nil
	default:
		var valueType = reflect.TypeOf(value)
		return float64(0), fmt.Errorf("value type not supported :(%v)", valueType)
	}
}

// Run will periodically save status state and clean it.
func (a *Accumulator) Run(ctx context.Context) error {
	lastSave := time.Now()

	for ctx.Err() == nil {
		save := false

		select {
		case <-ctx.Done():
			save = true
		case <-time.After(time.Minute):
		}

		if time.Since(lastSave) > 60*time.Minute {
			save = true
		}

		a.run(save)

		if save {
			lastSave = time.Now()
		}
	}

	return nil
}

func (a *Accumulator) run(save bool) {
	a.l.Lock()
	defer a.l.Unlock()

	jsonList := make([]jsonState, 0, len(a.states))

	for k, v := range a.states {
		if time.Since(v.LastUpdate) > 60*time.Minute {
			delete(a.states, k)
		} else {
			jsonList = append(jsonList, jsonState{
				MetricNameItem: k,
				statusState:    v,
			})
		}
	}

	if save {
		_ = a.state.Set(statusCacheKey, jsonList)
	}
}

func formatValue(value float64, unit Unit) string {
	switch unit.UnitType {
	case UnitTypeUnit:
		return fmt.Sprintf("%.2f", value)
	case UnitTypeBit, UnitTypeByte:
		scales := []string{"", "K", "M", "G", "T", "P", "E"}

		i := 0
		for i < len(scales)-1 && math.Abs(value) >= 1024 {
			i++

			value /= 1024
		}

		return fmt.Sprintf("%.2f %s%ss", value, scales[i], unit.UnitText)
	default:
		return fmt.Sprintf("%.2f %s", value, unit.UnitText)
	}
}

func formatDuration(period time.Duration) string {
	if period <= 0 {
		return ""
	}

	units := []struct {
		Scale float64
		Name  string
	}{
		{1, "second"},
		{60, "minute"},
		{60, "hour"},
		{24, "day"},
	}

	currentUnit := ""
	value := period.Seconds()

	for _, unit := range units {
		if math.Round(value/unit.Scale) >= 1 {
			value /= unit.Scale
			currentUnit = unit.Name
		} else {
			break
		}
	}

	value = math.Round(value)
	if value > 1 {
		currentUnit += "s"
	}

	return fmt.Sprintf("%.0f %s", value, currentUnit)
}

func (a *Accumulator) addMetrics(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.l.Lock()
	defer a.l.Unlock()

	statuses := make(map[string]types.StatusDescription)

	for name, value := range fields {
		flatName := measurement + "_" + name
		if measurement == "" {
			flatName = name
		}

		key := MetricNameItem{Name: flatName, Item: tags["item"]}

		threshold := a.getThreshold(key)
		if threshold.IsZero() {
			continue
		}

		valueF, err := convertInterface(value)
		if err != nil {
			continue
		}

		softStatus, thresholdLimit := threshold.CurrentStatus(valueF)
		previousState := a.states[key]
		period := a.defaultSoftPeriod

		if tmp, ok := a.softPeriods[key.Name]; ok {
			period = tmp
		}

		newState := previousState.Update(softStatus, period, time.Now())
		a.states[key] = newState

		unit := a.units[key]
		// Consumer expect status description from threshold to start with "Current value:"
		statusDescription := fmt.Sprintf("Current value: %s", formatValue(valueF, unit))

		if newState.CurrentStatus != types.StatusOk {
			if period > 0 {
				statusDescription += fmt.Sprintf(
					" threshold (%s) exceeded over last %v",
					formatValue(thresholdLimit, unit),
					formatDuration(period),
				)
			} else {
				statusDescription += fmt.Sprintf(
					" threshold (%s) exceeded",
					formatValue(thresholdLimit, unit),
				)
			}
		}

		statuses[name] = types.StatusDescription{
			CurrentStatus:     newState.CurrentStatus,
			StatusDescription: statusDescription,
		}
	}

	a.acc.AddFieldsWithStatus(measurement, fields, tags, statuses, true, t...)
}
