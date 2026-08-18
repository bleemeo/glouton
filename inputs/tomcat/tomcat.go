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

package tomcat

import (
	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/inputs/internal"
	"github.com/bleemeo/glouton/types"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/tomcat"
)

// New initialise tomcat.Input.
func New(url string, username string, password string) (i telegraf.Input, err error) {
	input, ok := telegraf_inputs.Inputs["tomcat"]
	if ok {
		tomcatInput, ok := input().(*tomcat.Tomcat)
		if ok {
			tomcatInput.URL = url
			tomcatInput.Username = username
			tomcatInput.Password = password

			i = &internal.Input{
				Input: tomcatInput,
				Accumulator: internal.Accumulator{
					RenameGlobal:     renameGlobal,
					TransformMetrics: transformMetrics,
					DifferentiatedMetrics: []string{
						"bytes_received",
						"bytes_sent",
						"error_count",
						"processing_time",
						"request_count",
					},
				},
				Name: "tomcat",
			}
		} else {
			err = inputs.ErrUnexpectedType
		}
	} else {
		err = inputs.ErrDisabledInput
	}

	return i, err
}

// renameGlobal drops the "source" tag, which holds the whole status URL we queried
// and is redundant with the labels already set on service metrics. The "name" tag
// (connector or memory pool name) is kept since it identifies the item.
func renameGlobal(gatherContext internal.GatherContext) (internal.GatherContext, bool) {
	delete(gatherContext.Tags, "source")

	// The item is what tells the connectors and the memory pools apart: without it all
	// of them would end up on the same metric.
	if name := gatherContext.Tags["name"]; name != "" {
		gatherContext.Tags[types.LabelItem] = name
	}

	return gatherContext, false
}

func transformMetrics(currentContext internal.GatherContext, fields map[string]float64, _ map[string]any) map[string]float64 {
	if currentContext.Measurement != "tomcat_connector" {
		return fields
	}

	processingTimeRate, hasProcessingTime := fields["processing_time"]
	requestCountRate, hasRequestCount := fields["request_count"]

	delete(fields, "processing_time")

	// Protect from division by 0.
	if hasProcessingTime && hasRequestCount && requestCountRate > 0 {
		fields["processing_time_seconds"] = processingTimeRate / requestCountRate / 1000 // milliseconds -> seconds.
	}

	return fields
}
