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

package postgresql

import (
	"slices"

	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/inputs/internal"
	"github.com/bleemeo/glouton/types"

	"github.com/influxdata/telegraf"
	telegraf_config "github.com/influxdata/telegraf/config"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/postgresql"
)

// New initialise postgresql.Input.
func New(address string, detailedDatabases []string) (telegraf.Input, error) {
	input, ok := telegraf_inputs.Inputs["postgresql"]
	if !ok {
		return nil, inputs.ErrDisabledInput
	}

	postgresqlInput, ok := input().(*postgresql.Postgresql)
	if !ok {
		return nil, inputs.ErrUnexpectedType
	}

	postgresqlInput.Address = telegraf_config.NewSecret([]byte(address))

	globalMetricsInput := sumMetrics{
		input: postgresqlInput,
	}

	internalInput := &internal.Input{
		Input: globalMetricsInput,
		Accumulator: internal.Accumulator{
			RenameGlobal: renameGlobal(detailedDatabases),
			DifferentiatedMetrics: []string{
				"xact_commit", "xact_rollback", "blks_read", "blks_hit", "tup_returned", "tup_fetched",
				"tup_inserted", "tup_updated", "tup_deleted", "temp_files", "temp_bytes", "blk_read_time",
				"blk_write_time",
			},
			TransformMetrics: transformMetrics,
		},
		Name: "postgresql",
	}

	return internal.InputWithSecrets{Input: internalInput, Count: 1}, nil
}

func renameGlobal(detailedDatabases []string) func(internal.GatherContext) (internal.GatherContext, bool) {
	return func(gatherContext internal.GatherContext) (internal.GatherContext, bool) {
		// Always allow sum metrics.
		if _, ok := gatherContext.Tags["sum"]; ok {
			return gatherContext, false
		}

		if slices.Contains(detailedDatabases, gatherContext.Tags["db"]) {
			gatherContext.Tags[types.LabelItem] = gatherContext.Tags["db"]

			return gatherContext, false
		}

		return gatherContext, true
	}
}

func transformMetrics(
	currentContext internal.GatherContext,
	fields map[string]float64,
	originalFields map[string]any,
) map[string]float64 {
	_ = originalFields
	newFields := make(map[string]float64)

	suffix := ""
	if _, ok := currentContext.Tags["sum"]; ok {
		suffix = "_sum"
	}

	for metricName, value := range fields {
		switch metricName {
		case "xact_commit":
			newFields["commit"+suffix] = value
		case "xact_rollback":
			newFields["rollback"+suffix] = value
		case "blks_read", "blks_hit", "tup_returned", "tup_fetched", "tup_inserted", "tup_updated":
			newFields[metricName+suffix] = value
		case "tup_deleted", "temp_files", "temp_bytes":
			newFields[metricName+suffix] = value
		case "blk_read_time":
			newFields["blk_read_utilization"+suffix] = value / 10 // convert ms/s to %
		case "blk_write_time":
			newFields["blk_write_utilization"+suffix] = value / 10 // convert ms/s to %
		}
	}

	return newFields
}
