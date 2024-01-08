// Copyright 2015-2023 Bleemeo
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

package rabbitmq

import (
	"glouton/inputs"
	"glouton/inputs/internal"

	"github.com/influxdata/telegraf"
	telegraf_config "github.com/influxdata/telegraf/config"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/rabbitmq"
)

// New initialise rabbitmq.Input.
func New(url string, username string, password string) (i telegraf.Input, err error) {
	input, ok := telegraf_inputs.Inputs["rabbitmq"]
	if ok {
		rabbitmqInput, ok := input().(*rabbitmq.RabbitMQ)
		if ok {
			rabbitmqInput.URL = url
			rabbitmqInput.Username = telegraf_config.NewSecret([]byte(username))
			rabbitmqInput.Password = telegraf_config.NewSecret([]byte(password))
			i = &internal.Input{
				Input: rabbitmqInput,
				Accumulator: internal.Accumulator{
					RenameGlobal:     renameGlobal,
					DerivatedMetrics: []string{"messages_published", "messages_delivered", "messages_acked"},
					TransformMetrics: transformMetrics,
				},
				Name: "rabbitmq",
			}
		} else {
			err = inputs.ErrUnexpectedType
		}
	} else {
		err = inputs.ErrDisabledInput
	}

	return
}

func renameGlobal(gatherContext internal.GatherContext) (internal.GatherContext, bool) {
	gatherContext.Measurement = "rabbitmq"

	return gatherContext, false
}

func transformMetrics(currentContext internal.GatherContext, fields map[string]float64, originalFields map[string]interface{}) map[string]float64 {
	_ = currentContext
	_ = originalFields

	newFields := make(map[string]float64)

	for metricName, value := range fields {
		switch metricName {
		case "messages":
			newFields["messages_count"] = value
		case "messages_unacked":
			newFields["messages_unacked_count"] = value
		case "consumers", "connections", "queues", "messages_published", "messages_delivered", "messages_acked":
			newFields[metricName] = value
		}
	}

	return newFields
}
