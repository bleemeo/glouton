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

package modify

import (
	"strings"

	"github.com/bleemeo/glouton/inputs/internal"
	"github.com/bleemeo/glouton/types"

	"github.com/influxdata/telegraf"
)

// RenameCallback is a function which can mutate labels & annotations
// Mutation for labels could be done in place or by returning a new map.
type RenameCallback func(labels map[string]string, annotations types.MetricAnnotations) (newLabels map[string]string, newAnnotations types.MetricAnnotations)

// AddRenameCallback adds a rename callback that can mutate labels & annotations.
func AddRenameCallback(input telegraf.Input, f RenameCallback) telegraf.Input {
	if internalInput, ok := input.(*internal.Input); ok {
		internalInput.Accumulator.RenameCallbacks = append(internalInput.Accumulator.RenameCallbacks, internal.RenameCallback(f))

		return internalInput
	}

	// The first line of the sample config contains a comment with the input description.
	configLines := strings.Split(input.SampleConfig(), "\n")
	description := strings.TrimPrefix(configLines[0], "# ")

	internalInput := &internal.Input{
		Input: input,
		Accumulator: internal.Accumulator{
			RenameCallbacks: []internal.RenameCallback{internal.RenameCallback(f)},
		},
		Name: description,
	}

	return internalInput
}
