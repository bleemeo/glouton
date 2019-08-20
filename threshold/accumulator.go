package threshold

import (
	"agentgo/agent/state"
	"agentgo/logger"
	"agentgo/types"
	"context"
	"fmt"
	"math"
	"reflect"
	"sync"
	"time"

	"github.com/influxdata/telegraf"
)

// StatusAccumulator is the type used by Accumulator to send metrics. It's a subset of store.Accumulator
type StatusAccumulator interface {
	AddFieldsWithStatus(measurement string, fields map[string]interface{}, tags map[string]string, statuses map[string]types.StatusDescription, createStatusOf bool, t ...time.Time)
	AddFields(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time)
}

// Accumulator implement telegraf.Accumulator (+AddFieldsWithStatus, cf store package) but check threshold and
// emit the metric points with a status
type Accumulator struct {
	acc   StatusAccumulator
	state *state.State

	l                 sync.Mutex
	states            map[MetricNameItem]statusState
	thresholdsAllItem map[string]Threshold
	thresholds        map[MetricNameItem]Threshold
}

// New returns a new Accumulator
func New(acc StatusAccumulator, state *state.State) *Accumulator {
	return &Accumulator{
		acc:    acc,
		state:  state,
		states: make(map[MetricNameItem]statusState),
	}
}

// SetThresholds configure thresholds.
// The thresholdWithItem is searched first and only match if both the metric name and item match
// Then thresholdAllItem is searched and match is the metric name match regardless of the metric item.
func (a *Accumulator) SetThresholds(thresholdWithItem map[MetricNameItem]Threshold, thresholdAllItem map[string]Threshold) {
	a.l.Lock()
	defer a.l.Unlock()
	a.thresholdsAllItem = thresholdAllItem
	a.thresholds = thresholdWithItem
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
	LastUsage     time.Time
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

// IsZero returns true is all threshold limit are unset (NaN)
func (t Threshold) IsZero() bool {
	return math.IsNaN(t.LowCritical) && math.IsNaN(t.LowWarning) && math.IsNaN(t.HighWarning) && math.IsNaN(t.HighCritical)
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

func (a *Accumulator) getThreshold(key MetricNameItem) Threshold {
	if threshold, ok := a.thresholds[key]; ok {
		return threshold
	}
	return a.thresholdsAllItem[key.Name]
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
	<-ctx.Done()
	return nil
}

func formatValue(value float64, unit string) string {
	return fmt.Sprintf("%f %s", value, unit)
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
		period := 300 * time.Second // TODO
		newState := previousState.Update(softStatus, period, time.Now())
		a.states[key] = newState

		unit := "TODO"
		// Consumer expect status description from threshold to start with "Current value:"
		statusDescription := fmt.Sprintf("Current value: %s", formatValue(valueF, unit))
		if newState.CurrentStatus != types.StatusOk {
			if period > 0 {
				statusDescription += fmt.Sprintf(
					" threshold (%s) exceeded over last %v",
					formatValue(thresholdLimit, unit),
					period,
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
