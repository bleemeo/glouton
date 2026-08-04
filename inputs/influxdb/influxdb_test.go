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
	}, nil, time.Now())

	got := collectFinalMetrics(store)

	assertMetrics(t, got, map[string]float64{
		"influxdb_database_num_series":       1234,
		"influxdb_database_num_measurements": 12,
	})
}
