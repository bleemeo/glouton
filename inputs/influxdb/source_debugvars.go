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
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/bleemeo/glouton/inputs/internal"
	"github.com/bleemeo/glouton/version"

	"github.com/influxdata/telegraf"
)

// maxDebugVars caps the JSON read from a 1.x server. A 1.12 with two databases answers
// 12 KiB; the cap is far above anything a real instance produces and only there so a
// wrong URL cannot be read forever.
const maxDebugVars = 32 << 20

// debugVarsEntry is one of the InfluxDB-formatted objects "/debug/vars" is made of. The
// endpoint is a map of arbitrary keys ("httpd::8086", "database:mydb", "shard:/path:3")
// onto these, so the measurement name has to be read from the object rather than the key.
type debugVarsEntry struct {
	Name   string             `json:"name"`
	Tags   map[string]string  `json:"tags"`
	Values map[string]float64 `json:"values"`
}

// gatherDebugVars reads a 1.x server. Its counters exist nowhere else: 1.x also serves
// "/metrics", but with the Go client library's default registry and not one metric about
// InfluxDB -- 36 families, all go_*, process_* and promhttp_*.
//
// The durations are nanoseconds here, and are divided into seconds before being emitted so
// that everything downstream, transformMetrics included, works in one unit whatever the
// line.
func (i *metricsInput) gatherDebugVars(ctx context.Context, acc telegraf.Accumulator) error {
	entries, err := i.readDebugVars(ctx)
	if err != nil {
		return err
	}

	now := i.now()

	// The HTTP server and the write and query subsystems each come as a single object,
	// while there is one "database" object per database.
	httpd := entries["httpd"]
	write := entries["write"]
	queryExecutor := entries["queryExecutor"]

	core := map[string]any{}

	set := func(field string, from map[string]float64, key string) {
		if value, ok := from[key]; ok {
			core[field] = value
		}
	}

	set(fieldRequests, httpd, "req")
	set(fieldClientErrors, httpd, "clientError")
	set(fieldServerErrors, httpd, "serverError")
	set(fieldAuthFailures, httpd, "authFail")
	set(fieldPointsWritten, httpd, "pointsWrittenOK")
	set(fieldPointsWriteFailed, httpd, "pointsWrittenFail")
	set(fieldPointsWriteDropped, httpd, "pointsWrittenDropped")
	set(fieldWriteTimeouts, write, "writeTimeout")
	// From the query executor rather than the HTTP layer, so this counts the same thing
	// 3.x's query log does. httpd.queryReq counts query *requests*, which wrap the query
	// in HTTP handling and miss any query that did not arrive over HTTP -- on the recorded
	// server, 7.58 ms of HTTP-scoped duration against 5.96 ms of engine time for the same
	// four queries.
	set(fieldQueries, queryExecutor, "queriesExecuted")
	set(fieldQueriesActive, queryExecutor, "queriesActive")

	// The two halves of each average, in seconds. They have to be in the same point as
	// each other for AvgDuration to see them together.
	if value, ok := httpd["reqDurationNs"]; ok {
		core[fieldRequestDurationSum] = value / internal.NsPerSecond

		set(fieldRequestCount, httpd, "req")
	}

	if value, ok := queryExecutor["queryDurationNs"]; ok {
		core[fieldQueryDurationSum] = value / internal.NsPerSecond
		// Finished, not executed: the duration only accumulates for a query that ended.
		set(fieldQueryCount, queryExecutor, "queriesFinished")
	}

	if len(core) > 0 {
		acc.AddFields(measurement, core, nil, now)
	}

	// Series cardinality, the one thing only 1.x reports: neither 2.x nor 3.x has any
	// equivalent. One point per database, so the database has to be a label.
	for _, entry := range i.databases {
		if value, ok := entry.Values["numSeries"]; ok {
			acc.AddFields(
				measurement,
				map[string]any{fieldSeries: value},
				map[string]string{"database": entry.Tags["database"]},
				now,
			)
		}
	}

	return nil
}

// readDebugVars fetches the endpoint and indexes the entries by measurement name, keeping
// the per-database ones aside since there is more than one of them.
func (i *metricsInput) readDebugVars(ctx context.Context) (map[string]map[string]float64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, i.debugVarsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("prepare request to %s: %w", i.debugVarsURL, err)
	}

	req.Header.Set("User-Agent", version.UserAgent())

	// 1.x authenticates with a user and a password rather than a token, and leaves
	// "/debug/vars" open in either case -- measured against a 1.12 with
	// INFLUXDB_HTTP_AUTH_ENABLED. They are sent when configured all the same, for an
	// instance behind something that does ask.
	if i.username != "" || i.password != "" {
		req.SetBasicAuth(i.username, i.password)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("read from %s: %w", i.debugVarsURL, err)
	}

	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxDebugVars))
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: %s returned status %d", errUnexpectedStatus, i.debugVarsURL, resp.StatusCode)
	}

	// The values of an entry are numbers, but the endpoint also holds keys that are not
	// entries at all ("cmdline" is an array, "memstats" a different shape), so each one is
	// decoded on its own and the ones that do not fit are skipped.
	var raw map[string]json.RawMessage

	if err := json.NewDecoder(io.LimitReader(resp.Body, maxDebugVars)).Decode(&raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", i.debugVarsURL, err)
	}

	byName := make(map[string]map[string]float64, len(raw))
	i.databases = nil

	for _, value := range raw {
		var entry debugVarsEntry

		if err := json.Unmarshal(value, &entry); err != nil || entry.Name == "" {
			continue
		}

		if entry.Name == "database" {
			i.databases = append(i.databases, entry)

			continue
		}

		byName[entry.Name] = entry.Values
	}

	return byName, nil
}
