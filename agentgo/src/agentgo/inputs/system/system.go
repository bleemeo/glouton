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

package system

import (
	"agentgo/inputs/internal"
	"errors"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/system"
)

// New initialise system.Input
func New() (i telegraf.Input, err error) {
	var input, ok = telegraf_inputs.Inputs["system"]
	if ok {
		systemInput := input().(*system.SystemStats)
		i = &internal.Input{
			Input: systemInput,
			Accumulator: internal.Accumulator{
				TransformMetrics: transformMetrics,
				RenameMetrics:    renameMetrics,
			},
		}
	} else {
		err = errors.New("Telegraf don't have \"system\" input")
	}
	return
}

func transformMetrics(measurement string, fields map[string]float64, tags map[string]string) map[string]float64 {
	delete(fields, "n_cpus")
	delete(fields, "uptime")
	delete(fields, "uptime_format")
	return fields
}

func renameMetrics(measurement string, metricName string, tags map[string]string) (newMeasurement string, newMetricName string) {
	newMetricName = metricName
	newMeasurement = measurement
	if metricName == "n_users" {
		newMeasurement = "users"
		newMetricName = "logged"
	}
	return
}
