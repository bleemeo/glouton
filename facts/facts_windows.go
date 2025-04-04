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

//go:build windows

package facts

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"

	"github.com/bleemeo/glouton/logger"

	"github.com/yusufpapurcu/wmi"
	"golang.org/x/sys/windows/registry"
)

var errNoResults = errors.New("the WMI request returned 0 result")

//nolint:staticcheck
type Win32_ComputerSystem struct {
	Model        string
	Manufacturer string
}

//nolint:staticcheck
type Win32_BIOS struct {
	Manufacturer string
	Version      string
	ReleaseDate  string
	SerialNumber int
}

//nolint:staticcheck
type Win32_IP4RouteTable struct {
	InterfaceIndex int
	NextHop        string
}

//nolint:staticcheck
type Win32_PerfFormattedData_PerfOS_System struct {
	ProcessorQueueLength int
}

//nolint:staticcheck
type Win32_PerfFormattedData_PerfOS_Processor struct {
	PercentIdleTime int
}

const unsupportedVersion = "Unsupported Version"

func matchClientVersion(major, minor uint64) string {
	if major == 10 {
		return "10"
	}

	switch minor {
	case 0:
		return "Vista"
	case 1:
		return "7"
	case 2:
		return "8"
	case 3:
		return "8.1"
	default:
		return unsupportedVersion
	}
}

func matchServerVersion(major, minor uint64) string {
	if major == 10 {
		return "Server 2016 or higher"
	}

	switch minor {
	case 0:
		return "Server 2008"
	case 1:
		return "Server 2008 R2"
	case 2:
		return "Server 2012"
	case 3:
		return "Server 2012 R2"
	default:
		return unsupportedVersion
	}
}

func getWindowsVersionName(major uint64, minor uint64, isServer bool, servicePack string) string {
	if major != 6 && major != 10 {
		return unsupportedVersion
	}

	var res string

	var version string

	if isServer {
		version = matchServerVersion(major, minor)
	} else {
		version = matchClientVersion(major, minor)
	}

	if servicePack != "" {
		res = fmt.Sprintf("Windows %s SP %s", version, servicePack)
	} else {
		res = "Windows " + version
	}

	return res
}

func (f *FactProvider) platformFacts(_ context.Context) map[string]string {
	facts := make(map[string]string)

	facts["kernel"] = "Windows"
	facts["os_family"] = "NT"
	facts["os_name"] = "Windows"

	reg, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE)
	if err != nil {
		logger.V(1).Println("Couldn't open the windows registry, some facts may not be exposed")
	} else {
		defer reg.Close()

		major, _, err1 := reg.GetIntegerValue("CurrentMajorVersionNumber")
		minor, _, err2 := reg.GetIntegerValue("CurrentMinorVersionNumber")
		buildNumber, _, err3 := reg.GetStringValue("CurrentBuildNumber")
		servicePack, _, _ := reg.GetStringValue("CSDVersion")
		installationType, _, _ := reg.GetStringValue("InstallationType")

		isServer := true
		if installationType == "Client" {
			isServer = false
		}

		if err1 == nil && err2 == nil && err3 == nil {
			facts["os_version"] = getWindowsVersionName(major, minor, isServer, servicePack)
			facts["os_version_long"] = fmt.Sprintf("Windows NT %d.%d build %s", major, minor, buildNumber)
		}

		productName, _, err := reg.GetStringValue("ProductName")
		if err == nil {
			facts["os_pretty_name"] = productName
		}
	}

	wmiClient := &wmi.Client{AllowMissingFields: true}

	var system []Win32_ComputerSystem

	err = wmiClient.Query(wmi.CreateQuery(&system, ""), &system)

	switch {
	case err != nil:
		logger.V(1).Printf("unable to read wmi information: %v", err)
	case len(system) == 0:
		logger.V(1).Printf("the WMI request returned 0 result")
	default:
		facts["system_vendor"] = system[0].Manufacturer
		facts["product_name"] = system[0].Model
	}

	var bios []Win32_BIOS

	err = wmiClient.Query(wmi.CreateQuery(&bios, ""), &bios)

	switch {
	case err != nil:
		logger.V(1).Printf("unable to read wmi information: %v", err)
	case len(bios) == 0:
		logger.V(1).Printf("the WMI request returned 0 result")
	default:
		facts["bios_vendor"] = bios[0].Manufacturer
		facts["bios_version"] = bios[0].Version
		facts["serial_number"] = strconv.Itoa(bios[0].SerialNumber)
		facts["bios_released_at"] = bios[0].ReleaseDate
	}

	return facts
}

// primaryAddresses returns the primary IPv4
//
// This should be the IP address that this server use to communicate
// on internet. It may be the private IP if the box is NATed.
func (f *FactProvider) primaryAddress(context.Context) (ipAddress string, macAddress string) {
	// To get the primary IP, we retrieve the interface index for the destination 0.0.0.0,
	// and we iterate over the addresses of that interface until we find an address in the subnet
	// of the next hop (the gateway, in unix parlance) for 0.0.0.0 (return via WMI alongside the
	// interface index).
	var route []Win32_IP4RouteTable

	err := wmi.Query(wmi.CreateQuery(&route, `WHERE Destination="0.0.0.0"`), &route)

	switch {
	case err != nil:
		logger.V(1).Printf("unable to read wmi information: %v", err)

		return "", ""
	case len(route) == 0:
		logger.V(1).Printf("the WMI request returned 0 result")

		return "", ""
	}

	inter, err := net.InterfaceByIndex(route[0].InterfaceIndex)
	if err != nil {
		return "", ""
	}

	addrs, err := inter.Addrs()
	if err != nil {
		return "", ""
	}

	gwAddr := net.ParseIP(route[0].NextHop)
	if gwAddr == nil {
		return "", ""
	}

	for _, addr := range addrs {
		net, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}

		ip := net.IP.To4()
		if ip == nil {
			continue
		}

		if net.Contains(gwAddr) {
			return ip.String(), inter.HardwareAddr.String()
		}
	}

	return "", ""
}

func getCPULoads() ([]float64, error) {
	// reproduce the behavior exhibited in the python agent: we estimate the load to be
	// the current cpu_usage + Processor Queue Length (the number of starved threads)
	var process []Win32_PerfFormattedData_PerfOS_Processor

	err := wmi.Query(wmi.CreateQuery(&process, `WHERE Name = "_Total"`), &process)

	switch {
	case err != nil:
		return nil, fmt.Errorf("unable to read wmi information: %w", err)
	case len(process) == 0:
		return nil, errNoResults
	}

	var system []Win32_PerfFormattedData_PerfOS_System

	err = wmi.Query(wmi.CreateQuery(&system, ""), &system)

	switch {
	case err != nil:
		return nil, fmt.Errorf("unable to read wmi information: %w", err)
	case len(system) == 0:
		return nil, errNoResults
	}

	return []float64{1. - float64(process[0].PercentIdleTime)/100. + float64(system[0].ProcessorQueueLength)}, nil
}
