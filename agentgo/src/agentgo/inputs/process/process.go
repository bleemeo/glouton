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

package process

import (
	"errors"
	"fmt"
	"time"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/processes"
)

// Input countains input information about process
type Input struct {
	telegraf.Input
}

// New initialise process.Input
func New() (i *Input, err error) {
	var input, ok = telegraf_inputs.Inputs["processes"]
	if ok {
		processInput := input().(*processes.Processes)
		i = &Input{processInput}
	} else {
		err = errors.New("Telegraf don't have \"processes\" input")
	}
	return
}

// Gather takes in an accumulator and adds the metrics that the Input
// gathers. This is called every "interval"
func (input *Input) Gather(acc telegraf.Accumulator) error {
	processAccumulator := accumulator{acc}
	err := input.Input.Gather(&processAccumulator)
	return err
}

// accumulator save the process metric from telegraf
type accumulator struct {
	acc telegraf.Accumulator
}

// AddGauge adds a metric to the accumulator with the given measurement
// name, fields, and tags (and timestamp). If a timestamp is not provided,
// then the accumulator sets it to "now".
// Create a point with a value, decorating it with tags
// NOTE: tags is expected to be owned by the caller, don't mutate
// it after passing to Add.
// nolint: gocyclo
func (accumulator *accumulator) AddGauge(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	finalFields := make(map[string]interface{})
	for metricName, value := range fields {
		finalMetricName := measurement + "_" + metricName
		switch metricName {
		case "blocked":
			finalMetricName = "process_status_blocked"
		case "running":
			finalMetricName = "process_status_running"
		case "sleeping":
			finalMetricName = "process_status_sleeping"
		case "stopped":
			finalMetricName = "process_status_stopped"
		case "total":
			finalMetricName = "process_total"
		case "zombies":
			finalMetricName = "process_status_zombies"
		case "dead":
			continue
		case "idle":
			continue
		case "paging":
			finalMetricName = "process_status_paging"
		case "total_threads":
			finalMetricName = "process_total_threads"
		case "unknown":
			continue
		}
		finalFields[finalMetricName] = value
	}
	accumulator.acc.AddGauge(measurement, finalFields, nil, t...)
}

// AddError add an error to the accumulator
func (accumulator *accumulator) AddError(err error) {
	accumulator.acc.AddError(err)
}

// This functions are useless for process metric.
// They are not implemented

// AddFields is useless for process
func (accumulator *accumulator) AddFields(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	accumulator.acc.AddError(fmt.Errorf("AddFields not implemented for process accumulator"))
}

// AddCounter is useless for process
func (accumulator *accumulator) AddCounter(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	accumulator.acc.AddError(fmt.Errorf("AddCounter not implemented for process accumulator"))
}

// AddSummary is useless for process
func (accumulator *accumulator) AddSummary(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	accumulator.acc.AddError(fmt.Errorf("AddSummary not implemented for process accumulator"))
}

// AddHistogram is useless for process
func (accumulator *accumulator) AddHistogram(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	accumulator.acc.AddError(fmt.Errorf("AddHistogram not implemented for process accumulator"))
}

// SetPrecision is useless for process
func (accumulator *accumulator) SetPrecision(precision, interval time.Duration) {
	accumulator.acc.AddError(fmt.Errorf("SetPrecision not implemented for process accumulator"))
}
