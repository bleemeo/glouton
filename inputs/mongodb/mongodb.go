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

package mongodb

import (
	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/inputs/internal"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/mongodb"
)

// New initialise mongodb.Input.
func New(url string) (i telegraf.Input, err error) {
	input, ok := telegraf_inputs.Inputs["mongodb"]
	if ok {
		mongodbInput, ok := input().(*mongodb.MongoDB)
		if ok {
			slice := append(make([]string, 0), url)
			mongodbInput.Servers = slice
			mongodbInput.Log = internal.NewLogger()
			i = &internal.Input{
				Input: mongodbInput,
				Accumulator: internal.Accumulator{
					DifferentiatedMetrics: []string{"net_out_bytes", "net_in_bytes"},
					TransformMetrics:      transformMetrics,
				},
				Name: "mongodb",
			}
		} else {
			err = inputs.ErrUnexpectedType
		}
	} else {
		err = inputs.ErrDisabledInput
	}

	return
}

func transformMetrics(currentContext internal.GatherContext, fields map[string]float64, originalFields map[string]any) map[string]float64 {
	_ = currentContext
	_ = originalFields
	newFields := make(map[string]float64)

	for metricName, value := range fields {
		switch metricName {
		case "open_connections", "queued_reads", "queued_writes", "active_reads", "active_writes", "net_out_bytes", "net_in_bytes":
			newFields[metricName] = value
		case "queries_per_sec":
			newFields["queries"] = value
		}
	}

	return newFields
}
