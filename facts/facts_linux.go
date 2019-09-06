// Copyright 2015-2019 Bleemeo
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

package facts

import (
	"agentgo/logger"
	"bytes"
	"io/ioutil"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

const dmiDir = "/sys/devices/virtual/dmi/id/"

func (f *FactProvider) platformFacts() map[string]string {
	facts := make(map[string]string)
	if f.hostRootPath != "" {
		osReleasePath := filepath.Join(f.hostRootPath, "etc/os-release")
		if osReleaseData, err := ioutil.ReadFile(osReleasePath); err != nil {
			logger.V(1).Printf("unable to read os-release file: %v", err)
		} else {
			osRelease, err := decodeOsRelease(string(osReleaseData))
			if err != nil {
				logger.V(1).Printf("os-release file is invalid: %v", err)
			}
			facts["os_family"] = osRelease["ID_LIKE"]
			facts["os_name"] = osRelease["NAME"]
			facts["os_pretty_name"] = osRelease["PRETTY_NAME"]
			facts["os_version"] = osRelease["VERSION_ID"]
			facts["os_version_long"] = osRelease["VERSION"]
		}
	}
	if f.hostRootPath == "/" {
		out, err := exec.Command("lsb_release", "--codename", "--short").Output()
		if err != nil {
			logger.V(1).Printf("unable to run lsb_release: %v", err)
		} else {
			facts["os_codename"] = strings.TrimSpace(string(out))
		}
	}

	var utsName syscall.Utsname
	err := syscall.Uname(&utsName)
	if err == nil {
		facts["kernel"] = int8ToString(utsName.Sysname[:])
		facts["kernel_release"] = int8ToString(utsName.Release[:])
		l := strings.SplitN(facts["kernel_release"], "-", 2)
		facts["kernel_version"] = l[0]
		l = strings.SplitN(facts["kernel_release"], ".", 3)
		facts["kernel_major_version"] = strings.Join(l[0:2], ".")
	}

	v, err := ioutil.ReadFile(filepath.Join(dmiDir, "bios_date"))
	if err == nil {
		facts["bios_released_at"] = strings.TrimSpace(string(v))
	}
	v, err = ioutil.ReadFile(filepath.Join(dmiDir, "bios_vendor"))
	if err == nil {
		facts["bios_vendor"] = strings.TrimSpace(string(v))
	}
	v, err = ioutil.ReadFile(filepath.Join(dmiDir, "bios_version"))
	if err == nil {
		facts["bios_version"] = strings.TrimSpace(string(v))
	}
	v, err = ioutil.ReadFile(filepath.Join(dmiDir, "product_name"))
	if err == nil {
		facts["product_name"] = strings.TrimSpace(string(v))
	}
	v, err = ioutil.ReadFile(filepath.Join(dmiDir, "sys_vendor"))
	if err == nil {
		facts["system_vendor"] = strings.TrimSpace(string(v))
	}

	return facts
}

// primaryAddresses returns the primary IPv4
//
// This should be the IP address that this server use to communicate
// on internet. It may be the private IP if the box is NATed.
func (f *FactProvider) primaryAddress() (ipAddress string, ifaceName string) {
	out, err := exec.Command("ip", "route", "get", "8.8.8.8").Output()
	if err != nil {
		logger.V(1).Printf("unable to run ip route get: %v", err)
		return
	}
	l := strings.Split(string(out), " ")
	for i, v := range l {
		if v == "dev" && len(l) > i+1 {
			ifaceName = l[i+1]
		}
		if v == "src" && len(l) > i+1 {
			ipAddress = l[i+1]
		}
	}
	return
}

func int8ToString(input []int8) string {
	buffer := make([]byte, len(input))
	for i, v := range input {
		buffer[i] = byte(v)
	}
	n := bytes.IndexByte(buffer, 0)
	return string(buffer[:n])
}
