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

package nsq

import (
	"math"
	"testing"
	"time"

	"github.com/bleemeo/glouton/inputs/internal"
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
		DifferentiatedMetrics: []string{
			"message_count",
			"requeue_count",
			"timeout_count",
		},
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

// TestRenamePipelineServer exercises the "nsq_server" measurement, which
// has no differentiated metric (server_count, topic_count are instant
// gauges): the "_count" suffix is only dropped and pluralized.
func TestRenamePipelineServer(t *testing.T) {
	store := &internal.StoreAccumulator{}
	acc := newAccumulator(store)

	acc.PrepareGather()
	acc.AddFields("nsq_server", map[string]any{
		"server_count": int64(1),
		"topic_count":  int64(3),
	}, nil, time.Now())

	got := collectFinalMetrics(store)

	assertMetrics(t, got, map[string]float64{
		"nsq_server_servers": 1,
		"nsq_server_topics":  3,
	})
}

// TestRenamePipelineTopic exercises the "nsq_topic" measurement: depth is
// an instant gauge, message_count is a cumulative counter converted to a
// per-second rate then renamed to "messages".
func TestRenamePipelineTopic(t *testing.T) {
	store := &internal.StoreAccumulator{}
	acc := newAccumulator(store)

	t0 := time.Now()
	t1 := t0.Add(10 * time.Second)

	acc.PrepareGather()
	acc.AddFields("nsq_topic", map[string]any{
		"depth":         int64(10),
		"backend_depth": int64(2),
		"message_count": uint64(1000),
		"channel_count": int64(2),
	}, nil, t0)

	// Discard the first gather: message_count has no rate yet (no history).
	store.Measurement = nil

	acc.PrepareGather()
	acc.AddFields("nsq_topic", map[string]any{
		"depth":         int64(15),
		"backend_depth": int64(1),
		"message_count": uint64(1000 + 30), // rate = 3/s
		"channel_count": int64(2),
	}, nil, t1)

	got := collectFinalMetrics(store)

	assertMetrics(t, got, map[string]float64{
		"nsq_topic_depth":    15,
		"nsq_topic_messages": 3,
	})
}

// TestRenamePipelineChannel exercises the "nsq_channel" measurement, which
// mixes instant gauges (depth, inflight_count, client_count) with
// cumulative counters (message_count, requeue_count, timeout_count)
// converted to per-second rates and pluralized.
func TestRenamePipelineChannel(t *testing.T) {
	store := &internal.StoreAccumulator{}
	acc := newAccumulator(store)

	t0 := time.Now()
	t1 := t0.Add(10 * time.Second)

	acc.PrepareGather()
	acc.AddFields("nsq_channel", map[string]any{
		"depth":          int64(5),
		"backend_depth":  int64(0),
		"inflight_count": int64(2),
		"deferred_count": int64(1),
		"client_count":   int64(3),
		"message_count":  uint64(2000),
		"requeue_count":  uint64(50),
		"timeout_count":  uint64(20),
	}, nil, t0)

	// Discard the first gather: message/requeue/timeout counts have no rate yet (no history).
	store.Measurement = nil

	acc.PrepareGather()
	acc.AddFields("nsq_channel", map[string]any{
		"depth":          int64(8),
		"backend_depth":  int64(0),
		"inflight_count": int64(1),
		"deferred_count": int64(0),
		"client_count":   int64(4),
		"message_count":  uint64(2000 + 20), // rate = 2/s
		"requeue_count":  uint64(50 + 10),   // rate = 1/s
		"timeout_count":  uint64(20 + 5),    // rate = 0.5/s
	}, nil, t1)

	got := collectFinalMetrics(store)

	assertMetrics(t, got, map[string]float64{
		"nsq_channel_depth":     8,
		"nsq_channel_inflights": 1,
		"nsq_channel_clients":   4,
		"nsq_channel_messages":  2,
		"nsq_channel_requeues":  1,
		"nsq_channel_timeouts":  0.5,
	})
}
