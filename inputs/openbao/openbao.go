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

package openbao

import (
	"strings"

	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/inputs/internal"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/vault"
)

// New initialise openbao Input.
func New(url string, token string) (i telegraf.Input, err error) {
	input, ok := telegraf_inputs.Inputs["vault"]
	if ok {
		openbaoInput, ok := input().(*vault.Vault)
		if ok {
			openbaoInput.URL = url
			openbaoInput.Token = token

			i = &internal.Input{
				Input: openbaoInput,
				Accumulator: internal.Accumulator{
					RenameGlobal: renameGlobal,
					DifferentiatedMetrics: []string{
						"handle_request_count",
						"handle_login_request_count",
						"check_token_count",
						"response_status_code_count",
						"leadership_lost_count",
					},
				},
				Name: "openbao",
			}
		} else {
			err = inputs.ErrUnexpectedType
		}
	} else {
		err = inputs.ErrDisabledInput
	}

	return i, err
}

func renameGlobal(gatherContext internal.GatherContext) (internal.GatherContext, bool) {
	newName := strings.ReplaceAll(gatherContext.Measurement, ".", "_")

	if newName, found := strings.CutPrefix(newName, "vault"); found {
		gatherContext.Measurement = "bao" + newName
	} else {
		gatherContext.Measurement = newName
	}

	return gatherContext, false
}
