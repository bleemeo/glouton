// Copyright 2015-2019 Bleemeo
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
	"glouton/inputs/internal"
	"reflect"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/statsd"
)

func reflectSetPercentile(input *statsd.Statsd) {
	inputValue := reflect.Indirect(reflect.ValueOf(input))
	percentilesValue := inputValue.FieldByName("Percentiles")

	slice := reflect.MakeSlice(percentilesValue.Type(), 1, 1)
	internalNumber := slice.Index(0)
	value := internalNumber.FieldByName("Value")
	value.Set(reflect.ValueOf(90.0))
	percentilesValue.Set(slice)
}

// New initialise statsd.Input
func New(bindAddress string) (i telegraf.Input, err error) {
	var input, ok = telegraf_inputs.Inputs["statsd"]
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

			func() {
				defer func() {
					if r := recover(); r != nil {
						err = fmt.Errorf("error during creation of StatsD input: %v", r)
					}
				}()
				reflectSetPercentile(statsdInput)
			}()

			i = &internal.Input{
				Input: statsdInput,
				Accumulator: internal.Accumulator{
					RenameGlobal:          renameGlobal,
					ShouldDerivateMetrics: shouldDerivateMetrics,
					TransformMetrics:      transformMetrics,
				},
			}
		} else {
			err = errors.New("input StatsD is not the expected type")
		}
	} else {
		err = errors.New("input StatsD is not enabled in Telegraf")
	}

	return i, nil
}

func renameGlobal(originalContext internal.GatherContext) (newContext internal.GatherContext, drop bool) {
	newContext.Measurement = "statsd"
	newContext.Tags = nil

	return
}

func shouldDerivateMetrics(originalContext internal.GatherContext, currentContext internal.GatherContext, metricName string) bool {
	return originalContext.Tags["metric_type"] == "counter"
}

func transformMetrics(originalContext internal.GatherContext, currentContext internal.GatherContext, fields map[string]float64, originalFields map[string]interface{}) map[string]float64 {
	newFields := make(map[string]float64)

	if originalContext.Tags["metric_type"] == "timing" {
		for key, value := range fields {
			if key == "count" {
				value /= 10
			}

			newFields[fmt.Sprintf("%s_%s", originalContext.Measurement, key)] = value
		}
	} else if _, ok := fields["value"]; ok {
		newFields[originalContext.Measurement] = fields["value"]
	}

	return newFields
}
