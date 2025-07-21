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

package haproxy

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/inputs/internal"
	"github.com/bleemeo/glouton/types"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	_ "github.com/influxdata/telegraf/plugins/inputs/haproxy" // we use it
)

var errCreation = errors.New("error during creation of HAproxy input")

// We use a dedicated function to be able to recover from a panic.
func reflectSet(url string, input telegraf.Input) {
	inputValue := reflect.Indirect(reflect.ValueOf(input))
	serverValue := inputValue.FieldByName("Servers")
	serverValue.Set(reflect.ValueOf(append(make([]string, 0), url)))
}

// New initialise haproxy.Input.
func New(url string) (i telegraf.Input, err error) {
	input, ok := telegraf_inputs.Inputs["haproxy"]
	if ok {
		haproxyInput := input()
		// Telegraf does not export haproxy structure :(
		// We need to use reflect to access fields
		func() {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("%w: %v", errCreation, r)
				}
			}()

			reflectSet(url, haproxyInput)
		}()

		if err != nil {
			return nil, err
		}

		i = &internal.Input{
			Input: haproxyInput,
			Accumulator: internal.Accumulator{
				RenameGlobal:          renameGlobal,
				DifferentiatedMetrics: []string{"stot", "bin", "bout", "dreq", "dresp", "ereq", "econ", "eresp", "req_tot"},
				TransformMetrics:      transformMetrics,
			},
			Name: "haproxy",
		}
	} else {
		err = inputs.ErrDisabledInput
	}

	return i, err
}

func renameGlobal(gatherContext internal.GatherContext) (internal.GatherContext, bool) {
	if gatherContext.Tags["sv"] != "BACKEND" && gatherContext.Tags["sv"] != "FRONTEND" {
		return gatherContext, true
	}

	gatherContext.Tags = map[string]string{
		types.LabelItem: gatherContext.Tags["proxy"],
	}

	return gatherContext, false
}

func transformMetrics(currentContext internal.GatherContext, fields map[string]float64, originalFields map[string]any) map[string]float64 {
	_ = currentContext
	_ = originalFields
	newFields := make(map[string]float64)

	for name, value := range fields {
		switch name {
		case "stot", "bin", "bout", "dreq", "dresp", "ereq", "econ", "eresp", "req_tot":
			newFields[name] = value
		case "qcur", "scur":
			newFields[name] = value
		case "qtime", "ctime", "rtime", "ttime":
			newFields[name] = value / 1000 // convert from milliseconds to seconds
		case "active_servers":
			newFields["act"] = value
		}
	}

	return newFields
}
