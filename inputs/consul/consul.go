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
	measurement := stripNodeName(gatherContext)
	measurement = strings.ReplaceAll(measurement, ".", "_")
	gatherContext.Measurement = strings.ToLower(measurement)

	return gatherContext, false
}

// gaugeSubsystems are the Consul subsystems reporting gauges. They are the only place
// a node name has to be stripped, see stripNodeName. A subsystem missing from this list
// only means its gauges keep the node name, never that another metric gets renamed by
// mistake.
//
//nolint:gochecknoglobals
var gaugeSubsystems = map[string]bool{
	"autopilot":   true,
	"members":     true,
	"memberlist":  true,
	"raft":        true,
	"rpc":         true,
	"runtime":     true,
	"serf":        true,
	"server":      true,
	"session_ttl": true,
	"state":       true,
	"version":     true,
}

// stripNodeName removes the node name Consul inserts in the name of its gauges:
// "consul.<node>.runtime.num_goroutines" is reported as consul_runtime_num_goroutines,
// like it already is when the agent runs with telemetry.disable_hostname. Keeping the
// node name would make the metric name differ on every node, so it could neither be
// listed in the default metrics nor be compared between nodes.
//
// Only gauges carry it -- counters and samples (consul.raft.apply, consul.kvs.apply,
// ...) never do -- and the accumulator tells them apart by their single "value" field,
// the shape the consul_agent plugin gives gauges.
func stripNodeName(gatherContext internal.GatherContext) string {
	if len(gatherContext.OriginalFields) != 1 {
		return gatherContext.Measurement
	}

	if _, isGauge := gatherContext.OriginalFields["value"]; !isGauge {
		return gatherContext.Measurement
	}

	parts := strings.Split(gatherContext.Measurement, ".")
	// A node name is only there when a subsystem follows it, "consul.<node>.runtime.x"
	// against "consul.runtime.x" without one.
	if len(parts) < 4 || !gaugeSubsystems[parts[2]] {
		return gatherContext.Measurement
	}

	return strings.Join(append(parts[:1:1], parts[2:]...), ".")
}

func renameMetrics(currentContext internal.GatherContext, metricName string) (newMeasurement string, newMetricName string) {
	if metricName == "value" {
		return "", currentContext.Measurement
	}

	return currentContext.Measurement, metricName
}
