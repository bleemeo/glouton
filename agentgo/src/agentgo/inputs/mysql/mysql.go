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

// Package for mysql input not finished

package mysql

import (
	"errors"
	"fmt"
	"time"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/mysql"
)

// Input contains input information about Mysql
type Input struct {
	telegraf.Input
}

// New initialise mysql.Input
func New(server string) (i *Input, err error) {
	var input, ok = telegraf_inputs.Inputs["mysql"]
	if ok {
		mysqlInput, ok := input().(*mysql.Mysql)
		if ok {
			slice := append(make([]string, 0), server)
			mysqlInput.Servers = slice
			i = &Input{mysqlInput}
		} else {
			err = errors.New("Telegraf \"mysql\" input type is not mysql.Mysql")
		}
	} else {
		err = errors.New("Telegraf don't have \"mysql\" input")
	}
	return
}

// Gather takes in an accumulator and adds the metrics that the Input
// gathers. This is called every "interval"
func (i *Input) Gather(acc telegraf.Accumulator) error {
	mysqlAccumulator := accumulator{acc}
	err := i.Input.Gather(&mysqlAccumulator)
	return err
}

// accumulator save the mysql metric from telegraf
type accumulator struct {
	accumulator telegraf.Accumulator
}

// AddFields adds a metric to the accumulator with the given measurement
// name, fields, and tags (and timestamp). If a timestamp is not provided,
// then the accumulator sets it to "now".
// Create a point with a value, decorating it with tags
// NOTE: tags is expected to be owned by the caller, don't mutate
// it after passing to Add.
func (a *accumulator) AddFields(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	// TODO
	a.accumulator.AddFields(measurement, fields, tags, t...)
}

// AddError add an error to the accumulator
func (a *accumulator) AddError(err error) {
	a.accumulator.AddError(err)
}

// This functions are useless for mysql metric.
// They are not implemented

// AddGauge is useless for mysql
func (a *accumulator) AddGauge(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.accumulator.AddError(fmt.Errorf("AddGauge not implemented for mysql accumulator"))
}

// AddCounter is useless for mysql
func (a *accumulator) AddCounter(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.accumulator.AddError(fmt.Errorf("AddCounter not implemented for mysql accumulator"))
}

// AddSummary is useless for mysql
func (a *accumulator) AddSummary(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.accumulator.AddError(fmt.Errorf("AddSummary not implemented for mysql accumulator"))
}

// AddHistogram is useless for mysql
func (a *accumulator) AddHistogram(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.accumulator.AddError(fmt.Errorf("AddHistogram not implemented for mysql accumulator"))
}

// SetPrecision is useless for mysql
func (a *accumulator) SetPrecision(precision time.Duration) {
	a.accumulator.AddError(fmt.Errorf("SetPrecision not implemented for mysql accumulator"))
}

func (a *accumulator) AddMetric(telegraf.Metric) {
	a.accumulator.AddError(fmt.Errorf("AddMetric not implemented for mysql accumulator"))
}

func (a *accumulator) WithTracking(maxTracked int) telegraf.TrackingAccumulator {
	a.accumulator.AddError(fmt.Errorf("WithTracking not implemented for mysql accumulator"))
	return nil
}
