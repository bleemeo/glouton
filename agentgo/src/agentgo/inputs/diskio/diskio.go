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

package diskio

import (
	"fmt"
	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/diskio"
	"strings"
	"time"
)

// Input countains input information about diskio
type Input struct {
	diskioInput telegraf.Input
}

// New initialise diskio.Input
func New() Input {
	var input, ok = telegraf_inputs.Inputs["diskio"]
	if ok {
		diskioInput := input().(*diskio.DiskIO)
		return Input{
			diskioInput: diskioInput,
		}
	}
	return Input{
		diskioInput: nil,
	}
}

// SampleConfig returns the default configuration of the Input
func (input Input) SampleConfig() string {
	return input.diskioInput.SampleConfig()
}

// Description returns a one-sentence description of the Input
func (input Input) Description() string {
	return input.diskioInput.Description()
}

// Gather takes in an accumulator and adds the metrics that the Input
// gathers. This is called every "interval"
func (input Input) Gather(acc telegraf.Accumulator) error {
	diskioAccumulator := initAccumulator(acc)
	err := input.diskioInput.Gather(&diskioAccumulator)
	return err
}

// Accumulator save the diskio metric from telegraf
type Accumulator struct {
	acc telegraf.Accumulator
}

// InitAccumulator initialize an accumulator
func initAccumulator(acc telegraf.Accumulator) Accumulator {
	return Accumulator{
		acc: acc,
	}
}

// AddCounter adds a metric to the accumulator with the given measurement
// name, fields, and tags (and timestamp). If a timestamp is not provided,
// then the accumulator sets it to "now".
// Create a point with a value, decorating it with tags
// NOTE: tags is expected to be owned by the caller, don't mutate
// it after passing to Add.
// nolint: gocyclo
func (accumulator *Accumulator) AddCounter(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	finalFields := make(map[string]interface{})
	finalTags := make(map[string]string)
	item, ok := tags["name"]
	if ok {
		finalTags["item"] = item
	}
	for metricName, value := range fields {
		finalMetricName := strings.Replace(measurement+"_"+metricName, "disk", "", 1)
		if finalMetricName == "io_weighted_io_time" {
			continue
		} else if finalMetricName == "io_iops_in_progress" {
			continue
		}

		if finalMetricName == "io_io_time" {
			finalMetricName = "io_time"
			valuef := value.(uint64)
			finalFields["io_utilization"] = valuef * 1000
		}
		finalFields[finalMetricName] = value
	}
	(accumulator.acc).AddGauge(measurement, finalFields, finalTags)
}

// AddError add an error to the Accumulator
func (accumulator *Accumulator) AddError(err error) {
	(accumulator.acc).AddError(err)
}

// This functions are useless for diskio metric.
// They are not implemented

// AddFields is useless for diskio
func (accumulator *Accumulator) AddFields(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	(accumulator.acc).AddError(fmt.Errorf("AddFields not implemented for diskio accumulator"))
}

// AddGauge is useless for diskio
func (accumulator *Accumulator) AddGauge(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	(accumulator.acc).AddError(fmt.Errorf("AddCounter not implemented for diskio accumulator"))
}

// AddSummary is useless for diskio
func (accumulator *Accumulator) AddSummary(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	(accumulator.acc).AddError(fmt.Errorf("AddSummary not implemented for diskio accumulator"))
}

// AddHistogram is useless for diskio
func (accumulator *Accumulator) AddHistogram(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	(accumulator.acc).AddError(fmt.Errorf("AddHistogram not implemented for diskio accumulator"))
}

// SetPrecision is useless for diskio
func (accumulator *Accumulator) SetPrecision(precision, interval time.Duration) {
	(accumulator.acc).AddError(fmt.Errorf("SetPrecision not implemented for diskio accumulator"))
}
