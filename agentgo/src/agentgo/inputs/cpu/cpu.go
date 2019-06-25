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
	"strings"

	"agentgo/inputs/internal"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/cpu"
)

// New initialise cpu.Input
func New() (i telegraf.Input, err error) {
	var input, ok = telegraf_inputs.Inputs["cpu"]
	if ok {
		cpuInput := input().(*cpu.CPUStats)
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
		err = errors.New("Telegraf don't have \"cpu\" input")
	}
	return
}

func renameGlobal(measurement string, tags map[string]string) (string, map[string]string, bool) {
	return measurement, nil, false
}

func transformMetrics(measurement string, fields map[string]float64, tags map[string]string) map[string]float64 {
	finalFields := make(map[string]float64)
	var cpuOther float64
	var cpuUsed float64
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
	return finalFields
}
