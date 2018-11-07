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

package net

import (
	"errors"
	"fmt"
	"time"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/net"
)

// Input countains input information about net
type Input struct {
	telegraf.Input
}

// New initialise met.Input
func New() (i *Input, err error) {
	var input, ok = telegraf_inputs.Inputs["net"]
	if ok {
		netInput := input().(*net.NetIOStats)
		netInput.IgnoreProtocolStats = true
		i = &Input{netInput}
	} else {
		err = errors.New("Telegraf don't have \"net\" input")
	}
	return
}

// Gather takes in an accumulator and adds the metrics that the Input
// gathers. This is called every "interval"
func (i *Input) Gather(acc telegraf.Accumulator) error {
	netAccumulator := accumulator{acc}
	err := i.Input.Gather(&netAccumulator)
	return err
}

// accumulator save the net metric from telegraf
type accumulator struct {
	accumulator telegraf.Accumulator
}

// AddCounter adds a metric to the accumulator with the given measurement
// name, fields, and tags (and timestamp). If a timestamp is not provided,
// then the accumulator sets it to "now".
// Create a point with a value, decorating it with tags
// NOTE: tags is expected to be owned by the caller, don't mutate
// it after passing to Add.
func (a *accumulator) AddCounter(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	finalFields := make(map[string]interface{})
	finalTags := make(map[string]string)
	item, ok := tags["interface"]
	if ok {
		finalTags["item"] = item
	}
	for metricName, value := range fields {
		finalMetricName := measurement + "_" + metricName
		if finalMetricName == "net_bytes_sent" {
			valuef := value.(uint64)
			finalMetricName = "net_bits_sent"
			valuef = 8 * valuef
			finalFields[finalMetricName] = valuef
		} else if finalMetricName == "net_bytes_recv" {
			valuef := value.(uint64)
			finalMetricName = "net_bits_recv"
			valuef = 8 * valuef
			finalFields[finalMetricName] = valuef
		} else {
			finalFields[finalMetricName] = value
		}
	}
	a.accumulator.AddGauge(measurement, finalFields, finalTags, t...)
}

// AddError add an error to the accumulator
func (a *accumulator) AddError(err error) {
	a.accumulator.AddError(err)
}

// This functions are useless for net metric.
// They are not implemented

// AddFields is useless for net
func (a *accumulator) AddFields(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.accumulator.AddError(fmt.Errorf("AddFields not implemented for net accumulator"))
}

// AddGauge is useless for net
func (a *accumulator) AddGauge(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.accumulator.AddError(fmt.Errorf("AddCounter not implemented for net accumulator"))
}

// AddSummary is useless for net
func (a *accumulator) AddSummary(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.accumulator.AddError(fmt.Errorf("AddSummary not implemented for net accumulator"))
}

// AddHistogram is useless for net
func (a *accumulator) AddHistogram(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.accumulator.AddError(fmt.Errorf("AddHistogram not implemented for net accumulator"))
}

// SetPrecision is useless for net
func (a *accumulator) SetPrecision(precision, interval time.Duration) {
	a.accumulator.AddError(fmt.Errorf("SetPrecision not implemented for net accumulator"))
}
