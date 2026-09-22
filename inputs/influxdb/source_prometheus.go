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
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/bleemeo/glouton/prometheus/registry"
	"github.com/bleemeo/glouton/prometheus/scrapper"

	"github.com/influxdata/telegraf"
	dto "github.com/prometheus/client_model/go"
)

// promCounter is a cumulative counter published as a rate.
//
// keep lists the labels that stay; everything else is summed over. That is required
// rather than cosmetic: two points reaching the accumulator with the same tags in one
// gather would collide. It is also how the label sets are kept small -- 2.x reports its
// HTTP counter per handler, method, path, response code and user agent, and 3.x per
// method, path and a "method_path" that repeats both.
//
// match narrows the points to those carrying one label value, for the metrics that are a
// slice of a wider counter: the 4xx and 5xx rates are the HTTP counter filtered by status.
type promCounter struct {
	family string
	field  string
	keep   []string
	match  map[string]string
}

// promGauge is an instant value, published as it is read.
type promGauge struct {
	family string
	field  string
	keep   []string
}

// promDuration is a histogram published as the average duration of one operation, built
// from the server's own "_sum" and "_count" -- which Glouton's parser hands over as
// families of their own -- and never from the buckets, which are 140 points for 3.x's HTTP
// histogram alone.
type promDuration struct {
	sumFamily   string
	countFamily string
	sumField    string
	countField  string
}

// promSource is everything one line publishes on "/metrics".
type promSource struct {
	counters  []promCounter
	gauges    []promGauge
	durations []promDuration
	// startTimeFamily holds a unix start time to be published as an uptime. 3.x reports
	// one; 2.x reports the uptime itself and uses a gauge instead.
	startTimeFamily string
}

// v2Source is what InfluxDB 2.x publishes, measured against 2.9.1: 61 families, of which
// these are the ones with a counterpart in the stable set. It has no query metric of any
// kind -- "grep -i quer" over its families finds nothing -- and no series cardinality, so
// influxdb_queries, influxdb_query_duration_seconds and influxdb_series are absent here
// and the dashboard's query widgets stay empty for a 2.x server.
//
//nolint:gochecknoglobals
var v2Source = promSource{
	counters: []promCounter{
		{family: "http_api_requests_total", field: fieldRequests, keep: nil, match: nil},
		{
			family: "http_api_requests_total",
			field:  fieldClientErrors,
			keep:   nil,
			match:  map[string]string{"status": "4XX"},
		},
		{
			family: "http_api_requests_total",
			field:  fieldServerErrors,
			keep:   nil,
			match:  map[string]string{"status": "5XX"},
		},
		{family: "storage_writer_timeouts", field: fieldWriteTimeouts, keep: nil, match: nil},
		// The sum of the histogram rather than its count: the sum is points, the count is
		// write batches.
		{family: "storage_writer_ok_points_sum", field: fieldPointsWritten, keep: nil, match: nil},
		{family: "storage_writer_err_points_sum", field: fieldPointsWriteFailed, keep: nil, match: nil},
		{family: "storage_writer_dropped_points_sum", field: fieldPointsWriteDropped, keep: nil, match: nil},
	},
	gauges: []promGauge{
		// Summed over the node id it carries, which a single-node server reports once.
		{family: "influxdb_uptime_seconds", field: fieldUptime, keep: nil},
	},
	durations: []promDuration{
		{
			sumFamily:   "http_api_request_duration_seconds_sum",
			countFamily: "http_api_request_duration_seconds_count",
			sumField:    fieldRequestDurationSum,
			countField:  fieldRequestCount,
		},
	},
	startTimeFamily: "",
}

