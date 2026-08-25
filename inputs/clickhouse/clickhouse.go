// Copyright 2015-2026 Bleemeo
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

package clickhouse

import (
	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/inputs/internal"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/clickhouse"
)

// New initialise clickhouse.Input.
func New(url string, username string, password string) (i telegraf.Input, err error) {
	input, ok := telegraf_inputs.Inputs["clickhouse"]
	if ok {
		clickhouseInput, ok := input().(*clickhouse.ClickHouse)
		if ok {
			clickhouseInput.Servers = []string{url}
			clickhouseInput.Username = username
			clickhouseInput.Password = password
			clickhouseInput.AutoDiscovery = false

			i = &internal.Input{
				Input: clickhouseInput,
				Accumulator: internal.Accumulator{
					RenameGlobal:     renameGlobal,
					TransformMetrics: transformMetrics,
					DifferentiatedMetrics: []string{
						"query",
						"select_query",
						"query_time_microseconds",
						"failed_query",
						"mutation_total_milliseconds",
						"network_receive_bytes",
						"network_send_bytes",
						"slow_read",
						"mutation_total_parts",
					},
				},
				Name: "clickhouse",
			}
		} else {
			err = inputs.ErrUnexpectedType
		}
	} else {
		err = inputs.ErrDisabledInput
	}

	return i, err
}

func renameGlobal(gatherContext internal.GatherContext) (internal.GatherContext, bool) {
	if gatherContext.Measurement == "clickhouse_metrics" {
		// Rename query field to active_query for clarity and conflict with query from events measurement.
		if value, ok := gatherContext.OriginalFields["query"]; ok {
			gatherContext.OriginalFields["active_query"] = value
			delete(gatherContext.OriginalFields, "query")
		}
	}

	return gatherContext, false
}

// transformMetrics replaces the query and mutation duration counters, which aren't usable
// by themselves, by the average duration of one query and of one mutation.
func transformMetrics(_ internal.GatherContext, fields map[string]float64, _ map[string]any) map[string]float64 {
	internal.AvgDuration(fields, "query_time_microseconds", "query", "query_time_seconds", internal.UsPerSecond)
	internal.AvgDuration(fields, "mutation_total_milliseconds", "mutation_total_parts", "mutation_time_seconds", internal.MsPerSecond)

	return fields
}
