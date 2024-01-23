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
	"bytes"
	"glouton/logger"
	"path/filepath"
	"strings"
	"sync"

	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/plugins/inputs/smart"
)

const storageDevicesPattern = "/dev/sg[0-9]*"

type inputWrapper struct {
	*smart.Smart

	allowedDevices []string
	// Storage devices are only listed at startup,
	// then reused at each gathering.
	sgDevices []string

	l sync.Mutex
}

func newInputWrapper(input *smart.Smart, allowedDevices []string) *inputWrapper {
	iw := &inputWrapper{
		Smart:          input,
		allowedDevices: allowedDevices,
	}

	if len(allowedDevices) == 0 {
		sgDevices, err := filepath.Glob(storageDevicesPattern)
		if err != nil {
			logger.V(1).Printf("SMART: Failed to detect storage devices: %v", err)
		} else {
			iw.sgDevices = sgDevices
		}
	}

	return iw
}

func (iw *inputWrapper) Gather(acc telegraf.Accumulator) error {
	iw.l.Lock()
	defer iw.l.Unlock()

	out, err := runCmd(iw.Smart.Timeout, iw.Smart.UseSudo, iw.PathSmartctl, "--scan")
	if err != nil {
		return err
	}

	iw.Smart.Devices = iw.parseScanOutput(out)

	return iw.Smart.Gather(acc)
}

func (iw *inputWrapper) parseScanOutput(out []byte) []string {
	var devices []string

	for _, line := range bytes.Split(out, []byte("\n")) {
		devWithType := strings.SplitN(string(line), " ", 2)
		if len(devWithType) <= 1 {
			continue
		}

		if dev := strings.TrimSpace(devWithType[0]); iw.shouldHandleDevice(dev) {
			devices = append(devices, dev+deviceTypeFor(devWithType[1]))
		}
	}

	if len(iw.sgDevices) != 0 {
		// TODO: don't include sg devices if:
		// - they are duplicates of /dev/sda-like devices | or only if RAID is present ?
		// - they are a CD/DVD
		devices = append(devices, iw.sgDevices...)
	}

	return devices
}

func deviceTypeFor(devType string) string {
	if !strings.HasPrefix(devType, "-d ") {
		return ""
	}

	// Preventing some device types to be specified
	switch typ := strings.Split(devType, " ")[1]; typ {
	case "":
		return ""
	case "scsi":
		return ""
	default:
		return " -d " + typ
	}
}

func (iw *inputWrapper) shouldHandleDevice(device string) bool {
	if len(iw.allowedDevices) != 0 {
		for _, dev := range iw.allowedDevices {
			if device == dev {
				return true
			}
		}

		return false
	}

	for _, excluded := range iw.Smart.Excludes {
		if device == excluded {
			return false
		}
	}

	return true
}
