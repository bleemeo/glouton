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

package mem

import (
	"fmt"

	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/inputs/internal"
	"github.com/bleemeo/glouton/version"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/mem"
)

// New initialise mem.Input.
func New() (i telegraf.Input, err error) {
	input, ok := telegraf_inputs.Inputs["mem"]
	if ok {
		memInput, _ := input().(*mem.Mem)

		if err := memInput.Init(); err != nil {
			return nil, fmt.Errorf("init: %w", err)
		}

		i = &internal.Input{
			Input: memInput,
			Accumulator: internal.Accumulator{
				TransformMetrics: transformMetrics,
			},
			Name: "mem",
		}
	} else {
		err = inputs.ErrDisabledInput
	}

	return
}

func transformMetrics(currentContext internal.GatherContext, fields map[string]float64, originalFields map[string]any) map[string]float64 {
	_ = currentContext
	_ = originalFields

	if version.IsLinux() {
		// On Linux, mem_used is used the "old" procps < 4.0 formula (based on buffered, cached and free memory)
		// This will avoid too many that are close to 80% of memory usage to generate notifications.
		// Note: at this point, fields["cached"] already take sreclaimable in account (gopsutil does the sum).
		fields["used"] = fields["total"] - fields["free"] - fields["cached"] - fields["buffered"]

		// Update used_perc
		fields["used_percent"] = (fields["used"] / fields["total"]) * 100
	}

	for metricName, value := range fields {
		switch metricName {
		case "available_percent":
			delete(fields, metricName)
			fields["available_perc"] = value
		case "used_percent":
			delete(fields, metricName)
			fields["used_perc"] = value
		case "sreclaimable":
			delete(fields, "sreclaimable")
			fields["slab_recl"] = value
		case "sunreclaim":
			delete(fields, "sunreclaim")
			fields["slab_unrecl"] = value
		case "buffered", "cached":
			if version.IsFreeBSD() {
				// It's unclear whether buffered memory is already counter in mem_wired or not.
				// Anyway, it seems that FreeBSD only had buffered memory with UFS and not with ZFS,
				// since we support TrueNAS, we should only had ZFS.
				// For "cached", it no longer exists on recent FreeBSD. It's replaced by "laundry".
				delete(fields, metricName)
			}
		// Only include inactive, because active & wired are already included in used.
		// Inactive can't really be mapped to existing metrics (buffered or cached).
		// On FreeBSD inactive are metric allocated by application not recently used (on Linux, we would
		// report it as used). It may contains both clean and dirty (likely anonymous) pages which on
		// Linux are more or less buffers (dirty pages) and cached (clean pages).
		case "inactive":
			if !version.IsFreeBSD() {
				// Those metrics aren't used on non-FreeBSD system.
				delete(fields, metricName)
			}
		// All next cases are metric ignored. They are on different case to
		// avoid very long line.
		case "active", "wired", "commit_limit", "committed_as", "dirty", "high_free", "high_total":
			delete(fields, metricName)
		case "huge_page_size", "huge_pages_free", "huge_pages_total", "low_free", "low_total", "mapped", "page_tables":
			delete(fields, metricName)
		case "shared", "swap_cached", "swap_free", "swap_total", "vmalloc_chunk", "vmalloc_total", "vmalloc_used":
			delete(fields, metricName)
		case "write_back", "write_back_tmp", "slab":
			delete(fields, metricName)
		}
	}

	return fields
}
