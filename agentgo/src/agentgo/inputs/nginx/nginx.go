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

// Package for nginx input not finished

package nginx

import (
	"errors"
	"fmt"
	"time"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/nginx"
)

type metricPoint struct {
	value      uint64
	metricTime time.Time
}

// Input countains input information about Mysql
type Input struct {
	telegraf.Input
	pastValues map[string]metricPoint // metricName => metricPoint
}

// New initialise nginx.Input
func New(url string) (i *Input, err error) {
	var input, ok = telegraf_inputs.Inputs["nginx"]
	if ok {
		nginxInput, ok := input().(*nginx.Nginx)
		if ok {
			slice := append(make([]string, 0), url)
			nginxInput.Urls = slice
			nginxInput.InsecureSkipVerify = false
			i = &Input{
				nginxInput,
				make(map[string]metricPoint),
			}
		} else {
			err = errors.New("Telegraf \"nginx\" input type is not nginx.Nginx")
		}
	} else {
		err = errors.New("Telegraf don't have \"nginx\" input")
	}
	return
}

// Gather takes in an accumulator and adds the metrics that the Input
// gathers. This is called every "interval"
func (i *Input) Gather(acc telegraf.Accumulator) error {
	nginxAccumulator := accumulator{
		acc,
		i.pastValues,
		make(map[string]metricPoint),
	}
	err := i.Input.Gather(&nginxAccumulator)
	i.pastValues = nginxAccumulator.currentValues
	return err
}

// accumulator save the nginx metric from telegraf
type accumulator struct {
	accumulator   telegraf.Accumulator
	pastValues    map[string]metricPoint
	currentValues map[string]metricPoint
}

// AddFields adds a metric to the accumulator with the given measurement
// name, fields, and tags (and timestamp). If a timestamp is not provided,
// then the accumulator sets it to "now".
// Create a point with a value, decorating it with tags
// NOTE: tags is expected to be owned by the caller, don't mutate
// it after passing to Add.
func (a *accumulator) AddFields(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	finalFields := make(map[string]interface{})
	for metricName, value := range fields {
		deriveValue := false
		finalMetricName := measurement + "_" + metricName
		switch metricName {
		case "requests":
			finalMetricName = "nginx_requests"
			deriveValue = true
		case "accepts":
			finalMetricName = "nginx_connections_accepted"
			deriveValue = true
		case "handled":
			deriveValue = true
		case "reading", "writing", "waiting", "active":
			//Keep name unchanged
		default:
			continue
		}

		if deriveValue {
			var metricTime time.Time
			if len(t) != 1 {
				metricTime = time.Now()
			} else {
				metricTime = t[0]
			}
			pastMetricSave, ok := a.pastValues[finalMetricName]
			a.currentValues[finalMetricName] = metricPoint{value.(uint64), metricTime}
			if ok {
				valuef := (float64(value.(uint64)) - float64(pastMetricSave.value)) / metricTime.Sub(pastMetricSave.metricTime).Seconds()
				finalFields[finalMetricName] = valuef
			} else {
				continue
			}
		} else {
			finalFields[finalMetricName] = value
		}
	}
	a.accumulator.AddFields(measurement, finalFields, nil, t...)
}

// AddError add an error to the accumulator
func (a *accumulator) AddError(err error) {
	a.accumulator.AddError(err)
}

// This functions are useless for nginx metric.
// They are not implemented

// AddGauge is useless for nginx
func (a *accumulator) AddGauge(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.accumulator.AddError(fmt.Errorf("AddGauge not implemented for nginx accumulator"))
}

// AddCounter is useless for nginx
func (a *accumulator) AddCounter(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.accumulator.AddError(fmt.Errorf("AddCounter not implemented for nginx accumulator"))
}

// AddSummary is useless for nginx
func (a *accumulator) AddSummary(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.accumulator.AddError(fmt.Errorf("AddSummary not implemented for nginx accumulator"))
}

// AddHistogram is useless for nginx
func (a *accumulator) AddHistogram(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.accumulator.AddError(fmt.Errorf("AddHistogram not implemented for nginx accumulator"))
}

// SetPrecision is useless for nginx
func (a *accumulator) SetPrecision(precision time.Duration) {
	a.accumulator.AddError(fmt.Errorf("SetPrecision not implemented for nginx accumulator"))
}

func (a *accumulator) AddMetric(telegraf.Metric) {
	a.accumulator.AddError(fmt.Errorf("AddMetric not implemented for nginx accumulator"))
}

func (a *accumulator) WithTracking(maxTracked int) telegraf.TrackingAccumulator {
	a.accumulator.AddError(fmt.Errorf("WithTracking not implemented for nginx accumulator"))
	return nil
}
