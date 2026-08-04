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
	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/inputs/internal"

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
			i = &internal.Input{
				Input:       chronyInput,
				Accumulator: internal.Accumulator{},
				Name:        "chrony",
			}
		} else {
			err = inputs.ErrUnexpectedType
		}
	} else {
		err = inputs.ErrDisabledInput
	}

	return i, err
}
