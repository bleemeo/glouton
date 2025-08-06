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

package threshold

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/types"
	"github.com/bleemeo/glouton/utils/metricutils"
)

const (
	statusCacheKey     = "CacheStatusState"
	statusMetricSuffix = "_status"
	statesTTL          = 25 * time.Hour // some metrics are send once per day (like system_pending_security_updates)
)

// State store information about current firing threshold.
type State interface {
	Get(key string, result any) error
	Set(key string, object any) error
}

// Registry keep track of threshold states to update metrics if the exceed a threshold for a period
// Use WithPusher() to create a pusher and sent metric points to it.
type Registry struct {
	state State

	agentID string

	l sync.Mutex
	// Threshold states by labels text.
	states map[string]statusState
	// Units by labels text.
	units map[string]Unit
	// Thresholds by labels text.
	thresholds map[string]Threshold
	// Thresholds that apply to multiple metrics, by metric name.
	thresholdsAllItem map[string]Threshold
	nowFunc           func() time.Time
}

// New returns a new ThresholdState.
func New(state State) *Registry {
	self := &Registry{
		state:   state,
		states:  make(map[string]statusState),
		nowFunc: time.Now,
	}

	var jsonList []jsonState

	err := state.Get(statusCacheKey, &jsonList)
	if err == nil {
		for _, v := range jsonList {
			self.states[v.LabelsText] = v.statusState
		}
	}

	return self
}

// SetThresholds configure thresholds.
// The thresholdWithItem is searched first and only match if both the metric name and item match
// Then thresholdAllItem is searched and match is the metric name match regardless of the metric item.
func (r *Registry) SetThresholds(agentID string, thresholdWithItem map[string]Threshold, thresholdAllItem map[string]Threshold) {
	r.l.Lock()
	defer r.l.Unlock()

	r.agentID = agentID

	// The finality of this map is to contain only the deleted thresholds
	// to be able to delete their related status states,
	// which is no longer relevant without a threshold
	oldThresholds := make(map[string]struct{}, len(r.thresholds))

	for labelsText := range r.thresholds {
		oldThresholds[labelsText] = struct{}{}
	}

	// When threshold is *updated*, we want to immediately apply the new threshold
	changedName := make(map[string]bool)
	changedItem := make(map[string]bool)

	for metricName, threshold := range thresholdAllItem {
		old, ok := r.thresholdsAllItem[metricName]
		if ok && !threshold.Equal(old) {
			changedName[metricName] = true
		}
	}

	for labelsText, threshold := range thresholdWithItem {
		old, ok := r.thresholds[labelsText]
		if ok && !threshold.Equal(old) {
			changedItem[labelsText] = true
		}
		// Remove this threshold from the list of supposedly deleted thresholds
		delete(oldThresholds, labelsText)
	}

	r.thresholdsAllItem = thresholdAllItem
	r.thresholds = thresholdWithItem

	for labelsText, state := range r.states {
		if _, isDeleted := oldThresholds[labelsText]; isDeleted {
			delete(r.states, labelsText)

			continue
		}

		lbls := types.TextToLabels(labelsText)

		if changedItem[labelsText] || changedName[lbls[types.LabelName]] {
			state.CurrentStatus = types.StatusUnset
			state.CriticalSince = time.Time{}
			state.WarningSince = time.Time{}
			r.states[labelsText] = state
		}
	}

	logger.V(2).Printf("Thresholds contains %d definitions for specific item and %d definitions for any item", len(thresholdWithItem), len(thresholdAllItem))
}

// SetUnits configure the units.
func (r *Registry) SetUnits(units map[string]Unit) {
	r.l.Lock()
	defer r.l.Unlock()

	r.units = units

	logger.V(2).Printf("Units contains %d definitions", len(units))
}

type statusState struct { //nolint: recvcheck
	CurrentStatus types.Status
	CriticalSince time.Time
	WarningSince  time.Time
	LastUpdate    time.Time
}

type jsonState struct {
	statusState

	LabelsText string
}

