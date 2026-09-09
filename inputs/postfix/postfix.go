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

// The "queue" tag telegraf's plugin attaches is left alone, and kept as a label of its
// own rather than written into the item: the five queues share their metric names, so
// something has to tell them apart, and the item is the service instance -- for a
// containerised Postfix the container name, which modify.AddInstance sets. Writing the
// queue there too would glue the two together into "test-postfix_deferred".
//
// Keeping it needs CompatibilityNameItem to be off for this service, since the
// compatibility naming keeps only the item and would drop the queue; see the Postfix case
// of Discovery.createInput.

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
