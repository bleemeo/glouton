// Copyright 2015-2022 Bleemeo
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

package uwsgi

import (
	"glouton/inputs"
	"glouton/inputs/internal"
	"strings"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/uwsgi"
)

// New returns a uWSGI input.
func New(url string) (internalInput telegraf.Input, err error) {
	input, ok := telegraf_inputs.Inputs["uwsgi"]
	if !ok {
		return nil, inputs.ErrDisabledInput
	}

	uwsgiInput, ok := input().(*uwsgi.Uwsgi)
	if !ok {
		return nil, inputs.ErrUnexpectedType
	}

	uwsgiInput.Servers = []string{url}

	internalInput = &internal.Input{
		Input: uwsgiInput,
		Accumulator: internal.Accumulator{
			RenameGlobal:     renameGlobal,
			TransformMetrics: transformMetrics,
		},
		Name: "uwsgi",
	}

	return internalInput, nil
}

func renameGlobal(gatherContext internal.GatherContext) (internal.GatherContext, bool) {
	// TODO: Let worker_id, source and other labels pass -> concatenate them in the item?

	return gatherContext, false
}

func transformMetrics(
	currentContext internal.GatherContext,
	fields map[string]float64,
	originalFields map[string]interface{},
) map[string]float64 {
	newFields := make(map[string]float64)

	// Remove the "uwsgi_" prefix as we already add the service name to the fields.
	for metricName, value := range fields {
		newFields[strings.TrimPrefix(metricName, "uwsgi_")] = value
	}

	return newFields
}