func (s statusState) Update(
	newStatus types.Status,
	warningDelay, criticalDelay time.Duration,
	now time.Time,
) statusState {
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

	criticalDuration, warningDuration := s.setNewStatus(newStatus, now)

	switch {
	case newStatus == types.StatusOk:
		// downgrade status immediately
		s.CurrentStatus = types.StatusOk
	case (criticalDuration != 0 || newStatus == types.StatusCritical) && criticalDuration >= criticalDelay:
		s.CurrentStatus = types.StatusCritical
	case (warningDuration != 0 || newStatus == types.StatusWarning) && warningDuration >= warningDelay:
		s.CurrentStatus = types.StatusWarning
	case s.CurrentStatus == types.StatusCritical && newStatus == types.StatusWarning:
		// downgrade status immediately
		s.CurrentStatus = types.StatusWarning
	}

	s.LastUpdate = now

	return s
}

func (s *statusState) setNewStatus(newStatus types.Status, now time.Time) (time.Duration, time.Duration) {
	criticalDuration := time.Duration(0)
	warningDuration := time.Duration(0)

	switch newStatus { //nolint:exhaustive,nolintlint
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

	return criticalDuration, warningDuration
}

// Threshold define a min/max thresholds.
// Use NaN to mark the limit as unset.
type Threshold struct {
	LowCritical   float64
	LowWarning    float64
	WarningDelay  time.Duration
	HighWarning   float64
	HighCritical  float64
	CriticalDelay time.Duration
}

func (t Threshold) MarshalJSON() ([]byte, error) {
	floatToStr := func(f float64) string {
		if math.IsNaN(f) {
			return "null"
		}

		return fmt.Sprint(f)
	}

	str := fmt.Sprintf(
		`{"LowCritical":%s,"LowWarning":%s,"HighWarning":%s,"HighCritical":%s,"WarningDelay":"%s","CriticalDelay":"%s"}`,
		floatToStr(t.LowCritical), floatToStr(t.LowWarning), floatToStr(t.HighWarning), floatToStr(t.HighCritical),
		t.WarningDelay.String(), t.CriticalDelay.String(),
	)

	return []byte(str), nil
}

// Equal test equality of threshold object.
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

// Merge two thresholds, keep the stricter conditions.
func (t Threshold) Merge(other Threshold) Threshold {
	if math.IsNaN(t.LowWarning) || !math.IsNaN(other.LowWarning) && other.LowWarning > t.LowWarning {
		t.LowWarning = other.LowWarning
		t.WarningDelay = other.WarningDelay
	}

	if math.IsNaN(t.LowCritical) || !math.IsNaN(other.LowCritical) && other.LowCritical > t.LowCritical {
		t.LowCritical = other.LowCritical
		t.CriticalDelay = other.CriticalDelay
	}

	if math.IsNaN(t.HighWarning) || !math.IsNaN(other.HighWarning) && other.HighWarning < t.HighWarning {
		t.HighWarning = other.HighWarning
		t.WarningDelay = other.WarningDelay
	}

	if math.IsNaN(t.HighCritical) || !math.IsNaN(other.HighCritical) && other.HighCritical < t.HighCritical {
		t.HighCritical = other.HighCritical
		t.CriticalDelay = other.CriticalDelay
	}

	return t
}

// Unit represent the unit of a metric.
type Unit struct {
	UnitType int    `json:"unit,omitempty"`
	UnitText string `json:"unit_text,omitempty"`
}

// Possible value for UnitType. It must match value in Bleemeo API.
const (
	UnitTypeUnit    = 0
	UnitTypeByte    = 2
	UnitTypeBit     = 3
	UnitTypeSecond  = 6
	UnitTypeDay     = 8
	UnitTypeBytesPS = 10
	UnitTypeBitsPS  = 11
)

// FromConfig converts the threshold config to thresholds.
func FromConfig(
	config config.Threshold,
	metricName string,
	softPeriods map[string]time.Duration,
	defaultSoftPeriod time.Duration,
) Threshold {
	thresh := Threshold{
		LowCritical:  math.NaN(),
		LowWarning:   math.NaN(),
		HighWarning:  math.NaN(),
		HighCritical: math.NaN(),
	}

	if config.LowCritical != nil {
		thresh.LowCritical = *config.LowCritical
	}

	if config.LowWarning != nil {
		thresh.LowWarning = *config.LowWarning
	}

	if config.HighWarning != nil {
		thresh.HighWarning = *config.HighWarning
	}

	if config.HighCritical != nil {
		thresh.HighCritical = *config.HighCritical
	}

	// Apply delays from config or default delay.
	thresh.WarningDelay = defaultSoftPeriod
	thresh.CriticalDelay = defaultSoftPeriod

	if softPeriod, ok := softPeriods[metricName]; ok {
		thresh.WarningDelay = softPeriod
		thresh.CriticalDelay = softPeriod
	}

	return thresh
}

// IsZero returns true is all threshold limit are unset (NaN).
// Is also returns true is all threshold are equal and 0 (which is the zero-value of Threshold structure
// and is an invalid threshold configuration).
func (t Threshold) IsZero() bool {
	if math.IsNaN(t.LowCritical) && math.IsNaN(t.LowWarning) && math.IsNaN(t.HighWarning) && math.IsNaN(t.HighCritical) {
		return true
	}

	return t.LowCritical == 0.0 && t.LowWarning == 0.0 && t.HighWarning == 0.0 && t.HighCritical == 0.0
}

// CurrentStatus returns the current status regarding the threshold and
// (if not ok) a boolean true if the value exceeded the high threshold.
func (t Threshold) CurrentStatus(value float64) (types.Status, bool) {
	if !math.IsNaN(t.LowCritical) && value < t.LowCritical {
		return types.StatusCritical, false
	}

	if !math.IsNaN(t.LowWarning) && value < t.LowWarning {
		return types.StatusWarning, false
	}

	if !math.IsNaN(t.HighCritical) && value > t.HighCritical {
		return types.StatusCritical, true
	}

	if !math.IsNaN(t.HighWarning) && value > t.HighWarning {
		return types.StatusWarning, true
	}

	return types.StatusOk, false
}

func (r *Registry) GetThresholdMetricNames() []string {
	res := []string{}

	r.l.Lock()
	defer r.l.Unlock()

	for labelsText, threshold := range r.thresholds {
		if !threshold.IsZero() {
			lbls := types.TextToLabels(labelsText)

			res = append(res, lbls[types.LabelName])
		}
	}

	return res
}

// GetThreshold returns the current threshold for a Metric by labels text.
func (r *Registry) GetThreshold(labelsText string) Threshold {
	r.l.Lock()
	defer r.l.Unlock()

	return r.getThreshold(labelsText)
}

func (r *Registry) getThreshold(labelsText string) Threshold {
	labelsMap := types.TextToLabels(labelsText)
	labelsText = r.labelsWithoutInstance(labelsText)

	if threshold, ok := r.thresholds[labelsText]; ok {
		return threshold
	}

	metricName := labelsMap[types.LabelName]
	threshold := r.thresholdsAllItem[metricName]

	if threshold.IsZero() {
		return Threshold{
			LowCritical:  math.NaN(),
			LowWarning:   math.NaN(),
			HighWarning:  math.NaN(),
			HighCritical: math.NaN(),
		}
	}

	return threshold
}

// Run will periodically save status state and clean it.
func (r *Registry) Run(ctx context.Context) error {
	lastSave := r.nowFunc()

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

		r.run(save)

		if save {
			lastSave = r.nowFunc()
		}
	}

	return nil
}

