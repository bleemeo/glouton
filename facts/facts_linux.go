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

//go:build linux

package facts

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/utils/gloutonexec"

	"github.com/shirou/gopsutil/v3/load"
	psutilNet "github.com/shirou/gopsutil/v3/net"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

const dmiDir = "/sys/devices/virtual/dmi/id/"

func (f *FactProvider) platformFacts(ctx context.Context) map[string]string {
	facts := make(map[string]string)

	if f.hostRootPath != "" {
		osReleasePath := filepath.Join(f.hostRootPath, "etc/os-release")
		if osReleaseData, err := os.ReadFile(osReleasePath); err != nil {
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
			facts["os_codename"] = osRelease["VERSION_CODENAME"]
		}
	}

	out, err := f.runner.Run(ctx, gloutonexec.Option{SkipInContainer: true}, "lsb_release", "--codename", "--short")
	if err != nil && !errors.Is(err, gloutonexec.ErrExecutionSkipped) {
		logger.V(1).Printf("unable to run lsb_release: %v", err)
	} else if err == nil {
		facts["os_codename"] = strings.TrimSpace(string(out))
	}

	var utsName unix.Utsname

	err = unix.Uname(&utsName)
	if err == nil {
		facts["kernel"] = bytesToString(utsName.Sysname[:])
		facts["kernel_release"] = bytesToString(utsName.Release[:])
		l := strings.SplitN(facts["kernel_release"], "-", 2)
		facts["kernel_version"] = l[0]
		l = strings.SplitN(facts["kernel_release"], ".", 3)
		facts["kernel_major_version"] = strings.Join(l[0:2], ".")
	}

	v, err := os.ReadFile(filepath.Join(dmiDir, "bios_date"))
	if err == nil {
		facts["bios_released_at"] = strings.TrimSpace(string(v))
	}

	v, err = os.ReadFile(filepath.Join(dmiDir, "bios_vendor"))
	if err == nil {
		facts["bios_vendor"] = strings.TrimSpace(string(v))
	}

	v, err = os.ReadFile(filepath.Join(dmiDir, "bios_version"))
	if err == nil {
		facts["bios_version"] = strings.TrimSpace(string(v))
	}

	v, err = os.ReadFile(filepath.Join(dmiDir, "product_name"))
	if err == nil {
		facts["product_name"] = strings.TrimSpace(string(v))
	}

	v, err = os.ReadFile(filepath.Join(dmiDir, "sys_vendor"))
	if err == nil {
		facts["system_vendor"] = strings.TrimSpace(string(v))
	}

	return facts
}

// primaryAddresses returns the primary IPv4
//
// This should be the IP address that this server use to communicate
// on internet. It may be the private IP if the box is NATed.
func (f *FactProvider) primaryAddress(ctx context.Context) (ipAddress string, macAddress string) {
	routes, err := netlink.RouteGet(net.ParseIP("8.8.8.8"))
	if err != nil || len(routes) == 0 {
		logger.V(1).Printf("unable to run ip route get: %v", err)

		return
	}

	link, err := netlink.LinkByIndex(routes[0].LinkIndex)
	if err == nil {
		attrs := link.Attrs()
		if attrs != nil {
			return routes[0].Src.String(), attrs.HardwareAddr.String()
		}
	}

	return routes[0].Src.String(), macAddressByAddress(ctx, routes[0].Src.String())
}

func macAddressByAddress(ctx context.Context, ipAddress string) string {
	ifs, err := psutilNet.InterfacesWithContext(ctx)
	if err != nil {
		return ""
	}

	for _, i := range ifs {
		for _, a := range i.Addrs {
			if a.Addr == ipAddress {
				return i.HardwareAddr
			}
		}
	}

	return ""
}

func getCPULoads() ([]float64, error) {
	loads, err := load.Avg()
	if err != nil {
		return nil, err
	}

	return []float64{loads.Load1, loads.Load5, loads.Load15}, nil
}
