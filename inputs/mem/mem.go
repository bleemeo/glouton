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

package mem

import (
	"errors"
	"glouton/inputs/internal"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/mem"
)

// New initialise mem.Input
func New() (i telegraf.Input, err error) {
	var input, ok = telegraf_inputs.Inputs["mem"]
	if ok {
		memInput := input().(*mem.MemStats)
		i = &internal.Input{
			Input: memInput,
			Accumulator: internal.Accumulator{
				TransformMetrics: transformMetrics,
			},
		}
	} else {
		err = errors.New("input mem not enabled in Telegraf")
	}

	return
}

func transformMetrics(originalContext internal.GatherContext, currentContext internal.GatherContext, fields map[string]float64, originalFields map[string]interface{}) map[string]float64 {
	for metricName, value := range fields {
		switch metricName {
		case "available_percent":
			delete(fields, metricName)
			fields["available_perc"] = value
		case "used_percent":
			delete(fields, metricName)
			fields["used_perc"] = value
		// All next cases are metric ignored. They are on different case to
		// avoid very long line.
		case "active", "inactive", "wired", "commit_limit", "committed_as", "dirty", "high_free", "high_total":
			delete(fields, metricName)
		case "huge_page_size", "huge_pages_free", "huge_pages_total", "low_free", "low_total", "mapped", "page_tables":
			delete(fields, metricName)
		case "shared", "swap_cached", "swap_free", "swap_total", "vmalloc_chunk", "vmalloc_total", "vmalloc_used":
			delete(fields, metricName)
		case "write_back", "write_back_tmp":
			delete(fields, metricName)
		}
	}

	return fields
}
