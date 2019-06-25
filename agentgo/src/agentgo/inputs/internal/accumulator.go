package internal

import (
	"fmt"
	"log"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/influxdata/telegraf"
)

type metricPoint struct {
	Value interface{} // could be uint64 or int64
	Time  time.Time
}

// Accumulator implements telegraf.Accumulator with the capabilities to
// renames metrics, apply transformation (including derivation of value).
//
// Transformations are implemented via a callback function
// The processing will be the following:
// * Fields should be set (and not modified during run)
// * PrepareGather should be called.
// * The Gather() method should be called
// * If TransformTags is set, it's applied. TransformTags must not mutate the map but create a new copy of the tags map.
// * Any metrics matching DerivatedMetrics are derivated. Metric seen for the first time are dropped.
// * Then TransformMetrics is called on a float64 version of fields (so TransformMetrics may mutate the map)
type Accumulator struct {
	Accumulator telegraf.Accumulator

	// NewMeasurement (if set) is the overridden measurment name for all metric
	NewMeasurement string

	// NewMeasurementMap (if set) override the measurment for each metric. It map a metric name to the target measurment name.
	// If a metric name is not in this map, the default measurement name (or NewMeasurement if set) is used.
	NewMeasurementMap map[string]string

	// DerivatedMetrics is the list of metric counter to derivate
	DerivatedMetrics []string

	// TransformTags take a list of tags and could change the name/value or even add/delete some tags.
	// If the boolean is true, it will drop the whole Gather (useful for blacklisting from disk/network interface/...)
	TransformTags func(tags map[string]string) (newTags map[string]string, drop bool)

	// TransformMetrics take a list of metrics and could change the name/value or even add/delete some points
	TransformMetrics func(fields map[string]float64, tags map[string]string) map[string]float64

	// map a flattened tags to a map[fieldName]value
	currentValues map[string]map[string]metricPoint
	pastValues    map[string]map[string]metricPoint
}

// PrepareGather should be called before each gather. It's mainly useful for delta computation.
func (a *Accumulator) PrepareGather() {
	a.pastValues = a.currentValues
	a.currentValues = make(map[string]map[string]metricPoint)
}

// convertToFloat convert the interface type in float64
func convertToFloat(value interface{}) (valueFloat float64, err error) {
	switch value.(type) {
	case uint64:
		valueFloat = float64(value.(uint64))
	case float64:
		valueFloat = value.(float64)
	case int:
		valueFloat = float64(value.(int))
	case int64:
		valueFloat = float64(value.(int64))
	default:
		var valueType = reflect.TypeOf(value)
		err = fmt.Errorf("Value type not supported: %v", valueType)
	}
	return
}

// rateAsFloat compute the delta/duration between two points
func rateAsFloat(pastPoint, currentPoint metricPoint) (value float64, err error) {
	switch pastPoint.Value.(type) {
	case uint64:
		// Special case here. If pastPoint if bigger that currentPoint, the unsigned int will overflow.
		pastValue := pastPoint.Value.(uint64)
		currentValue := currentPoint.Value.(uint64)
		if pastValue > currentValue {
			value = -float64(pastValue - currentValue)
		} else {
			value = float64(currentValue - pastValue)
		}
	case float64:
		value = currentPoint.Value.(float64) - pastPoint.Value.(float64)
	case int:
		value = float64(currentPoint.Value.(int) - pastPoint.Value.(int))
	case int64:
		value = float64(currentPoint.Value.(int64) - pastPoint.Value.(int64))
	default:
		var valueType = reflect.TypeOf(pastPoint.Value)
		err = fmt.Errorf("Value type not supported :(%v)", valueType)
	}
	value = value / float64(currentPoint.Time.Unix()-pastPoint.Time.Unix())
	return
}

func flattenTag(tags map[string]string) string {
	tagsList := make([]string, 0, len(tags))
	for k, v := range tags {
		tagsList = append(tagsList, fmt.Sprintf("%s=%s", k, v))
	}
	sort.Strings(tagsList)
	return strings.Join(tagsList, ",")
}

