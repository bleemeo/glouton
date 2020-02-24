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

package store

import (
	"fmt"
	"glouton/logger"
	"glouton/types"
	"reflect"
	"time"

	"github.com/influxdata/telegraf"
)

// Accumulator store points in the Store and optionally add extra information like
// service association and container association
type Accumulator struct {
	store *Store
}

// Accumulator returns an Accumulator compatible with our input collector.
//
// This accumlator will store point in the Store
func (s *Store) Accumulator() *Accumulator {
	return &Accumulator{
		store: s,
	}
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
func (a *Accumulator) AddFieldsWithAnnotations(measurement string, fields map[string]interface{}, tags map[string]string, annotations types.MetricAnnotations, t ...time.Time) {
	a.addMetrics(measurement, fields, tags, annotations, t...)
}

func (a *Accumulator) addMetrics(measurement string, fields map[string]interface{}, tags map[string]string, annotations types.MetricAnnotations, t ...time.Time) {
	var ts time.Time
	if len(t) == 1 {
		ts = t[0]
	} else {
		ts = time.Now()
	}
	a.store.notifeeLock.Lock()
	defer a.store.notifeeLock.Unlock()
	hasNotifiee := len(a.store.notifyCallbacks) > 0
	points := a.addMetricsLock(measurement, fields, tags, annotations, ts, hasNotifiee)
	for _, cb := range a.store.notifyCallbacks {
		cb(points)
	}
}

func (a *Accumulator) addMetricsLock(measurement string, fields map[string]interface{}, tags map[string]string, annotations types.MetricAnnotations, ts time.Time, returnPoints bool) []types.MetricPoint {
	a.store.lock.Lock()
	defer a.store.lock.Unlock()
	var result []types.MetricPoint

	for name, value := range fields {
		labels := make(map[string]string)
		for k, v := range tags {
			labels[k] = v
		}
		if measurement == "" {
			labels[types.LabelName] = name
		} else {
			labels[types.LabelName] = measurement + "_" + name
		}
		value, err := convertInterface(value)
		if err != nil {
			logger.V(1).Printf("convertInterface failed. Ignoring point: %s", err)
			continue
		}
		metric := a.store.metricGetOrCreate(labels, annotations)
		point := types.Point{Time: ts, Value: value}

		if returnPoints {
			result = append(result, types.MetricPoint{
				Point:       point,
				Labels:      labels,
				Annotations: annotations,
			})
		}
		a.store.addPoint(metric.metricID, point)
	}
	return result
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
