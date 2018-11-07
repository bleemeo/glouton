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
func (i *Input) Gather(acc telegraf.Accumulator) error {
	diskioAccumulator := accumulator{
		acc,
		i.pastValues,
		make(map[string]map[string]metricSave),
	}
	err := i.Input.Gather(&diskioAccumulator)
	i.pastValues = diskioAccumulator.currentValues
	return err
}

// accumulator save the diskio metric from telegraf
type accumulator struct {
	accumulator   telegraf.Accumulator
	pastValues    map[string]map[string]metricSave
	currentValues map[string]map[string]metricSave
}

// AddCounter adds a metric to the accumulator with the given measurement
// name, fields, and tags (and timestamp). If a timestamp is not provided,
// then the accumulator sets it to "now".
// Create a point with a value, decorating it with tags
// NOTE: tags is expected to be owned by the caller, don't mutate
// it after passing to Add.
// nolint: gocyclo
func (a *accumulator) AddCounter(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
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
		a.currentValues[item] = make(map[string]metricSave)
	}

	for metricName, value := range fields {
		finalMetricName := strings.Replace(measurement+"_"+metricName, "disk", "", 1)
		switch metricName {
		case "read_bytes", "read_time", "reads", "write_bytes", "writes", "write_time", "io_time":
			pastMetricSave, ok := a.pastValues[item][metricName]
			a.currentValues[item][metricName] = metricSave{value, metricTime}
			if ok {
				valuef := (float64(value.(uint64)) - float64(pastMetricSave.value.(uint64))) / metricTime.Sub(pastMetricSave.metricTime).Seconds()
				if finalMetricName == "io_io_time" {
					finalMetricName = "io_time"
					// io_time is millisecond per second.
					finalFields["io_utilization"] = valuef / 1000. * 100.
				}
				finalFields[finalMetricName] = valuef
			} else {
				continue
			}
		case "io_weighted_io_time", "io_iops_in_progress":
			continue
		default:
			finalFields[finalMetricName] = value
		}
	}
	a.accumulator.AddGauge(measurement, finalFields, finalTags, t...)
}

// AddError add an error to the accumulator
func (a *accumulator) AddError(err error) {
	a.accumulator.AddError(err)
}

// This functions are useless for diskio metric.
// They are not implemented

// AddFields is useless for diskio
func (a *accumulator) AddFields(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.accumulator.AddError(fmt.Errorf("AddFields not implemented for diskio accumulator"))
}

// AddGauge is useless for diskio
func (a *accumulator) AddGauge(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.accumulator.AddError(fmt.Errorf("AddCounter not implemented for diskio accumulator"))
}

// AddSummary is useless for diskio
func (a *accumulator) AddSummary(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.accumulator.AddError(fmt.Errorf("AddSummary not implemented for diskio accumulator"))
}

// AddHistogram is useless for diskio
func (a *accumulator) AddHistogram(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.accumulator.AddError(fmt.Errorf("AddHistogram not implemented for diskio accumulator"))
}

// SetPrecision is useless for diskio
func (a *accumulator) SetPrecision(precision, interval time.Duration) {
	a.accumulator.AddError(fmt.Errorf("SetPrecision not implemented for diskio accumulator"))
}
