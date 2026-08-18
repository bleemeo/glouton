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
		// Subscribers are named by the client that holds them.
		name = gatherContext.Tags["client_id"]
	}

	gatherContext.Tags[types.LabelItem] = name

	return gatherContext, false
}
