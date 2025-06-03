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

package internal

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/types"

	"github.com/influxdata/telegraf"
)

var errNotImplemented = errors.New("not implemented")

type metricPoint struct {
	Value any // could be uint64 or int64
	Time  time.Time
}

// GatherContext is the couple Measurement and tags.
type GatherContext struct {
	Measurement         string
	OriginalMeasurement string
	OriginalTags        map[string]string // OriginalTags had to be filled by RenameGlobal callback
	Tags                map[string]string
	Annotations         types.MetricAnnotations
	OriginalFields      map[string]any
}

// RenameCallback is a function which can mutate labels & annotations.
type RenameCallback func(labels map[string]string, annotations types.MetricAnnotations) (newLabels map[string]string, newAnnotations types.MetricAnnotations)

// Accumulator implements telegraf.Accumulator with the capabilities to
// renames metrics, apply transformation (including derivation of value).
//
// Transformations are implemented via a callback function
// The processing will be the following:
//   - PrepareGather must be called.
//   - The Gather() method should be called
//   - If TransformGlobal is set, it's applied. RenameTransform allow to rename measurement and alter tags. It could also completely drop
//     a batch of metrics
//   - Any metrics matching DerivatedMetrics are derivated. Metric seen for the first time are dropped.
//     Derivation is only applied to Counter values, that is something that only go upward. If value does downward, it's skipped.
//   - Then TransformMetrics is called on a float64 version of fields. It may apply per-metric transformation.
type Accumulator struct {
	Accumulator telegraf.Accumulator

	// RenameGlobal apply global rename on all metrics from one batch.
	// It may:
	// * change the measurement name (prefix of metric name)
	// * alter tags
	// * completely drop this base (e.g. blocking a disk/network interface/...))
	// You should return a modified version of originalContext to kept all non-modified field from originalContext.
	RenameGlobal func(gatherContext GatherContext) (result GatherContext, drop bool)

	// DifferentiatedMetrics is the list of metric counter to derive
	DifferentiatedMetrics []string

	// ShouldDifferentiateMetrics indicate if a metric should be derivated. It's an alternate way to DerivatedMetrics.
	// If both ShouldDifferentiateMetrics and DerivatedMetrics are set, only metrics not found in DerivatedMetrics are passed to ShouldDifferentiateMetrics
	ShouldDifferentiateMetrics func(currentContext GatherContext, metricName string) bool

	// TransformMetrics take a list of metrics and could change the name/value or even add/delete some points.
	// tags & measurement are given as indication and should not be mutated.
	TransformMetrics func(currentContext GatherContext, fields map[string]float64, originalFields map[string]any) map[string]float64

	// RenameMetrics apply a per-metric rename of metric name and measurement. tags can't be mutated
	RenameMetrics func(currentContext GatherContext, metricName string) (newMeasurement string, newMetricName string)

	RenameCallbacks []RenameCallback

	// map a flattened tags to a map[fieldName]value
	currentValues    map[string]map[string]metricPoint
	pastValues       map[string]map[string]metricPoint
	workStringBuffer []string
	workResult       map[string]float64
	now              time.Time
	l                sync.Mutex
}

// PrepareGather should be called before each gather. It's mainly useful for delta computation.
func (a *Accumulator) PrepareGather() {
	a.pastValues = a.currentValues
	a.currentValues = nil
	a.now = time.Now()
}

// rateAsFloat compute the delta/duration between two points.
func rateAsFloat(pastPoint, currentPoint metricPoint) (value float64, err error) {
	switch pastValue := pastPoint.Value.(type) {
	case uint64:
		// Special case here. If pastPoint if bigger that currentPoint, the unsigned int will overflow.
		currentValue, _ := currentPoint.Value.(uint64)
		if pastValue > currentValue {
			value = -float64(pastValue - currentValue)
		} else {
			value = float64(currentValue - pastValue)
		}
	case int:
		currentValue, _ := currentPoint.Value.(int)
		value = float64(currentValue - pastValue)
	case int64:
		currentValue, _ := currentPoint.Value.(int64)
		value = float64(currentValue - pastValue)
	default:
		pastValueFloat, err := inputs.ConvertToFloat(pastPoint.Value)
		if err != nil {
			return 0.0, err
		}

		currentValue, err := inputs.ConvertToFloat(currentPoint.Value)
		if err != nil {
			return 0.0, err
		}

		value = currentValue - pastValueFloat
	}

	value /= float64(currentPoint.Time.Unix() - pastPoint.Time.Unix())

	return value, err
}

