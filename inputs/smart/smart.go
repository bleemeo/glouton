// Copyright 2015-2023 Bleemeo
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

package smart

import (
	"glouton/config"
	"glouton/inputs"
	"glouton/inputs/internal"
	"time"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/smart"
)

// New returns a SMART input.
func New(config config.Smart) (telegraf.Input, *inputs.GathererOptions, error) {
	input, ok := telegraf_inputs.Inputs["smart"]
	if !ok {
		return nil, nil, inputs.ErrDisabledInput
	}

	smartInput, ok := input().(*smart.Smart)
	if !ok {
		return nil, nil, inputs.ErrUnexpectedType
	}

	smartInput.UseSudo = true
	smartInput.Devices = config.Devices
	smartInput.Excludes = config.Excludes
	smartInput.PathSmartctl = config.PathSmartctl

	internalInput := &internal.Input{
		Input: smartInput,
		Accumulator: internal.Accumulator{
			RenameGlobal: renameGlobal,
		},
		Name: "SMART",
	}

	options := &inputs.GathererOptions{
		// This input uses an external command with sudo so we gather less often.
		MinInterval: 60 * time.Second,
	}

	return internalInput, options, nil
}

func renameGlobal(gatherContext internal.GatherContext) (result internal.GatherContext, drop bool) {
	// Remove labels that are not useful.
	delete(gatherContext.Tags, "capacity")
	delete(gatherContext.Tags, "enabled")
	delete(gatherContext.Tags, "power")

	return gatherContext, false
}
