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

package smart

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/inputs/internal"
	"github.com/bleemeo/glouton/prometheus/registry"
	"github.com/bleemeo/glouton/types"
	"github.com/bleemeo/glouton/utils/gloutonexec"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/smart"
)

var megaraidRegexp = regexp.MustCompile(`^megaraid,(\d+)$`)

// New returns a SMART input.
func New(config config.Smart, runner *gloutonexec.Runner, hostroot string, factStatusCallback func(binaryInstalled bool)) (telegraf.Input, registry.RegistrationOption, error) {
	SetupGlobalWrapper(runner)
	globalRunCmd.SetConcurrency(config.MaxConcurrency)

	input, ok := telegraf_inputs.Inputs["smart"]
	if !ok {
		return nil, registry.RegistrationOption{}, inputs.ErrDisabledInput
	}

	smartInput, ok := input().(*smart.Smart)
	if !ok {
		return nil, registry.RegistrationOption{}, inputs.ErrUnexpectedType
	}

	if config.PathSmartctl == "" {
		config.PathSmartctl = "smartctl"
	}

	if !strings.ContainsRune(config.PathSmartctl, os.PathSeparator) {
		fullPath, err := exec.LookPath(config.PathSmartctl)
		if err != nil {
			factStatusCallback(false)

			return nil, registry.RegistrationOption{}, fmt.Errorf("%w: \"%s\" not found in $PATH", inputs.ErrMissingCommand, config.PathSmartctl)
		}

		config.PathSmartctl = fullPath
	}

	factStatusCallback(true)

	// Don't use sudo if we are already root. This is mandatory on TrueNAS... because root isn't allowed
	// to sudo on TrueNAS
	smartInput.UseSudo = os.Getuid() != 0
	smartInput.Devices = config.Devices
	smartInput.Excludes = config.Excludes
	smartInput.PathSmartctl = config.PathSmartctl

	smartInput.TagWithDeviceType = true

	wrapperOpts := inputWrapperOptions{
		hostRootPath:  hostroot,
		input:         smartInput,
		configDevices: config.Devices,
	}

	smartInputWrapper, err := newInputWrapper(wrapperOpts)
	if err != nil {
		return nil, registry.RegistrationOption{}, err
	}

	internalInput := &internal.Input{
		Input: smartInputWrapper,
		Accumulator: internal.Accumulator{
			RenameGlobal:     renameGlobal,
			TransformMetrics: transformMetrics,
		},
		Name: "SMART",
	}

	options := registry.RegistrationOption{
		// smartctl might take some time to gather, especially with a large number of disks.
		MinInterval: 5 * 60 * time.Second,
	}

	return internalInput, options, nil
}

func DiagnosticArchive(ctx context.Context, archive types.ArchiveWriter) error {
	return globalRunCmd.diagnosticArchive(ctx, archive)
}

func transformMetrics(currentContext internal.GatherContext, fields map[string]float64, originalFields map[string]any) map[string]float64 {
	_ = currentContext
	_ = originalFields

	if tempC, ok := fields["temp_c"]; ok && tempC == 0 {
		// 0°C is way too improbable to be a real temperature.
		// Some disks, when SMART is unavailable/disabled, will report 0°C (at least PERC H710 does).
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

	if deviceType, ok := gatherContext.Tags["device_type"]; ok {
		delete(gatherContext.Tags, "device_type")

		device := gatherContext.Tags["device"]
		if _, err := strconv.Atoi(device); err == nil {
			gatherContext.Tags["device"] = overrideDeviceName(device, deviceType)
		}
	}

	return gatherContext, false
}

func overrideDeviceName(device string, deviceType string) string {
	raidMatches := megaraidRegexp.FindStringSubmatch(deviceType)
	if len(raidMatches) == 2 {
		raidIndex := raidMatches[1]
		device = "RAID Disk " + raidIndex
	}

	return device
}
