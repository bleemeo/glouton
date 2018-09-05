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

package cpu

import (
	"agentgo/types"
	"fmt"
	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/cpu"
	"strings"
	"time"
)

// Input countains input information abour CPU
type Input struct {
	cpuInput telegraf.Input
}

// NewInput initialise cpu.Input
func NewInput() Input {
	var input, ok = telegraf_inputs.Inputs["cpu"]
	if ok {
		cpuInput := input().(*cpu.CPUStats)
		cpuInput.PerCPU = false
		cpuInput.CollectCPUTime = false
		return Input{
			cpuInput: cpuInput,
		}
	}
	return Input{
		cpuInput: nil,
	}
}

// SampleConfig returns the default configuration of the Input
func (input Input) SampleConfig() string {
	return input.cpuInput.SampleConfig()
}

// Description returns a one-sentence description on the Input
func (input Input) Description() string {
	return input.cpuInput.Description()
}

// Gather takes in an accumulator and adds the metrics that the Input
// gathers. This is called every "interval"
func (input Input) Gather(acc telegraf.Accumulator) error {
	cpuAccumulator := initAccumulator(&acc)
	err := input.cpuInput.Gather(&cpuAccumulator)
	//fmt.Println(cpuAccumulator.GetAccumulator().GetMetricPointSlice())
	return err
}

// Accumulator save the cpu metric from telegraf
type Accumulator struct {
	acc *telegraf.Accumulator
}

// InitAccumulator initialize an accumulator
func initAccumulator(acc *telegraf.Accumulator) Accumulator {
	return Accumulator{
		acc: acc,
	}
}

// AddGauge adds a metric to the accumulator with the given measurement
// name, fields, and tags (and timestamp). If a timestamp is not provided,
// then the accumulator sets it to "now".
// Create a point with a value, decorating it with tags
// NOTE: tags is expected to be owned by the caller, don't mutate
// it after passing to Add.
func (accumulator *Accumulator) AddGauge(measurement string,
	fields map[string]interface{},
	tags map[string]string,
	t ...time.Time) {
	finalFields := make(map[string]interface{})
	finalTags := make(map[string]string)
	finalTags["item"] = tags["cpu"]
	var cpuOther float64
	var cpuUsed float64
	for metricName, value := range fields {
		valuef, err := types.ConvertInterface(value)
		if err != nil {
			(*accumulator.acc).AddError(fmt.Errorf("Error when converting type of %v_%v : %v", measurement, metricName, err))
			continue
		}
		finalMetricName := measurement + strings.Replace(metricName, "usage", "", -1)
		if finalMetricName == "cpu_irq" {
			finalMetricName = "cpu_interrupt"
		} else if finalMetricName == "cpu_iowait" {
			finalMetricName = "cpu_wait"
		}
		finalFields[finalMetricName] = valuef
		if finalMetricName == "cpu_user" {
			cpuUsed += valuef
		} else if finalMetricName == "cpu_nice" {
			cpuUsed += valuef
			cpuOther += valuef
		} else if finalMetricName == "cpu_system" {
			cpuUsed += valuef
		} else if finalMetricName == "cpu_interrupt" {
			cpuUsed += valuef
			cpuOther += valuef
		} else if finalMetricName == "cpu_softirq" {
			cpuUsed += valuef
			cpuOther += valuef
		} else if finalMetricName == "cpu_steal" {
			cpuUsed += valuef
			cpuOther += valuef
		}
	}
	(*accumulator.acc).AddGauge(measurement, finalFields, finalTags, t[0])
}

// AddError add an error to the Accumulator
func (accumulator *Accumulator) AddError(err error) {
	(*accumulator.acc).AddError(err)
}

// GetAccumulator return the accumulator field
func (accumulator Accumulator) GetAccumulator() telegraf.Accumulator {
	return *accumulator.acc
}

// This functions are useless for Cpu metric.
// They are not implemented

// AddFields is useless for Cpu
func (accumulator *Accumulator) AddFields(measurement string,
	fields map[string]interface{},
	tags map[string]string,
	t ...time.Time) {
}

// AddCounter is useless for Cpu
func (accumulator *Accumulator) AddCounter(measurement string,
	fields map[string]interface{},
	tags map[string]string,
	t ...time.Time) {
}

// AddSummary is useless for Cpu
func (accumulator *Accumulator) AddSummary(measurement string,
	fields map[string]interface{},
	tags map[string]string,
	t ...time.Time) {
}

// AddHistogram is useless for Cpu
func (accumulator *Accumulator) AddHistogram(measurement string,
	fields map[string]interface{},
	tags map[string]string,
	t ...time.Time) {
}

// SetPrecision is useless for Cpu
func (accumulator *Accumulator) SetPrecision(precision, interval time.Duration) {}
