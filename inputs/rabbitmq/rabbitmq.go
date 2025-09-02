// Copyright 2015-2025 Bleemeo
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
	"fmt"
	"maps"
	"slices"
	_ "unsafe"

	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/inputs/internal"
	"github.com/bleemeo/glouton/logger"

	"github.com/influxdata/telegraf"
	telegraf_config "github.com/influxdata/telegraf/config"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/rabbitmq"
)

const secretCount = 2

//go:linkname gatherFunctions github.com/influxdata/telegraf/plugins/inputs/rabbitmq.gatherFunctions
var gatherFunctions map[string]func(r *rabbitmq.RabbitMQ, acc telegraf.Accumulator) //nolint: gochecknoglobals

// New initialise rabbitmq.Input.
func New(url string, username string, password string) (telegraf.Input, error) {
	// As the RabbitMQ input starts multiple gathering goroutines in parallel,
	// more than 2 secrets may be decoded at the same time.
	// If we can't lock enough memory to handle the worst-case scenario,
	// we split the input into multiple sub-inputs so that each only starts one goroutine,
	// and then start the gathering on each input sequentially.
	if inputs.MaxParallelSecrets() >= secretCount*len(gatherFunctions) {
		i, err := newForMetrics(url, username, password, slices.Collect(maps.Keys(gatherFunctions)))
		if err != nil {
			return nil, err
		}

		return i, nil
	}

	logger.V(1).Printf("Splitting RabbitMQ input because of insufficient lockable memory")

	inpts := make(map[string]internal.InputWithSecrets, len(gatherFunctions))

	for name := range gatherFunctions {
		i, err := newForMetrics(url, username, password, []string{name})
		if err != nil {
			return nil, fmt.Errorf("%q: %w", name, err)
		}

		inpts[name] = i
	}

	return &splitInput{inputs: inpts}, nil
}

func newForMetrics(url string, username string, password string, metricInclude []string) (internal.InputWithSecrets, error) {
	var err error

	input, ok := telegraf_inputs.Inputs["rabbitmq"]
	if ok {
		rabbitmqInput, ok := input().(*rabbitmq.RabbitMQ)
		if ok {
			rabbitmqInput.URL = url
			rabbitmqInput.Username = telegraf_config.NewSecret([]byte(username))
			rabbitmqInput.Password = telegraf_config.NewSecret([]byte(password))
			rabbitmqInput.MetricInclude = metricInclude

			i := &internal.Input{
				Input: rabbitmqInput,
				Accumulator: internal.Accumulator{
					RenameGlobal:          renameGlobal,
					DifferentiatedMetrics: []string{"messages_published", "messages_delivered", "messages_acked"},
					TransformMetrics:      transformMetrics,
				},
				Name: "rabbitmq",
			}

			return internal.InputWithSecrets{Input: i, Count: secretCount * len(metricInclude)}, nil
		}

		err = inputs.ErrUnexpectedType
	} else {
		err = inputs.ErrDisabledInput
	}

	return internal.InputWithSecrets{}, err
}

func renameGlobal(gatherContext internal.GatherContext) (internal.GatherContext, bool) {
	gatherContext.Measurement = "rabbitmq"

	return gatherContext, false
}

func transformMetrics(currentContext internal.GatherContext, fields map[string]float64, originalFields map[string]any) map[string]float64 {
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

type splitInput struct {
	inputs map[string]internal.InputWithSecrets
}

func (si splitInput) SampleConfig() string {
	return slices.Collect(maps.Values(si.inputs))[0].SampleConfig()
}

func (si splitInput) Init() error {
	for name, input := range si.inputs {
		err := input.Init()
		if err != nil {
			return fmt.Errorf("%q: %w", name, err)
		}
	}

	return nil
}

func (si splitInput) Gather(acc telegraf.Accumulator) error {
	for name, input := range si.inputs {
		err := input.Gather(acc)
		if err != nil {
			return fmt.Errorf("%q: %w", name, err)
		}
	}

	return nil
}

func (si splitInput) SecretCount() int {
	return secretCount
}
