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

package chrony

import (
	"math/bits"

	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/inputs/internal"
	"github.com/bleemeo/glouton/types"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/chrony"
)

// New initialise chrony.Input. It queries the local chronyd through its
// control socket (/run/chrony/chronyd.sock) or, failing that, over UDP on
// localhost:323 -- chronyd must be reachable from the host/container running
// Glouton.
func New() (i telegraf.Input, err error) {
	input, ok := telegraf_inputs.Inputs["chrony"]
	if ok {
		chronyInput, ok := input().(*chrony.Chrony)
		if ok {
			// tracking is the system-wide summary (last_offset, rms_offset). activity
			// counts how many configured sources are actually reachable right now.
			// sources gives per-source detail, mirroring what ntpq already reports
			// per-peer for ntpd.
			chronyInput.Metrics = []string{"tracking", "activity", "sources"}

			i = &internal.Input{
				Input: chronyInput,
				Accumulator: internal.Accumulator{
					RenameGlobal:     renameGlobal,
					TransformMetrics: transformMetrics,
				},
				Name: "chrony",
			}
		} else {
			err = inputs.ErrUnexpectedType
		}
	} else {
		err = inputs.ErrDisabledInput
	}

	return i, err
}

// renameGlobal drops the tags describing the current synchronization state
// (leap_status, reference_id and stratum) and the socket we queried. Their value
// changes while chronyd runs -- each change would start a new metric series -- and
// they don't tell which metric this is.
func renameGlobal(gatherContext internal.GatherContext) (internal.GatherContext, bool) {
	delete(gatherContext.Tags, "leap_status")
	delete(gatherContext.Tags, "reference_id")
	delete(gatherContext.Tags, "stratum")
	delete(gatherContext.Tags, "source")

	// chrony_sources reports one point per configured time source, tagged with the
	// resolved "peer" name. Sources coming from the same "pool" directive all
	// resolve to that pool's name, so several distinct IPs would share the same
	// peer and collapse into a single series; the IP address (always unique) is
	// used as the item instead, the same choice made for ntpq's "remote" tag (see
	// inputs/ntp) and for the same reason.
	if ip, ok := gatherContext.OriginalFields["ip"].(string); ok && ip != "" {
		gatherContext.Tags[types.LabelItem] = ip
	}

	return gatherContext, false
}

// transformMetrics converts chrony_sources' reachability from its raw 0..255 value
// (the decimal form of the same 8-bit reach shift register ntpq reports in octal,
// see inputs/ntp) into a 0..100 percentage of the last 8 polls that succeeded --
// the count of bits set, not the register's numeric value -- and renames
// latest_measurement (chrony_sources' per-source offset, already in seconds)
// accordingly.
func transformMetrics(_ internal.GatherContext, fields map[string]float64, _ map[string]any) map[string]float64 {
	if value, ok := fields["reachability"]; ok {
		delete(fields, "reachability")

		fields["reachability_perc"] = float64(bits.OnesCount8(uint8(uint32(value)&0xFF))) / 8 * 100
	}

	if value, ok := fields["latest_measurement"]; ok {
		delete(fields, "latest_measurement")

		fields["latest_measurement_seconds"] = value
	}

	return fields
}
