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

//go:build !windows

package postfix

import (
	"math"
	"testing"
	"time"

	"github.com/bleemeo/glouton/inputs/internal"
	"github.com/bleemeo/glouton/types"
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

// TestRenameQueueFields checks the per-queue fields of the "postfix_queue"
// measurement. They are instant counts (a gauge each), so none is differentiated. The
// "size" field must be renamed: it holds the number of bytes of the queue, while
// postfix_queue_size is already the number of mails waiting in the whole queue,
// gathered from "postqueue -p" by the agent itself.
func TestRenameQueueFields(t *testing.T) {
	store := &internal.StoreAccumulator{}
	acc := newAccumulator(store)

	acc.PrepareGather()
	acc.AddFields("postfix_queue", map[string]any{
		"length": int64(42),
		"size":   int64(1024000),
		"age":    int64(3600),
	}, map[string]string{"queue": "deferred"}, time.Now())

	got := collectFinalMetrics(store)

	assertMetrics(t, got, map[string]float64{
		"postfix_queue_length":      42,
		"postfix_queue_bytes":       1024000,
		"postfix_queue_age_seconds": 3600,
	})

	if _, ok := got["postfix_queue_size"]; ok {
		t.Errorf("postfix_queue_size must not be emitted by this input, it would collide with"+
			" the queue size gathered from postqueue, got value %v", got["postfix_queue_size"])
	}

	// The queue this is about is the only tag, and it must be kept.
	if queue := store.Measurement[0].Tags["queue"]; queue != "deferred" {
		t.Errorf("tags[queue] == %q, want %q", queue, "deferred")
	}
}

// TestEmptyQueue checks an empty queue: the plugin reports no "age" field at all in
// that case, and the two others must still be emitted (as zeros, not dropped).
func TestEmptyQueue(t *testing.T) {
	store := &internal.StoreAccumulator{}
	acc := newAccumulator(store)

	acc.PrepareGather()
	acc.AddFields("postfix_queue", map[string]any{
		"length": int64(0),
		"size":   int64(0),
	}, map[string]string{"queue": "active"}, time.Now())

	got := collectFinalMetrics(store)

	assertMetrics(t, got, map[string]float64{
		"postfix_queue_length": 0,
		"postfix_queue_bytes":  0,
	})
}

// TestQueueIsItsOwnLabel checks the queue stays a label of its own and is not written
// into the item.
//
// The five queues share their metric names, so something has to tell them apart. Using
// the item for it looks like the obvious answer and is wrong: the item is the service
// instance, and modify.AddInstance glues the two together, so a containerised Postfix
// ended up reporting item="test-postfix_deferred" instead of the container name with a
// queue label beside it.
func TestQueueIsItsOwnLabel(t *testing.T) {
	store := &internal.StoreAccumulator{}
	acc := newAccumulator(store)

	acc.PrepareGather()

	for _, queue := range []string{"active", "deferred"} {
		acc.AddFields("postfix_queue", map[string]any{
			"length": int64(1),
		}, map[string]string{"queue": queue}, time.Now())
	}

	queues := make(map[string]bool)

	for _, m := range store.Measurement {
		queues[m.Tags["queue"]] = true

		if item, ok := m.Tags[types.LabelItem]; ok {
			t.Errorf("item should be left to the service instance, got %q", item)
		}
	}

	if !queues["active"] || !queues["deferred"] {
		t.Errorf("queue labels = %v, want one per queue", queues)
	}
}
