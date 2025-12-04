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

package inputs

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"sync"
	"time"

	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/types"

	"github.com/influxdata/telegraf"
)

var (
	errNotImplemented   = errors.New("not implemented")
	errMissingMethod    = errors.New("AddFieldsWithAnnotations method missing, the annotation is lost")
	errTypeNotSupported = errors.New("value type not supported")
)

// AnnotationAccumulator is a similar to an telegraf.Accumulator but allow to send metric with annocations.
type AnnotationAccumulator interface {
	AddFieldsWithAnnotations(measurement string, fields map[string]any, tags map[string]string, annotations types.MetricAnnotations, t ...time.Time)
	AddError(err error)
}

type ErrorAccumulator interface {
	Errors() []error
}

// Accumulator implement telegraf.Accumulator (+AddFieldsWithAnnotations) and emit the metric points.
// Gathering errors can be added and retrieved concurrently, using AddError() and Errors().
type Accumulator struct {
	Pusher  types.PointPusher
	Context context.Context //nolint:containedctx

	l    sync.Mutex
	errs []error
}

// AddFields adds a metric to the accumulator with the given measurement
// name, fields, and tags (and timestamp). If a timestamp is not provided,
// then the accumulator sets it to "now".
// Create a point with a value, decorating it with tags
// NOTE: tags is expected to be owned by the caller, don't mutate
// it after passing to Add.
func (a *Accumulator) AddFields(measurement string, fields map[string]any, tags map[string]string, t ...time.Time) {
	a.addMetrics(measurement, fields, tags, types.MetricAnnotations{}, t...)
}

// AddGauge is the same as AddFields, but will add the metric as a "Gauge" type.
func (a *Accumulator) AddGauge(measurement string, fields map[string]any, tags map[string]string, t ...time.Time) {
	a.addMetrics(measurement, fields, tags, types.MetricAnnotations{}, t...)
}

// AddCounter is the same as AddFields, but will add the metric as a "Counter" type.
func (a *Accumulator) AddCounter(measurement string, fields map[string]any, tags map[string]string, t ...time.Time) {
	a.addMetrics(measurement, fields, tags, types.MetricAnnotations{}, t...)
}

// AddSummary is the same as AddFields, but will add the metric as a "Summary" type.
func (a *Accumulator) AddSummary(measurement string, fields map[string]any, tags map[string]string, t ...time.Time) {
	a.addMetrics(measurement, fields, tags, types.MetricAnnotations{}, t...)
}

// AddHistogram is the same as AddFields, but will add the metric as a "Histogram" type.
func (a *Accumulator) AddHistogram(measurement string, fields map[string]any, tags map[string]string, t ...time.Time) {
	a.addMetrics(measurement, fields, tags, types.MetricAnnotations{}, t...)
}

// SetPrecision do nothing right now.
func (a *Accumulator) SetPrecision(time.Duration) {
	a.AddError(fmt.Errorf("SetPrecision %w", errNotImplemented))
}

// AddMetric is not yet implemented.
func (a *Accumulator) AddMetric(telegraf.Metric) {
	a.AddError(fmt.Errorf("AddMetric %w", errNotImplemented))
}

// WithTracking is not yet implemented.
func (a *Accumulator) WithTracking(maxTracked int) telegraf.TrackingAccumulator {
	_ = maxTracked

	a.AddError(fmt.Errorf("WithTracking %w", errNotImplemented))

	return nil
}

// AddError add an error to the Accumulator.
func (a *Accumulator) AddError(err error) {
	a.l.Lock()
	defer a.l.Unlock()

	a.errs = append(a.errs, err)
}

func (a *Accumulator) Errors() []error {
	a.l.Lock()
	defer a.l.Unlock()

	return slices.Clone(a.errs)
}

// AddFieldsWithAnnotations have extra fields for the annotations attached to the measurement and fields
//
// Note the annotation are not attached to the measurement, but to the resulting labels set.
// Resulting labels set are all tags + the metric name which is measurement concatenated with field name.
//
// This also means that if the same measurement (e.g. "cpu") need different annotations (e.g. a status for field "used" but none for field "system"),
// you must to multiple call to AddFieldsWithAnnotations
//
// If a status is set in the annotation, not threshold will be applied on the metrics.
func (a *Accumulator) AddFieldsWithAnnotations(measurement string, fields map[string]any, tags map[string]string, annotations types.MetricAnnotations, t ...time.Time) {
	a.addMetrics(measurement, fields, tags, annotations, t...)
}

// ConvertToFloat convert the interface type in float64.
func ConvertToFloat(value any) (float64, error) {
	switch value := value.(type) {
	case float64:
		return value, nil
	case float32:
		return float64(value), nil
	case int:
		return float64(value), nil
	case int32:
		return float64(value), nil
	case int64:
		return float64(value), nil
	case uint:
		return float64(value), nil
	case uint32:
		return float64(value), nil
	case uint64:
		return float64(value), nil
	case bool:
		if value {
			return 1, nil
		}

		return 0, nil
	default:
		valueType := reflect.TypeOf(value)

		return 0, fmt.Errorf("%w : %v", errTypeNotSupported, valueType)
	}
}

