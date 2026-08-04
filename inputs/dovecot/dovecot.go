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
					DifferentiatedMetrics: []string{
						"num_logins",
						"num_cmds",
						"mail_cache_hits",
						"disk_input",
						"disk_output",
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
