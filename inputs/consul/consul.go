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

package consul

import (
	"strings"

	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/inputs/internal"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/consul_agent"
)

// New initialise consul_agent.Input.
//
// This uses the consul_agent plugin (Consul's own /v1/agent/metrics endpoint)
// rather than the consul plugin, which only exposes health-check results
// already covered by Glouton's own check system.
func New(url string, token string) (i telegraf.Input, err error) {
	input, ok := telegraf_inputs.Inputs["consul_agent"]
	if ok {
		consulInput, ok := input().(*consul_agent.ConsulAgent)
		if ok {
			consulInput.URL = url
			consulInput.Token = token

			i = &internal.Input{
				Input: consulInput,
				Accumulator: internal.Accumulator{
					RenameGlobal:  renameGlobal,
					RenameMetrics: renameMetrics,
				},
				Name: "consul",
			}
		} else {
			err = inputs.ErrUnexpectedType
		}
	} else {
		err = inputs.ErrDisabledInput
	}

	return i, err
}

func renameGlobal(gatherContext internal.GatherContext) (internal.GatherContext, bool) {
	measurement := strings.ReplaceAll(gatherContext.Measurement, ".", "_")
	gatherContext.Measurement = strings.ToLower(measurement)

	return gatherContext, false
}

func renameMetrics(currentContext internal.GatherContext, metricName string) (newMeasurement string, newMetricName string) {
	if metricName == "value" {
		return "", currentContext.Measurement
	}

	return currentContext.Measurement, metricName
}