func (a *Accumulator) addMetrics(measurement string, fields map[string]any, tags map[string]string, annotations types.MetricAnnotations, t ...time.Time) {
	var ts time.Time

	if len(t) == 1 {
		ts = t[0]
	} else {
		ts = time.Now()
	}

	points := make([]types.MetricPoint, 0, len(fields))

	for name, valueRaw := range fields {
		labels := make(map[string]string)

		maps.Copy(labels, tags)

		if measurement == "" {
			labels[types.LabelName] = name
		} else {
			labels[types.LabelName] = measurement + "_" + name
		}

		value, err := ConvertToFloat(valueRaw)
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

	a.Pusher.PushPoints(a.Context, points)
}

// CollectorConfig represents the configuration of a collector.
type CollectorConfig struct {
	DFRootPath      string
	DFPathMatcher   types.Matcher
	DFIgnoreFSTypes []string
	NetIfMatcher    types.MatcherRegexp
	IODiskMatcher   types.MatcherRegexp
}

// FixedTimeAccumulator implement telegraf.Accumulator (+AddFieldsWithAnnotations) and use given Time for all points.
type FixedTimeAccumulator struct {
	Time time.Time
	Acc  telegraf.Accumulator
}

// AddFields adds a metric to the accumulator with the given measurement
// name, fields, and tags (and timestamp). If a timestamp is not provided,
// then the accumulator sets it to "now".
// Create a point with a value, decorating it with tags
// NOTE: tags is expected to be owned by the caller, don't mutate
// it after passing to Add.
func (a FixedTimeAccumulator) AddFields(measurement string, fields map[string]any, tags map[string]string, _ ...time.Time) {
	a.Acc.AddFields(measurement, fields, tags, a.Time)
}

// AddGauge is the same as AddFields, but will add the metric as a "Gauge" type.
func (a FixedTimeAccumulator) AddGauge(measurement string, fields map[string]any, tags map[string]string, _ ...time.Time) {
	a.Acc.AddGauge(measurement, fields, tags, a.Time)
}

// AddCounter is the same as AddFields, but will add the metric as a "Counter" type.
func (a FixedTimeAccumulator) AddCounter(measurement string, fields map[string]any, tags map[string]string, _ ...time.Time) {
	a.Acc.AddCounter(measurement, fields, tags, a.Time)
}

// AddSummary is the same as AddFields, but will add the metric as a "Summary" type.
func (a FixedTimeAccumulator) AddSummary(measurement string, fields map[string]any, tags map[string]string, _ ...time.Time) {
	a.Acc.AddSummary(measurement, fields, tags, a.Time)
}

// AddHistogram is the same as AddFields, but will add the metric as a "Histogram" type.
func (a FixedTimeAccumulator) AddHistogram(measurement string, fields map[string]any, tags map[string]string, _ ...time.Time) {
	a.Acc.AddHistogram(measurement, fields, tags, a.Time)
}

// SetPrecision do nothing right now.
func (a FixedTimeAccumulator) SetPrecision(time.Duration) {
	a.AddError(fmt.Errorf("SetPrecision %w", errNotImplemented))
}

// AddMetric is not yet implemented.
func (a FixedTimeAccumulator) AddMetric(telegraf.Metric) {
	a.AddError(fmt.Errorf("AddMetric %w", errNotImplemented))
}

// WithTracking is not yet implemented.
func (a FixedTimeAccumulator) WithTracking(maxTracked int) telegraf.TrackingAccumulator {
	_ = maxTracked

	a.AddError(fmt.Errorf("WithTracking %w", errNotImplemented))

	return nil
}

// AddError add an error to the Accumulator.
func (a FixedTimeAccumulator) AddError(err error) {
	a.Acc.AddError(err)
}

// AddFieldsWithAnnotations have extra fields for the annotations attached to the measurement and fields
//
// This method call AddFieldsWithAnnotations() is available and call AddGauge + AddError otherwise.
func (a FixedTimeAccumulator) AddFieldsWithAnnotations(measurement string, fields map[string]any, tags map[string]string, annotations types.MetricAnnotations, _ ...time.Time) {
	if annocationAcc, ok := a.Acc.(AnnotationAccumulator); ok {
		annocationAcc.AddFieldsWithAnnotations(measurement, fields, tags, annotations, a.Time)

		return
	}

	a.Acc.AddGauge(measurement, fields, tags, a.Time)
	a.Acc.AddError(errMissingMethod)
}

var (
	ErrUnexpectedType = errors.New("input does not have the expected type")
	ErrDisabledInput  = errors.New("input is not enabled in service Telegraf")
	ErrMissingCommand = errors.New("missing command for input")
)
