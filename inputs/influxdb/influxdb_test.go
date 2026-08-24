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
	"math"
	"testing"
	"time"

	"github.com/bleemeo/glouton/inputs/internal"
	"github.com/bleemeo/glouton/types"

	"github.com/google/go-cmp/cmp"
)

// collectFinalMetrics replicates the measurement/field -> final metric name
// convention applied downstream (inputs.Accumulator.addMetrics): the metric
// name is the field name alone when the measurement was renamed to "", or
// "<measurement>_<field>" otherwise.
func collectFinalMetrics(store *internal.StoreAccumulator) map[string]float64 {
	got := make(map[string]float64)

	for _, m := range store.Measurement {
		for field, value := range m.Fields {
			name := field
			if m.Name != "" {
				name = m.Name + "_" + field
			}

			switch v := value.(type) {
			case float64:
				got[name] = v
			case uint64:
				got[name] = float64(v)
			}
		}
	}

	return got
}

func newAccumulator(store *internal.StoreAccumulator) internal.Accumulator {
	return internal.Accumulator{
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
			"queryDurationNs",
			"queriesExecuted",
			"queriesFinished",
		},
		Accumulator: store,
	}
}

func assertMetrics(t *testing.T, got map[string]float64, want map[string]float64) {
	t.Helper()

	for name, value := range want {
		gotValue, ok := got[name]
		if !ok {
			t.Errorf("metric %q not emitted, got metrics: %v", name, got)

			continue
		}

		if math.Abs(gotValue-value) > 0.0001 {
			t.Errorf("metric %q == %v, want %v", name, gotValue, value)
		}
	}
}

// assertTags checks the tags kept on every emitted measurement.
func assertTags(t *testing.T, store *internal.StoreAccumulator, want map[string]string) {
	t.Helper()

	for _, m := range store.Measurement {
		if diff := cmp.Diff(want, m.Tags); diff != "" {
			t.Errorf("tags of measurement %q (-want +got):\n%s", m.Name, diff)
		}
	}
}

// TestRenamePipelineHTTPD exercises the "influxdb_httpd" measurement:
// cumulative counters converted to per-second rates (via
// DifferentiatedMetrics), with the three *DurationNs counters further
// combined by transformMetrics into an average duration per request,
// mirroring inputs/clickhouse's query_time_seconds pattern.
func TestRenamePipelineHTTPD(t *testing.T) {
	store := &internal.StoreAccumulator{}
	acc := newAccumulator(store)

	t0 := time.Now()
	t1 := t0.Add(10 * time.Second)

	acc.PrepareGather()
	acc.AddFields("influxdb_httpd", map[string]any{
		"req":                  uint64(100),
		"reqDurationNs":        uint64(1_000_000_000),
		"queryReq":             uint64(50),
		"queryReqDurationNs":   uint64(500_000_000),
		"writeReq":             uint64(50),
		"writeReqDurationNs":   uint64(500_000_000),
		"clientError":          uint64(5),
		"serverError":          uint64(1),
		"authFail":             uint64(0),
		"writeReqBytes":        uint64(100000),
		"pointsWrittenOK":      uint64(1000),
		"pointsWrittenFail":    uint64(10),
		"pointsWrittenDropped": uint64(2),
	}, nil, t0)

	// Discard the first gather: every field is a differentiated counter, so
	// it has no rate yet (no history).
	store.Measurement = nil

	acc.PrepareGather()
	acc.AddFields("influxdb_httpd", map[string]any{
		"req":                  uint64(100 + 50),                    // rate = 5/s
		"reqDurationNs":        uint64(1_000_000_000 + 400_000_000), // rate = 40 000 000 ns/s
		"queryReq":             uint64(50 + 20),                     // rate = 2/s
		"queryReqDurationNs":   uint64(500_000_000 + 200_000_000),   // rate = 20 000 000 ns/s
		"writeReq":             uint64(50 + 30),                     // rate = 3/s
		"writeReqDurationNs":   uint64(500_000_000 + 300_000_000),   // rate = 30 000 000 ns/s
		"clientError":          uint64(5 + 2),                       // rate = 0.2/s
		"serverError":          uint64(1 + 1),                       // rate = 0.1/s
		"authFail":             uint64(0 + 3),                       // rate = 0.3/s
		"writeReqBytes":        uint64(100000 + 50000),              // rate = 5000/s
		"pointsWrittenOK":      uint64(1000 + 400),                  // rate = 40/s
		"pointsWrittenFail":    uint64(10 + 5),                      // rate = 0.5/s
		"pointsWrittenDropped": uint64(2 + 1),                       // rate = 0.1/s
	}, nil, t1)

	got := collectFinalMetrics(store)

	// req_duration_seconds = reqDurationNsRate / reqRate / 1e9 = 40 000 000 / 5 / 1e9 = 0.008
	// query_req_duration_seconds = 20 000 000 / 2 / 1e9 = 0.01
	// write_req_duration_seconds = 30 000 000 / 3 / 1e9 = 0.01
	assertMetrics(t, got, map[string]float64{
		"influxdb_httpd_req":                        5,
		"influxdb_httpd_req_duration_seconds":       0.008,
		"influxdb_httpd_query_req":                  2,
		"influxdb_httpd_query_req_duration_seconds": 0.01,
		"influxdb_httpd_write_req":                  3,
		"influxdb_httpd_write_req_duration_seconds": 0.01,
		"influxdb_httpd_client_error":               0.2,
		"influxdb_httpd_server_error":               0.1,
		"influxdb_httpd_auth_fail":                  0.3,
		"influxdb_httpd_write_req_bytes":            5000,
		"influxdb_httpd_points_written_ok":          40,
		"influxdb_httpd_points_written_fail":        0.5,
		"influxdb_httpd_points_written_dropped":     0.1,
	})

	// The raw nanosecond-duration counters are only used internally to
	// compute the average durations above, they must not leak into output.
	for _, name := range []string{
		"influxdb_httpd_req_duration_ns",
		"influxdb_httpd_query_req_duration_ns",
		"influxdb_httpd_write_req_duration_ns",
	} {
		if _, ok := got[name]; ok {
			t.Errorf("raw duration counter %q should have been dropped, got value %v", name, got[name])
		}
	}
}

