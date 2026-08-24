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

			i = &internal.Input{
				Input: NTPInput,
				Accumulator: internal.Accumulator{
					RenameGlobal: renameGlobal,
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
