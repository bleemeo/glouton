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
	"math"
	"strings"
	"testing"
	"time"

	"github.com/bleemeo/glouton/inputs/internal"
	"github.com/bleemeo/glouton/types"

	"github.com/google/go-cmp/cmp"
)

// collectFinalMetrics replicates the measurement/field -> final metric name
// convention applied downstream (inputs.Accumulator.addMetrics): the metric
// name is the field name alone when the measurement was renamed to "", or
// "<measurement>_<field>" otherwise.
func collectFinalMetrics(store *internal.StoreAccumulator) map[string]float64 {
	got := make(map[string]float64)

	for _, m := range store.Measurement {
		for field, value := range m.Fields {
			name := field
			if m.Name != "" {
				name = m.Name + "_" + field
			}

			switch v := value.(type) {
			case float64:
				got[name] = v
			case uint64:
				got[name] = float64(v)
			}
		}
	}

	return got
}

func newAccumulator(store *internal.StoreAccumulator) internal.Accumulator {
	return internal.Accumulator{
		RenameGlobal:  renameGlobal,
		RenameMetrics: renameMetrics,
		Accumulator:   store,
	}
}

func assertMetrics(t *testing.T, got map[string]float64, want map[string]float64) {
	t.Helper()

	for name, value := range want {
		gotValue, ok := got[name]
		if !ok {
			t.Errorf("metric %q not emitted, got metrics: %v", name, got)

			continue
		}

		if math.Abs(gotValue-value) > 0.0001 {
			t.Errorf("metric %q == %v, want %v", name, gotValue, value)
		}
	}
}

// TestGaugeRename checks that a gauge -- reported by the consul_agent plugin as a
// single "value" field on a measurement named after the Consul metric -- ends up as
// one metric named after that measurement, without a "_value" suffix.
func TestGaugeRename(t *testing.T) {
	store := &internal.StoreAccumulator{}
	acc := newAccumulator(store)

	acc.PrepareGather()
	acc.AddGauge("consul.autopilot.healthy", map[string]any{
		"value": 1.0,
	}, nil, time.Now())
	acc.AddGauge("consul.runtime.num_goroutines", map[string]any{
		"value": 87.0,
	}, nil, time.Now())

	got := collectFinalMetrics(store)

	assertMetrics(t, got, map[string]float64{
		"consul_autopilot_healthy":      1,
		"consul_runtime_num_goroutines": 87,
	})

	for name := range got {
		if name == "consul_autopilot_healthy_value" || name == "consul_runtime_num_goroutines_value" {
			t.Errorf("gauge %q should have been renamed to drop the \"value\" field name", name)
		}
	}
}

// TestCounterAndSampleFields checks the counters and samples, which the plugin reports
// with one field per aggregation (rate, mean, ...). Those are already aggregated by
// Consul over its own interval, so they must not be differentiated again -- only the
// dotted measurement name is normalized, the fields keep their name.
func TestCounterAndSampleFields(t *testing.T) {
	store := &internal.StoreAccumulator{}
	acc := newAccumulator(store)

	acc.PrepareGather()
	acc.AddCounter("consul.rpc.request", map[string]any{
		"count": 100.0,
		"rate":  10.0,
		"sum":   100.0,
	}, nil, time.Now())
	acc.AddCounter("consul.raft.commitTime", map[string]any{
		"count": 42.0,
		"mean":  1.5,
		"max":   3.0,
	}, nil, time.Now())
	acc.AddCounter("consul.kvs.apply", map[string]any{
		"count": 12.0,
		"mean":  2.5,
	}, nil, time.Now())

	got := collectFinalMetrics(store)

	assertMetrics(t, got, map[string]float64{
		"consul_rpc_request_rate":     10,
		"consul_raft_committime_mean": 1.5,
		"consul_kvs_apply_mean":       2.5,
	})
}

// TestGaugeNodeNameStripped checks the node name Consul inserts in the name of its
// gauges is removed: "consul.<node>.autopilot.healthy" must be reported as
// consul_autopilot_healthy, not consul_<node>_autopilot_healthy, which would differ on
// every node and never match the default metrics.
func TestGaugeNodeNameStripped(t *testing.T) {
	store := &internal.StoreAccumulator{}
	acc := newAccumulator(store)

	acc.PrepareGather()
	// A gauge, as reported by an agent with the default telemetry settings.
	acc.AddGauge("consul.cbcf2176063c.autopilot.healthy", map[string]any{"value": 1.0}, nil, time.Now())
	acc.AddGauge("consul.cbcf2176063c.runtime.num_goroutines", map[string]any{"value": 194.0}, nil, time.Now())
	// The same gauge on an agent running with telemetry.disable_hostname.
	acc.AddGauge("consul.state.services", map[string]any{"value": 3.0}, nil, time.Now())
	// A counter: those never carry the node name, and their second segment must be kept.
	acc.AddCounter("consul.raft.apply", map[string]any{"rate": 10.0}, nil, time.Now())

	got := collectFinalMetrics(store)

	assertMetrics(t, got, map[string]float64{
		"consul_autopilot_healthy":      1,
		"consul_runtime_num_goroutines": 194,
		"consul_state_services":         3,
		"consul_raft_apply_rate":        10,
	})

	for name := range got {
		if strings.Contains(name, "cbcf2176063c") {
			t.Errorf("metric %q still carries the node name", name)
		}
	}
}

