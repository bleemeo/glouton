package inputs

import (
	"fmt"
	"glouton/logger"
	"glouton/types"
	"reflect"
	"time"

	"github.com/influxdata/telegraf"
)

// AnnotationAccumulator is a similar to an telegraf.Accumulator but allow to send metric with annocations
type AnnotationAccumulator interface {
	AddFieldsWithAnnotations(measurement string, fields map[string]interface{}, tags map[string]string, annotations types.MetricAnnotations, t ...time.Time)
	AddError(err error)
}

// Accumulator implement telegraf.Accumulator (+AddFieldsWithAnnotations) and emit the metric points
type Accumulator struct {
	Pusher types.PointPusher
}

// AddFields adds a metric to the accumulator with the given measurement
// name, fields, and tags (and timestamp). If a timestamp is not provided,
// then the accumulator sets it to "now".
// Create a point with a value, decorating it with tags
// NOTE: tags is expected to be owned by the caller, don't mutate
// it after passing to Add.
func (a *Accumulator) AddFields(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.addMetrics(measurement, fields, tags, types.MetricAnnotations{}, t...)
}

// AddGauge is the same as AddFields, but will add the metric as a "Gauge" type
func (a *Accumulator) AddGauge(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.addMetrics(measurement, fields, tags, types.MetricAnnotations{}, t...)
}

// AddCounter is the same as AddFields, but will add the metric as a "Counter" type
func (a *Accumulator) AddCounter(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.addMetrics(measurement, fields, tags, types.MetricAnnotations{}, t...)
}

// AddSummary is the same as AddFields, but will add the metric as a "Summary" type
func (a *Accumulator) AddSummary(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.addMetrics(measurement, fields, tags, types.MetricAnnotations{}, t...)
}

// AddHistogram is the same as AddFields, but will add the metric as a "Histogram" type
func (a *Accumulator) AddHistogram(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.addMetrics(measurement, fields, tags, types.MetricAnnotations{}, t...)
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

// AddFieldsWithAnnotations have extra fields for the annotations attached to the measurement and fields
//
// Note the annotation are not attached to the measurement, but to the resulting labels set.
// Resulting labels set are all tags + the metric name which is measurement concatened with field name.
//
// This also means that if the same measurement (e.g. "cpu") need different annotations (e.g. a status for field "used" but none for field "system"),
// you must to multiple call to AddFieldsWithAnnotations
//
// If a status is set in the annotation, not threshold will be applied on the metrics
func (a *Accumulator) AddFieldsWithAnnotations(measurement string, fields map[string]interface{}, tags map[string]string, annotations types.MetricAnnotations, t ...time.Time) {
	a.addMetrics(measurement, fields, tags, annotations, t...)
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

func (a *Accumulator) addMetrics(measurement string, fields map[string]interface{}, tags map[string]string, annotations types.MetricAnnotations, t ...time.Time) {
	var ts time.Time

	if len(t) == 1 {
		ts = t[0]
	} else {
		ts = time.Now()
	}

	points := make([]types.MetricPoint, 0, len(fields))

	for name, valueRaw := range fields {
		labels := make(map[string]string)

		for k, v := range tags {
			labels[k] = v
		}

		if measurement == "" {
			labels[types.LabelName] = name
		} else {
			labels[types.LabelName] = measurement + "_" + name
		}

		value, err := convertInterface(valueRaw)
		if err != nil {
			logger.V(1).Printf("convertInterface failed. Ignoring point: %s", err)
			continue
		}

		points = append(points, types.MetricPoint{
			Point:       types.Point{Time: ts, Value: value},
			Labels:      labels,
			Annotations: annotations,
		})
	}

	a.Pusher.PushPoints(points)
}
