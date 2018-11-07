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
	"fmt"
	"time"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/nginx"
)

// Input countains input information about Mysql
type Input struct {
	telegraf.Input
}

// New initialise nginx.Input
func New(url string) *Input {
	var input, ok = telegraf_inputs.Inputs["nginx"]
	if ok {
		nginxInput, ok := input().(*nginx.Nginx)
		if ok {
			slice := append(make([]string, 0), url)
			nginxInput.Urls = slice
			nginxInput.InsecureSkipVerify = false
			return &Input{nginxInput}
		}
	}
	return &Input{nil}
}

// Gather takes in an accumulator and adds the metrics that the Input
// gathers. This is called every "interval"
func (input *Input) Gather(acc telegraf.Accumulator) error {
	nginxAccumulator := initAccumulator(acc)
	err := input.Input.Gather(&nginxAccumulator)
	return err
}

// accumulator save the nginx metric from telegraf
type accumulator struct {
	acc telegraf.Accumulator
}

// InitAccumulator initialize an accumulator
func initAccumulator(acc telegraf.Accumulator) accumulator {
	return accumulator{
		acc: acc,
	}
}

// AddFields adds a metric to the accumulator with the given measurement
// name, fields, and tags (and timestamp). If a timestamp is not provided,
// then the accumulator sets it to "now".
// Create a point with a value, decorating it with tags
// NOTE: tags is expected to be owned by the caller, don't mutate
// it after passing to Add.
func (accumulator *accumulator) AddFields(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	// TODO
	(accumulator.acc).AddFields(measurement, fields, tags, t...)
}

// AddError add an error to the accumulator
func (accumulator *accumulator) AddError(err error) {
	(accumulator.acc).AddError(err)
}

// This functions are useless for nginx metric.
// They are not implemented

// AddGauge is useless for nginx
func (accumulator *accumulator) AddGauge(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	(accumulator.acc).AddError(fmt.Errorf("AddGauge not implemented for nginx accumulator"))
}

// AddCounter is useless for nginx
func (accumulator *accumulator) AddCounter(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	(accumulator.acc).AddError(fmt.Errorf("AddCounter not implemented for nginx accumulator"))
}

// AddSummary is useless for nginx
func (accumulator *accumulator) AddSummary(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	(accumulator.acc).AddError(fmt.Errorf("AddSummary not implemented for nginx accumulator"))
}

// AddHistogram is useless for nginx
func (accumulator *accumulator) AddHistogram(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	(accumulator.acc).AddError(fmt.Errorf("AddHistogram not implemented for nginx accumulator"))
}

// SetPrecision is useless for nginx
func (accumulator *accumulator) SetPrecision(precision, interval time.Duration) {
	(accumulator.acc).AddError(fmt.Errorf("SetPrecision not implemented for nginx accumulator"))
}
