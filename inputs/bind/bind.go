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

package bind

import (
	"strings"

	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/inputs/internal"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/bind"
)

// New initialise bind.Input.
func New(url string) (i telegraf.Input, err error) {
	input, ok := telegraf_inputs.Inputs["bind"]
	if ok {
		bindInput, ok := input().(*bind.Bind)
		if ok {
			bindInput.Urls = []string{url}

			i = &internal.Input{
				Input: bindInput,
				Accumulator: internal.Accumulator{
					RenameMetrics:              renameMetrics,
					ShouldDifferentiateMetrics: shouldDifferentiateMetrics,
				},
				Name: "bind",
			}
		} else {
			err = inputs.ErrUnexpectedType
		}
	} else {
		err = inputs.ErrDisabledInput
	}

	return i, err
}

var counterFieldRenames = map[string]string{ //nolint:gochecknoglobals
	"QUERY":       "query",
	"NXDOMAIN":    "nxdomain",
	"SERVFAIL":    "servfail",
	"QrySuccess":  "qry_success",
	"QryNXDOMAIN": "qry_nxdomain",
}

func renameMetrics(currentContext internal.GatherContext, metricName string) (newMeasurement string, newMetricName string) {
	if renamed, ok := counterFieldRenames[metricName]; ok {
		return currentContext.Measurement, renamed
	}

	return currentContext.Measurement, strings.ToLower(metricName)
}

func shouldDifferentiateMetrics(currentContext internal.GatherContext, _ string) bool {
	return currentContext.Measurement == "bind_counter"
}
