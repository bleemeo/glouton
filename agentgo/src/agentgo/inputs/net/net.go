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
	"agentgo/types"
	"fmt"
	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/net"
	"time"
)

// Input countains input information about net
type Input struct {
	netInput telegraf.Input
}

// NewInput initialise met.Input
func NewInput() Input {
	var input, ok = telegraf_inputs.Inputs["net"]
	if ok {
		netInput := input().(*net.NetIOStats)
		netInput.IgnoreProtocolStats = true
		return Input{
			netInput: netInput,
		}
	}
	return Input{
		netInput: nil,
	}
}

// SampleConfig returns the default configuration of the Input
func (input Input) SampleConfig() string {
	return input.netInput.SampleConfig()
}

// Description returns a one-sentence description on the Input
func (input Input) Description() string {
	return input.netInput.Description()
}

// Gather takes in an accumulator and adds the metrics that the Input
// gathers. This is called every "interval"
func (input Input) Gather(acc telegraf.Accumulator) error {
	netAccumulator := initAccumulator(acc)
	err := input.netInput.Gather(&netAccumulator)
	return err
}

// Accumulator save the mem metric from telegraf
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
	item, ok := tags["interface"]
	if ok {
		finalTags["item"] = item
	}
	for metricName, value := range fields {
		finalMetricName := measurement + "_" + metricName
		valuef, err := types.ConvertInterface(value)
		if err != nil {
			(accumulator.acc).AddError(fmt.Errorf("Error when converting type of %v_%v : %v", measurement, metricName, err))
			continue
		}
		if finalMetricName == "net_bytes_sent" {
			finalMetricName = "net_bits_sent"
			valuef = 8 * valuef
		} else if finalMetricName == "net_bytes_recv" {
			finalMetricName = "net_bits_recv"
			valuef = 8 * valuef
		}
		finalFields[finalMetricName] = valuef
	}
	(accumulator.acc).AddGauge(measurement, finalFields, finalTags)
}

// AddError add an error to the Accumulator
func (accumulator *Accumulator) AddError(err error) {
	(accumulator.acc).AddError(err)
}

// This functions are useless for net metric.
// They are not implemented

// AddFields is useless for net
func (accumulator *Accumulator) AddFields(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	(accumulator.acc).AddError(fmt.Errorf("AddFields not implemented for net accumulator"))
}

// AddGauge is useless for net
func (accumulator *Accumulator) AddGauge(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	(accumulator.acc).AddError(fmt.Errorf("AddCounter not implemented for net accumulator"))
}

// AddSummary is useless for net
func (accumulator *Accumulator) AddSummary(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	(accumulator.acc).AddError(fmt.Errorf("AddSummary not implemented for net accumulator"))
}

// AddHistogram is useless for net
func (accumulator *Accumulator) AddHistogram(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	(accumulator.acc).AddError(fmt.Errorf("AddHistogram not implemented for net accumulator"))
}

// SetPrecision is useless for net
func (accumulator *Accumulator) SetPrecision(precision, interval time.Duration) {
	(accumulator.acc).AddError(fmt.Errorf("SetPrecision not implemented for net accumulator"))
}
