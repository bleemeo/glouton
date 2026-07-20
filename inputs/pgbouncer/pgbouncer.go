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

package pgbouncer

import (
	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/inputs/internal"

	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/config"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/pgbouncer"
)

// New initialise pgbouncer.Input.
func New(address string) (i telegraf.Input, err error) {
	input, ok := telegraf_inputs.Inputs["pgbouncer"]
	if ok {
		pgbouncerInput, ok := input().(*pgbouncer.PgBouncer)
		if ok {
			pgbouncerInput.Address = config.NewSecret([]byte(address))

			i = &internal.Input{
				Input: pgbouncerInput,
				Accumulator: internal.Accumulator{
					TransformMetrics: transformMetrics,
					DifferentiatedMetrics: []string{
						"total_query_count",
						"total_query_time",
						"total_received",
						"total_sent",
					},
				},
				Name: "pgbouncer",
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

	queryTimeRate, hasQueryTime := fields["total_query_time"]
	queryCountRate, hasQueryCount := fields["total_query_count"]

	for metricName, value := range fields {
		if metricName == "total_query_time" {
			// Not used by itslef but replaced below by an actual average duration.
			continue
		}

		if metricName == "total_query_count" {
			metricName = "query_count"
		}

		if metricName == "total_received" {
			metricName = "received_bytes"
		}

		if metricName == "total_sent" {
			metricName = "sent_bytes"
		}

		newFields[metricName] = value
	}

	// Protect from division by 0.
	if hasQueryTime && hasQueryCount && queryCountRate > 0 {
		newFields["query_time_seconds"] = queryTimeRate / queryCountRate / 1000000 // microseconds -> seconds.
	}

	return newFields
}
