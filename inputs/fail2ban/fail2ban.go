// Copyright 2015-2024 Bleemeo
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

package fail2ban

import (
	"time"

	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/inputs/internal"
	"github.com/bleemeo/glouton/prometheus/registry"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/fail2ban"
)

// New returns a Fail2ban input.
func New() (telegraf.Input, registry.RegistrationOption, error) {
	input, ok := telegraf_inputs.Inputs["fail2ban"]
	if !ok {
		return nil, registry.RegistrationOption{}, inputs.ErrDisabledInput
	}

	fail2banInput, ok := input().(*fail2ban.Fail2ban)
	if !ok {
		return nil, registry.RegistrationOption{}, inputs.ErrUnexpectedType
	}

	// The input uses "sudo fail2ban-client status myjail" to retrieve the metrics.
	fail2banInput.UseSudo = true

	internalInput := &internal.Input{
		Input:       fail2banInput,
		Accumulator: internal.Accumulator{},
		Name:        "fail2ban",
	}

	options := registry.RegistrationOption{
		// The input uses an external command with sudo so we gather metrics less often.
		Interval: 60 * time.Second,
	}

	return internalInput, options, nil
}
