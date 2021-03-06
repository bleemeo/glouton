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

package internal

import (
	"errors"
	"fmt"
	"glouton/inputs"
	"glouton/logger"
	"glouton/types"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/influxdata/telegraf"
)

var (
	errNotImplemented  = errors.New("not implemented")
	errUnsupportedType = errors.New("value type not supported")
)

type metricPoint struct {
	Value interface{} // could be uint64 or int64
	Time  time.Time
}

// GatherContext is the couple Measurement and tags.
type GatherContext struct {
	Measurement    string
	Tags           map[string]string
	Annotations    types.MetricAnnotations
	OriginalFields map[string]interface{}
}

// RenameCallback is a function which can mutate labels & annotations.
type RenameCallback func(labels map[string]string, annotations types.MetricAnnotations) (newLabels map[string]string, newAnnotations types.MetricAnnotations)

// Accumulator implements telegraf.Accumulator with the capabilities to
// renames metrics, apply transformation (including derivation of value).
//
// Transformations are implemented via a callback function
// The processing will be the following:
// * PrepareGather must be called.
// * The Gather() method should be called
// * If TransformGlobal is set, it's applied. RenameTransform allow to rename measurement and alter tags. It could also completly drop
//   a batch of metrics
// * Any metrics matching DerivatedMetrics are derivated. Metric seen for the first time are dropped.
//   Derivation is only applied to Counter values, that is something that only go upward. If value does downward, it's skipped.
// * Then TransformMetrics is called on a float64 version of fields. It may apply per-metric transformation.
type Accumulator struct {
	Accumulator telegraf.Accumulator

	// RenameGlobal apply global rename on all metrics from one batch.
	// It may:
	// * change the measurement name (prefix of metric name)
	// * alter tags
	// * completly drop this base (e.g. blacklisting for disk/network interface/...))
	RenameGlobal func(originalContext GatherContext) (newContext GatherContext, drop bool)

	// DerivatedMetrics is the list of metric counter to derive
	DerivatedMetrics []string

	// ShouldDerivateMetrics indicate if a metric should be derivated. It's an alternate way to DerivatedMetrics.
	// If both ShouldDerivateMetrics and DerivatedMetrics are set, only metrics not found in DerivatedMetrics are passed to ShouldDerivateMetrics
	ShouldDerivateMetrics func(originalContext GatherContext, currentContext GatherContext, metricName string) bool

	// TransformMetrics take a list of metrics and could change the name/value or even add/delete some points.
	// tags & measurement are given as indication and should not be mutated.
	TransformMetrics func(originalContext GatherContext, currentContext GatherContext, fields map[string]float64, originalFields map[string]interface{}) map[string]float64

	// RenameMetrics apply a per-metric rename of metric name and measurement. tags can't be mutated
	RenameMetrics func(originalContext GatherContext, currentContext GatherContext, metricName string) (newMeasurement string, newMetricName string)

	RenameCallbacks []RenameCallback

	// map a flattened tags to a map[fieldName]value
	currentValues map[string]map[string]metricPoint
	pastValues    map[string]map[string]metricPoint
	now           time.Time
	l             sync.Mutex
}

// PrepareGather should be called before each gather. It's mainly useful for delta computation.
func (a *Accumulator) PrepareGather() {
	a.pastValues = a.currentValues
	a.currentValues = make(map[string]map[string]metricPoint)
	a.now = time.Now()
}

// convertToFloat convert the interface type in float64.
func convertToFloat(value interface{}) (valueFloat float64, err error) {
	switch value := value.(type) {
	case uint64:
		valueFloat = float64(value)
	case float64:
		valueFloat = value
	case float32:
		valueFloat = float64(value)
	case int:
		valueFloat = float64(value)
	case int64:
		valueFloat = float64(value)
	case bool:
		if value {
			valueFloat = 1.0
		} else {
			valueFloat = 0.0
		}
	default:
		var valueType = reflect.TypeOf(value)
		err = fmt.Errorf("%w: %v", errUnsupportedType, valueType)
	}

	return
}

// rateAsFloat compute the delta/duration between two points.
func rateAsFloat(pastPoint, currentPoint metricPoint) (value float64, err error) {
	switch pastValue := pastPoint.Value.(type) {
	case uint64:
		// Special case here. If pastPoint if bigger that currentPoint, the unsigned int will overflow.
		currentValue := currentPoint.Value.(uint64)
		if pastValue > currentValue {
			value = -float64(pastValue - currentValue)
		} else {
			value = float64(currentValue - pastValue)
		}
	case int:
		value = float64(currentPoint.Value.(int) - pastValue)
	case int64:
		value = float64(currentPoint.Value.(int64) - pastValue)
	default:
		pastValueFloat, err := convertToFloat(pastPoint.Value)
		if err != nil {
			return 0.0, err
		}

		currentValue, err := convertToFloat(currentPoint.Value)
		if err != nil {
			return 0.0, err
		}

		value = currentValue - pastValueFloat
	}

	value /= float64(currentPoint.Time.Unix() - pastPoint.Time.Unix())

	return value, err
}

