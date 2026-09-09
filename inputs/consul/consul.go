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
					RenameGlobal:     renameGlobal,
					RenameMetrics:    renameMetrics,
					TransformMetrics: transformMetrics,
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

// renameGlobal normalises the measurement name and leaves Consul's own labels alone.
//
// Those labels are what tells apart the series of one metric: some Consul metrics come as
// one series per label set -- one per network (lan and wan) for the memberlist and serf
// queues, one per datacenter and kind of config entry for the state ones. They used to be
// joined into the item, because the compatibility naming keeps only the item and would
// otherwise drop them, leaving every series of one metric with the same name and the same
// empty label set: "collected metric ... was collected before with the same name and label
// values", which is what still happens to rabbitmq_consumers. Turning that naming off for
// this service (see the Consul case of Discovery.createInput) keeps them as labels
// instead, and leaves the item to the service instance rather than gluing a network name
// onto a container name.
//
// Taking their mean is deliberately not the answer either: the aggregate that makes sense
// differs per field -- summing is right for count and sum, taking the max for max, and
// nothing is right for stddev -- and it would report a number Consul never measured.
//
// The labels Consul uses are dimensions of the thing measured, not of the event: network
// (lan, wan), datacenter, kind of config entry, version, HTTP method and path, and peer_id
// on the leader's raft replication metrics. The last one is the id of a server, so it does
// change when a server is replaced, but like the others it is bounded by the size of the
// cluster and can't grow one series per event.
func renameGlobal(gatherContext internal.GatherContext) (internal.GatherContext, bool) {
	measurement := stripNodeName(gatherContext)
	measurement = strings.ReplaceAll(measurement, ".", "_")
	gatherContext.Measurement = strings.ToLower(measurement)

	return gatherContext, false
}

// gaugeSubsystems are the Consul subsystems reporting gauges. They are the only place
// a node name has to be stripped, see stripNodeName, and they are what tells the node
// name apart from the rest of the measurement. A subsystem missing from this list only
// means its gauges keep the node name, never that another metric gets renamed by
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

// stripNodeName removes the host name Consul inserts in the name of its gauges:
// "consul.<node>.runtime.num_goroutines" is reported as consul_runtime_num_goroutines,
// like it already is when the agent runs with telemetry.disable_hostname. The metrics are
// deliberately those of the service as a whole, not of one node: keeping the node name
// would make the metric name differ on every node, so it could neither be listed in the
// default metrics nor be compared between nodes, and a large cluster would flood the user
// with one series per node.
//
// That name is the hostname of the agent reporting the metrics, inserted by its go-metrics
// sink (which does it for gauges only, and only while telemetry.disable_hostname is off).
// It is the hostname and not Consul's node_name, and it is not sanitized, so it holds
// however many dots the hostname does ("consul.web01.prod.example.com.runtime.x"). What is
// looked for is therefore the subsystem, and everything between "consul" and it is dropped
// whatever its shape. The search goes from the end of the name backwards, so it finds the
// subsystem itself rather than a hostname label that happens to share a subsystem's name:
// the real subsystem is always the match closest to the field name, while a coincidental
// one in the hostname can only be further from it.
//
// It is always the reporting agent's own hostname -- never another node's, whatever the
// size of the cluster -- so this merges no series: one dump holds one such name. What is
// genuinely per-node in Consul (raft replication towards each follower) is reported by the
// leader as counters and samples keyed by peer, which this never touches.
//
// Only gauges carry that name -- counters and samples (consul.raft.apply,
// consul.kvs.apply, ...) never do -- and the accumulator tells them apart by their single
// "value" field, the shape the consul_agent plugin gives gauges.
func stripNodeName(gatherContext internal.GatherContext) string {
	if len(gatherContext.OriginalFields) != 1 {
		return gatherContext.Measurement
	}

	if _, isGauge := gatherContext.OriginalFields["value"]; !isGauge {
		return gatherContext.Measurement
	}

	parts := strings.Split(gatherContext.Measurement, ".")

	for i := len(parts) - 1; i >= 1; i-- {
		if gaugeSubsystems[parts[i]] {
			return strings.Join(append(parts[:1:1], parts[i:]...), ".")
		}
	}

	// Either the gauge has no node name at all ("consul.runtime.x", the
	// telemetry.disable_hostname shape) or its subsystem isn't a known one.
	return gatherContext.Measurement
}

func renameMetrics(currentContext internal.GatherContext, metricName string) (newMeasurement string, newMetricName string) {
	if metricName == "value" {
		return "", currentContext.Measurement
	}

	return currentContext.Measurement, metricName
}

// timerMeasurementsInMilliseconds are the samples/timers Consul's go-metrics sink reports
// in milliseconds. transformMetrics converts their mean into seconds, matching every other
// duration metric in this codebase, and renames the field so the unit is visible in the
// name.
//
// Only the timers Glouton publishes are listed, so promoting another one to the default
// metrics means adding it here too.
//
//nolint:gochecknoglobals
var timerMeasurementsInMilliseconds = map[string]bool{
	"consul_raft_committime":         true,
	"consul_raft_leader_lastcontact": true,
}

func transformMetrics(currentContext internal.GatherContext, fields map[string]float64, _ map[string]any) map[string]float64 {
	if timerMeasurementsInMilliseconds[currentContext.Measurement] {
		if mean, ok := fields["mean"]; ok {
			delete(fields, "mean")

			fields["mean_seconds"] = mean / 1000
		}
	}

	return fields
}
