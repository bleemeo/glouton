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

package ntp

import (
	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/inputs/internal"
	"github.com/bleemeo/glouton/types"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/ntpq"
)

// New initialise ntp.Input. It queries the local ntpd through the ntpq
// command-line tool (which must be present on the host/container running Glouton).
// The tool is run by Telegraf itself and not through Glouton's command runner, so
// an ntpd running in a container isn't reachable when Glouton runs on the host.
//
// The plugin runs ntpq with no timeout, and ntpq has no timeout flag either, so the only
// thing bounding a gather is ntpq itself: it gives up on a peer that never answers after
// about 10s (measured against a blackholed address), which is the worst case for a gather.
// Only name resolution could last longer, which is what "-n" below is for.
func New() (i telegraf.Input, err error) {
	input, ok := telegraf_inputs.Inputs["ntpq"]
	if ok {
		NTPInput, ok := input().(*ntpq.NTPQ)
		if ok {
			// Query ntpd with "ntpq -n": resolving the name of every peer can make a
			// gather last minutes on a host using a pool -- the plugin runs ntpq without
			// any timeout -- and an IP is a more stable label value than the peer name,
			// which ntpq truncates anyway. "-p" is added by the plugin itself.
			NTPInput.Options = "-n"

			// ntpq prints "reach" in octal, and the plugin's default ("octal") mode just
			// stores that raw text as a number: a fully reachable peer reports 377, a
			// number that reads as badly out of range to anyone who doesn't know it's
			// octal for "every one of the last 8 polls succeeded". "ratio" instead reports
			// the fraction of the last 8 polls that succeeded, a plain 0..1 that
			// transformMetrics below scales to a percentage.
			NTPInput.ReachFormat = "ratio"

			i = &internal.Input{
				Input: NTPInput,
				Accumulator: internal.Accumulator{
					RenameGlobal:     renameGlobal,
					TransformMetrics: transformMetrics,
				},
				Name: "NTP",
			}
		} else {
			err = inputs.ErrUnexpectedType
		}
	} else {
		err = inputs.ErrDisabledInput
	}

	return i, err
}

// renameGlobal keeps only the "remote" tag, which identifies the peer the metrics are
// about. The other tags either describe the current selection state (state_prefix,
// refid, stratum), whose value changes while ntpd runs -- each change would start a
// new metric series --, or the peer type which doesn't tell which metric this is.
func renameGlobal(gatherContext internal.GatherContext) (internal.GatherContext, bool) {
	delete(gatherContext.Tags, "state_prefix")
	delete(gatherContext.Tags, "refid")
	delete(gatherContext.Tags, "stratum")
	delete(gatherContext.Tags, "type")
	delete(gatherContext.Tags, "source")

	// The item is what tells the peers apart: without it they would all end up on the
	// same metric.
	if remote := gatherContext.Tags["remote"]; remote != "" {
		gatherContext.Tags[types.LabelItem] = remote
	}

	return gatherContext, false
}

// transformMetrics converts delay/jitter/offset from ntpq's own millisecond scale into
// seconds, matching every other duration metric in this codebase, and renames each field
// so the unit is visible in the name. It also converts reach from the plugin's 0..1 ratio
// (see ReachFormat above) into a 0..100 percentage, matching every other percentage metric
// in this codebase (e.g. cpu_used, mem_used_percent). poll/when (already durations telegraf
// itself normalizes to seconds) are left untouched.
func transformMetrics(_ internal.GatherContext, fields map[string]float64, _ map[string]any) map[string]float64 {
	for _, name := range []string{"delay", "jitter", "offset"} {
		if value, ok := fields[name]; ok {
			delete(fields, name)

			fields[name+"_seconds"] = value / 1000
		}
	}

	if value, ok := fields["reach"]; ok {
		delete(fields, "reach")

		fields["reach_perc"] = value * 100
	}

	return fields
}