// applyDerivate compute the derivated value for metrics in DerivatedMetrics
func (a *Accumulator) applyDerivate(fields map[string]interface{}, tags map[string]string, metricTime time.Time) map[string]float64 {
	result := make(map[string]float64)
	searchMetrics := make(map[string]bool)

	for _, m := range a.DerivatedMetrics {
		searchMetrics[m] = true
	}

	flatTag := flattenTag(tags)

	if _, ok := a.currentValues[flatTag]; !ok {
		a.currentValues[flatTag] = make(map[string]metricPoint)
	}

	for metricName, value := range fields {
		if _, ok := value.(string); ok {
			// we ignore string without error
			continue
		}
		if _, ok := searchMetrics[metricName]; !ok {
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
			valueFloat, err := rateAsFloat(pastMetricPoint, currentPoint)
			if err == nil {
				result[metricName] = valueFloat
			} else {
				a.AddError(err)
			}
		} else {
			continue
		}
	}
	return result
}

type accumulatorFunc func(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time)

func (a *Accumulator) processMetrics(finalFunc accumulatorFunc, measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	newTags := tags
	if a.TransformTags != nil {
		drop := false
		newTags, drop = a.TransformTags(newTags)
		if drop {
			return
		}
	}
	var metricTime time.Time
	if len(t) != 1 {
		metricTime = time.Now()
	} else {
		metricTime = t[0]
	}

	newMeasurement := measurement
	if a.NewMeasurement != "" {
		newMeasurement = a.NewMeasurement
	}

	newFields := a.applyDerivate(fields, tags, metricTime)
	if a.TransformMetrics != nil {
		newFields = a.TransformMetrics(newFields, newTags)
	}

	if a.NewMeasurementMap != nil {
		newFields2 := make(map[string]map[string]interface{})
		for metricName, value := range newFields {
			measurementName, ok := a.NewMeasurementMap[metricName]
			if !ok {
				measurementName = newMeasurement
			}
			if _, ok := newFields2[measurementName]; !ok {
				newFields2[measurementName] = make(map[string]interface{})
			}
			newFields2[measurementName][metricName] = value
		}
		for measurementName, fields := range newFields2 {
			finalFunc(measurementName, fields, newTags, metricTime)
		}
	} else {
		newFields2 := make(map[string]interface{})
		for k, v := range newFields {
			newFields2[k] = v
		}
		finalFunc(newMeasurement, newFields2, newTags, metricTime)
	}
}

// Implementation of telegraf.Input interface

// AddFields adds a metric to the accumulator with the given measurement
// name, fields, and tags (and timestamp). If a timestamp is not provided,
// then the accumulator sets it to "now".//
func (a *Accumulator) AddFields(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.processMetrics(a.Accumulator.AddFields, measurement, fields, tags, t...)
}

// AddGauge is the same as AddFields, but will add the metric as a "Gauge" type
func (a *Accumulator) AddGauge(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.processMetrics(a.Accumulator.AddGauge, measurement, fields, tags, t...)
}

// AddCounter is the same as AddFields, but will add the metric as a "Counter" type
func (a *Accumulator) AddCounter(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.processMetrics(a.Accumulator.AddCounter, measurement, fields, tags, t...)
}

// AddSummary is the same as AddFields, but will add the metric as a "Summary" type
func (a *Accumulator) AddSummary(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.processMetrics(a.Accumulator.AddSummary, measurement, fields, tags, t...)
}

// AddHistogram is the same as AddFields, but will add the metric as a "Histogram" type
func (a *Accumulator) AddHistogram(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.processMetrics(a.Accumulator.AddHistogram, measurement, fields, tags, t...)
}

// AddMetric adds an metric to the accumulator.
func (a *Accumulator) AddMetric(telegraf.Metric) {
	a.AddError(fmt.Errorf("AddMetric not implemented"))
}

// AddError reports an error.
func (a *Accumulator) AddError(err error) {
	if a.Accumulator == nil {
		log.Fatalf("%v", err)
	} else {
		a.Accumulator.AddError(err)
	}
}

// SetPrecision takes two time.Duration objects. If the first is non-zero,
// it sets that as the precision. Otherwise, it takes the second argument
// as the order of time that the metrics should be rounded to, with the
// maximum being 1s.
func (a *Accumulator) SetPrecision(precision time.Duration) {
	a.AddError(fmt.Errorf("SetPrecision not implemented"))
}

// WithTracking upgrades to a TrackingAccumulator with space for maxTracked
// metrics/batches.
func (a *Accumulator) WithTracking(maxTracked int) telegraf.TrackingAccumulator {
	a.AddError(fmt.Errorf("WithTracking not implemented"))
	return nil
}