func (r *Registry) run(save bool) {
	r.cleanExpired()

	if save {
		jsonList := r.getAsJSONList()
		_ = r.state.Set(statusCacheKey, jsonList)
	}
}

func (r *Registry) getAsJSONList() []jsonState {
	r.l.Lock()
	defer r.l.Unlock()

	jsonList := make([]jsonState, 0, len(r.states))

	for k, v := range r.states {
		jsonList = append(jsonList, jsonState{
			LabelsText:  k,
			statusState: v,
		})
	}

	return jsonList
}

func (r *Registry) cleanExpired() {
	r.l.Lock()
	defer r.l.Unlock()

	for k, v := range r.states {
		if time.Since(v.LastUpdate) > statesTTL {
			delete(r.states, k)
		}
	}
}

// FormatValue takes a float value and a unit and transforms it to a standard format.
func FormatValue(value float64, unit Unit) string {
	switch unit.UnitType {
	case UnitTypeUnit:
		return fmt.Sprintf("%.2f", value)
	case UnitTypeBit, UnitTypeBitsPS, UnitTypeByte, UnitTypeBytesPS:
		scales := []string{"", "K", "M", "G", "T", "P", "E"}

		i := 0
		for i < len(scales)-1 && math.Abs(value) >= 1024 {
			i++

			value /= 1024
		}

		pluralMark := ""
		if !strings.HasSuffix(unit.UnitText, "s") {
			pluralMark = "s"
		}

		return fmt.Sprintf("%.2f %s%s%s", value, scales[i], unit.UnitText, pluralMark)
	case UnitTypeSecond:
		return formatDuration(time.Duration(value) * time.Second)
	case UnitTypeDay:
		return formatDuration(time.Duration(value) * 24 * time.Hour)
	default:
		return fmt.Sprintf("%.2f %s", value, unit.UnitText)
	}
}

