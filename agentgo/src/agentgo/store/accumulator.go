package store

import (
	"agentgo/types"
	"fmt"
	"log"
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
	a.addMetrics(measurement, fields, tags, nil, false, t...)
}

// AddGauge is the same as AddFields, but will add the metric as a "Gauge" type
func (a *Accumulator) AddGauge(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.addMetrics(measurement, fields, tags, nil, false, t...)
}

// AddCounter is the same as AddFields, but will add the metric as a "Counter" type
func (a *Accumulator) AddCounter(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.addMetrics(measurement, fields, tags, nil, false, t...)
}

// AddSummary is the same as AddFields, but will add the metric as a "Summary" type
func (a *Accumulator) AddSummary(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.addMetrics(measurement, fields, tags, nil, false, t...)
}

// AddHistogram is the same as AddFields, but will add the metric as a "Histogram" type
func (a *Accumulator) AddHistogram(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.addMetrics(measurement, fields, tags, nil, false, t...)
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
		log.Printf("DBG: Add error called with: %v", err)
	}
}

// AddFieldsWithStatus have extra fields for the status of each points
//
// It works like AddFields, but:
// * If a metric in fields also have an entry in statuses, a status for this point will be set
// * If createStatusOf is true, each metric which has a statuses will also generate an additional metrics named "$NAME_status"
func (a *Accumulator) AddFieldsWithStatus(measurement string, fields map[string]interface{}, tags map[string]string, statuses map[string]types.StatusDescription, createStatusOf bool, t ...time.Time) {
	a.addMetrics(measurement, fields, tags, statuses, createStatusOf, t...)
}

func (a *Accumulator) addMetrics(measurement string, fields map[string]interface{}, tags map[string]string, statuses map[string]types.StatusDescription, createStatusOf bool, t ...time.Time) {
	labels := make(map[string]string)
	for k, v := range tags {
		labels[k] = v
	}
	var ts time.Time
	if len(t) == 1 {
		ts = t[0]
	} else {
		ts = time.Now()
	}
	a.store.lock.Lock()
	defer a.store.lock.Unlock()
	for name, value := range fields {
		if measurement == "" {
			labels["__name__"] = name
		} else {
			labels["__name__"] = measurement + "_" + name
		}
		value, err := convertInterface(value)
		if err != nil {
			log.Panicf("convertInterface failed. Ignoring point: %s", err)
			continue
		}
		metric := a.store.metricGetOrCreate(labels, 0)
		point := types.PointStatus{
			Point: types.Point{Time: ts, Value: value},
		}
		if status, ok := statuses[name]; ok {
			point.StatusDescription = status
			if createStatusOf {
				copyPoint := point
				copyPoint.Value = float64(point.CurrentStatus.NagiosCode())
				labels["__name__"] += "_status"
				metric2 := a.store.metricGetOrCreate(labels, metric.metricID)
				a.store.addPoint(metric2.metricID, copyPoint)
			}
		}
		a.store.addPoint(metric.metricID, point)
	}
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
