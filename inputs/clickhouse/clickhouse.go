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

// Clickhouse sometimes generates false negative metrics we can't fix (notably when tables are dropped and freed elsewhere).
// The incorrect negative value is then cast from Int64 to Uint64, turning it into a massive number even more wrong.
const wrappedNegativeThreshold = 1 << 63

func transformMetrics(currentContext internal.GatherContext, fields map[string]float64, originalFields map[string]any) map[string]float64 {
	_ = originalFields

	newFields := make(map[string]float64)

	queryTimeRate, hasQueryTime := fields["query_time_microseconds"]
	queryCountRate, hasQueryCount := fields["query"]
	mutationTimeRate, hasMutationTime := fields["mutation_total_milliseconds"]
	mutationCountRate, hasMutationCount := fields["mutation_total_parts"]

	for metricName, value := range fields {
		if metricName == "query_time_microseconds" || metricName == "mutation_total_milliseconds" {
			// Not used by themselves but replaced below by actual average durations for queries and mutations.
			continue
		}

		if currentContext.Measurement == "clickhouse_metrics" && value >= wrappedNegativeThreshold {
			// Drop wrapped-negative incorrect values interpreted as near-2^64 numbers.
			continue
		}

		newFields[metricName] = value
	}

	// Protect from division by 0.
	if hasQueryTime && hasQueryCount && queryCountRate > 0 {
		newFields["query_time_seconds"] = queryTimeRate / queryCountRate / 1000000 // microseconds -> seconds.
	}

	// Protect from division by 0.
	if hasMutationTime && hasMutationCount && mutationCountRate > 0 {
		newFields["mutation_time_seconds"] = mutationTimeRate / mutationCountRate / 1000 // milliseconds -> seconds.
	}

	return newFields
}
