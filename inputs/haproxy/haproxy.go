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

package haproxy

import (
	"errors"
	"fmt"
	"glouton/inputs/internal"
	"reflect"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	_ "github.com/influxdata/telegraf/plugins/inputs/haproxy" // we use it
)

// We use a dedicated function to be able to recover from a panic
func reflectSet(url string, input telegraf.Input) {
	inputValue := reflect.Indirect(reflect.ValueOf(input))
	serverValue := inputValue.FieldByName("Servers")
	serverValue.Set(reflect.ValueOf(append(make([]string, 0), url)))
}

// New initialise haproxy.Input
func New(url string) (i telegraf.Input, err error) {
	var input, ok = telegraf_inputs.Inputs["haproxy"]
	if ok {
		haproxyInput := input()
		// Telegraf does not export haproxy structure :(
		// We need to use reflect to access fields
		func() {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("error during creation of HAproxy input: %v", r)
				}
			}()
			reflectSet(url, haproxyInput)
		}()

		if err != nil {
			return
		}

		i = &internal.Input{
			Input: haproxyInput,
			Accumulator: internal.Accumulator{
				RenameGlobal:     renameGlobal,
				DerivatedMetrics: []string{"stot", "bin", "bout", "dreq", "dresp", "ereq", "econ", "eresp", "req_tot"},
				TransformMetrics: transformMetrics,
			},
		}
	} else {
		err = errors.New("input HAProxy not enabled in Telegraf")
	}

	return i, err
}

func renameGlobal(originalContext internal.GatherContext) (newContext internal.GatherContext, drop bool) {
	newContext = originalContext

	if originalContext.Tags["sv"] != "BACKEND" && originalContext.Tags["sv"] != "FRONTEND" {
		drop = true
	}

	newContext.Tags["item"] = newContext.Tags["proxy"]

	return
}

func transformMetrics(originalContext internal.GatherContext, currentContext internal.GatherContext, fields map[string]float64, originalFields map[string]interface{}) map[string]float64 {
	newFields := make(map[string]float64)

	for name, value := range fields {
		switch name {
		case "stot", "bin", "bout", "dreq", "dresp", "ereq", "econ", "eresp", "req_tot":
			newFields[name] = value
		case "qcur", "scur", "qtime", "ctime", "rtime", "ttime":
			newFields[name] = value
		case "active_servers":
			newFields["act"] = value
		}
	}

	return newFields
}
