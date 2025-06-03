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

package swap

import (
	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/inputs/internal"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/swap"
)

// New initialise swap.Input.
func New() (i telegraf.Input, err error) {
	input, ok := telegraf_inputs.Inputs["swap"]
	if ok {
		swapInput, _ := input().(*swap.Swap)
		i = &internal.Input{
			Input: swapInput,
			Accumulator: internal.Accumulator{
				DifferentiatedMetrics: []string{"in", "out"},
				TransformMetrics:      transformMetrics,
			},
			Name: "swap",
		}
	} else {
		err = inputs.ErrDisabledInput
	}

	return
}

func transformMetrics(currentContext internal.GatherContext, fields map[string]float64, originalFields map[string]any) map[string]float64 {
	_ = currentContext
	_ = originalFields

	if value, ok := fields["used_percent"]; ok {
		delete(fields, "used_percent")
		fields["used_perc"] = value
	}

	return fields
}