// TestLabelledMetricsGetTheirOwnItem checks the series of a metric Consul labels are told
// apart by their item. Consul reports its memberlist and serf queues once per network, and
// its state metrics once per datacenter and kind of config entry; a service metric keeps no
// label but the item, so without it those series would share a name and an empty label set
// and be rejected as duplicates.
func TestLabelledMetricsGetTheirOwnItem(t *testing.T) {
	store := &internal.StoreAccumulator{}
	acc := newAccumulator(store)

	acc.PrepareGather()
	acc.AddCounter("consul.memberlist.gossip", map[string]any{"mean": 0.016}, map[string]string{"network": "lan"}, time.Now())
	acc.AddCounter("consul.memberlist.gossip", map[string]any{"mean": 0.021}, map[string]string{"network": "wan"}, time.Now())
	// Several labels: joined in a stable order, whatever order the map is walked in.
	acc.AddGauge("consul.node1.state.config", map[string]any{"value": 3.0},
		map[string]string{"datacenter": "dc1", "kind": "service-defaults"}, time.Now())
	// A label Consul leaves empty adds nothing to the item.
	acc.AddGauge("consul.node1.version", map[string]any{"value": 1.0},
		map[string]string{"version": "1.20.6", "pre_release": ""}, time.Now())
	// And an unlabelled metric keeps no item, so the service instance stays its item.
	acc.AddGauge("consul.node1.autopilot.healthy", map[string]any{"value": 1.0}, nil, time.Now())

	gotItems := make(map[string][]string)

	for _, m := range store.Measurement {
		for field := range m.Fields {
			// Same naming convention as collectFinalMetrics: a gauge has its name moved
			// into the field, the measurement being emptied by renameMetrics.
			name := field
			if m.Name != "" {
				name = m.Name + "_" + field
			}

			gotItems[name] = append(gotItems[name], m.Tags[types.LabelItem])
		}
	}

	wantItems := map[string][]string{
		"consul_memberlist_gossip_mean": {"lan", "wan"},
		"consul_state_config":           {"dc1_service-defaults"},
		"consul_version":                {"1.20.6"},
		"consul_autopilot_healthy":      {""},
	}

	if diff := cmp.Diff(wantItems, gotItems); diff != "" {
		t.Errorf("items (-want +got):\n%s", diff)
	}
}

// TestGaugeNodeNameWithDotsStripped checks a host name holding dots is stripped too.
// What Consul inserts is the hostname (not its node_name) and it isn't sanitized, so
// a host with an FQDN hostname reports "consul.web01.prod.example.com.runtime.x": treating
// it as a single segment would leave it in the metric name, and the default metrics would
// never match on such a host.
func TestGaugeNodeNameWithDotsStripped(t *testing.T) {
	store := &internal.StoreAccumulator{}
	acc := newAccumulator(store)

	acc.PrepareGather()
	acc.AddGauge("consul.web01.prod.example.com.autopilot.healthy", map[string]any{"value": 1.0}, nil, time.Now())
	acc.AddGauge("consul.web01.prod.example.com.runtime.num_goroutines", map[string]any{"value": 194.0}, nil, time.Now())
	// A node name whose first segment is itself a subsystem name: the search starts after
	// it, so "raft" is not mistaken for the subsystem.
	acc.AddGauge("consul.raft.example.com.state.services", map[string]any{"value": 3.0}, nil, time.Now())
	// A node named after a subsystem, the tightest case: only one of the two segments goes.
	acc.AddGauge("consul.runtime.runtime.alloc_bytes", map[string]any{"value": 42.0}, nil, time.Now())

	got := collectFinalMetrics(store)

	assertMetrics(t, got, map[string]float64{
		"consul_autopilot_healthy":      1,
		"consul_runtime_num_goroutines": 194,
		"consul_state_services":         3,
		"consul_runtime_alloc_bytes":    42,
	})

	for name := range got {
		if strings.Contains(name, "example") || strings.Contains(name, "web01") {
			t.Errorf("metric %q still carries the node name", name)
		}
	}
}