func formatDuration(period time.Duration) string {
	sign := ""
	if period < 0 {
		sign = "-"
		period = -period
	}

	units := []struct {
		Scale int
		Name  string
	}{
		{1, "second"},
		{60, "minute"},
		{60, "hour"},
		{24, "day"},
	}

	result := "0 second"
	currentScale := 1

	for i, unit := range units {
		currentScale *= unit.Scale
		value := math.Round(period.Seconds() / float64(currentScale))
		remainder := period.Seconds() - value*float64(currentScale)

		if value < 1 || (value == 1 && remainder < -0.1*value*float64(currentScale)) {
			break
		}

		if remainder < -0.1*value*float64(currentScale) {
			value--

			remainder += float64(currentScale)
		}

		result = fmt.Sprintf("%s%.0f %s", sign, value, unit.Name)
		if value > 1 {
			result += "s"
		}

		if math.Abs(remainder) >= 0.1*value*float64(currentScale) && i > 0 {
			previousScale := currentScale / unit.Scale
			valueRemainder := math.Round(remainder / float64(previousScale))

			result += fmt.Sprintf(" %.0f %s", valueRemainder, units[i-1].Name)
			if valueRemainder > 1 {
				result += "s"
			}
		}
	}

	return result
}

// labelsWithoutInstance remove all labels but the name, the item and the instance_uuid
// if the given labelsText match metricutils.MetricOnlyHasItem.
// Otherwise, it returns the labels unchanged.
func (r *Registry) labelsWithoutInstance(labelsText string) string {
	labelsMap := types.TextToLabels(labelsText)
	if metricutils.MetricOnlyHasItem(labelsMap, r.agentID) {
		// Getting rid of the 'instance' label
		labelsMap = map[string]string{
			types.LabelName:         labelsMap[types.LabelName],
			types.LabelItem:         labelsMap[types.LabelItem],
			types.LabelInstanceUUID: labelsMap[types.LabelInstanceUUID],
		}
		labelsText = types.LabelsToText(labelsMap)
	}

	return labelsText
}

// ApplyThresholds applies the thresholds. It returns input points with their status modified by the thresholds,
// and the new status points generated.
func (r *Registry) ApplyThresholds(points []types.MetricPoint) ([]types.MetricPoint, []types.MetricPoint) {
	r.l.Lock()
	defer r.l.Unlock()

	newPoints := make([]types.MetricPoint, 0, len(points))
	statusPoints := make([]types.MetricPoint, 0, len(points))

	for _, point := range points {
		if !point.Annotations.Status.CurrentStatus.IsSet() {
			labelsText := types.LabelsToText(point.Labels)
			threshold := r.getThreshold(labelsText)

			if !threshold.IsZero() && !math.IsNaN(point.Value) {
				newPoints, statusPoints = r.addPointWithThreshold(newPoints, statusPoints, point, threshold, labelsText)

				continue
			}
		}

		newPoints = append(newPoints, point)
	}

	return newPoints, statusPoints
}

