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

package nfs

import (
	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/inputs/internal"
	"github.com/bleemeo/glouton/prometheus/registry"
	"github.com/bleemeo/glouton/types"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/nfsclient"
)

// New returns a NFS client input.
func New() (telegraf.Input, registry.RegistrationOption, error) {
	input, ok := telegraf_inputs.Inputs["nfsclient"]
	if !ok {
		return nil, registry.RegistrationOption{}, inputs.ErrDisabledInput
	}

	nfsInput, ok := input().(*nfsclient.NFSClient)
	if !ok {
		return nil, registry.RegistrationOption{}, inputs.ErrUnexpectedType
	}

	// Limit metric to read and write operations.
	// There are more than 20 operations for NFSv3, and over 50 for NFSv4.
	nfsInput.IncludeOperations = []string{"READ", "WRITE"}

	internalInput := &internal.Input{
		Input: nfsInput,
		Accumulator: internal.Accumulator{
			RenameGlobal: renameGlobal,
			DifferentiatedMetrics: []string{
				"bytes",
				"ops",
				"retrans",
			},
		},
		Name: "nfsclient",
	}

	options := registry.RegistrationOption{
		Rules: []types.SimpleRule{
			{
				TargetName:  "nfs_transmitted_bits",
				PromQLQuery: "nfs_bytes*8",
			},
			{
				TargetName:  "nfs_rtt_per_op_seconds",
				PromQLQuery: "nfs_rtt_per_op/1000",
			},
		},
	}

	return internalInput, options, nil
}

func renameGlobal(gatherContext internal.GatherContext) (internal.GatherContext, bool) {
	// Rename measurement to nfs to have metrics with the prefix "nfs_".
	if gatherContext.Measurement == "nfsstat" {
		gatherContext.Measurement = "nfs"
	}

	return gatherContext, false
}
