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
	"errors"
	"fmt"
	"time"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/mem"
)

// Input countains input information about mem
type Input struct {
	telegraf.Input
}

// New initialise mem.Input
func New() (i *Input, err error) {
	var input, ok = telegraf_inputs.Inputs["mem"]
	if ok {
		memInput := input().(*mem.MemStats)
		i = &Input{memInput}
	} else {
		err = errors.New("Telegraf don't have \"mem\" input")
	}
	return
}

// Gather takes in an accumulator and adds the metrics that the Input
// gathers. This is called every "interval"
func (i *Input) Gather(acc telegraf.Accumulator) error {
	memAccumulator := accumulator{acc}
	err := i.Input.Gather(&memAccumulator)
	return err
}

// accumulator save the mem metric from telegraf
type accumulator struct {
	accumulator telegraf.Accumulator
}

// AddGauge adds a metric to the accumulator with the given measurement
// name, fields, and tags (and timestamp). If a timestamp is not provided,
// then the accumulator sets it to "now".
// Create a point with a value, decorating it with tags
// NOTE: tags is expected to be owned by the caller, don't mutate
// it after passing to Add.
// nolint: gocyclo
func (a *accumulator) AddGauge(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	finalFields := make(map[string]interface{})
	for metricName, value := range fields {
		finalMetricName := metricName
		switch metricName {
		case "available_percent":
			finalMetricName = "available_perc"
		case "used_percent":
			finalMetricName = "used_perc"
		// All next cases are metric ignored. They are on different case to
		// avoid very long line.
		case "active", "inactive", "wired", "commit_limit", "committed_as", "dirty", "high_free", "high_total":
			continue
		case "huge_page_size", "huge_pages_free", "huge_pages_total", "low_free", "low_total", "mapped", "page_tables":
			continue
		case "shared", "swap_cached", "swap_free", "swap_total", "vmalloc_chunk", "vmalloc_total", "vmalloc_used":
			continue
		case "write_back", "write_back_tmp":
			continue
		}
		finalFields[finalMetricName] = value
	}
	a.accumulator.AddGauge(measurement, finalFields, nil, t...)
}

// AddError add an error to the accumulator
func (a *accumulator) AddError(err error) {
	a.accumulator.AddError(err)
}

// This functions are useless for mem metric.
// They are not implemented

// AddFields is useless for mem
func (a *accumulator) AddFields(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.accumulator.AddError(fmt.Errorf("AddFields not implemented for mem accumulator"))
}

// AddCounter is useless for mem
func (a *accumulator) AddCounter(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.accumulator.AddError(fmt.Errorf("AddCounter not implemented for mem accumulator"))
}

// AddSummary is useless for mem
func (a *accumulator) AddSummary(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.accumulator.AddError(fmt.Errorf("AddSummary not implemented for mem accumulator"))
}

// AddHistogram is useless for mem
func (a *accumulator) AddHistogram(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.accumulator.AddError(fmt.Errorf("AddHistogram not implemented for mem accumulator"))
}

// SetPrecision is useless for mem
func (a *accumulator) SetPrecision(precision time.Duration) {
	a.accumulator.AddError(fmt.Errorf("SetPrecision not implemented for mem accumulator"))
}

func (a *accumulator) AddMetric(telegraf.Metric) {
	a.accumulator.AddError(fmt.Errorf("AddMetric not implemented for mem accumulator"))
}

func (a *accumulator) WithTracking(maxTracked int) telegraf.TrackingAccumulator {
	a.accumulator.AddError(fmt.Errorf("WithTracking not implemented for mem accumulator"))
	return nil
}
