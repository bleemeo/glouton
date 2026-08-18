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
	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/inputs/internal"
	"github.com/bleemeo/glouton/types"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/postfix"
)

// New initialise postfix.Input. It reports the length, size and age of each Postfix
// queue by walking queueDirectory, which must be readable by the user running Glouton.
func New(queueDirectory string) (i telegraf.Input, err error) {
	input, ok := telegraf_inputs.Inputs["postfix"]
	if ok {
		postfixInput, ok := input().(*postfix.Postfix)
		if ok {
			postfixInput.QueueDirectory = queueDirectory

			i = &internal.Input{
				Input: postfixInput,
				Accumulator: internal.Accumulator{
					RenameGlobal:  renameGlobal,
					RenameMetrics: renameMetrics,
				},
				Name: "postfix",
			}
		} else {
			err = inputs.ErrUnexpectedType
		}
	} else {
		err = inputs.ErrDisabledInput
	}

	return i, err
}

// renameGlobal sets the item to the queue the metrics are about: without it the five
// queues would all end up on the same metric.
func renameGlobal(gatherContext internal.GatherContext) (internal.GatherContext, bool) {
	if queue := gatherContext.Tags["queue"]; queue != "" {
		gatherContext.Tags[types.LabelItem] = queue
	}

	return gatherContext, false
}

var fieldRenames = map[string]string{ //nolint:gochecknoglobals
	// "size" is the number of bytes held in the queue. It must not be named
	// postfix_queue_size, which is the number of mails waiting in the whole queue,
	// gathered on its own from "postqueue -p" (see agent.postfixQueueSize).
	"size": "bytes",
	// "age" is the age in seconds of the oldest mail of the queue.
	"age": "age_seconds",
}

func renameMetrics(currentContext internal.GatherContext, metricName string) (newMeasurement string, newMetricName string) {
	if renamed, ok := fieldRenames[metricName]; ok {
		return currentContext.Measurement, renamed
	}

	return currentContext.Measurement, metricName
}
