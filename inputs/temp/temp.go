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

package temp

import (
	"errors"

	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/inputs/internal"
	"github.com/bleemeo/glouton/prometheus/registry"
	"github.com/bleemeo/glouton/version"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/temp"
)

var ErrUnsupportedSystem = errors.New("system is not supported")

// New returns a temperature input.
func New() (telegraf.Input, registry.RegistrationOption, error) {
	if version.IsMacOS() {
		return nil, registry.RegistrationOption{}, ErrUnsupportedSystem
	}

	input, ok := telegraf_inputs.Inputs["temp"]
	if !ok {
		return nil, registry.RegistrationOption{}, inputs.ErrDisabledInput
	}

	tempInput, ok := input().(*temp.Temperature)
	if !ok {
		return nil, registry.RegistrationOption{}, inputs.ErrUnexpectedType
	}

	internalInput := &internal.Input{
		Input: tempInput,
		Accumulator: internal.Accumulator{
			RenameGlobal:     renameGlobal,
			TransformMetrics: transformMetrics,
		},
		Name: "Temp",
	}

	return internalInput, registry.RegistrationOption{}, nil
}

// Rename "temp" measurement to "sensor".
func renameGlobal(gatherContext internal.GatherContext) (internal.GatherContext, bool) {
	if gatherContext.Measurement == "temp" {
		gatherContext.Measurement = "sensor"
	}

	return gatherContext, false
}

// Rename "temp" field to "temperature".
func transformMetrics(
	currentContext internal.GatherContext,
	fields map[string]float64,
	originalFields map[string]any,
) map[string]float64 {
	_ = currentContext
	_ = originalFields
	newFields := make(map[string]float64, len(fields))

	for name, value := range fields {
		switch name {
		case "temp":
			newFields["temperature"] = value
		default:
			newFields[name] = value
		}
	}

	return newFields
}
