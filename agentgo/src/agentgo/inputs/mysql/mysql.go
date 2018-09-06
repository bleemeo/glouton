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

// Package for mysql input

package mysql

import (
	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/mysql"
	"time"
)

// Input countains input information about Mysql
type Input struct {
	mysqlInput telegraf.Input
}

// NewInput initialise mysql.Input
func NewInput(server string) Input {
	var input, ok = telegraf_inputs.Inputs["mysql"]
	if ok {
		mysqlInput, ok := input().(*mysql.Mysql)
		if ok {
			slice := append(make([]string, 0), server)
			mysqlInput.Servers = slice
			return Input{
				mysqlInput: mysqlInput,
			}
		}
	}
	return Input{
		mysqlInput: nil,
	}
}

// SampleConfig returns the default configuration of the Input
func (input Input) SampleConfig() string {
	return input.mysqlInput.SampleConfig()
}

// Description returns a one-sentence description on the Input
func (input Input) Description() string {
	return input.mysqlInput.Description()
}

// Gather takes in an accumulator and adds the metrics that the Input
// gathers. This is called every "interval"
func (input Input) Gather(acc telegraf.Accumulator) error {
	mysqlAccumulator := initAccumulator(&acc)
	err := input.mysqlInput.Gather(&mysqlAccumulator)
	return err
}

// Accumulator save the mysql metric from telegraf
type Accumulator struct {
	acc *telegraf.Accumulator
}

// InitAccumulator initialize an accumulator
func initAccumulator(acc *telegraf.Accumulator) Accumulator {
	return Accumulator{
		acc: acc,
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
	// TODO
	(*accumulator.acc).AddFields(measurement, fields, nil)
}

// AddError add an error to the Accumulator
func (accumulator *Accumulator) AddError(err error) {
	(*accumulator.acc).AddError(err)
}

// GetAccumulator return the accumulator field
func (accumulator Accumulator) GetAccumulator() telegraf.Accumulator {
	return *accumulator.acc
}

// This functions are useless for mysql metric.
// They are not implemented

// AddGauge is useless for mysql
func (accumulator *Accumulator) AddGauge(measurement string,
	fields map[string]interface{},
	tags map[string]string,
	t ...time.Time) {
}

// AddCounter is useless for mysql
func (accumulator *Accumulator) AddCounter(measurement string,
	fields map[string]interface{},
	tags map[string]string,
	t ...time.Time) {
}

// AddSummary is useless for mysql
func (accumulator *Accumulator) AddSummary(measurement string,
	fields map[string]interface{},
	tags map[string]string,
	t ...time.Time) {
}

// AddHistogram is useless for mysql
func (accumulator *Accumulator) AddHistogram(measurement string,
	fields map[string]interface{},
	tags map[string]string,
	t ...time.Time) {
}

// SetPrecision is useless for mysql
func (accumulator *Accumulator) SetPrecision(precision, interval time.Duration) {}
