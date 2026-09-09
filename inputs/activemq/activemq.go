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
	"strings"

	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/inputs/internal"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/activemq"
)

// New initialise activeMQ.Input.
func New(url string, username string, password string) (i telegraf.Input, err error) {
	input, ok := telegraf_inputs.Inputs["activemq"]
	if ok {
		activeMQInput, ok := input().(*activemq.ActiveMQ)
		if ok {
			activeMQInput.URL = url
			activeMQInput.Username = username
			activeMQInput.Password = password

			i = &internal.Input{
				Input: activeMQInput,
				Accumulator: internal.Accumulator{
					RenameGlobal: renameGlobal,
					DifferentiatedMetrics: []string{
						"enqueue_count",
						"dequeue_count",
					},
				},
				Name: "activeMQ",
			}
		} else {
			err = inputs.ErrUnexpectedType
		}
	} else {
		err = inputs.ErrDisabledInput
	}

	return i, err
}

// advisoryTopicPrefix is the prefix of the topics ActiveMQ creates on its own to
// publish broker events (one per destination, per connection, ...).
const advisoryTopicPrefix = "ActiveMQ.Advisory."

// subscriberVolatileTags are the tags of activemq_subscribers whose value changes as
// clients come and go: connection_id is per TCP connection, so a client that reconnects
// gets a new one, and active flips between "true" and "false". They are dropped like the
// volatile chrony and ntpq tags are, and deliberately not kept as labels: as part of the
// series identity they would start a new series on every reconnect.
//
// selector is dropped too -- a filter expression describes how the subscription was
// declared, not what the metric is about.
//
//nolint:gochecknoglobals
var subscriberVolatileTags = []string{"connection_id", "active", "selector"}

// renameGlobal drops the tags describing the ActiveMQ console we queried: they are
// redundant with the labels already set on service metrics.
//
// What identifies a destination is kept as a label of its own rather than written into
// the item: "name" for a queue or a topic, and client_id/destination_name/
// subscription_name for a subscriber -- destination_name tells apart the subscriptions of
// one client, and subscription_name the durable subscriptions of one client on one
// destination. The item is the service instance, which for a containerised broker is its
// container name, so putting a destination there too would glue the two together into
// something like "test-activemq_glouton.test".
//
// Keeping them needs CompatibilityNameItem to be off for this service, since the
// compatibility naming keeps only the item and would drop every one of these; see the
// ActiveMQ case of Discovery.createInput.
//
// One collision is left, and deliberately: subscription_name is empty on a non-durable
// subscription, so several non-durable subscriptions of one client to one destination -- a
// multi-threaded consumer, typically -- still share a label set. What would separate them
// is connection_id, which changes on every reconnect and would churn a new series each
// time; between a collision on an unusual topology and churn on a common one, the
// collision is the lesser evil. Durable subscriptions, the ones
// activemq_subscribers_pending_queue_size is really about, are unambiguous.
//
// It also drops the advisory topics, whose metrics only describe the broker's own
// bookkeeping, and would add a few series per destination and per connection.
func renameGlobal(gatherContext internal.GatherContext) (internal.GatherContext, bool) {
	delete(gatherContext.Tags, "source")
	delete(gatherContext.Tags, "port")

	// The plugin trims the name of queues but not the one of topics, and none of the
	// subscriber tags. Trimmed here because they are all series labels: padding the broker
	// happens to emit would otherwise become part of the series identity, and the same
	// destination would read as two.
	for _, tag := range []string{"name", "client_id", "destination_name", "subscription_name"} {
		if value, ok := gatherContext.Tags[tag]; ok {
			gatherContext.Tags[tag] = strings.TrimSpace(value)
		}
	}

	if strings.HasPrefix(gatherContext.Tags["name"], advisoryTopicPrefix) {
		return gatherContext, true
	}

	for _, tag := range subscriberVolatileTags {
		delete(gatherContext.Tags, tag)
	}

	return gatherContext, false
}
