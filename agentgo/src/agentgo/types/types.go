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

import "time"
import "fmt"
import "reflect"

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
}

// Accumulator save the metric from telegraf
type Accumulator struct {
	metricPointSlice []MetricPoint
	errors           []error
}

// InitAccumulator initialize an accumulator
func InitAccumulator() Accumulator {
	return Accumulator{
		metricPointSlice: nil,
		errors:           nil,
	}
}

// AddFields adds a metric to the accumulator with the given measurement
// name, fields, and tags (and timestamp). If a timestamp is not provided,
// then the accumulator sets it to "now".
// Create a point with a value, decorating it with tags
// NOTE: tags is expected to be owned by the caller, don't mutate
// it after passing to Add.
func (accumulator *Accumulator) AddFields(measurement string,
	fields map[string]interface{},
	tags map[string]string,
	t ...time.Time) {
	for key, value := range fields {
		valuef, err := convertInterface(value)
		if err != nil {
			accumulator.AddError(fmt.Errorf("Error when converting type of %v_%v : %v", measurement, key, err))
		}
		accumulator.metricPointSlice = append(accumulator.metricPointSlice, MetricPoint{
			Name:  measurement + "_" + key,
			Tags:  tags,
			Type:  Fields,
			Value: valuef,
		})
	}
}

// AddGauge is the same as AddFields, but will add the metric as a "Gauge" type
func (accumulator *Accumulator) AddGauge(measurement string,
	fields map[string]interface{},
	tags map[string]string,
	t ...time.Time) {
	for key, value := range fields {
		valuef, err := convertInterface(value)
		if err != nil {
			accumulator.AddError(fmt.Errorf("Error when converting type of %v_%v : %v", measurement, key, err))
		}
		accumulator.metricPointSlice = append(accumulator.metricPointSlice, MetricPoint{
			Name:  measurement + "_" + key,
			Tags:  tags,
			Type:  Gauge,
			Value: valuef,
		})
	}
}

// AddCounter is the same as AddFields, but will add the metric as a "Counter" type
func (accumulator *Accumulator) AddCounter(measurement string,
	fields map[string]interface{},
	tags map[string]string,
	t ...time.Time) {
	for key, value := range fields {
		valuef, err := convertInterface(value)
		if err != nil {
			accumulator.AddError(fmt.Errorf("Error when converting type of %v_%v : %v", measurement, key, err))
		}
		accumulator.metricPointSlice = append(accumulator.metricPointSlice, MetricPoint{
			Name:  measurement + "_" + key,
			Tags:  tags,
			Type:  Counter,
			Value: valuef,
		})
	}
}

// AddSummary is the same as AddFields, but will add the metric as a "Summary" type
func (accumulator *Accumulator) AddSummary(measurement string,
	fields map[string]interface{},
	tags map[string]string,
	t ...time.Time) {
	for key, value := range fields {
		valuef, err := convertInterface(value)
		if err != nil {
			accumulator.AddError(fmt.Errorf("Error when converting type of %v_%v : %v", measurement, key, err))
		}
		accumulator.metricPointSlice = append(accumulator.metricPointSlice, MetricPoint{
			Name:  measurement + "_" + key,
			Tags:  tags,
			Type:  Summary,
			Value: valuef,
		})
	}
}

// AddHistogram is the same as AddFields, but will add the metric as a "Histogram" type
func (accumulator *Accumulator) AddHistogram(measurement string,
	fields map[string]interface{},
	tags map[string]string,
	t ...time.Time) {
	for key, value := range fields {
		valuef, err := convertInterface(value)
		if err != nil {
			accumulator.AddError(fmt.Errorf("Error when converting type of %v_%v : %v", measurement, key, err))
		}
		accumulator.metricPointSlice = append(accumulator.metricPointSlice, MetricPoint{
			Name:  measurement + "_" + key,
			Tags:  tags,
			Type:  Histogram,
			Value: valuef,
		})
	}
}

// SetPrecision do nothing right now
func (accumulator *Accumulator) SetPrecision(precision, interval time.Duration) {}

// AddError add an error to the Accumulator
func (accumulator *Accumulator) AddError(err error) {
	newError := make([]error, 1)
	newError[0] = err
	newError = newError[:0]
	if accumulator.errors == nil {
		accumulator.errors = newError
	} else {
		accumulator.errors = append(accumulator.errors, newError...)
	}
}

// GetMetricPointSlice return a slice of metrics containing by the accumulator
func (accumulator Accumulator) GetMetricPointSlice() []MetricPoint {
	return accumulator.metricPointSlice
}

// GetErrors return a slice of errors containings by the accumulator
func (accumulator Accumulator) GetErrors() []error {
	return accumulator.errors
}

// convertInterface convert the interface type in float64
// if it impossible return 0 and an error
func convertInterface(value interface{}) (float64, error) {
	switch value.(type) {
	case uint64:
		return float64(value.(uint64)), nil
	case float64:
		return value.(float64), nil
	default:
		var r = reflect.TypeOf(value)
		return float64(0), fmt.Errorf("Value type not supported :(%v)", r)
	}
}