func (r *Registry) addPointWithThreshold(
	points, statusPoints []types.MetricPoint,
	point types.MetricPoint,
	threshold Threshold,
	labelsText string,
) ([]types.MetricPoint, []types.MetricPoint) {
	labelsText = r.labelsWithoutInstance(labelsText)
	softStatus, highThreshold := threshold.CurrentStatus(point.Value)
	previousState := r.states[labelsText]

	newState := previousState.Update(softStatus, threshold.WarningDelay, threshold.CriticalDelay, r.nowFunc())
	r.states[labelsText] = newState

	unit := r.units[labelsText]
	// Consumer expects status description from threshold to start with "Current value:"
	statusDescription := "Current value: " + FormatValue(point.Value, unit)

	if newState.CurrentStatus != types.StatusOk {
		thresholdLimit := math.NaN()

		switch {
		case highThreshold && newState.CurrentStatus == types.StatusCritical:
			thresholdLimit = threshold.HighCritical
		case highThreshold && newState.CurrentStatus == types.StatusWarning:
			thresholdLimit = threshold.HighWarning
		case !highThreshold && newState.CurrentStatus == types.StatusCritical:
			thresholdLimit = threshold.LowCritical
		case !highThreshold && newState.CurrentStatus == types.StatusWarning:
			thresholdLimit = threshold.LowWarning
		}

		switch {
		case newState.CurrentStatus == types.StatusWarning && threshold.WarningDelay > 0:
			statusDescription += fmt.Sprintf(
				" threshold (%s) exceeded over last %v",
				FormatValue(thresholdLimit, unit),
				formatDuration(threshold.WarningDelay),
			)
		case newState.CurrentStatus == types.StatusCritical && threshold.CriticalDelay > 0:
			statusDescription += fmt.Sprintf(
				" threshold (%s) exceeded over last %v",
				FormatValue(thresholdLimit, unit),
				formatDuration(threshold.CriticalDelay),
			)
		default:
			statusDescription += fmt.Sprintf(
				" threshold (%s) exceeded",
				FormatValue(thresholdLimit, unit),
			)
		}
	}

	status := types.StatusDescription{
		CurrentStatus:     newState.CurrentStatus,
		StatusDescription: statusDescription,
	}
	annotationsCopy := point.Annotations
	annotationsCopy.Status = status

	points = append(points, types.MetricPoint{
		Point:       point.Point,
		Labels:      point.Labels,
		Annotations: annotationsCopy,
	})

	// Create status point.
	labelsCopy := make(map[string]string, len(point.Labels))

	for k, v := range point.Labels {
		labelsCopy[k] = v
	}

	labelsCopy[types.LabelName] += statusMetricSuffix

	annotationsCopy.StatusOf = point.Labels[types.LabelName]

	statusPoints = append(statusPoints, types.MetricPoint{
		Point:       types.Point{Time: point.Time, Value: float64(status.CurrentStatus.NagiosCode())},
		Labels:      labelsCopy,
		Annotations: annotationsCopy,
	})

	return points, statusPoints
}

func (r *Registry) DiagnosticThresholds(_ context.Context, archive types.ArchiveWriter) error {
	file, err := archive.Create("thresholds.json")
	if err != nil {
		return err
	}

	nonZeroThresholds := make(map[string]Threshold)

	func() {
		r.l.Lock()
		defer r.l.Unlock()

		for labelsText, threshold := range r.thresholds {
			if !threshold.IsZero() {
				nonZeroThresholds[labelsText] = threshold
			}
		}

		for labelsText, threshold := range r.thresholdsAllItem {
			if !threshold.IsZero() {
				nonZeroThresholds[labelsText] = threshold
			}
		}
	}()

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")

	return enc.Encode(nonZeroThresholds)
}

func (r *Registry) DiagnosticStatusStates(_ context.Context, archive types.ArchiveWriter) error {
	file, err := archive.Create("threshold-status-states.json")
	if err != nil {
		return err
	}

	r.l.Lock()

	jsonStates := make([]jsonState, 0, len(r.states))

	for labelsText, state := range r.states {
		jsonStates = append(jsonStates, jsonState{
			LabelsText:  labelsText,
			statusState: state,
		})
	}

	r.l.Unlock()

	sort.Slice(jsonStates, func(i, j int) bool {
		return jsonStates[i].LabelsText < jsonStates[j].LabelsText
	})

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")

	return enc.Encode(jsonStates)
}