func flattenTag(tags map[string]string) string {
	tagsList := make([]string, 0, len(tags))

	for k, v := range tags {
		tagsList = append(tagsList, fmt.Sprintf("%s=%s", k, v))
	}

	sort.Strings(tagsList)

	return strings.Join(tagsList, ",")
}

// applyDerivate compute the derivated value for metrics in DerivatedMetrics.
func (a *Accumulator) applyDerivate(originalContext GatherContext, currentContext GatherContext, fields map[string]interface{}, metricTime time.Time) map[string]float64 {
	a.l.Lock()
	defer a.l.Unlock()

	result := make(map[string]float64)
	searchMetrics := make(map[string]bool)

	for _, m := range a.DerivatedMetrics {
		searchMetrics[m] = true
	}

	flatTag := flattenTag(currentContext.Tags)

	if _, ok := a.currentValues[flatTag]; !ok {
		a.currentValues[flatTag] = make(map[string]metricPoint)
	}

	for metricName, value := range fields {
		if _, ok := value.(string); ok {
			// we ignore string without error
			continue
		}

		derive := false

		if _, ok := searchMetrics[metricName]; ok {
			derive = true
		}

		if !derive && a.ShouldDerivateMetrics != nil && a.ShouldDerivateMetrics(originalContext, currentContext, metricName) {
			derive = true
		}

		if !derive {
			valueFloat, err := convertToFloat(value)
			if err == nil {
				result[metricName] = valueFloat
			} else {
				a.AddError(err)
			}

			continue
		}

		pastMetricPoint, ok := a.pastValues[flatTag][metricName]
		currentPoint := metricPoint{Time: metricTime, Value: value}
		a.currentValues[flatTag][metricName] = currentPoint

		if ok {
			if tmp, ok := a.getDerivativeValue(pastMetricPoint, currentPoint); ok {
				result[metricName] = tmp
			}
		} else {
			continue
		}
	}

	return result
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

type accumulatorFunc func(measurement string, fields map[string]interface{}, tags map[string]string, annotations types.MetricAnnotations, t ...time.Time)

func (a *Accumulator) processMetrics(finalFunc accumulatorFunc, measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	originalContext := GatherContext{
		Measurement:    measurement,
		Tags:           tags,
		OriginalFields: fields,
	}
	currentContext := originalContext
	currentContext.Tags = make(map[string]string, len(tags))

	for k, v := range tags {
		currentContext.Tags[k] = v
	}

	if a.RenameGlobal != nil {
		drop := false
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

	floatFields := a.applyDerivate(originalContext, currentContext, fields, metricTime)

	if a.TransformMetrics != nil {
		floatFields = a.TransformMetrics(originalContext, currentContext, floatFields, fields)
	}

	fieldsPerMeasurements := make(map[string]map[string]interface{})

	if a.RenameMetrics != nil {
		for metricName, value := range floatFields {
			newMeasurement, newMetricName := a.RenameMetrics(originalContext, currentContext, metricName)
			if _, ok := fieldsPerMeasurements[newMeasurement]; !ok {
				fieldsPerMeasurements[newMeasurement] = make(map[string]interface{})
			}

			fieldsPerMeasurements[newMeasurement][newMetricName] = value
		}
	} else {
		currentMap := make(map[string]interface{})
		for k, v := range floatFields {
			currentMap[k] = v
		}
		fieldsPerMeasurements[currentContext.Measurement] = currentMap
	}

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

	return func(measurement string, fields map[string]interface{}, tags map[string]string, annotations types.MetricAnnotations, t ...time.Time) {
		fallbackMethod(measurement, fields, tags, t...)
	}
}

// Implementation of telegraf.Input interface

// AddFields adds a metric to the accumulator with the given measurement
// name, fields, and tags (and timestamp). If a timestamp is not provided,
// then the accumulator sets it to "now".
func (a *Accumulator) AddFields(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.processMetrics(a.wrapAdd("fields"), measurement, fields, tags, t...)
}

// AddGauge is the same as AddFields, but will add the metric as a "Gauge" type.
func (a *Accumulator) AddGauge(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.processMetrics(a.wrapAdd("gauge"), measurement, fields, tags, t...)
}

// AddCounter is the same as AddFields, but will add the metric as a "Counter" type.
func (a *Accumulator) AddCounter(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.processMetrics(a.wrapAdd("counter"), measurement, fields, tags, t...)
}

// AddSummary is the same as AddFields, but will add the metric as a "Summary" type.
func (a *Accumulator) AddSummary(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.processMetrics(a.wrapAdd("summary"), measurement, fields, tags, t...)
}

// AddHistogram is the same as AddFields, but will add the metric as a "Histogram" type.
func (a *Accumulator) AddHistogram(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
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
func (a *Accumulator) SetPrecision(precision time.Duration) {
	a.AddError(errNotImplemented)
}

// WithTracking upgrades to a TrackingAccumulator with space for maxTracked
// metrics/batches.
func (a *Accumulator) WithTracking(maxTracked int) telegraf.TrackingAccumulator {
	a.AddError(errNotImplemented)
	return nil
}