func (a *Accumulator) flattenTag(tags map[string]string) string {
	if cap(a.workStringBuffer) < len(tags) {
		a.workStringBuffer = make([]string, 0, len(tags))
	}

	a.workStringBuffer = a.workStringBuffer[:0]

	for k, v := range tags {
		a.workStringBuffer = append(a.workStringBuffer, fmt.Sprintf("%s=%s", k, v))
	}

	sort.Strings(a.workStringBuffer)

	return strings.Join(a.workStringBuffer, ",")
}

// doDifferentiate compute the derivated value for metrics in DerivatedMetrics (or matching ShouldDerivativeMetrics).
func (a *Accumulator) doDifferentiate(result map[string]float64, flatTag string, fieldsLength int, metricName string, value any, metricTime time.Time) {
	if a.currentValues == nil {
		a.currentValues = make(map[string]map[string]metricPoint)
	}

	if _, ok := a.currentValues[flatTag]; !ok {
		a.currentValues[flatTag] = make(map[string]metricPoint, fieldsLength)
	}

	pastMetricPoint, ok := a.pastValues[flatTag][metricName]
	currentPoint := metricPoint{Time: metricTime, Value: value}
	a.currentValues[flatTag][metricName] = currentPoint

	if ok {
		if tmp, ok := a.getDerivativeValue(pastMetricPoint, currentPoint); ok {
			result[metricName] = tmp
		}
	}
}

func (a *Accumulator) convertToFloatFields(currentContext GatherContext, fields map[string]any, metricTime time.Time) map[string]float64 {
	var (
		searchMetrics map[string]bool
		flatTag       string
	)

	if a.workResult == nil {
		a.workResult = make(map[string]float64, len(fields))
	}

	for k := range a.workResult {
		delete(a.workResult, k)
	}

	for _, m := range a.DifferentiatedMetrics {
		if searchMetrics == nil {
			searchMetrics = make(map[string]bool, len(a.DifferentiatedMetrics))
		}

		searchMetrics[m] = true
	}

	for metricName, value := range fields {
		// Some Telegraf inputs return nil values, we just ignore them.
		if value == nil {
			continue
		}

		if _, ok := value.(string); ok {
			// we ignore string without error
			continue
		}

		differentiate := false

		if _, ok := searchMetrics[metricName]; ok {
			differentiate = true
		}

		if !differentiate && a.ShouldDifferentiateMetrics != nil && a.ShouldDifferentiateMetrics(currentContext, metricName) {
			differentiate = true
		}

		if !differentiate {
			valueFloat, err := inputs.ConvertToFloat(value)
			if err == nil {
				a.workResult[metricName] = valueFloat
			} else {
				a.AddError(fmt.Errorf("convert %s to float: %w", metricName, err))
			}

			continue
		}

		if flatTag == "" && len(currentContext.Tags) > 0 {
			flatTag = a.flattenTag(currentContext.Tags)
		}

		a.doDifferentiate(a.workResult, flatTag, len(fields), metricName, value, metricTime)
	}

	return a.workResult
}

func (a *Accumulator) getDerivativeValue(pastMetricPoint metricPoint, currentPoint metricPoint) (float64, bool) {
	valueFloat, err := rateAsFloat(pastMetricPoint, currentPoint)

	switch {
	case err == nil && valueFloat >= 0:
		return valueFloat, true
	case err == nil:
		return 0, false
	default:
		a.AddError(err)

		return 0, false
	}
}

type accumulatorFunc func(
	measurement string,
	fields map[string]any,
	tags map[string]string,
	annotations types.MetricAnnotations,
	t ...time.Time,
)

