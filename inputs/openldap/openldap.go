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

package openldap

import (
	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/inputs/internal"
	"github.com/bleemeo/glouton/prometheus/registry"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/openldap"
)

// New returns a OpenLDAP input.
func New(host string, port int, config config.Service) (telegraf.Input, registry.RegistrationOption, error) {
	input, ok := telegraf_inputs.Inputs["openldap"]
	if !ok {
		return nil, registry.RegistrationOption{}, inputs.ErrDisabledInput
	}

	ldapInput, ok := input().(*openldap.Openldap)
	if !ok {
		return nil, registry.RegistrationOption{}, inputs.ErrUnexpectedType
	}

	ldapInput.Host = host
	ldapInput.Port = port
	ldapInput.BindDn = config.Username
	ldapInput.BindPassword = config.Password
	ldapInput.ReverseMetricNames = true

	tls := ""
	if config.StartTLS {
		tls = "starttls"
	} else if config.SSL {
		tls = "ldaps"
	}

	ldapInput.TLS = tls
	ldapInput.TLSCA = config.CAFile
	ldapInput.InsecureSkipVerify = config.SSLInsecure

	internalInput := &internal.Input{
		Input: ldapInput,
		Accumulator: internal.Accumulator{
			RenameGlobal: func(gatherContext internal.GatherContext) (result internal.GatherContext, drop bool) {
				// Remove the IP address of the server. Glouton will add item and/or container to identify the source
				delete(gatherContext.Tags, "server")
				// Remove the port, Glouton store them in a Service object.
				delete(gatherContext.Tags, "port")

				return gatherContext, false
			},
			DerivatedMetrics: []string{
				"statistics_bytes",
				"statistics_entries",
				"operations_add_completed",
				"operations_bind_completed",
				"operations_delete_completed",
				"operations_modify_completed",
				"operations_search_completed",
			},
		},
		Name: "openldap",
	}

	return internalInput, registry.RegistrationOption{}, nil
}
