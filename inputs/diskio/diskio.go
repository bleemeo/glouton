// Copyright 2015-2025 Bleemeo
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
	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/inputs/internal"
	"github.com/bleemeo/glouton/types"
	"github.com/bleemeo/glouton/version"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/diskio"
)

type diskIOTransformer struct {
	matcher types.Matcher
}

// New initialise diskio.Input.
func New(diskMatcher types.Matcher) (i telegraf.Input, err error) {
	input, ok := telegraf_inputs.Inputs["diskio"]

	if ok {
		diskioInput, _ := input().(*diskio.DiskIO)
		diskioInput.Log = internal.NewLogger()
		dt := diskIOTransformer{
			matcher: diskMatcher,
		}
		i = &internal.Input{
			Input: diskioInput,
			Accumulator: internal.Accumulator{
				RenameGlobal:          dt.renameGlobal,
				DifferentiatedMetrics: []string{"merged_reads", "read_bytes", "read_time", "reads", "merged_writes", "write_bytes", "writes", "write_time", "io_time"},
				TransformMetrics:      dt.transformMetrics,
			},
			Name: "diskio",
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

	if !dt.matcher.Match(item) {
		return gatherContext, true
	}

	gatherContext.Tags[types.LabelItem] = item

	return gatherContext, false
}

func (dt diskIOTransformer) transformMetrics(currentContext internal.GatherContext, fields map[string]float64, originalFields map[string]any) map[string]float64 {
	_ = currentContext
	_ = originalFields

	for _, name := range []string{"io_time", "read_time", "write_time"} {
		if value, ok := fields[name]; ok {
			delete(fields, name)

			newName := name

			switch name {
			case "io_time":
				newName = "utilization"
			case "read_time":
				newName = "read_utilization"
			case "write_time":
				newName = "write_utilization"
			}

			// io_time & co are millisecond per second.
			fields[newName] = value / 1000. * 100.
		}
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
