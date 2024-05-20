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

//go:build freebsd

package facts

import (
	"context"
	"os"
	"os/exec"

	"github.com/bleemeo/glouton/logger"

	"github.com/shirou/gopsutil/v3/load"
	"golang.org/x/sys/unix"
)

func (f *FactProvider) platformFacts() map[string]string {
	var utsName unix.Utsname

	err := unix.Uname(&utsName)
	if err != nil {
		logger.V(1).Printf("unable to execute uname: %v", err)

		return nil
	}

	facts := make(map[string]string)

	facts["kernel"] = bytesToString(utsName.Sysname[:])
	facts["kernel_release"] = bytesToString(utsName.Release[:])
	facts["os_family"] = "FreeBSD"

	if versionData, err := os.ReadFile("/etc/version"); err != nil {
		logger.V(1).Printf("unable to read os-release file: %v", err)
	} else {
		osRelease, err := decodeFreeBSDVersion(string(versionData))
		if err != nil {
			logger.V(1).Printf("version file is invalid: %v", err)
		}

		facts["os_name"] = osRelease["NAME"]
		facts["os_version"] = osRelease["VERSION_ID"]
		facts["os_pretty_name"] = osRelease["PRETTY_NAME"]
	}

	return facts
}

// primaryAddresses returns the primary IPv4
//
// This should be the IP address that this server use to communicate
// on internet. It may be the private IP if the box is NATed.
func (f *FactProvider) primaryAddress(ctx context.Context) (ipAddress string, macAddress string) {
	out, err := exec.CommandContext(ctx, "route", "-nv", "get", "8.8.8.8").Output()
	if err != nil {
		logger.V(1).Printf("unable to run route get: %v", err)

		return "", ""
	}

	return decodeFreeBSDRouteGet(string(out))
}

func getCPULoads() ([]float64, error) {
	loads, err := load.Avg()
	if err != nil {
		return nil, err
	}

	return []float64{loads.Load1, loads.Load5, loads.Load15}, nil
}
