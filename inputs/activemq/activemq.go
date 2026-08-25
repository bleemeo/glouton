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
	"github.com/bleemeo/glouton/types"

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
// volatile chrony and ntpq tags are, and deliberately not used to build the item: the
// item they would produce would change on every reconnect.
//
// selector is dropped too -- a filter expression describes how the subscription was
// declared, not what the metric is about.
//
//nolint:gochecknoglobals
var subscriberVolatileTags = []string{"connection_id", "active", "selector"}

// subscriberItemTags are the tags identifying a subscriber, in the order they are joined
// into its item. destination_name tells apart the subscriptions of one client, and
// subscription_name the durable subscriptions of one client on one destination. Without
// them, every subscription of one client would share a name and an item, and the whole
// gather would be rejected as a duplicate series.
//
// It isn't airtight, and the missing piece is deliberate: subscription_name is empty on a
// non-durable subscription, so several non-durable subscriptions of one client to one
// destination -- a multi-threaded consumer, typically -- still share an item and still
// collide. What would separate them is connection_id, which changes on every reconnect and
// would churn a new series each time; between a collision on an unusual topology and churn
// on a common one, the collision is the lesser evil. Durable subscriptions, the ones
// activemq_subscribers_pending_queue_size is really about, are unambiguous.
//
//nolint:gochecknoglobals
var subscriberItemTags = []string{"client_id", "destination_name", "subscription_name"}

// renameGlobal drops the tags describing the ActiveMQ console we queried: they are
// redundant with the labels already set on service metrics. The queue/topic/subscriber
// tags are kept since they identify the item the metric is about.
//
// It also drops the advisory topics, whose metrics only describe the broker's own
// bookkeeping, and would add a few series per destination and per connection.
func renameGlobal(gatherContext internal.GatherContext) (internal.GatherContext, bool) {
	delete(gatherContext.Tags, "source")
	delete(gatherContext.Tags, "port")

	// The plugin trims the name of queues but not the one of topics.
	name := strings.TrimSpace(gatherContext.Tags["name"])
	if name != "" {
		gatherContext.Tags["name"] = name
	}

	if strings.HasPrefix(name, advisoryTopicPrefix) {
		return gatherContext, true
	}

	// The item is what tells the destinations apart: without it every queue and topic
	// would end up on the same metric.
	if name == "" {
		// Subscribers have no name tag: they are identified by the client holding them,
		// the destination and, for a durable one, the subscription name.
		name = subscriberItem(gatherContext.Tags)
	}

	for _, tag := range subscriberVolatileTags {
		delete(gatherContext.Tags, tag)
	}

	gatherContext.Tags[types.LabelItem] = name

	return gatherContext, false
}

// subscriberItem builds the item of a subscriber by joining the tags identifying it,
// skipping those the broker left empty.
func subscriberItem(tags map[string]string) string {
	return internal.JoinNonEmptyTags(tags, subscriberItemTags)
}
