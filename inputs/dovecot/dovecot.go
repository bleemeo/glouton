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

package dovecot

import (
	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/inputs/internal"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/dovecot"
)

// New initialise dovecot.Input.
//
// server is either a "host:port" TCP address or a unix socket path of the
// old-stats plugin listener.
func New(server string) (i telegraf.Input, err error) {
	input, ok := telegraf_inputs.Inputs["dovecot"]
	if ok {
		dovecotInput, ok := input().(*dovecot.Dovecot)
		if ok {
			dovecotInput.Type = "global"
			dovecotInput.Servers = []string{server}

			i = &internal.Input{
				Input: dovecotInput,
				Accumulator: internal.Accumulator{
					RenameGlobal: renameGlobal,
					DifferentiatedMetrics: []string{
						"num_logins",
						"num_cmds",
						"mail_cache_hits",
						"disk_input",
						"disk_output",
						"auth_successes",
						"auth_failures",
					},
				},
				Name: "dovecot",
			}
		} else {
			err = inputs.ErrUnexpectedType
		}
	} else {
		err = inputs.ErrDisabledInput
	}

	return i, err
}

// renameGlobal drops the tag naming the old_stats listener we queried, redundant with
// the labels already set on service metrics, and the "type" tag which is always
// "global" since that's the only query type we ask for.
//
// It also drops the two timestamps Dovecot reports, which aren't metrics: they are
// time.Time values, so keeping them would only add a conversion error to every gather.
func renameGlobal(gatherContext internal.GatherContext) (internal.GatherContext, bool) {
	delete(gatherContext.Tags, "server")
	delete(gatherContext.Tags, "type")

	delete(gatherContext.OriginalFields, "last_update")
	delete(gatherContext.OriginalFields, "reset_timestamp")

	return gatherContext, false
}
