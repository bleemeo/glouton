// Copyright 2015-2023 Bleemeo
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

package smart

import (
	"glouton/config"
	"glouton/inputs"
	"glouton/inputs/internal"
	"os"
	"strings"
	"time"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/smart"
)

// New returns a SMART input.
func New(config config.Smart) (telegraf.Input, *inputs.GathererOptions, error) {
	input, ok := telegraf_inputs.Inputs["smart"]
	if !ok {
		return nil, nil, inputs.ErrDisabledInput
	}

	smartInput, ok := input().(*smart.Smart)
	if !ok {
		return nil, nil, inputs.ErrUnexpectedType
	}

	// Don't use sudo if we are already root. This is mandatory on TrueNAS... because root isn't allowed
	// to sudo on TrueNAS
	smartInput.UseSudo = os.Getuid() != 0
	smartInput.Devices = config.Devices
	smartInput.Excludes = config.Excludes
	smartInput.PathSmartctl = config.PathSmartctl

	internalInput := &internal.Input{
		Input: smartInput,
		Accumulator: internal.Accumulator{
			RenameGlobal:     renameGlobal,
			TransformMetrics: transformMetrics,
		},
		Name: "SMART",
	}

	options := &inputs.GathererOptions{
		// smartctl might take some time to gather, especially with large number of disk.
		MinInterval: 5 * 60 * time.Second,
	}

	return internalInput, options, nil
}

func transformMetrics(currentContext internal.GatherContext, fields map[string]float64, originalFields map[string]interface{}) map[string]float64 {
	_ = currentContext
	_ = originalFields

	if tempC, ok := fields["temp_c"]; ok && tempC == 0 {
		// 0°C is way to improbable to be a real temperature.
		// Some disk, when SMART is unavailable/disabled, will report 0°C (at least PERC H710 does).
		delete(fields, "temp_c")
	}

	return fields
}

func renameGlobal(gatherContext internal.GatherContext) (result internal.GatherContext, drop bool) {
	// It possible to don't have SMART active. In this case exclude the devices.
	// The exact output of this tag depend on smartctl output.
	// The following are known existing values:
	// * "Enabled"
	// * absent (e.g. for NVME disk)
	// * "Unavailable - device lacks SMART capability." on Dell PERC H710P
	lowerEnabled := strings.ToLower(gatherContext.Tags["enabled"])
	if strings.Contains(lowerEnabled, "unavailable") || strings.Contains(lowerEnabled, "disabled") {
		return gatherContext, true
	}

	// Remove labels that are not useful.
	delete(gatherContext.Tags, "capacity")
	delete(gatherContext.Tags, "enabled")
	delete(gatherContext.Tags, "power")

	return gatherContext, false
}
