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
					// The counters InfluxDB accumulates since it started, turned into
					// per-second rates. Only the ones Glouton publishes, plus the
					// *DurationNs fields transformMetrics needs a rate of to derive an
					// average duration: writeReqBytes, queriesExecuted and queriesFinished
					// are counters of the same shape, but aren't default metrics, so
					// nothing would read their rate.
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

	// What identifies a series is kept as labels of its own -- database,
	// retentionPolicy, measurement and id -- rather than joined into the item.
	//
	// The database alone isn't enough: the storage-engine measurements (influxdb_shard,
	// influxdb_tsm1_cache, _engine, _filestore, _wal) are reported once per shard and
	// influxdb_measurement once per measurement, all of them repeating the same database.
	// A real 1.8 instance with only its own _internal database already reports 7 shards,
	// so 7 series of each would land on the same name and be rejected as duplicates, the
	// way rabbitmq_consumers is. The retention policy and shard id are what separate them.
	//
	// Keeping them needs CompatibilityNameItem to be off for this service, since the
	// compatibility naming keeps only the item and would drop all four; see the InfluxDB
	// case of Discovery.createInput. The item is then left to the service instance, which
	// for a containerised InfluxDB is its container name, instead of being glued to a
	// shard id.
	//
	// The four below describe a shard rather than identify one, so they are dropped
	// instead of becoming labels: engine and indexType hold the same value on every shard
	// of an instance, and path and walPath would put the filesystem layout into a label
	// for something the id already identifies.
	delete(gatherContext.Tags, "engine")
	delete(gatherContext.Tags, "indexType")
	delete(gatherContext.Tags, "path")
	delete(gatherContext.Tags, "walPath")

	return gatherContext, false
}

func transformMetrics(currentContext internal.GatherContext, fields map[string]float64, _ map[string]any) map[string]float64 {
	if currentContext.Measurement != "influxdb_httpd" {
		return fields
	}

	internal.AvgDuration(fields, "reqDurationNs", "req", "req_duration_seconds", internal.NsPerSecond)
	internal.AvgDuration(fields, "queryReqDurationNs", "queryReq", "query_req_duration_seconds", internal.NsPerSecond)
	internal.AvgDuration(fields, "writeReqDurationNs", "writeReq", "write_req_duration_seconds", internal.NsPerSecond)

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
	"queriesExecuted":      "queries_executed",
	"queriesFinished":      "queries_finished",
	"numSeries":            "num_series",
	"numMeasurements":      "num_measurements",
}

func renameMetrics(currentContext internal.GatherContext, metricName string) (newMeasurement string, newMetricName string) {
	if renamed, ok := fieldRenames[metricName]; ok {
		return currentContext.Measurement, renamed
	}

	return currentContext.Measurement, strings.ToLower(metricName)
}
