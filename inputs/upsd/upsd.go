// Copyright 2015-2022 Bleemeo
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

package upsd

import (
	"glouton/inputs"
	"glouton/inputs/internal"
	"glouton/types"
	"strings"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/upsd"
)

// New returns a UPSD input.
func New(server string, port int, username, password string) (telegraf.Input, *inputs.GathererOptions, error) {
	input, ok := telegraf_inputs.Inputs["upsd"]
	if !ok {
		return nil, nil, inputs.ErrDisabledInput
	}

	upsdInput, ok := input().(*upsd.Upsd)
	if !ok {
		return nil, nil, inputs.ErrUnexpectedType
	}

	upsdInput.Server = server
	upsdInput.Port = port
	upsdInput.Username = username
	upsdInput.Password = password

	internalInput := &internal.Input{
		Input: upsdInput,
		Accumulator: internal.Accumulator{
			RenameGlobal: renameGlobal,
		},
		Name: "UPSD",
	}

	options := &inputs.GathererOptions{
		Rules: []types.SimpleRule{
			{
				TargetName:  "upsd_time_left_seconds",
				PromQLQuery: "upsd_time_left_ns/1e9",
			},
			{
				TargetName:  "upsd_time_on_battery_seconds",
				PromQLQuery: "upsd_time_on_battery_ns/1e9",
			},
		},
	}

	return internalInput, options, nil
}

func renameGlobal(gatherContext internal.GatherContext) (result internal.GatherContext, drop bool) {
	for name := range gatherContext.Tags {
		// Status labels are added (status_OL, status_OB, ...) depending on the UPS state.
		// To prevent a new metric being created every time the state changes, remove these tags.
		if strings.HasPrefix(name, "status") {
			delete(gatherContext.Tags, name)
		}
	}

	delete(gatherContext.Tags, "serial")

	return gatherContext, false
}
