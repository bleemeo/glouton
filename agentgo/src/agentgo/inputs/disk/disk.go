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

package disk

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/disk"
)

// Input countains input information about disk
type Input struct {
	telegraf.Input
	mountPoint string
	blacklist  []string
}

// New initialise disk.Input
//
// mountPoint is the root path to monitor. Useful when running inside a Docker.
//
// blacklist is a list of path-prefix to ignore. Path prefix means that "/mnt" and "/mnt/disk" both have "/mnt"
// as prefix, but "/mnt-disk" does not.
func New(mountPoint string, blacklist []string) (i *Input, err error) {
	blacklistTrimed := make([]string, len(blacklist))
	for i, v := range blacklist {
		blacklistTrimed[i] = strings.TrimRight(v, "/")
	}
	var input, ok = telegraf_inputs.Inputs["disk"]
	if ok {
		diskInput := input().(*disk.DiskStats)
		diskInput.IgnoreFS = []string{
			"tmpfs", "devtmpfs", "devfs", "overlay", "aufs", "squashfs",
		}
		i = &Input{
			diskInput,
			strings.TrimRight(mountPoint, "/"),
			blacklistTrimed,
		}
	} else {
		err = errors.New("Telegraf don't have \"disk\" input")
	}
	return
}

// Gather takes in an accumulator and adds the metrics that the Input
// gathers. This is called every "interval"
func (i *Input) Gather(acc telegraf.Accumulator) error {
	diskAccumulator := accumulator{acc, i.mountPoint, i.blacklist}
	err := i.Input.Gather(&diskAccumulator)
	return err
}

// accumulator save the disk metric from telegraf
type accumulator struct {
	accumulator telegraf.Accumulator
	mountPoint  string
	blacklist   []string
}

// AddGauge adds a metric to the accumulator with the given measurement
// name, fields, and tags (and timestamp). If a timestamp is not provided,
// then the accumulator sets it to "now".
// Create a point with a value, decorating it with tags
// NOTE: tags is expected to be owned by the caller, don't mutate
// it after passing to Add.
func (a *accumulator) AddGauge(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	finalFields := make(map[string]interface{})
	finalTags := make(map[string]string)
	item, ok := tags["path"]
	if ok {
		if !strings.HasPrefix(item, a.mountPoint) {
			// partition don't start with mountPoint, so it's a parition
			// which is only inside the container. Ignore it
			return
		}
		item = strings.TrimPrefix(item, a.mountPoint)
		if item == "" {
			item = "/"
		}
		for _, v := range a.blacklist {
			if v == item || strings.HasPrefix(item, v+"/") {
				return
			}
		}
		finalTags["item"] = item
	}
	for metricName, value := range fields {
		finalMetricName := measurement + "_" + metricName
		if finalMetricName == "disk_used_percent" {
			finalMetricName = "disk_used_perc"
		}
		finalFields[finalMetricName] = value
	}
	a.accumulator.AddGauge(measurement, finalFields, finalTags, t...)
}

// AddError add an error to the Accumulator
func (a *accumulator) AddError(err error) {
	a.accumulator.AddError(err)
}

// This functions are useless for disk metric.
// They are not implemented

// AddFields is useless for disk
func (a *accumulator) AddFields(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.accumulator.AddError(fmt.Errorf("AddFields not implemented for disk accumulator"))
}

// AddCounter is useless for disk
func (a *accumulator) AddCounter(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.accumulator.AddError(fmt.Errorf("AddCounter not implemented for disk accumulator"))
}

// AddSummary is useless for disk
func (a *accumulator) AddSummary(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.accumulator.AddError(fmt.Errorf("AddSummary not implemented for disk accumulator"))
}

// AddHistogram is useless for disk
func (a *accumulator) AddHistogram(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.accumulator.AddError(fmt.Errorf("AddHistogram not implemented for disk accumulator"))
}

// SetPrecision is useless for disk
func (a *accumulator) SetPrecision(precision, interval time.Duration) {
	a.accumulator.AddError(fmt.Errorf("SetPrecision not implemented for disk accumulator"))
}
