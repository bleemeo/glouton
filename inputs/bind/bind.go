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

package bind

import (
	"strings"
	"time"

	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/inputs/internal"

	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/config"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/bind"
)

// statsTimeout bounds a request to the statistics-channel. The plugin registers itself
// without any timeout (the 4s of its sample.conf is only applied to a Telegraf TOML
// config, which Glouton doesn't use), so its HTTP client would be built with
// Timeout: 0 -- no limit on how long a response may take. A firewalled or stalled
// statistics-channel would then block the gather forever, and since gathers of one
// registration are serialized, every later gather with it.
const statsTimeout = 4 * time.Second

// New initialise bind.Input.
func New(url string) (i telegraf.Input, err error) {
	input, ok := telegraf_inputs.Inputs["bind"]
	if ok {
		bindInput, ok := input().(*bind.Bind)
		if ok {
			bindInput.Urls = []string{url}
			bindInput.Timeout = config.Duration(statsTimeout)

			i = &internal.Input{
				Input: bindInput,
				Accumulator: internal.Accumulator{
					RenameGlobal:               renameGlobal,
					RenameMetrics:              renameMetrics,
					ShouldDifferentiateMetrics: shouldDifferentiateMetrics,
				},
				Name: "bind",
			}
		} else {
			err = inputs.ErrUnexpectedType
		}
	} else {
		err = inputs.ErrDisabledInput
	}

	return i, err
}

// renameGlobal drops the tags describing the statistics-channel we queried: they are
// redundant with the labels already set on service metrics. The "type" tag (opcode,
// rcode, qtype, ...) and the per-zone tags are kept since they identify the counter.
func renameGlobal(gatherContext internal.GatherContext) (internal.GatherContext, bool) {
	delete(gatherContext.Tags, "url")
	delete(gatherContext.Tags, "source")
	delete(gatherContext.Tags, "port")

	return gatherContext, false
}

var counterFieldRenames = map[string]string{ //nolint:gochecknoglobals
	"QUERY":       "query",
	"NXDOMAIN":    "nxdomain",
	"SERVFAIL":    "servfail",
	"QrySuccess":  "qry_success",
	"QryNXDOMAIN": "qry_nxdomain",
}

// resolverCounterGroups are the counter groups counting what BIND's own resolver did: the
// queries it sent upstream (resqtype) and the answers it got back (resstats).
//
// Their counter names overlap the ones of the queries BIND itself answers -- NXDOMAIN,
// SERVFAIL, REFUSED and FormErr are in both rcode and resstats, and every record type is in
// both qtype and resqtype -- while the plugin puts every group in the same bind_counter
// measurement with only a "type" tag to tell them apart, and no tag but the item survives on
// a service metric. Two counters would then be the same metric, and the value of
// bind_counter_nxdomain or bind_counter_servfail (both default metrics) would be whichever
// of the two was gathered last. So the resolver groups are given a prefix of their own,
// leaving the plain name to the authoritative counters the default metrics are about -- the
// same fix as inputs/clickhouse renaming "query" to "active_query" where two of its
// measurements share a field name.
//
// This is a trap being closed, not a bug being fixed: these groups sit in the per-view part
// of the statistics, which the plugin only reads with GatherViews, and that is left off. Two
// things are needed before turning it on: this prefix, and the view name in the item --
// a server with several views repeats every counter group once per view, and "view" is
// dropped just like "type" is.
//
// The prefix is applied per group rather than per colliding name: BIND adds counters between
// versions, and a new name in one of these groups must not start colliding silently.
//
//nolint:gochecknoglobals
var resolverCounterGroups = map[string]bool{
	"resqtype": true,
	"resstats": true,
}

func renameMetrics(currentContext internal.GatherContext, metricName string) (newMeasurement string, newMetricName string) {
	if renamed, ok := counterFieldRenames[metricName]; ok {
		metricName = renamed
	} else {
		metricName = strings.ToLower(metricName)
	}

	if resolverCounterGroups[currentContext.Tags["type"]] {
		metricName = "res_" + metricName
	}

	return currentContext.Measurement, metricName
}

func shouldDifferentiateMetrics(currentContext internal.GatherContext, _ string) bool {
	return currentContext.Measurement == "bind_counter"
}
