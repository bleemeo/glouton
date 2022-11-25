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

package nats

import (
	"glouton/inputs"
	"glouton/inputs/internal"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/nats"
)

// New returns a NATS input.
func New(url string) (i telegraf.Input, err error) {
	input, ok := telegraf_inputs.Inputs["nats"]
	if !ok {
		return nil, inputs.ErrDisabledInput
	}

	natsInput, ok := input().(*nats.Nats)
	if !ok {
		return nil, inputs.ErrUnexpectedType
	}

	natsInput.Server = url

	i = &internal.Input{
		Input: natsInput,
		Accumulator: internal.Accumulator{
			DerivatedMetrics: []string{"in_bytes", "out_bytes", "in_msgs", "out_msgs"},
			TransformMetrics: transformMetrics,
		},
		Name: "nats",
	}

	return
}

func transformMetrics(currentContext internal.GatherContext, fields map[string]float64, originalFields map[string]interface{}) map[string]float64 {
	// TODO: Should we ignore some fields?
	// "uptime", "mem", "cpu", "cores", "routes", "remotes", "total_connections", "slow_consumers"
	// "subscriptions", "in_bytes", "out_bytes", "in_msgs", "out_msgs", "connections"

	return fields
}
