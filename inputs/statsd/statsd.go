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

package statsd

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/inputs/internal"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/statsd"
)

var errCreation = errors.New("error during creation of StatsD input")

func reflectSetPercentile(input *statsd.Statsd) {
	inputValue := reflect.Indirect(reflect.ValueOf(input))
	percentilesValue := inputValue.FieldByName("Percentiles")

	slice := reflect.MakeSlice(percentilesValue.Type(), 1, 1)
	internalNumber := slice.Index(0)
	value := internalNumber.FieldByName("Value")
	value.Set(reflect.ValueOf(90.0))
	percentilesValue.Set(slice)
}

// New initialise statsd.Input.
func New(bindAddress string) (i telegraf.Input, err error) {
	input, ok := telegraf_inputs.Inputs["statsd"]
	if ok {
		statsdInput, ok := input().(*statsd.Statsd)
		if ok {
			statsdInput.ServiceAddress = bindAddress
			statsdInput.DeleteGauges = false
			statsdInput.DeleteCounters = false
			statsdInput.DeleteTimings = true
			statsdInput.MetricSeparator = "_"
			statsdInput.AllowedPendingMessages = 10000
			statsdInput.PercentileLimit = 1000
			statsdInput.Log = internal.NewLogger()

			func() {
				defer func() {
					if r := recover(); r != nil {
						err = fmt.Errorf("%w: %v", errCreation, r)
					}
				}()
				reflectSetPercentile(statsdInput)
			}()

			i = &internal.Input{
				Input: statsdInput,
				Accumulator: internal.Accumulator{
					RenameGlobal:               renameGlobal,
					ShouldDifferentiateMetrics: shouldDerivativeMetrics,
					TransformMetrics:           transformMetrics,
				},
				Name: "statsd",
			}
		} else {
			err = inputs.ErrUnexpectedType
		}
	} else {
		err = inputs.ErrDisabledInput
	}

	return i, nil
}

func renameGlobal(gatherContext internal.GatherContext) (internal.GatherContext, bool) {
	gatherContext.Measurement = "statsd"
	gatherContext.OriginalTags = gatherContext.Tags
	gatherContext.Tags = nil

	return gatherContext, false
}

func shouldDerivativeMetrics(currentContext internal.GatherContext, metricName string) bool {
	_ = metricName

	return currentContext.OriginalTags["metric_type"] == "counter"
}

func transformMetrics(currentContext internal.GatherContext, fields map[string]float64, originalFields map[string]any) map[string]float64 {
	_ = originalFields

	newFields := make(map[string]float64)

	if currentContext.OriginalTags["metric_type"] == "timing" {
		for key, value := range fields {
			if key == "count" {
				value /= 10
			}

			newFields[fmt.Sprintf("%s_%s", currentContext.OriginalMeasurement, key)] = value
		}
	} else if _, ok := fields["value"]; ok {
		newFields[currentContext.OriginalMeasurement] = fields["value"]
	}

	return newFields
}