// TestRenamePipelineWrite exercises the "influxdb_write" measurement's
// cumulative counters.
func TestRenamePipelineWrite(t *testing.T) {
	store := &internal.StoreAccumulator{}
	acc := newAccumulator(store)

	t0 := time.Now()
	t1 := t0.Add(10 * time.Second)

	acc.PrepareGather()
	acc.AddFields("influxdb_write", map[string]any{
		"pointReq":     uint64(2000),
		"writeError":   uint64(5),
		"writeDrop":    uint64(1),
		"writeTimeout": uint64(0),
	}, nil, t0)

	store.Measurement = nil

	acc.PrepareGather()
	acc.AddFields("influxdb_write", map[string]any{
		"pointReq":     uint64(2000 + 300), // rate = 30/s
		"writeError":   uint64(5 + 2),      // rate = 0.2/s
		"writeDrop":    uint64(1 + 1),      // rate = 0.1/s
		"writeTimeout": uint64(0 + 1),      // rate = 0.1/s
	}, nil, t1)

	got := collectFinalMetrics(store)

	assertMetrics(t, got, map[string]float64{
		"influxdb_write_point_req":     30,
		"influxdb_write_write_error":   0.2,
		"influxdb_write_write_drop":    0.1,
		"influxdb_write_write_timeout": 0.1,
	})
}

// TestRenamePipelineQueryExecutor checks that the camelCase
// "influxdb_queryExecutor" measurement name is fixed to
// "influxdb_query_executor", and that the (gauge) queriesActive field is
// left untouched by differentiation.
func TestRenamePipelineQueryExecutor(t *testing.T) {
	store := &internal.StoreAccumulator{}
	acc := newAccumulator(store)

	acc.PrepareGather()
	acc.AddFields("influxdb_queryExecutor", map[string]any{
		"queriesActive": 7.0,
	}, nil, time.Now())

	got := collectFinalMetrics(store)

	assertMetrics(t, got, map[string]float64{
		"influxdb_query_executor_queries_active": 7,
	})
}

