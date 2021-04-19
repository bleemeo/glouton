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

package postgresql

import (
	"glouton/inputs"
	"glouton/inputs/internal"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/postgresql"
)

const inputName = "postgreSQL"

// New initialise postgresql.Input.
func New(url string) (i telegraf.Input, err error) {
	var input, ok = telegraf_inputs.Inputs["postgresql"]
	if ok {
		postgresqlInput, ok := input().(*postgresql.Postgresql)
		if ok {
			postgresqlInput.Address = url
			i = &internal.Input{
				Input: postgresqlInput,
				Accumulator: internal.Accumulator{
					RenameGlobal: renameGlobal,
					DerivatedMetrics: []string{
						"xact_commit", "xact_rollback", "blks_read", "blks_hit", "tup_returned", "tup_fetched",
						"tup_inserted", "tup_updated", "tup_deleted", "temp_files", "temp_bytes", "blk_read_time",
						"blk_write_time",
					},
					TransformMetrics: transformMetrics,
				},
			}
		} else {
			err = inputs.ErrUnexpectedType(inputName)
		}
	} else {
		err = inputs.ErrDisabledInput(inputName, inputs.TelegrafService)
	}

	return
}

func renameGlobal(originalContext internal.GatherContext) (newContext internal.GatherContext, drop bool) {
	if originalContext.Tags["db"] == "template0" || originalContext.Tags["db"] == "template1" {
		return originalContext, true
	}

	originalContext.Annotations.BleemeoItem = originalContext.Tags["db"]

	return originalContext, false
}

func transformMetrics(originalContext internal.GatherContext, currentContext internal.GatherContext, fields map[string]float64, originalFields map[string]interface{}) map[string]float64 {
	newFields := make(map[string]float64)

	for metricName, value := range fields {
		switch metricName {
		case "xact_commit":
			newFields["commit"] = value
		case "xact_rollback":
			newFields["rollback"] = value
		case "blks_read", "blks_hit", "tup_returned", "tup_fetched", "tup_inserted", "tup_updated":
			newFields[metricName] = value
		case "tup_deleted", "temp_files", "temp_bytes", "blk_read_time", "blk_write_time":
			newFields[metricName] = value
		}
	}

	return newFields
}
