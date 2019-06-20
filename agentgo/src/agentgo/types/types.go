// Copyright 2015-2018 Bleemeo
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

// Types for agentgo

package types

import (
	"fmt"
	"reflect"
	"time"

	"github.com/influxdata/telegraf"
)

const (
	// Fields type
	Fields int = iota

	// Gauge type
	Gauge

	// Counter type
	Counter

	// Summary type
	Summary

	// Histogram type
	Histogram
)

// Metric represent a metric object
type Metric interface {
	// Labels returns labels of the metric
	Labels() map[string]string

	// Points returns points between the two given time range (boundary are included).
	Points(start, end time.Time) ([]Point, error)
}

// Point is the value of one metric at a given time
type Point struct {
	Time  time.Time
	Value float64
}

// MetricPoint contains metric information and his value
type MetricPoint struct {
	// Name of the metric
	Name string

	// Tag list of the metric
	Tags map[string]string

	// Type of the metric
	Type int

	// Value of the metric
	Value float64

	// Time
	Time time.Time
}

// Accumulator save the metric from telegraf
type Accumulator struct {
	metricPointSlice []MetricPoint
	errors           []error
}

// AddFields adds a metric to the accumulator with the given measurement
// name, fields, and tags (and timestamp). If a timestamp is not provided,
// then the accumulator sets it to "now".
// Create a point with a value, decorating it with tags
// NOTE: tags is expected to be owned by the caller, don't mutate
// it after passing to Add.
func (a *Accumulator) AddFields(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.addMetrics(measurement, fields, tags, Fields, t...)
}

// AddGauge is the same as AddFields, but will add the metric as a "Gauge" type
func (a *Accumulator) AddGauge(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.addMetrics(measurement, fields, tags, Gauge, t...)
}

// AddCounter is the same as AddFields, but will add the metric as a "Counter" type
func (a *Accumulator) AddCounter(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.addMetrics(measurement, fields, tags, Counter, t...)
}

// AddSummary is the same as AddFields, but will add the metric as a "Summary" type
func (a *Accumulator) AddSummary(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.addMetrics(measurement, fields, tags, Summary, t...)
}

// AddHistogram is the same as AddFields, but will add the metric as a "Histogram" type
func (a *Accumulator) AddHistogram(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.addMetrics(measurement, fields, tags, Histogram, t...)
}

// SetPrecision do nothing right now
func (a *Accumulator) SetPrecision(precision time.Duration) {
	a.AddError(fmt.Errorf("SetPrecision not implemented for types accumulator"))
}

// AddMetric is not yet implemented
func (a *Accumulator) AddMetric(telegraf.Metric) {
	a.AddError(fmt.Errorf("AddMetric not implemented for types accumulator"))
}

// WithTracking is not yet implemented
func (a *Accumulator) WithTracking(maxTracked int) telegraf.TrackingAccumulator {
	a.AddError(fmt.Errorf("WithTracking not implemented for types accumulator"))
	return nil
}

// AddError add an error to the Accumulator
func (a *Accumulator) AddError(err error) {
	a.errors = append(a.errors, err)
}

// GetMetricPointSlice return a slice of metrics containing by the accumulator
func (a Accumulator) GetMetricPointSlice() []MetricPoint {
	return a.metricPointSlice
}

// GetErrors return a slice of errors containings by the accumulator
func (a Accumulator) GetErrors() []error {
	return a.errors
}

// convertInterface convert the interface type in float64
func convertInterface(value interface{}) (float64, error) {
	switch value.(type) {
	case uint64:
		return float64(value.(uint64)), nil
	case float64:
		return value.(float64), nil
	case int:
		return float64(value.(int)), nil
	case int64:
		return float64(value.(int64)), nil
	default:
		var valueType = reflect.TypeOf(value)
		return float64(0), fmt.Errorf("Value type not supported :(%v)", valueType)
	}
}

func (a *Accumulator) addMetrics(measurement string, fields map[string]interface{}, tags map[string]string, metricType int, t ...time.Time) {
	var metricTime time.Time
	if len(t) == 1 {
		metricTime = t[0]
	} else {
		metricTime = time.Now()
	}
	for metricName, value := range fields {
		valuef, err := convertInterface(value)
		if err == nil {
			a.metricPointSlice = append(a.metricPointSlice, MetricPoint{
				Name:  metricName,
				Tags:  tags,
				Type:  metricType,
				Value: valuef,
				Time:  metricTime,
			})
		} else {
			a.AddError(err)
		}
	}
}
