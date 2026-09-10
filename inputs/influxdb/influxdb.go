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

// Package influxdb reads the metrics of an InfluxDB server, whichever of the three lines
// it belongs to.
//
// The lines have nothing in common but the concepts. 1.x publishes InfluxDB-formatted JSON
// on "/debug/vars" and is the only one to report series cardinality; 2.x and 3.x publish
// Prometheus text on "/metrics" under names that overlap neither each other nor 1.x, and
// 2.x reports no query metric at all. So the same handful of published names is built from
// three different sources, chosen from the version the server reports (see version.go),
// and a name a line does not have is simply absent for it.

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/inputs/internal"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/prometheus/registry"
	"github.com/bleemeo/glouton/prometheus/scrapper"

	"github.com/influxdata/telegraf"
)

const (
	// measurement is the prefix of every metric name this input produces: the accumulator
	// joins it to the field name, so the "requests" field becomes influxdb_requests.
	measurement = "influxdb"

	gatherTimeout = 10 * time.Second
)

// The published fields. The ones ending in Sum or Count are the halves of an average and
// never reach the API: transformMetrics replaces them.
const (
	fieldRequests           = "requests"
	fieldClientErrors       = "client_errors"
	fieldServerErrors       = "server_errors"
	fieldRequestDurationSum = "request_duration_sum"
	fieldRequestCount       = "request_count"
	fieldRequestDuration    = "request_duration_seconds"

	fieldPointsWritten      = "points_written"
	fieldPointsWriteFailed  = "points_write_failed"
	fieldPointsWriteDropped = "points_write_dropped"
	fieldWriteTimeouts      = "write_timeouts"

	fieldQueries          = "queries"
	fieldQueriesFailed    = "queries_failed"
	fieldQueriesActive    = "queries_active"
	fieldQueryDurationSum = "query_duration_sum"
	fieldQueryCount       = "query_count"
	fieldQueryDuration    = "query_duration_seconds"
	fieldQueryOOMs        = "query_ooms"

	fieldUptime       = "uptime"
	fieldSeries       = "series"
	fieldAuthFailures = "auth_failures"

	fieldParquetCacheSize    = "parquet_cache_size_bytes"
	fieldParquetCacheFiles   = "parquet_cache_files"
	fieldParquetCacheAccess  = "parquet_cache_access"
	fieldObjectStoreTransfer = "object_store_transfer_bytes"
	fieldMemPool             = "mem_pool_bytes"
	fieldMemory              = "memory_bytes"
	fieldThreadPanics        = "thread_panics"
)

var (
	// errUnauthorized is returned when the server refuses the scrape for lack of a token.
	errUnauthorized = errors.New("unauthorized")
	// errUnexpectedStatus is returned when an endpoint answers something that identifies
	// nothing.
	errUnexpectedStatus = errors.New("unexpected HTTP status")
)

// differentiatedFields are the cumulative counters, published as a rate. It is the union
// over the three lines: a field a line does not report never reaches the accumulator, so
// listing it costs nothing.
//
//nolint:gochecknoglobals
var differentiatedFields = []string{
	fieldRequests,
	fieldClientErrors,
	fieldServerErrors,
	fieldAuthFailures,
	fieldPointsWritten,
	fieldPointsWriteFailed,
	fieldPointsWriteDropped,
	fieldWriteTimeouts,
	fieldQueries,
	fieldQueriesFailed,
	fieldQueryOOMs,
	fieldParquetCacheAccess,
	fieldObjectStoreTransfer,
	fieldThreadPanics,
	// The halves of each average, differentiated so the average is the one of the period
	// rather than since the server started. AvgDuration consumes them.
	fieldRequestDurationSum,
	fieldRequestCount,
	fieldQueryDurationSum,
	fieldQueryCount,
}

// New returns an input reading an InfluxDB server, whichever line it is.
//
// baseURL is the server's root: which endpoint holds the metrics depends on the version,
// and that is not known until the server is asked.
//
// The token is only used by 3.x, which authenticates every route and answers 401 on
// "/metrics", "/health" and "/ping" alike without one. The user and password are only used
// by 1.x, which leaves "/debug/vars" open in practice but may sit behind something that
// does not.
func New(baseURL, username, password, token string) (telegraf.Input, registry.RegistrationOption, error) {
	if baseURL == "" {
		return nil, registry.RegistrationOption{}, inputs.ErrDisabledInput //nolint:exhaustruct
	}

	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, registry.RegistrationOption{}, fmt.Errorf("%w: %s", inputs.ErrDisabledInput, err) //nolint:exhaustruct
	}

	withPath := func(path string) string {
		u := *parsed
		u.Path = path

		return u.String()
	}

	metricsURL, err := url.Parse(withPath("/metrics"))
	if err != nil {
		return nil, registry.RegistrationOption{}, fmt.Errorf("%w: %s", inputs.ErrDisabledInput, err) //nolint:exhaustruct
	}

	// The shared scrapper rather than a request and a parser of our own: it is what reads
	// every other Prometheus endpoint Glouton knows about, and it splits a histogram into
	// the "_sum" and "_count" families the averages are built from.
	target := scrapper.New(metricsURL, nil)
	target.BearerToken = token

	input := &metricsInput{ //nolint:exhaustruct
		target:       target,
		pingURL:      withPath("/ping"),
		debugVarsURL: withPath("/debug/vars"),
		username:     username,
		password:     password,
		token:        token,
		now:          time.Now,
	}

	internalInput := &internal.Input{
		Input: input,
		Accumulator: internal.Accumulator{ //nolint:exhaustruct
			TransformMetrics:      transformMetrics,
			DifferentiatedMetrics: differentiatedFields,
		},
		Name: "influxdb",
	}

	// Registered with its own options rather than the default compatibility naming, which
	// keeps only the item: what tells these series apart is a label -- the state of the
	// memory pool, the database a cardinality belongs to -- and the compatibility naming
	// would drop every one of them and collapse the series onto a single name. The item is
	// left to the service instance.
	options := registry.RegistrationOption{ //nolint:exhaustruct
		CompatibilityNameItem: false,
	}

	return internalInput, options, nil
}

