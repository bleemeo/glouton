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

package cpu

import (
	"runtime"
	"strings"

	"glouton/inputs"
	"glouton/inputs/internal"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/cpu"
)

// New initialise cpu.Input.
func New() (i telegraf.Input, err error) {
	var input, ok = telegraf_inputs.Inputs["cpu"]
	if ok {
		cpuInput := input().(*cpu.CPUStats)
		// were we to change this, we should consider returning "interrupt' metrics on Windows
		// (see the comment below)
		cpuInput.PerCPU = false
		cpuInput.CollectCPUTime = false
		i = &internal.Input{
			Input: cpuInput,
			Accumulator: internal.Accumulator{
				RenameGlobal:     renameGlobal,
				TransformMetrics: transformMetrics,
			},
		}
	} else {
		err = inputs.ErrDisabledInput
	}

	return
}

func renameGlobal(originalContext internal.GatherContext) (newContext internal.GatherContext, drop bool) {
	return internal.GatherContext{
		Measurement: originalContext.Measurement,
		Tags:        nil,
	}, false
}

func transformMetrics(originalContext internal.GatherContext, currentContext internal.GatherContext, fields map[string]float64, originalFields map[string]interface{}) map[string]float64 {
	finalFields := make(map[string]float64)

	var (
		cpuOther float64
		cpuUsed  float64
	)

	for metricName, value := range fields {
		finalMetricName := strings.Replace(metricName, "usage_", "", -1)
		if finalMetricName == "irq" {
			finalMetricName = "interrupt"
		} else if finalMetricName == "iowait" {
			finalMetricName = "wait"
		}

		finalFields[finalMetricName] = value

		switch finalMetricName {
		case "user":
			cpuUsed += value
		case "nice":
			cpuOther += value
		case "system":
			cpuUsed += value
		case "interrupt":
			cpuUsed += value
			cpuOther += value
		case "softirq":
			cpuUsed += value
			cpuOther += value
		case "steal":
			cpuUsed += value
			cpuOther += value
		}
	}

	finalFields["other"] = cpuOther
	finalFields["used"] = cpuUsed

	// drop unsupported fields on windows, it is needless to generate traffic for "null" metrics
	if runtime.GOOS == "windows" {
		for k := range finalFields {
			switch k {
			// note: cpu interrupt is a special case, as it CAN be reported (telegraf
			// uses gopsutil to retrieve cpu metrics, and gopsutil is capable of doing so:
			// https://github.com/shirou/gopsutil/blob/0e9462eed2c80a710fafb6bea1d412f822c481f6/cpu/cpu_windows.go#L153,
			// but that ultimately depends on whether the user configuration asks for per-cpu stats or not:
			// https://github.com/influxdata/telegraf/blob/ef262b137275e63103ef83770c9bcf7388f0eeb7/plugins/inputs/cpu/cpu.go#L51)
			// It is not returned now as we disabled per-cpu stats in this collector.
			case "used", "other", "system", "user", "idle":
				continue
			default:
				// apparently, there is no risks of iterator invalidation here in go (https://github.com/golang/go/issues/9926), so...
				delete(finalFields, k)
			}
		}
	}

	return finalFields
}
