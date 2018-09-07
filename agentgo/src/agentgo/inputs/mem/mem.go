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

package mem

import (
	"agentgo/types"
	"fmt"
	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/mem"
	"time"
)

// Input countains input information about mem
type Input struct {
	memInput telegraf.Input
}

// NewInput initialise mem.Input
func NewInput() Input {
	var input, ok = telegraf_inputs.Inputs["mem"]
	if ok {
		memInput := input().(*mem.MemStats)
		return Input{
			memInput: memInput,
		}
	}
	return Input{
		memInput: nil,
	}
}

// SampleConfig returns the default configuration of the Input
func (input Input) SampleConfig() string {
	return input.memInput.SampleConfig()
}

// Description returns a one-sentence description of the Input
func (input Input) Description() string {
	return input.memInput.Description()
}

// Gather takes in an accumulator and adds the metrics that the Input
// gathers. This is called every "interval"
func (input Input) Gather(acc telegraf.Accumulator) error {
	memAccumulator := initAccumulator(acc)
	err := input.memInput.Gather(&memAccumulator)
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

// AddGauge adds a metric to the accumulator with the given measurement
// name, fields, and tags (and timestamp). If a timestamp is not provided,
// then the accumulator sets it to "now".
// Create a point with a value, decorating it with tags
// NOTE: tags is expected to be owned by the caller, don't mutate
// it after passing to Add.
// nolint: gocyclo
func (accumulator *Accumulator) AddGauge(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	finalFields := make(map[string]interface{})
	for metricName, value := range fields {
		finalMetricName := measurement + "_" + metricName
		if finalMetricName == "mem_available_percent" {
			finalMetricName = "mem_available_perc"
		} else if finalMetricName == "mem_used_percent" {
			finalMetricName = "mem_used_perc"
		} else if finalMetricName == "mem_active" {
			continue
		} else if finalMetricName == "mem_inactive" {
			continue
		} else if finalMetricName == "mem_wired" {
			continue
		} else if finalMetricName == "mem_commit_limit" {
			continue
		} else if finalMetricName == "mem_committed_as" {
			continue
		} else if finalMetricName == "mem_dirty" {
			continue
		} else if finalMetricName == "mem_high_free" {
			continue
		} else if finalMetricName == "mem_high_total" {
			continue
		} else if finalMetricName == "mem_huge_page_size" {
			continue
		} else if finalMetricName == "mem_huge_pages_free" {
			continue
		} else if finalMetricName == "mem_huge_pages_total" {
			continue
		} else if finalMetricName == "mem_low_free" {
			continue
		} else if finalMetricName == "mem_low_total" {
			continue
		} else if finalMetricName == "mem_mapped" {
			continue
		} else if finalMetricName == "mem_page_tables" {
			continue
		} else if finalMetricName == "mem_shared" {
			continue
		} else if finalMetricName == "mem_swap_cached" {
			continue
		} else if finalMetricName == "mem_swap_free" {
			continue
		} else if finalMetricName == "mem_swap_total" {
			continue
		} else if finalMetricName == "mem_vmalloc_chunk" {
			continue
		} else if finalMetricName == "mem_vmalloc_total" {
			continue
		} else if finalMetricName == "mem_vmalloc_used" {
			continue
		} else if finalMetricName == "mem_write_back" {
			continue
		} else if finalMetricName == "mem_write_back_tmp" {
			continue
		}
		valuef, err := types.ConvertInterface(value)
		if err != nil {
			(accumulator.acc).AddError(fmt.Errorf("Error when converting type of %v_%v : %v", measurement, metricName, err))
			continue
		}
		finalFields[finalMetricName] = valuef
	}
	(accumulator.acc).AddGauge(measurement, finalFields, nil)
}

// AddError add an error to the Accumulator
func (accumulator *Accumulator) AddError(err error) {
	(accumulator.acc).AddError(err)
}

// This functions are useless for mem metric.
// They are not implemented

// AddFields is useless for mem
func (accumulator *Accumulator) AddFields(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	(accumulator.acc).AddError(fmt.Errorf("AddFields not implemented for mem accumulator"))
}

// AddCounter is useless for mem
func (accumulator *Accumulator) AddCounter(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	(accumulator.acc).AddError(fmt.Errorf("AddCounter not implemented for mem accumulator"))
}

// AddSummary is useless for mem
func (accumulator *Accumulator) AddSummary(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	(accumulator.acc).AddError(fmt.Errorf("AddSummary not implemented for mem accumulator"))
}

// AddHistogram is useless for mem
func (accumulator *Accumulator) AddHistogram(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	(accumulator.acc).AddError(fmt.Errorf("AddHistogram not implemented for mem accumulator"))
}

// SetPrecision is useless for mem
func (accumulator *Accumulator) SetPrecision(precision, interval time.Duration) {
	(accumulator.acc).AddError(fmt.Errorf("SetPrecision not implemented for mem accumulator"))
}
