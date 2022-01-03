// Copyright 2015-2019 Bleemeo
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
	"glouton/inputs"
	"glouton/inputs/internal"
	"glouton/version"
	"regexp"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/diskio"
)

type diskIOTransformer struct {
	whitelist []*regexp.Regexp
	blacklist []*regexp.Regexp
}

// New initialise diskio.Input.
//
// whitelist is a list of regular expretion for device to include
// blacklist is a list of regular expretion for device to include
//
// If blacklist is provided (not empty) it's used and whitelist is ignored.
func New(whitelist []*regexp.Regexp, blacklist []*regexp.Regexp) (i telegraf.Input, err error) {
	input, ok := telegraf_inputs.Inputs["diskio"]

	if ok {
		diskioInput, _ := input().(*diskio.DiskIO)
		diskioInput.Log = internal.Logger{}
		dt := diskIOTransformer{
			whitelist: whitelist,
			blacklist: blacklist,
		}
		i = &internal.Input{
			Input: diskioInput,
			Accumulator: internal.Accumulator{
				RenameGlobal:     dt.renameGlobal,
				DerivatedMetrics: []string{"merged_reads", "read_bytes", "read_time", "reads", "merged_writes", "write_bytes", "writes", "write_time", "io_time"},
				TransformMetrics: dt.transformMetrics,
			},
		}
	} else {
		err = inputs.ErrDisabledInput
	}

	return i, err
}

func (dt diskIOTransformer) renameGlobal(gatherContext internal.GatherContext) (internal.GatherContext, bool) {
	item, ok := gatherContext.Tags["name"]
	gatherContext.Measurement = "io"
	gatherContext.Tags = make(map[string]string)

	if !ok {
		return gatherContext, true
	}

	match := false

	if len(dt.blacklist) > 0 {
		match = true

		for _, r := range dt.blacklist {
			if r.MatchString(item) {
				match = false

				break
			}
		}
	} else {
		for _, r := range dt.whitelist {
			if r.MatchString(item) {
				match = true

				break
			}
		}
	}

	if !match {
		return gatherContext, true
	}

	gatherContext.Annotations.BleemeoItem = item
	gatherContext.Tags["device"] = item

	return gatherContext, false
}

func (dt diskIOTransformer) transformMetrics(currentContext internal.GatherContext, fields map[string]float64, originalFields map[string]interface{}) map[string]float64 {
	if ioTime, ok := fields["io_time"]; ok {
		delete(fields, "io_time")

		fields["time"] = ioTime
		// io_time is millisecond per second.
		fields["utilization"] = ioTime / 1000. * 100.
	}

	if rmerged, ok := fields["merged_reads"]; ok {
		delete(fields, "merged_reads")

		fields["read_merged"] = rmerged
	}

	if wmerged, ok := fields["merged_writes"]; ok {
		delete(fields, "merged_writes")

		fields["write_merged"] = wmerged
	}

	// win_perf_counters will report io_time and io_utilization on windows
	if version.IsWindows() {
		delete(fields, "time")
		delete(fields, "utilization")
	}

	delete(fields, "weighted_io_time")
	delete(fields, "iops_in_progress")

	return fields
}
