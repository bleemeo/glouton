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

package phpfpm

import (
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/inputs/internal"

	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/config"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	_ "github.com/influxdata/telegraf/plugins/inputs/phpfpm" // we use it
)

var errInputCreation = errors.New("error during creation of PHP-FPM input")

// We use a dedicated function to be able to recover from a panic.
func reflectSet(url string, input telegraf.Input) {
	inputValue := reflect.Indirect(reflect.ValueOf(input))
	serverValue := inputValue.FieldByName("Urls")
	serverValue.Set(reflect.ValueOf(append(make([]string, 0), url)))

	timeoutValue := inputValue.FieldByName("Timeout")
	timeoutValue.Set(reflect.ValueOf(config.Duration(10 * time.Second)))
}

// New initialise phpfpm.Input.
func New(url string) (i telegraf.Input, err error) {
	input, ok := telegraf_inputs.Inputs["phpfpm"]
	if ok {
		phpfpmInput := input()

		func() {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("%w: %v", errInputCreation, r)
				}
			}()

			reflectSet(url, phpfpmInput)
		}()

		if err != nil {
			return i, err
		}

		i = &internal.Input{
			Input: phpfpmInput,
			Accumulator: internal.Accumulator{
				DifferentiatedMetrics: []string{"accepted_conn", "slow_requests"},
			},
			Name: "phpfpm",
		}
	} else {
		err = inputs.ErrDisabledInput
	}

	return i, err
}