func (a *Accumulator) processMetrics(
	finalFunc accumulatorFunc,
	measurement string,
	fields map[string]any,
	tags map[string]string,
	t ...time.Time,
) {
	if tags == nil {
		tags = make(map[string]string)
	}

	currentContext := GatherContext{
		OriginalMeasurement: measurement,
		Measurement:         measurement,
		Tags:                tags,
		OriginalFields:      fields,
	}

	if a.RenameGlobal != nil {
		var drop bool
		currentContext, drop = a.RenameGlobal(currentContext)

		if drop {
			return
		}
	}

	var metricTime time.Time

	if len(t) != 1 {
		metricTime = a.now
	} else {
		metricTime = t[0]
	}

	// Lock is needed for convertToFloatFields and for floatFields (which is
	// a reference to a.workResult)
	a.l.Lock()

	floatFields := a.convertToFloatFields(currentContext, fields, metricTime)

	if a.TransformMetrics != nil {
		floatFields = a.TransformMetrics(currentContext, floatFields, fields)
	}

	fieldsPerMeasurements := make(map[string]map[string]any)

	if a.RenameMetrics != nil {
		for metricName, value := range floatFields {
			newMeasurement, newMetricName := a.RenameMetrics(currentContext, metricName)
			if _, ok := fieldsPerMeasurements[newMeasurement]; !ok {
				fieldsPerMeasurements[newMeasurement] = make(map[string]any, len(floatFields))
			}

			fieldsPerMeasurements[newMeasurement][newMetricName] = value
		}
	} else {
		currentMap := make(map[string]any)
		for k, v := range floatFields {
			currentMap[k] = v
		}

		fieldsPerMeasurements[currentContext.Measurement] = currentMap
	}

	a.l.Unlock()

	for _, f := range a.RenameCallbacks {
		currentContext.Tags, currentContext.Annotations = f(currentContext.Tags, currentContext.Annotations)
	}

	for measurementName, fields := range fieldsPerMeasurements {
		finalFunc(measurementName, fields, currentContext.Tags, currentContext.Annotations, metricTime)
	}
}

// wrapAdd return an Add* method that support annotation. If the backend accumulator does not support annotation, discard them and use fallbackMethod.
func (a *Accumulator) wrapAdd(metricType string) accumulatorFunc {
	if annocationAcc, ok := a.Accumulator.(inputs.AnnotationAccumulator); ok {
		return annocationAcc.AddFieldsWithAnnotations
	}

	fallbackMethod := a.Accumulator.AddFields

	switch metricType {
	case "gauge":
		fallbackMethod = a.Accumulator.AddGauge
	case "counter":
		fallbackMethod = a.Accumulator.AddCounter
	case "summary":
		fallbackMethod = a.Accumulator.AddSummary
	case "histogram":
		fallbackMethod = a.Accumulator.AddHistogram
	}

	return func(measurement string, fields map[string]any, tags map[string]string, _ types.MetricAnnotations, t ...time.Time) {
		fallbackMethod(measurement, fields, tags, t...)
	}
}

// Implementation of telegraf.Input interface

// AddFields adds a metric to the accumulator with the given measurement
// name, fields, and tags (and timestamp). If a timestamp is not provided,
// then the accumulator sets it to "now".
func (a *Accumulator) AddFields(measurement string, fields map[string]any, tags map[string]string, t ...time.Time) {
	a.processMetrics(a.wrapAdd("fields"), measurement, fields, tags, t...)
}

// AddGauge is the same as AddFields, but will add the metric as a "Gauge" type.
func (a *Accumulator) AddGauge(measurement string, fields map[string]any, tags map[string]string, t ...time.Time) {
	a.processMetrics(a.wrapAdd("gauge"), measurement, fields, tags, t...)
}

// AddCounter is the same as AddFields, but will add the metric as a "Counter" type.
func (a *Accumulator) AddCounter(measurement string, fields map[string]any, tags map[string]string, t ...time.Time) {
	a.processMetrics(a.wrapAdd("counter"), measurement, fields, tags, t...)
}

// AddSummary is the same as AddFields, but will add the metric as a "Summary" type.
func (a *Accumulator) AddSummary(measurement string, fields map[string]any, tags map[string]string, t ...time.Time) {
	a.processMetrics(a.wrapAdd("summary"), measurement, fields, tags, t...)
}

// AddHistogram is the same as AddFields, but will add the metric as a "Histogram" type.
func (a *Accumulator) AddHistogram(measurement string, fields map[string]any, tags map[string]string, t ...time.Time) {
	a.processMetrics(a.wrapAdd("histogram"), measurement, fields, tags, t...)
}

// AddMetric adds an metric to the accumulator.
func (a *Accumulator) AddMetric(telegraf.Metric) {
	a.AddError(errNotImplemented)
}

// AddError reports an error.
func (a *Accumulator) AddError(err error) {
	if a.Accumulator == nil {
		logger.Printf("AddError(%v)", err)
	} else {
		a.Accumulator.AddError(err)
	}
}

// SetPrecision takes two time.Duration objects. If the first is non-zero,
// it sets that as the precision. Otherwise, it takes the second argument
// as the order of time that the metrics should be rounded to, with the
// maximum being 1s.
func (a *Accumulator) SetPrecision(time.Duration) {
	a.AddError(errNotImplemented)
}

// WithTracking upgrades to a TrackingAccumulator with space for maxTracked
// metrics/batches.
func (a *Accumulator) WithTracking(maxTracked int) telegraf.TrackingAccumulator {
	_ = maxTracked

	a.AddError(errNotImplemented)

	return nil
}
