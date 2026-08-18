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

package influxdb

import (
	"strings"

	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/inputs/internal"
	"github.com/bleemeo/glouton/types"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/influxdb"
)

// New initialise influxdb.Input.
func New(url string, username string, password string) (i telegraf.Input, err error) {
	input, ok := telegraf_inputs.Inputs["influxdb"]
	if ok {
		influxdbInput, ok := input().(*influxdb.InfluxDB)
		if ok {
			influxdbInput.URLs = []string{url}
			influxdbInput.Username = username
			influxdbInput.Password = password

			i = &internal.Input{
				Input: influxdbInput,
				Accumulator: internal.Accumulator{
					RenameGlobal:     renameGlobal,
					TransformMetrics: transformMetrics,
					RenameMetrics:    renameMetrics,
					DifferentiatedMetrics: []string{
						"req",
						"reqDurationNs",
						"clientError",
						"serverError",
						"authFail",
						"queryReq",
						"queryReqDurationNs",
						"writeReq",
						"writeReqDurationNs",
						"writeReqBytes",
						"pointsWrittenOK",
						"pointsWrittenFail",
						"pointsWrittenDropped",
						"pointReq",
						"writeError",
						"writeDrop",
						"writeTimeout",
					},
				},
				Name: "influxdb",
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
	if gatherContext.Measurement == "influxdb_queryExecutor" {
		gatherContext.Measurement = "influxdb_query_executor"
	}

	// The URL we queried is redundant with the labels already set on service metrics.
	delete(gatherContext.Tags, "url")

	// The item is what tells the databases apart: without it they would all end up on
	// the same metric.
	if database := gatherContext.Tags["database"]; database != "" {
		gatherContext.Tags[types.LabelItem] = database
	}

	return gatherContext, false
}

func avgDuration(fields map[string]float64, durationField string, countField string, outputName string) {
	durationRate, hasDuration := fields[durationField]
	countRate, hasCount := fields[countField]

	delete(fields, durationField)

	// Protect from division by 0.
	if hasDuration && hasCount && countRate > 0 {
		fields[outputName] = durationRate / countRate / 1e9 // nanoseconds -> seconds.
	}
}

func transformMetrics(currentContext internal.GatherContext, fields map[string]float64, _ map[string]any) map[string]float64 {
	if currentContext.Measurement != "influxdb_httpd" {
		return fields
	}

	avgDuration(fields, "reqDurationNs", "req", "req_duration_seconds")
	avgDuration(fields, "queryReqDurationNs", "queryReq", "query_req_duration_seconds")
	avgDuration(fields, "writeReqDurationNs", "writeReq", "write_req_duration_seconds")

	return fields
}

var fieldRenames = map[string]string{ //nolint:gochecknoglobals
	"queryReq":             "query_req",
	"writeReq":             "write_req",
	"clientError":          "client_error",
	"serverError":          "server_error",
	"writeError":           "write_error",
	"pointReq":             "point_req",
	"authFail":             "auth_fail",
	"writeReqBytes":        "write_req_bytes",
	"pointsWrittenOK":      "points_written_ok",
	"pointsWrittenFail":    "points_written_fail",
	"pointsWrittenDropped": "points_written_dropped",
	"writeDrop":            "write_drop",
	"writeTimeout":         "write_timeout",
	"queriesActive":        "queries_active",
	"numSeries":            "num_series",
	"numMeasurements":      "num_measurements",
}

func renameMetrics(currentContext internal.GatherContext, metricName string) (newMeasurement string, newMetricName string) {
	if renamed, ok := fieldRenames[metricName]; ok {
		return currentContext.Measurement, renamed
	}

	return currentContext.Measurement, strings.ToLower(metricName)
}
