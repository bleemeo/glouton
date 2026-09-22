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

package activemq

import (
	"math"
	"testing"
	"time"

	"github.com/bleemeo/glouton/inputs/internal"
	"github.com/google/go-cmp/cmp"

	"github.com/influxdata/telegraf/plugins/inputs/activemq"
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
		RenameGlobal: renameGlobal,
		DifferentiatedMetrics: []string{
			"enqueue_count",
			"dequeue_count",
		},
		Accumulator: store,
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

// assertTags checks the tags kept on every emitted measurement.
func assertTags(t *testing.T, store *internal.StoreAccumulator, want map[string]string) {
	t.Helper()

	for _, m := range store.Measurement {
		if diff := cmp.Diff(want, m.Tags); diff != "" {
			t.Errorf("tags of measurement %q (-want +got):\n%s", m.Name, diff)
		}
	}
}

// TestDifferentiation exercises both the "activemq_queues" and
// "activemq_topics" measurements: enqueue_count/dequeue_count are broker
// lifetime totals and get differentiated into per-second rates, while
// size/consumer_count are live gauges and pass through untouched. Both
// measurements share the exact same field names, so this also checks that
// differentiation state doesn't leak across measurements.
func TestDifferentiation(t *testing.T) {
	store := &internal.StoreAccumulator{}
	acc := newAccumulator(store)

	t0 := time.Now()
	t1 := t0.Add(10 * time.Second)

	acc.PrepareGather()
	acc.AddFields("activemq_queues", map[string]any{
		"size":           uint64(5),
		"consumer_count": uint64(2),
		"enqueue_count":  uint64(1000),
		"dequeue_count":  uint64(950),
	}, map[string]string{"name": "orders", "source": "127.0.0.1", "port": "8161"}, t0)
	acc.AddFields("activemq_topics", map[string]any{
		"size":           uint64(1),
		"consumer_count": uint64(4),
		"enqueue_count":  uint64(2000),
		"dequeue_count":  uint64(2000),
	}, map[string]string{"name": "notifications"}, t0)

	// Discard the first gather: enqueue_count/dequeue_count have no rate yet
	// (no history).
	store.Measurement = nil

	acc.PrepareGather()
	acc.AddFields("activemq_queues", map[string]any{
		"size":           uint64(8),
		"consumer_count": uint64(3),
		"enqueue_count":  uint64(1000 + 50), // rate = 5/s
		"dequeue_count":  uint64(950 + 40),  // rate = 4/s
	}, map[string]string{"name": "orders", "source": "127.0.0.1", "port": "8161"}, t1)
	acc.AddFields("activemq_topics", map[string]any{
		"size":           uint64(2),
		"consumer_count": uint64(4),
		"enqueue_count":  uint64(2000 + 100), // rate = 10/s
		"dequeue_count":  uint64(2000 + 90),  // rate = 9/s
	}, map[string]string{"name": "notifications"}, t1)

	got := collectFinalMetrics(store)

	assertMetrics(t, got, map[string]float64{
		"activemq_queues_size":           8,
		"activemq_queues_consumer_count": 3,
		"activemq_queues_enqueue_count":  5,
		"activemq_queues_dequeue_count":  4,
		"activemq_topics_size":           2,
		"activemq_topics_consumer_count": 4,
		"activemq_topics_enqueue_count":  10,
		"activemq_topics_dequeue_count":  9,
	})
}

// TestTagsDropped checks that the tags describing the ActiveMQ console we queried are
// dropped, since they are redundant with the labels already set on service metrics,
// while the queue/topic name is kept.
func TestTagsDropped(t *testing.T) {
	store := &internal.StoreAccumulator{}
	acc := newAccumulator(store)

	acc.PrepareGather()
	acc.AddFields("activemq_queues", map[string]any{
		"size": uint64(5),
	}, map[string]string{"name": "orders", "source": "127.0.0.1", "port": "8161"}, time.Now())

	assertTags(t, store, map[string]string{"name": "orders"})
}

// TestSubscriberLabels checks a subscriber, which has no name tag, keeps as labels what
// identifies it durably, and nothing else. The volatile tags must not be among them:
// connection_id changes on every reconnect and active flips as the client comes and goes,
// so keeping either would start a new series with them. Two subscriptions of one client
// must still differ, or they would collide on the same name and label set and the gather
// would be rejected as a duplicate series.
//
// None of this goes into the item, which is the service instance: for a containerised
// broker that is its container name, and a subscriber identity glued onto it would read
// "test-activemq_billing_orders.events_invoices".
func TestSubscriberLabels(t *testing.T) {
	cases := []struct {
		name string
		tags map[string]string
		want string
	}{
		{
			name: "durable subscriber",
			tags: map[string]string{
				"client_id": "billing", "destination_name": "orders.events",
				"subscription_name": "invoices", "connection_id": "ID:host-42-1", "active": "true",
			},
			want: "invoices",
		},
		{
			name: "same client, same destination, other subscription",
			tags: map[string]string{
				"client_id": "billing", "destination_name": "orders.events",
				"subscription_name": "receipts", "connection_id": "ID:host-42-2", "active": "true",
			},
			want: "receipts",
		},
		{
			name: "non-durable subscriber has no subscription name",
			tags: map[string]string{
				"client_id": "billing", "destination_name": "orders.events",
				"subscription_name": "", "connection_id": "ID:host-42-3", "active": "false",
			},
			want: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &internal.StoreAccumulator{}
			acc := newAccumulator(store)

			acc.PrepareGather()
			acc.AddFields("activemq_subscribers", map[string]any{
				"pending_queue_size": uint64(2),
			}, tc.tags, time.Now())

			assertTags(t, store, map[string]string{
				"client_id": "billing", "destination_name": "orders.events",
				"subscription_name": tc.want,
			})
		})
	}
}

