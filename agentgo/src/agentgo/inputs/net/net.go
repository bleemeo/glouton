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
	"strings"
	"time"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/net"
)

type metricPoint struct {
	value      uint64
	metricTime time.Time
}

// Input countains input information about net
type Input struct {
	telegraf.Input
	blacklist  []string
	pastValues map[string]map[string]metricPoint // item => metricName => metricPoint
}

// New initialise net.Input
//
// blacklist contains a list of interface name prefix to ignore
func New(blacklist []string) (i *Input, err error) {
	var input, ok = telegraf_inputs.Inputs["net"]
	if ok {
		netInput := input().(*net.NetIOStats)
		netInput.IgnoreProtocolStats = true
		i = &Input{
			netInput,
			blacklist,
			make(map[string]map[string]metricPoint),
		}
	} else {
		err = errors.New("Telegraf don't have \"net\" input")
	}
	return
}

// Gather takes in an accumulator and adds the metrics that the Input
// gathers. This is called every "interval"
func (i *Input) Gather(acc telegraf.Accumulator) error {
	netAccumulator := accumulator{
		acc,
		i.blacklist,
		i.pastValues,
		make(map[string]map[string]metricPoint)}
	err := i.Input.Gather(&netAccumulator)
	i.pastValues = netAccumulator.currentValues
	return err
}

// accumulator save the net metric from telegraf
type accumulator struct {
	accumulator   telegraf.Accumulator
	blacklist     []string
	pastValues    map[string]map[string]metricPoint // item => metricName => metricPoint
	currentValues map[string]map[string]metricPoint // item => metricName => metricPoint
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
		for _, b := range a.blacklist {
			if strings.HasPrefix(item, b) {
				return
			}
		}
		finalTags["item"] = item
		a.currentValues[item] = make(map[string]metricPoint)
	}

	for metricName, value := range fields {
		var valuef uint64
		finalMetricName := metricName
		if finalMetricName == "bytes_sent" {
			valuef = value.(uint64)
			finalMetricName = "bits_sent"
			valuef = 8 * valuef
		} else if finalMetricName == "bytes_recv" {
			valuef = value.(uint64)
			finalMetricName = "bits_recv"
			valuef = 8 * valuef
		} else {
			valuef = value.(uint64)

		}

		var metricTime time.Time
		if len(t) != 1 {
			metricTime = time.Now()
		} else {
			metricTime = t[0]
		}
		pastMetricSave, ok := a.pastValues[item][finalMetricName]
		a.currentValues[item][finalMetricName] = metricPoint{valuef, metricTime}
		if ok {
			valuef := (float64(valuef) - float64(pastMetricSave.value)) / metricTime.Sub(pastMetricSave.metricTime).Seconds()
			finalFields[finalMetricName] = valuef
		} else {
			continue
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
func (a *accumulator) SetPrecision(precision time.Duration) {
	a.accumulator.AddError(fmt.Errorf("SetPrecision not implemented for net accumulator"))
}

func (a *accumulator) AddMetric(telegraf.Metric) {
	a.accumulator.AddError(fmt.Errorf("AddMetric not implemented for net accumulator"))
}

func (a *accumulator) WithTracking(maxTracked int) telegraf.TrackingAccumulator {
	a.accumulator.AddError(fmt.Errorf("WithTracking not implemented for net accumulator"))
	return nil
}
