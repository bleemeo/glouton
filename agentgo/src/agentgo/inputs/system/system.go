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

package system

import (
	"errors"
	"fmt"
	"time"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/system"
)

// Input countains input information about system
type Input struct {
	telegraf.Input
}

// New initialise system.Input
func New() (i *Input, err error) {
	var input, ok = telegraf_inputs.Inputs["system"]
	if ok {
		systemInput := input().(*system.SystemStats)
		i = &Input{systemInput}
	} else {
		err = errors.New("Telegraf don't have \"system\" input")
	}
	return
}

// Gather takes in an accumulator and adds the metrics that the Input
// gathers. This is called every "interval"
func (input *Input) Gather(acc telegraf.Accumulator) error {
	systemAccumulator := initAccumulator(acc)
	err := input.Input.Gather(&systemAccumulator)
	return err
}

// accumulator save the system metric from telegraf
type accumulator struct {
	acc telegraf.Accumulator
}

// InitAccumulator initialize an accumulator
func initAccumulator(acc telegraf.Accumulator) accumulator {
	return accumulator{
		acc: acc,
	}
}

// AddGauge adds a metric to the accumulator with the given measurement
// name, fields, and tags (and timestamp). If a timestamp is not provided,
// then the accumulator sets it to "now".
// Create a point with a value, decorating it with tags
// NOTE: tags is expected to be owned by the caller, don't mutate
// it after passing to Add.
func (accumulator *accumulator) AddGauge(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	finalFields := make(map[string]interface{})
	for metricName, value := range fields {
		finalMetricName := measurement + "_" + metricName
		if finalMetricName == "system_n_users" {
			finalMetricName = "users_logged"
		} else if finalMetricName == "system_n_cpus" {
			continue
		}
		finalFields[finalMetricName] = value
	}
	accumulator.acc.AddGauge(measurement, finalFields, tags, t...)
}

// AddCounter adds a metric to the accumulator with the given measurement
// name, fields, and tags (and timestamp). If a timestamp is not provided,
// then the accumulator sets it to "now".
// Create a point with a value, decorating it with tags
// NOTE: tags is expected to be owned by the caller, don't mutate
// it after passing to Add.
func (accumulator *accumulator) AddCounter(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	// AddCounter add system_uptime metric that we do not want.
}

// AddFields adds a metric to the accumulator with the given measurement
// name, fields, and tags (and timestamp). If a timestamp is not provided,
// then the accumulator sets it to "now".
// Create a point with a value, decorating it with tags
// NOTE: tags is expected to be owned by the caller, don't mutate
// it after passing to Add.
func (accumulator *accumulator) AddFields(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	// AddCounter add system_uptime_format metric that we do not want.
}

// AddError add an error to the accumulator
func (accumulator *accumulator) AddError(err error) {
	accumulator.acc.AddError(err)
}

// This functions are useless for system metric.
// They are not implemented

// AddSummary is useless for system
func (accumulator *accumulator) AddSummary(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	accumulator.acc.AddError(fmt.Errorf("AddSummary not implemented for system accumulator"))
}

// AddHistogram is useless for system
func (accumulator *accumulator) AddHistogram(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	accumulator.acc.AddError(fmt.Errorf("AddHistogram not implemented for system accumulator"))
}

// SetPrecision is useless for system
func (accumulator *accumulator) SetPrecision(precision, interval time.Duration) {
	accumulator.acc.AddError(fmt.Errorf("SetPrecision not implemented for system accumulator"))
}