// v3Source is what InfluxDB 3 publishes, measured against 3.11.4 Core: 144 families and
// 2667 points, almost all of them Tokio, jemalloc and semaphore internals of the engine.
//
// It has no points-written metric and no series cardinality, so the write widgets stay
// empty for a 3.x server the way the query ones do for a 2.x.
//
//nolint:gochecknoglobals
var v3Source = promSource{
	counters: []promCounter{
		{family: "http_requests_total", field: fieldRequests, keep: nil, match: nil},
		{
			family: "http_requests_total",
			field:  fieldClientErrors,
			keep:   nil,
			match:  map[string]string{"status": "client_error"},
		},
		{
			family: "http_requests_total",
			field:  fieldServerErrors,
			keep:   nil,
			match:  map[string]string{"status": "server_error"},
		},
		{
			family: "influxdb_iox_query_log_phase_entered_total",
			field:  fieldQueries,
			keep:   nil,
			match:  map[string]string{"phase": "received"},
		},
		{
			family: "influxdb_iox_query_log_phase_entered_total",
			field:  fieldQueriesFailed,
			keep:   nil,
			match:  map[string]string{"phase": "fail"},
		},
		{family: "query_datafusion_query_execution_ooms_total", field: fieldQueryOOMs, keep: nil, match: nil},
		{family: "influxdb3_parquet_cache_access_total", field: fieldParquetCacheAccess, keep: []string{"status"}, match: nil},
		// Only the result, not the operation: eight operations against three results would
		// be 24 series for one metric, all but a couple of them zero. What a default metric
		// has to answer is whether the object store is moving bytes and whether it fails.
		{family: "object_store_transfer_bytes_total", field: fieldObjectStoreTransfer, keep: []string{"result"}, match: nil},
		{family: "thread_panic_count_total", field: fieldThreadPanics, keep: []string{"type"}, match: nil},
	},
	gauges: []promGauge{
		{family: "influxdb3_parquet_cache_size_bytes", field: fieldParquetCacheSize, keep: nil},
		{family: "influxdb3_parquet_cache_size_number_of_files", field: fieldParquetCacheFiles, keep: nil},
		{family: "datafusion_mem_pool_bytes", field: fieldMemPool, keep: []string{"state"}},
		{family: "jemalloc_memstats_bytes", field: fieldMemory, keep: []string{"stat"}},
	},
	durations: []promDuration{
		{
			sumFamily:   "http_request_duration_seconds_sum",
			countFamily: "http_request_duration_seconds_count",
			sumField:    fieldRequestDurationSum,
			countField:  fieldRequestCount,
		},
		{
			sumFamily:   "influxdb_iox_query_log_end2end_duration_seconds_sum",
			countFamily: "influxdb_iox_query_log_end2end_duration_seconds_count",
			sumField:    fieldQueryDurationSum,
			countField:  fieldQueryCount,
		},
	},
	// Its labels are dropped with it: alongside the version and the commit,
	// process_start_time_seconds carries a "uuid" the server picks anew at every start, so
	// keeping them would begin a new series on each restart.
	startTimeFamily: "process_start_time_seconds",
}

// gatherPrometheus reads a 2.x or 3.x server and returns how many known points it found.
// Zero means the body held none of them, which is a server of another line rather than an
// error: both 1.x and 2.x answer "/metrics" with a 200.
func (i *metricsInput) gatherPrometheus(ctx context.Context, acc telegraf.Accumulator, source promSource) (int, error) {
	families, err := i.scrape(ctx)
	if err != nil {
		return 0, err
	}

	now := i.now()
	found := 0

	for _, counter := range source.counters {
		points := sumByLabels(families[counter.family], counter.keep, counter.match)

		// A counter narrowed to one label value has no point until that value has
		// occurred: 2.x declares its HTTP counter per status seen, so a server that has
		// never answered a 5xx has no status="5XX" line at all. Publishing an explicit
		// zero keeps the core the same set of metrics on every line -- 1.x has a
		// clientError field whatever its value, and 3.x pre-declares every status -- and
		// keeps "no errors" tellable apart from "no metric".
		if len(points) == 0 && counter.match != nil && families[counter.family] != nil {
			points = map[string]aggregated{"": {tags: nil, value: 0}}
		}

		for _, point := range points {
			found++

			acc.AddFields(measurement, map[string]any{counter.field: point.value}, point.tags, now)
		}
	}

	for _, gauge := range source.gauges {
		for _, point := range sumByLabels(families[gauge.family], gauge.keep, nil) {
			found++

			acc.AddFields(measurement, map[string]any{gauge.field: point.value}, point.tags, now)
		}
	}

	// The two halves of an average have to reach the accumulator as one point for
	// AvgDuration to see them together, and every label is summed over so that point is a
	// single one.
	durations := make(map[string]any, 2*len(source.durations)+1)

	for _, duration := range source.durations {
		sum, hasSum := singleValue(families[duration.sumFamily])
		count, hasCount := singleValue(families[duration.countFamily])

		if hasSum && hasCount {
			durations[duration.sumField] = sum
			durations[duration.countField] = count
		}
	}

	// Uptime rather than the start time itself, which would be a constant the reader has
	// to subtract by hand.
	if source.startTimeFamily != "" {
		if startTime, ok := singleValue(families[source.startTimeFamily]); ok && startTime > 0 {
			durations[fieldUptime] = now.Sub(time.Unix(int64(startTime), 0)).Seconds()
		}
	}

	if len(durations) > 0 {
		found++

		acc.AddFields(measurement, durations, nil, now)
	}

	return found, nil
}