// TestShardItems checks the series of the storage-engine measurements are told apart. A 1.8
// instance reports influxdb_shard and the influxdb_tsm1_* family once per shard, all with the
// same database tag, so the database alone can't be the item: the series would share a name
// and an item and be rejected as duplicates.
func TestShardItems(t *testing.T) {
	store := &internal.StoreAccumulator{}
	acc := newAccumulator(store)

	acc.PrepareGather()

	for _, shard := range []struct{ id, retention string }{{"1", "monitor"}, {"2", "monitor"}, {"3", "autogen"}} {
		acc.AddFields("influxdb_tsm1_cache", map[string]any{"diskBytes": 1024.0}, map[string]string{
			"database": "_internal", "retentionPolicy": shard.retention, "id": shard.id,
			// Left out of the item on purpose: the same on every shard, or a path.
			"engine": "tsm1", "indexType": "inmem",
			"path": "/var/lib/influxdb/data/_internal/" + shard.retention + "/" + shard.id,
		}, time.Now())
	}

	// One measurement of one database, which has no shard id.
	acc.AddFields("influxdb_measurement", map[string]any{"numSeries": 12.0},
		map[string]string{"database": "_internal", "measurement": "httpd"}, time.Now())

	gotItems := make([]string, 0, len(store.Measurement))
	for _, m := range store.Measurement {
		gotItems = append(gotItems, m.Tags[types.LabelItem])
	}

	wantItems := []string{
		"_internal_monitor_1",
		"_internal_monitor_2",
		"_internal_autogen_3",
		"_internal_httpd",
	}

	if diff := cmp.Diff(wantItems, gotItems); diff != "" {
		t.Errorf("items (-want +got):\n%s", diff)
	}
}

// TestQueryDuration checks the average execution time of a query, derived from the
// cumulative queryDurationNs over the number of queries that finished. It is the only
// duration covering a query itself: influxdb_httpd_query_req_duration_seconds times the
// HTTP request that carried it.
func TestQueryDuration(t *testing.T) {
	store := &internal.StoreAccumulator{}
	acc := newAccumulator(store)

	t0 := time.Now()
	t1 := t0.Add(10 * time.Second)

	acc.PrepareGather()
	acc.AddFields("influxdb_queryExecutor", map[string]any{
		"queriesActive":   1.0,
		"queriesExecuted": uint64(100),
		"queriesFinished": uint64(100),
		"queryDurationNs": uint64(1_000_000_000),
	}, nil, t0)

	// Discard the first gather: the two cumulative fields have no rate yet.
	store.Measurement = nil

	acc.PrepareGather()
	acc.AddFields("influxdb_queryExecutor", map[string]any{
		"queriesActive":   2.0,
		"queriesExecuted": uint64(100 + 40),                    // rate = 4/s
		"queriesFinished": uint64(100 + 20),                    // rate = 2/s
		"queryDurationNs": uint64(1_000_000_000 + 600_000_000), // rate = 60 000 000 ns/s
	}, nil, t1)

	got := collectFinalMetrics(store)

	// duration_seconds = queryDurationNsRate / queriesFinishedRate / 1e9
	//                  = 60 000 000 / 2 / 1e9 = 0.03
	assertMetrics(t, got, map[string]float64{
		"influxdb_query_executor_duration_seconds": 0.03,
		"influxdb_query_executor_queries_finished": 2,
		"influxdb_query_executor_queries_executed": 4,
		"influxdb_query_executor_queries_active":   2,
	})

	// The cumulative nanoseconds themselves must not be published: they were consumed by
	// the average.
	if _, ok := got["influxdb_query_executor_querydurationns"]; ok {
		t.Error("influxdb_query_executor_querydurationns is still emitted")
	}
}

// TestRenamePipelineDatabase checks the (gauge) numSeries/numMeasurements
// fields, which must not be differentiated since they're instant counts, not
// cumulative counters.
func TestRenamePipelineDatabase(t *testing.T) {
	store := &internal.StoreAccumulator{}
	acc := newAccumulator(store)

	acc.PrepareGather()
	acc.AddFields("influxdb_database", map[string]any{
		"numSeries":       1234.0,
		"numMeasurements": 12.0,
	}, map[string]string{
		"database": "telegraf",
		"url":      "http://127.0.0.1:8086/debug/vars",
	}, time.Now())

	got := collectFinalMetrics(store)

	assertMetrics(t, got, map[string]float64{
		"influxdb_database_num_series":       1234,
		"influxdb_database_num_measurements": 12,
	})

	// The URL we queried is redundant with the labels already set on service
	// metrics, while "database" tells which database this is about.
	assertTags(t, store, map[string]string{"database": "telegraf", "item": "telegraf"})
}
