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

//go:build linux

package psi

import (
	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/inputs/internal"
	"github.com/bleemeo/glouton/prometheus/registry"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/kernel"
)

func New() (telegraf.Input, registry.RegistrationOption, error) {
	input, ok := telegraf_inputs.Inputs["kernel"]
	if !ok {
		return nil, registry.RegistrationOption{}, inputs.ErrDisabledInput
	}

	kernelInput, ok := input().(*kernel.Kernel)
	if !ok {
		return nil, registry.RegistrationOption{}, inputs.ErrUnexpectedType
	}

	kernelInput.ConfigCollect = []string{"psi"}

	internalInput := &internal.Input{
		Input: kernelInput,
		Accumulator: internal.Accumulator{
			RenameGlobal:          renameGlobal,
			DifferentiatedMetrics: []string{"total"},
			TransformMetrics:      transformMetrics,
		},
		Name: "PSI",
	}

	return internalInput, registry.RegistrationOption{}, nil
}

func renameGlobal(gatherContext internal.GatherContext) (result internal.GatherContext, drop bool) {
	if gatherContext.Measurement != "pressure" {
		return gatherContext, true
	}

	gatherContext.Measurement = "psi"

	return gatherContext, false
}

func transformMetrics(gatherContext internal.GatherContext, fields map[string]float64, _ map[string]any) map[string]float64 {
	result := make(map[string]float64, len(fields)/4)

	for field, value := range fields {
		if field == "total" {
			result[gatherContext.Tags["resource"]+"_"+gatherContext.Tags["type"]] = value
		}
	}

	delete(gatherContext.Tags, "resource")
	delete(gatherContext.Tags, "type")

	return result
}
