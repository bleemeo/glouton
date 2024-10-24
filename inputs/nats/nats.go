// Copyright 2015-2024 Bleemeo
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

//go:build !freebsd

package nats

import (
	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/inputs/internal"
	"github.com/bleemeo/glouton/prometheus/registry"
	"github.com/bleemeo/glouton/types"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/nats"
)

// New returns a NATS input.
func New(url string) (telegraf.Input, registry.RegistrationOption, error) {
	input, ok := telegraf_inputs.Inputs["nats"]
	if !ok {
		return nil, registry.RegistrationOption{}, inputs.ErrDisabledInput
	}

	natsInput, ok := input().(*nats.Nats)
	if !ok {
		return nil, registry.RegistrationOption{}, inputs.ErrUnexpectedType
	}

	natsInput.Server = url

	internalInput := &internal.Input{
		Input: natsInput,
		Accumulator: internal.Accumulator{
			RenameGlobal: func(gatherContext internal.GatherContext) (result internal.GatherContext, drop bool) {
				// Remove the IP address of the server. Glouton will add item and/or container to identify the source
				delete(gatherContext.Tags, "server")

				return gatherContext, false
			},
			DerivatedMetrics: []string{"in_bytes", "out_bytes", "in_msgs", "out_msgs"},
		},
		Name: "nats",
	}

	options := registry.RegistrationOption{
		Rules: []types.SimpleRule{
			{
				TargetName:  "nats_uptime_seconds",
				PromQLQuery: "nats_uptime/1e9",
			},
		},
	}

	return internalInput, options, nil
}
