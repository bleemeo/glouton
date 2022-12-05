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

package nvidia

import (
	"glouton/inputs"
	"glouton/inputs/internal"
	"time"

	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/config"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/nvidia_smi"
)

// New returns a NVIDIA SMI input, given the path to the
// nvidia-smi binary and the timeout used for GPU polling in seconds.
func New(binPath string, timeout int) (telegraf.Input, *inputs.GathererOptions, error) {
	input, ok := telegraf_inputs.Inputs["nvidia_smi"]
	if !ok {
		return nil, nil, inputs.ErrDisabledInput
	}

	nvidiaInput, ok := input().(*nvidia_smi.NvidiaSMI)
	if !ok {
		return nil, nil, inputs.ErrUnexpectedType
	}

	if binPath != "" {
		nvidiaInput.BinPath = binPath
	}

	if timeout != 0 {
		nvidiaInput.Timeout = config.Duration(timeout) * config.Duration(time.Second)
	}

	internalInput := &internal.Input{
		Input: nvidiaInput,
		Accumulator: internal.Accumulator{
			RenameGlobal: renameGlobal,
		},
		Name: "nvidia-smi",
	}

	return internalInput, &inputs.GathererOptions{}, nil
}

func renameGlobal(gatherContext internal.GatherContext) (result internal.GatherContext, drop bool) {
	// Remove overclocking state label as it's not stable.
	delete(gatherContext.Tags, "pstate")

	return gatherContext, false
}
