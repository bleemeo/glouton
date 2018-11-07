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
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/cpu"
)

// Input countains input information about CPU
type Input struct {
	telegraf.Input
}

// New initialise cpu.Input
func New() (i *Input, err error) {
	var input, ok = telegraf_inputs.Inputs["cpu"]
	if ok {
		cpuInput := input().(*cpu.CPUStats)
		cpuInput.PerCPU = false
		cpuInput.CollectCPUTime = false
		i = &Input{cpuInput}
	} else {
		err = errors.New("Telegraf don't have \"cpu\" input")
	}
	return
}

// Gather takes in an accumulator and adds the metrics that the Input
// gathers. This is called every "interval"
func (input *Input) Gather(acc telegraf.Accumulator) error {
	cpuAccumulator := accumulator{acc}
	err := input.Input.Gather(&cpuAccumulator)
	return err
}

// accumulator save the cpu metric from telegraf
type accumulator struct {
	acc telegraf.Accumulator
}

// AddGauge adds a metric to the accumulator with the given measurement
// name, fields, and tags (and timestamp). If a timestamp is not provided,
// then the accumulator sets it to "now".
// Create a point with a value, decorating it with tags
// NOTE: tags is expected to be owned by the caller, don't mutate
// it after passing to Add.
func (accumulator *accumulator) AddGauge(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	finalFields := make(map[string]interface{})
	var cpuOther float64
	var cpuUsed float64
	for metricName, value := range fields {
		finalMetricName := measurement + strings.Replace(metricName, "usage", "", -1)
		if finalMetricName == "cpu_irq" {
			finalMetricName = "cpu_interrupt"
		} else if finalMetricName == "cpu_iowait" {
			finalMetricName = "cpu_wait"
		}
		finalFields[finalMetricName] = value
		switch finalMetricName {
		case "cpu_user":
			valuef := value.(float64)
			cpuUsed += valuef
		case "cpu_nice":
			valuef := value.(float64)
			cpuOther += valuef
		case "cpu_system":
			valuef := value.(float64)
			cpuUsed += valuef
		case "cpu_interrupt":
			valuef := value.(float64)
			cpuUsed += valuef
			cpuOther += valuef
		case "cpu_softirq":
			valuef := value.(float64)
			cpuUsed += valuef
			cpuOther += valuef
		case "cpu_steal":
			valuef := value.(float64)
			cpuUsed += valuef
			cpuOther += valuef
		}
	}
	finalFields["cpu_other"] = cpuOther
	finalFields["cpu_used"] = cpuUsed
	accumulator.acc.AddGauge(measurement, finalFields, nil, t...)
}

// AddError add an error to the Accumulator
func (accumulator *accumulator) AddError(err error) {
	accumulator.acc.AddError(err)
}

// This functions are useless for Cpu metric.
// They are not implemented

// AddFields is useless for Cpu
func (accumulator *accumulator) AddFields(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	accumulator.acc.AddError(fmt.Errorf("AddFields not implemented for cpu accumulator"))
}

// AddCounter is useless for Cpu
func (accumulator *accumulator) AddCounter(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	accumulator.acc.AddError(fmt.Errorf("AddCounter not implemented for cpu accumulator"))
}

// AddSummary is useless for Cpu
func (accumulator *accumulator) AddSummary(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	accumulator.acc.AddError(fmt.Errorf("AddSummary not implemented for cpu accumulator"))
}

// AddHistogram is useless for Cpu
func (accumulator *accumulator) AddHistogram(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	accumulator.acc.AddError(fmt.Errorf("AddHistogram not implemented for cpu accumulator"))
}

// SetPrecision is useless for Cpu
func (accumulator *accumulator) SetPrecision(precision, interval time.Duration) {
	accumulator.acc.AddError(fmt.Errorf("SetPrecision not implemented for cpu accumulator"))
}