// transformMetrics turns each cumulative duration into the average duration of one
// operation. Every source divides its own units into seconds first -- 1.x counts
// nanoseconds where the others count seconds -- so there is nothing left to scale here.
//
// The operation count is dropped afterwards: AvgDuration only removes the duration it
// consumed, and the count says nothing the published counters don't already.
func transformMetrics(_ internal.GatherContext, fields map[string]float64, _ map[string]any) map[string]float64 {
	internal.AvgDuration(fields, fieldRequestDurationSum, fieldRequestCount, fieldRequestDuration, 1)
	internal.AvgDuration(fields, fieldQueryDurationSum, fieldQueryCount, fieldQueryDuration, 1)

	delete(fields, fieldRequestCount)
	delete(fields, fieldQueryCount)

	return fields
}

// metricsInput reads whichever endpoint the server's line publishes.
type metricsInput struct {
	target       *scrapper.Target
	pingURL      string
	debugVarsURL string
	username     string
	password     string
	token        string

	// now is time.Now, replaced in tests so an uptime can be asserted.
	now func() time.Time

	l sync.Mutex
	// line is remembered once identified: finding out costs a request, and a server does
	// not change major version between two gathers. A failure leaves it unknown so the
	// next gather asks again.
	line line
	// databases holds the per-database entries of the last 1.x read.
	databases []debugVarsEntry
	// nothingKnownLogged keeps the "none of that line's metrics" message to once.
	nothingKnownLogged bool
}

func (i *metricsInput) SampleConfig() string {
	return "Read the metrics of an InfluxDB server"
}

func (i *metricsInput) Gather(acc telegraf.Accumulator) error {
	ctx, cancel := context.WithTimeout(context.Background(), gatherTimeout)
	defer cancel()

	serverLine, err := i.currentLine(ctx)
	if err != nil {
		return err
	}

	switch serverLine {
	case lineV1:
		return i.gatherDebugVars(ctx, acc)
	case lineV2:
		return i.gatherAndCheck(ctx, acc, v2Source, serverLine)
	case lineV3:
		return i.gatherAndCheck(ctx, acc, v3Source, serverLine)
	case lineUnknown:
		return nil
	default:
		return nil
	}
}

// gatherAndCheck reads a Prometheus source and explains a body that held nothing known,
// which is the one failure that would otherwise be silent: every line answers its own
// endpoint with a 200, so reading the wrong one produces no error and no metrics.
func (i *metricsInput) gatherAndCheck(
	ctx context.Context,
	acc telegraf.Accumulator,
	source promSource,
	serverLine line,
) error {
	found, err := i.gatherPrometheus(ctx, acc, source)
	if err != nil {
		return err
	}

	if found == 0 {
		i.warnNothingKnown(serverLine)
	}

	return nil
}

// currentLine returns the server's line, asking it the first time and after a failure.
func (i *metricsInput) currentLine(ctx context.Context) (line, error) {
	i.l.Lock()
	known := i.line
	i.l.Unlock()

	if known != lineUnknown {
		return known, nil
	}

	detected, reported, err := detectLine(ctx, i.pingURL, i.token)
	if err != nil {
		return lineUnknown, err
	}

	if detected == lineUnknown {
		logger.V(1).Printf(
			"Not gathering InfluxDB at %s: it reports the version %q, which is none of the lines Glouton reads",
			i.target.URL, reported,
		)

		return lineUnknown, nil
	}

	i.l.Lock()
	i.line = detected
	i.l.Unlock()

	logger.V(1).Printf("InfluxDB at %s is a %s server (version %q)", i.target.URL, detected, reported)

	return detected, nil
}

// warnNothingKnown says once that the server answered but held none of the families its
// line is supposed to publish, which means the version was identified wrongly or the
// server renamed them.
func (i *metricsInput) warnNothingKnown(serverLine line) {
	i.l.Lock()
	defer i.l.Unlock()

	if i.nothingKnownLogged {
		return
	}

	i.nothingKnownLogged = true

	logger.Printf(
		"InfluxDB at %s was read as a %s server but reports none of that line's metrics; "+
			"no InfluxDB metric will be published",
		i.target.URL, serverLine,
	)
}