func (i *metricsInput) scrape(ctx context.Context) (map[string]*dto.MetricFamily, error) {
	gathered, err := i.target.GatherWithState(ctx, registry.GatherState{T0: time.Now()}) //nolint:exhaustruct
	if err != nil {
		// A 401 is the one worth naming: InfluxDB 3 enables authentication by default and
		// refuses /metrics without a token, which is what the service configuration is for.
		var targetErr scrapper.TargetError

		if errors.As(err, &targetErr) && targetErr.StatusCode == http.StatusUnauthorized {
			return nil, fmt.Errorf("%w: %s needs a token", errUnauthorized, i.target.URL)
		}

		return nil, err
	}

	families := make(map[string]*dto.MetricFamily, len(gathered))

	for _, family := range gathered {
		families[family.GetName()] = family
	}

	return families, nil
}

// aggregated is the sum of every point of a family that shares one set of kept labels.
type aggregated struct {
	tags  map[string]string
	value float64
}

// sumByLabels sums a family's points over the labels that are not kept, keyed by the ones
// that are, keeping only the points matching every label in match. A family the server
// did not report gives nothing rather than a zero, so a metric that exists on one line
// only is absent instead of wrong.
func sumByLabels(family *dto.MetricFamily, keep []string, match map[string]string) map[string]aggregated {
	if family == nil {
		return nil
	}

	result := make(map[string]aggregated, len(family.GetMetric()))

	for _, metric := range family.GetMetric() {
		labels := make(map[string]string, len(metric.GetLabel()))
		for _, label := range metric.GetLabel() {
			labels[label.GetName()] = label.GetValue()
		}

		wanted := true

		for name, value := range match {
			if labels[name] != value {
				wanted = false

				break
			}
		}

		if !wanted {
			continue
		}

		tags := make(map[string]string, len(keep))

		var key strings.Builder

		for _, name := range keep {
			if value, ok := labels[name]; ok {
				tags[name] = value

				key.WriteString(name)
				key.WriteByte(0)
				key.WriteString(value)
				key.WriteByte(0)
			}
		}

		if len(tags) == 0 {
			tags = nil
		}

		point := result[key.String()]
		point.tags = tags
		point.value += metricValue(metric)
		result[key.String()] = point
	}

	return result
}

// singleValue sums every point of a family into one, for the families published without
// any label worth keeping.
func singleValue(family *dto.MetricFamily) (float64, bool) {
	if family == nil || len(family.GetMetric()) == 0 {
		return 0, false
	}

	var total float64

	for _, metric := range family.GetMetric() {
		total += metricValue(metric)
	}

	return total, true
}

// metricValue reads whichever of the value holders the family's type filled in. The
// "_sum" and "_count" halves of a histogram arrive untyped, so that case is not the
// fallback it looks like.
func metricValue(metric *dto.Metric) float64 {
	switch {
	case metric.GetCounter() != nil:
		return metric.GetCounter().GetValue()
	case metric.GetGauge() != nil:
		return metric.GetGauge().GetValue()
	case metric.GetUntyped() != nil:
		return metric.GetUntyped().GetValue()
	default:
		return 0
	}
}
