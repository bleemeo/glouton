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

package system

import (
	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/inputs/internal"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/system"
)

// New initialise system.Input.
func New() (i telegraf.Input, err error) {
	input, ok := telegraf_inputs.Inputs["system"]
	if ok {
		systemInput, _ := input().(*system.SystemStats)
		systemInput.Log = internal.NewLogger()
		i = &internal.Input{
			Input: systemInput,
			Accumulator: internal.Accumulator{
				TransformMetrics: transformMetrics,
				RenameMetrics:    renameMetrics,
			},
			Name: "system",
		}
	} else {
		err = inputs.ErrDisabledInput
	}

	return
}

func transformMetrics(currentContext internal.GatherContext, fields map[string]float64, originalFields map[string]any) map[string]float64 {
	_ = currentContext
	_ = originalFields

	delete(fields, "n_cpus")
	delete(fields, "uptime_format")

	return fields
}

func renameMetrics(currentContext internal.GatherContext, metricName string) (newMeasurement string, newMetricName string) {
	newMetricName = metricName
	newMeasurement = currentContext.Measurement

	if metricName == "n_users" {
		newMeasurement = "users"
		newMetricName = "logged"
	}

	if metricName == "uptime" {
		newMeasurement = ""
	}

	return
}