// TestLabelsTrimmed checks the padding ActiveMQ leaves on its names is trimmed off every
// tag kept as a label. The plugin only trims queue names, so a padded topic or subscriber
// would otherwise carry the padding into the series identity and read as a destination of
// its own -- which is what the item-building code used to trim on the way past.
func TestLabelsTrimmed(t *testing.T) {
	store := &internal.StoreAccumulator{}
	acc := newAccumulator(store)

	acc.PrepareGather()
	acc.AddFields("activemq_subscribers", map[string]any{
		"pending_queue_size": uint64(2),
	}, map[string]string{
		"client_id": " billing ", "destination_name": "\torders.events\n",
		"subscription_name": "invoices  ",
	}, time.Now())

	assertTags(t, store, map[string]string{
		"client_id": "billing", "destination_name": "orders.events",
		"subscription_name": "invoices",
	})
}

// TestAdvisoryTopicsDropped checks the topics ActiveMQ creates for its own bookkeeping
// are dropped -- a broker adds a few of them per destination and per connection -- and
// that the trailing space the plugin leaves on topic names is trimmed.
func TestAdvisoryTopicsDropped(t *testing.T) {
	store := &internal.StoreAccumulator{}
	acc := newAccumulator(store)

	acc.PrepareGather()
	acc.AddFields("activemq_topics", map[string]any{
		"size": uint64(0),
	}, map[string]string{"name": "ActiveMQ.Advisory.MasterBroker "}, time.Now())
	acc.AddFields("activemq_topics", map[string]any{
		"size": uint64(3),
	}, map[string]string{"name": "orders.events "}, time.Now())

	if len(store.Measurement) != 1 {
		t.Fatalf("got %d measurements, want only the non-advisory one: %#v", len(store.Measurement), store.Measurement)
	}

	assertTags(t, store, map[string]string{"name": "orders.events"})
}

// TestNewConfiguresThePlugin checks the console URL and the credentials reach the plugin.
// The web console always requires authentication (admin/admin on a default install), so an
// input built without them only ever gets a 401.
func TestNewConfiguresThePlugin(t *testing.T) {
	input, err := New("http://172.23.0.2:8161", "glouton", "s3cret")
	if err != nil {
		t.Fatal(err)
	}

	internalInput, ok := input.(*internal.Input)
	if !ok {
		t.Fatalf("New() returned a %T, want *internal.Input", input)
	}

	activeMQInput, ok := internalInput.Input.(*activemq.ActiveMQ)
	if !ok {
		t.Fatalf("wrapped input is a %T, want *activemq.ActiveMQ", internalInput.Input)
	}

	if activeMQInput.URL != "http://172.23.0.2:8161" {
		t.Errorf("URL = %q, want %q", activeMQInput.URL, "http://172.23.0.2:8161")
	}

	if activeMQInput.Username != "glouton" || activeMQInput.Password != "s3cret" {
		t.Errorf("credentials = %q/%q, want glouton/s3cret", activeMQInput.Username, activeMQInput.Password)
	}
}
