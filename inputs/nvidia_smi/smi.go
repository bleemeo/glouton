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

package nvidia

import (
	"time"

	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/inputs/internal"
	"github.com/bleemeo/glouton/prometheus/registry"

	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/config"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/nvidia_smi"
)

// New returns a NVIDIA SMI input, given the path to the
// nvidia-smi binary and the timeout used for GPU polling in seconds.
func New(binPath string, timeout int) (telegraf.Input, registry.RegistrationOption, error) {
	input, ok := telegraf_inputs.Inputs["nvidia_smi"]
	if !ok {
		return nil, registry.RegistrationOption{}, inputs.ErrDisabledInput
	}

	nvidiaInput, ok := input().(*nvidia_smi.NvidiaSMI)
	if !ok {
		return nil, registry.RegistrationOption{}, inputs.ErrUnexpectedType
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
			RenameGlobal:     renameGlobal,
			TransformMetrics: transformMetrics,
		},
		Name: "nvidia-smi",
	}

	return internalInput, registry.RegistrationOption{}, nil
}

func renameGlobal(gatherContext internal.GatherContext) (result internal.GatherContext, drop bool) {
	// Remove overclocking state label as it's not stable.
	delete(gatherContext.Tags, "pstate")

	return gatherContext, false
}

func transformMetrics(currentContext internal.GatherContext, fields map[string]float64, originalFields map[string]any) map[string]float64 {
	_ = currentContext
	_ = originalFields

	factors := map[string]float64{
		"fbc_stats_average_latency":     1e-6,        // microsecond to second
		"encoder_stats_average_latency": 1e-6,        // microsecond to second
		"memory_free":                   1024 * 1024, // MiB to bytes
		"memory_used":                   1024 * 1024, // MiB to bytes
		"memory_total":                  1024 * 1024, // MiB to bytes
		"clocks_current_graphics":       1000000,     // MHz to Hz
		"clocks_current_sm":             1000000,     // MHz to Hz
		"clocks_current_memory":         1000000,     // MHz to Hz
		"clocks_current_video":          1000000,     // MHz to Hz
	}

	for k, factor := range factors {
		value, ok := fields[k]
		if ok {
			fields[k] = value * factor
		}
	}

	return fields
}
