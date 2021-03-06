package internal

import (
	"time"

	"github.com/influxdata/telegraf"
)

type Measurement struct {
	Name   string
	Fields map[string]interface{}
	Tags   map[string]string
	T      []time.Time
}

// StoreAccumulator store in memory all value pushed by AddFields, AddGauge...
// All type (fields, gague, counter) are processed the same, and can't be distinguished.
type StoreAccumulator struct {
	Measurement []Measurement
	Errors      []error
}

// Send forward all captured measurement using acc.AddFields. It also send errors using AddError.
func (a *StoreAccumulator) Send(acc telegraf.Accumulator) {
	for _, m := range a.Measurement {
		acc.AddFields(m.Name, m.Fields, m.Tags, m.T...)
	}

	for _, err := range a.Errors {
		acc.AddError(err)
	}
}

func (a *StoreAccumulator) processMetrics(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.Measurement = append(a.Measurement, Measurement{
		Name:   measurement,
		Fields: fields,
		Tags:   tags,
		T:      t,
	})
}

// AddFields adds a metric to the accumulator with the given measurement
// name, fields, and tags (and timestamp). If a timestamp is not provided,
// then the accumulator sets it to "now".
func (a *StoreAccumulator) AddFields(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.processMetrics(measurement, fields, tags, t...)
}

// AddGauge is the same as AddFields, but will add the metric as a "Gauge" type.
func (a *StoreAccumulator) AddGauge(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.processMetrics(measurement, fields, tags, t...)
}

// AddCounter is the same as AddFields, but will add the metric as a "Counter" type.
func (a *StoreAccumulator) AddCounter(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.processMetrics(measurement, fields, tags, t...)
}

// AddSummary is the same as AddFields, but will add the metric as a "Summary" type.
func (a *StoreAccumulator) AddSummary(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.processMetrics(measurement, fields, tags, t...)
}

// AddHistogram is the same as AddFields, but will add the metric as a "Histogram" type.
func (a *StoreAccumulator) AddHistogram(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.processMetrics(measurement, fields, tags, t...)
}

// AddMetric adds an metric to the accumulator.
func (a *StoreAccumulator) AddMetric(telegraf.Metric) {
	a.AddError(errNotImplemented)
}

// AddError reports an error.
func (a *StoreAccumulator) AddError(err error) {
	a.Errors = append(a.Errors, err)
}

// SetPrecision takes two time.Duration objects. If the first is non-zero,
// it sets that as the precision. Otherwise, it takes the second argument
// as the order of time that the metrics should be rounded to, with the
// maximum being 1s.
func (a *StoreAccumulator) SetPrecision(precision time.Duration) {
	a.AddError(errNotImplemented)
}

// WithTracking upgrades to a TrackingAccumulator with space for maxTracked
// metrics/batches.
func (a *StoreAccumulator) WithTracking(maxTracked int) telegraf.TrackingAccumulator {
	a.AddError(errNotImplemented)
	return nil
}
