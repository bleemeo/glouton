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
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/diskio"
)

type metricSave struct {
	value      interface{}
	metricTime time.Time
}

// Input countains input information about diskio
type Input struct {
	telegraf.Input
	pastValues map[string]map[string]metricSave
}

// New initialise diskio.Input
func New() (i *Input, err error) {
	var input, ok = telegraf_inputs.Inputs["diskio"]
	if ok {
		diskioInput := input().(*diskio.DiskIO)
		i = &Input{Input: diskioInput}
	} else {
		err = errors.New("Telegraf don't have \"diskio\" input")
	}
	return
}

// Gather takes in an accumulator and adds the metrics that the Input
// gathers. This is called every "interval"
func (input *Input) Gather(acc telegraf.Accumulator) error {
	diskioAccumulator := initAccumulator(acc, input.pastValues)
	err := input.Input.Gather(&diskioAccumulator)
	input.pastValues = diskioAccumulator.currentValues
	return err
}

// accumulator save the diskio metric from telegraf
type accumulator struct {
	acc           telegraf.Accumulator
	pastValues    map[string]map[string]metricSave
	currentValues map[string]map[string]metricSave
}

// InitAccumulator initialize an accumulator
func initAccumulator(acc telegraf.Accumulator, pastValues map[string]map[string]metricSave) accumulator {
	return accumulator{
		acc:           acc,
		pastValues:    pastValues,
		currentValues: nil,
	}
}

// AddCounter adds a metric to the accumulator with the given measurement
// name, fields, and tags (and timestamp). If a timestamp is not provided,
// then the accumulator sets it to "now".
// Create a point with a value, decorating it with tags
// NOTE: tags is expected to be owned by the caller, don't mutate
// it after passing to Add.
// nolint: gocyclo
func (accumulator *accumulator) AddCounter(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	if accumulator.currentValues == nil {
		accumulator.currentValues = make(map[string]map[string]metricSave)
	}
	var metricTime time.Time
	if len(t) != 1 {
		metricTime = time.Now()
	} else {
		metricTime = t[0]
	}

	finalFields := make(map[string]interface{})
	finalTags := make(map[string]string)
	item, ok := tags["name"]
	if ok {
		finalTags["item"] = item
		accumulator.currentValues[item] = make(map[string]metricSave)
	}

	for metricName, value := range fields {
		finalMetricName := strings.Replace(measurement+"_"+metricName, "disk", "", 1)
		switch finalMetricName {
		case "io_read_bytes", "io_read_time", "io_reads", "io_write_bytes", "io_writes", "io_write_time":
			pastMetricSave, ok := accumulator.pastValues[item][finalMetricName]
			accumulator.currentValues[item][finalMetricName] = metricSave{value, metricTime}
			if ok {
				valuef := (float64(value.(uint64)) - float64(pastMetricSave.value.(uint64))) / metricTime.Sub(pastMetricSave.metricTime).Seconds()
				finalFields[finalMetricName] = valuef
			} else {
				continue
			}
		case "io_io_time":
			finalMetricName = "io_time"
			pastMetricSave, ok := accumulator.pastValues[item][finalMetricName]
			accumulator.currentValues[item][finalMetricName] = metricSave{value, metricTime}
			if ok {
				valuef := (float64(value.(uint64)) - float64(pastMetricSave.value.(uint64))) / metricTime.Sub(pastMetricSave.metricTime).Seconds()
				finalFields[finalMetricName] = valuef

			} else {
				continue
			}

			pastIOUtilization, ok := accumulator.pastValues[item]["io_utilization"]
			accumulator.currentValues[item]["io_utilization"] = metricSave{value.(uint64) * 1000, metricTime}
			if ok {
				valuef := 100 * (float64(value.(uint64))*1000 - float64(pastIOUtilization.value.(uint64))) / metricTime.Sub(pastIOUtilization.metricTime).Seconds()
				finalFields["io_utilization"] = valuef
			} else {
				continue
			}

		case "io_weighted_io_time", "io_iops_in_progress":
			continue
		default:
			finalFields[finalMetricName] = value
		}
	}
	accumulator.acc.AddGauge(measurement, finalFields, finalTags, t...)
}

// AddError add an error to the accumulator
func (accumulator *accumulator) AddError(err error) {
	accumulator.acc.AddError(err)
}

// This functions are useless for diskio metric.
// They are not implemented

// AddFields is useless for diskio
func (accumulator *accumulator) AddFields(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	accumulator.acc.AddError(fmt.Errorf("AddFields not implemented for diskio accumulator"))
}

// AddGauge is useless for diskio
func (accumulator *accumulator) AddGauge(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	accumulator.acc.AddError(fmt.Errorf("AddCounter not implemented for diskio accumulator"))
}

// AddSummary is useless for diskio
func (accumulator *accumulator) AddSummary(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	accumulator.acc.AddError(fmt.Errorf("AddSummary not implemented for diskio accumulator"))
}

// AddHistogram is useless for diskio
func (accumulator *accumulator) AddHistogram(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	accumulator.acc.AddError(fmt.Errorf("AddHistogram not implemented for diskio accumulator"))
}

// SetPrecision is useless for diskio
func (accumulator *accumulator) SetPrecision(precision, interval time.Duration) {
	accumulator.acc.AddError(fmt.Errorf("SetPrecision not implemented for diskio accumulator"))
}
