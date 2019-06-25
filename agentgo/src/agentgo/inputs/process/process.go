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

package process

import (
	"agentgo/inputs/internal"
	"errors"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/processes"
)

// New initialise process.Input
func New() (i telegraf.Input, err error) {
	var input, ok = telegraf_inputs.Inputs["processes"]
	if ok {
		processInput := input().(*processes.Processes)
		i = &internal.Input{
			Input: processInput,
			Accumulator: internal.Accumulator{
				TransformMetrics: transformMetrics,
			},
		}
	} else {
		err = errors.New("Telegraf don't have \"processes\" input")
	}
	return
}

func transformMetrics(measurement string, fields map[string]float64, tags map[string]string) map[string]float64 {
	for metricName, value := range fields {
		newName := ""
		switch metricName {
		case "blocked":
			newName = "status_blocked"
		case "running":
			newName = "status_running"
		case "sleeping":
			newName = "status_sleeping"
		case "stopped":
			newName = "status_stopped"
		case "total":
			newName = "total"
		case "zombies":
			newName = "status_zombies"
		case "dead", "idle", "unknown":
			delete(fields, metricName)
		case "paging":
			newName = "status_paging"
		case "total_threads":
			newName = "total_threads"
		}
		if newName != "" {
			delete(fields, metricName)
			fields[newName] = value
		}
	}
	return fields
}
