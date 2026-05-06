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

//go:build linux

package facts

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/utils/gloutonexec"

	"github.com/shirou/gopsutil/v4/load"
	psutilNet "github.com/shirou/gopsutil/v4/net"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

const dmiDir = "/sys/devices/virtual/dmi/id/"

func (f *FactProvider) osFacts(ctx context.Context, facts map[string]string) map[string]string {
	if f.options.HostRootPath != "" {
		osReleasePath := filepath.Join(f.options.HostRootPath, "etc/os-release")
		if osReleaseData, err := os.ReadFile(osReleasePath); err != nil {
			logger.V(1).Printf("unable to read os-release file: %v", err)
		} else {
			osRelease, err := decodeOsRelease(string(osReleaseData))
			if err != nil {
				logger.V(1).Printf("os-release file is invalid: %v", err)
			}

			if osRelease["ID"] == "debian" {
				trueNASMarker := filepath.Join(f.options.HostRootPath, "lib/systemd/system/truenas.target")
				if _, err := os.Stat(trueNASMarker); err == nil {
					return f.trueNasScaleOSFact(facts)
				}
			}

			facts["os_family"] = osRelease["ID_LIKE"]
			facts["os_name"] = osRelease["NAME"]
			facts["os_pretty_name"] = osRelease["PRETTY_NAME"]
			facts["os_version"] = osRelease["VERSION_ID"]
			facts["os_version_long"] = osRelease["VERSION"]
			facts["os_codename"] = osRelease["VERSION_CODENAME"]
		}
	}

	out, err := f.options.Runner.Run(ctx, gloutonexec.Option{SkipInContainer: true}, "lsb_release", "--codename", "--short")
	if err != nil && !errors.Is(err, gloutonexec.ErrExecutionSkipped) {
		logger.V(1).Printf("unable to run lsb_release: %v", err)
	} else if err == nil {
		facts["os_codename"] = strings.TrimSpace(string(out))
	}

	return facts
}

func (f *FactProvider) trueNasScaleOSFact(facts map[string]string) map[string]string {
	facts["os_name"] = "TrueNAS"

	var manifest struct {
		Codename string `json:"codename"`
		Version  string `json:"version"`
	}

	manifestFile := filepath.Join(f.options.HostRootPath, "data/manifest.json")
	if manifestData, err := os.ReadFile(manifestFile); err != nil {
		logger.V(1).Printf("unable to read manifest.json file: %v", err)
	} else {
		err := json.Unmarshal(manifestData, &manifest)
		if err != nil {
			logger.V(1).Printf("unable to decode manifest.json file: %v", err)
		}
	}

	if manifest.Version == "" {
		versionFile := filepath.Join(f.options.HostRootPath, "etc/version")
		if versionData, err := os.ReadFile(versionFile); err != nil {
			logger.V(1).Printf("unable to read version file: %v", err)
		} else {
			manifest.Version = string(versionData)
		}
	}

	facts["os_codename"] = manifest.Codename

	if manifest.Version != "" {
		facts["os_pretty_name"] = "TrueNAS SCALE " + manifest.Version
	} else {
		facts["os_pretty_name"] = "TrueNAS SCALE"
	}

	facts["os_version"] = manifest.Version

	return facts
}

func (f *FactProvider) platformFacts(ctx context.Context) map[string]string {
	facts := make(map[string]string)

	facts = f.osFacts(ctx, facts)

	var utsName unix.Utsname

	err := unix.Uname(&utsName)
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

func (f *FactProvider) installedPackagesFacts(ctx context.Context, facts map[string]string) {
	osFamily := facts["os_family"]
	osName := strings.ToLower(facts["os_name"])

	trackedPackages := []struct {
		factName    string
		packageName string
	}{
		{factName: "glouton_package_version", packageName: "glouton"},
		{factName: "bleemeo_agent_package_version", packageName: "bleemeo-agent"},
		{factName: "bleemeo_agent_keyring_package_version", packageName: "bleemeo-agent-keyring"},
		{factName: "bleemeo_agent_jmx_package_version", packageName: "bleemeo-agent-jmx"},
		{factName: "bleemeo_agent_logs_package_version", packageName: "bleemeo-agent-logs"},
	}

	packageNames := make([]string, len(trackedPackages))
	packageToFact := make(map[string]string, len(trackedPackages))

	for i, pkg := range trackedPackages {
		packageNames[i] = pkg.packageName
		packageToFact[pkg.packageName] = pkg.factName
	}

	var versions map[string]string

	switch {
	case osFamily == "debian" || strings.Contains(osName, "debian"):
		versions = f.queryDebianPackageVersions(ctx, packageNames)
	case strings.Contains(osFamily, "rhel") || strings.Contains(osFamily, "fedora") || strings.Contains(osName, "fedora"):
		versions = f.queryRPMPackageVersions(ctx, packageNames)
	}

	for pkgName, version := range versions {
		facts[packageToFact[pkgName]] = version
	}
}

func (f *FactProvider) queryDebianPackageVersions(ctx context.Context, packageNames []string) map[string]string {
	args := append([]string{"-W", "-f=${Package} ${Version}\n"}, packageNames...)
	result, err := f.options.Runner.Run(ctx, gloutonexec.Option{SkipInContainer: true}, "dpkg-query", args...)

	if errors.Is(err, gloutonexec.ErrExecutionSkipped) {
		return nil
	}

	// dpkg-query exits non-zero if any package is missing, but still outputs the installed ones
	if err != nil && len(result) == 0 {
		logger.V(1).Printf("unable to run dpkg-query: %v", err)

		return nil
	}

	versions := make(map[string]string)

	for line := range strings.SplitSeq(string(result), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] != "" {
			versions[fields[0]] = fields[1]
		}
	}

	return versions
}

func (f *FactProvider) queryRPMPackageVersions(ctx context.Context, packageNames []string) map[string]string {
	args := append([]string{"-q", "--queryformat", "%{NAME} %{VERSION}-%{RELEASE}\n"}, packageNames...)
	result, err := f.options.Runner.Run(ctx, gloutonexec.Option{SkipInContainer: true}, "rpm", args...)

	if errors.Is(err, gloutonexec.ErrExecutionSkipped) {
		return nil
	}

	// rpm writes "package X is not installed" to stdout (not stderr) for missing packages,
	// so result is non-empty even when some packages are absent. len(result) == 0 only
	// when rpm itself failed to run.
	if err != nil && len(result) == 0 {
		logger.V(1).Printf("unable to run rpm: %v", err)

		return nil
	}

	versions := make(map[string]string)

	for line := range strings.SplitSeq(string(result), "\n") {
		fields := strings.Fields(line)
		// "package X is not installed" lines have more than 2 fields — skip them
		if len(fields) == 2 {
			versions[fields[0]] = fields[1]
		}
	}

	return versions
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
