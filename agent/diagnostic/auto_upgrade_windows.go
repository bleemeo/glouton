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

package diagnostic

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/bleemeo/glouton/types"
	"golang.org/x/sys/windows/registry"
)

func (d DiagnosticAutoUpgrade) diagnosticWindowsRegistry(ctx context.Context, archive types.ArchiveWriter) error { //nolint:maintidx
	_ = ctx

	type RegProductInfo1 struct {
		InstallGUID    string
		DisplayVersion string
		EstimatedSize  uint64
		InstallDate    string
		InstallSource  string
		LocalPackage   string
		Version        uint64
		VersionDecoded struct {
			Major int
			Minor int
			Build int
		}
		VersionMajor uint64
		VersionMinor uint64
	}

	type RegProductInfo2 struct {
		InstallGUID    string
		Version        uint64
		VersionDecoded struct {
			Major int
			Minor int
			Build int
		}
		LastUsedSource string
		PackageName    string
	}

	type DiagInfo struct {
		Errors           []string
		InstallDir       string
		Wow64InstallDir  string
		WindowsCurrent   []RegProductInfo1
		ClassesInstaller []RegProductInfo2
	}

	var (
		result DiagInfo
		err    error
	)

	processWindowsCurrentSubKey := func(subkey string) error {
		productPath := `SOFTWARE\Microsoft\Windows\CurrentVersion\Installer\UserData\S-1-5-18\Products\` + subkey + `\InstallProperties`

		ip, err := registry.OpenKey(registry.LOCAL_MACHINE, productPath, registry.QUERY_VALUE)
		if err != nil {
			return fmt.Errorf("can't open %s: %w", productPath, err)
		}
		defer ip.Close()

		displayName, _, err := ip.GetStringValue("DisplayName")
		if err != nil {
			return fmt.Errorf("can't GetStringValue(DisplayName) on %s: %w", productPath, err)
		}

		if displayName != "Glouton" {
			return nil
		}

		rowResult := RegProductInfo1{
			InstallGUID: subkey,
		}

		rowResult.DisplayVersion, _, err = ip.GetStringValue("DisplayVersion")
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("GetStringValue(DisplayVersion) on %s failed: %s", subkey, err.Error()))
		}

		rowResult.InstallDate, _, err = ip.GetStringValue("InstallDate")
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("GetStringValue(InstallDate) on %s failed: %s", subkey, err.Error()))
		}

		rowResult.EstimatedSize, _, err = ip.GetIntegerValue("EstimatedSize")
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("GetIntegerValue(EstimatedSize) on %s failed: %s", subkey, err.Error()))
		}

		rowResult.InstallSource, _, err = ip.GetStringValue("InstallSource")
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("GetStringValue(InstallSource) on %s failed: %s", subkey, err.Error()))
		}

		rowResult.LocalPackage, _, err = ip.GetStringValue("LocalPackage")
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("GetStringValue(LocalPackage) on %s failed: %s", subkey, err.Error()))
		}

		rowResult.Version, _, err = ip.GetIntegerValue("Version")
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("GetIntegerValue(Version) on %s failed: %s", subkey, err.Error()))
		}

		rowResult.VersionDecoded.Major = int((rowResult.Version >> 24) & 0xFF)
		rowResult.VersionDecoded.Minor = int((rowResult.Version >> 16) & 0xFF)
		rowResult.VersionDecoded.Build = int(rowResult.Version & 0xFFFF)

		rowResult.VersionMajor, _, err = ip.GetIntegerValue("VersionMajor")
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("GetIntegerValue(VersionMajor) on %s failed: %s", subkey, err.Error()))
		}

		rowResult.VersionMinor, _, err = ip.GetIntegerValue("VersionMinor")
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("GetIntegerValue(VersionMinor) on %s failed: %s", subkey, err.Error()))
		}

		result.WindowsCurrent = append(result.WindowsCurrent, rowResult)

		return nil
	}

	processClassesInstallerSubKey := func(subkey string) error {
		productPath := `SOFTWARE\Classes\Installer\Products\` + subkey
		sourceListPath := `SOFTWARE\Classes\Installer\Products\` + subkey + `\SourceList`

		key, err := registry.OpenKey(registry.LOCAL_MACHINE, productPath, registry.QUERY_VALUE)
		if err != nil {
			return fmt.Errorf("can't open %s: %w", productPath, err)
		}
		defer key.Close()

		productName, _, err := key.GetStringValue("ProductName")
		if err != nil {
			return fmt.Errorf("can't GetStringValue(ProductName) on %s: %w", productPath, err)
		}

		if productName != "Glouton" {
			return nil
		}

		rowResult := RegProductInfo2{
			InstallGUID: subkey,
		}

		sl, err := registry.OpenKey(registry.LOCAL_MACHINE, sourceListPath, registry.QUERY_VALUE)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("OpenKey(%s) failed: %s", sourceListPath, err.Error()))

			return nil
		}
		defer sl.Close()

		rowResult.LastUsedSource, _, err = sl.GetStringValue("LastUsedSource")
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("GetStringValue(LastUsedSource) on %s failed: %s", subkey, err.Error()))
		}

		rowResult.PackageName, _, err = sl.GetStringValue("PackageName")
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("GetStringValue(PackageName) on %s failed: %s", subkey, err.Error()))
		}

		rowResult.Version, _, err = key.GetIntegerValue("Version")
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("GetIntegerValue(Version) on %s failed: %s", subkey, err.Error()))
		}

		rowResult.VersionDecoded.Major = int((rowResult.Version >> 24) & 0xFF)
		rowResult.VersionDecoded.Minor = int((rowResult.Version >> 16) & 0xFF)
		rowResult.VersionDecoded.Build = int(rowResult.Version & 0xFFFF)

		result.ClassesInstaller = append(result.ClassesInstaller, rowResult)

		return nil
	}

	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `Software\Bleemeo\Glouton`, registry.QUERY_VALUE)
	if err != nil {
		result.Errors = append(result.Errors, `OpenKey(Software\Bleemeo\Glouton) failed: %s`+err.Error())
	} else {
		result.InstallDir, _, err = k.GetStringValue("InstallDir")
		if err != nil {
			result.Errors = append(result.Errors, "GetStringValue(InstallDir) failed: %s"+err.Error())
		}

		_ = k.Close()
	}

	k, err = registry.OpenKey(registry.LOCAL_MACHINE, `Software\Wow6432Node\Bleemeo\Glouton`, registry.QUERY_VALUE)
	if err != nil {
		result.Errors = append(result.Errors, `OpenKey(Software\Wow6432Node\Bleemeo\Glouton) failed: %s`+err.Error())
	} else {
		result.Wow64InstallDir, _, err = k.GetStringValue("InstallDir")
		if err != nil {
			result.Errors = append(result.Errors, "GetStringValue(InstallDir) (Wow64) failed: %s"+err.Error())
		}

		_ = k.Close()
	}

	k, err = registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\Installer\UserData\S-1-5-18\Products`, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		result.Errors = append(result.Errors, `OpenKey(SOFTWARE\Microsoft\Windows\CurrentVersion\Installer\UserData\S-1-5-18\Products) failed: %s`+err.Error())
	} else {
		subkeys, err := k.ReadSubKeyNames(-1)
		if err != nil {
			result.Errors = append(result.Errors, `ReadSubKeyNames on SOFTWARE\Microsoft\Windows\CurrentVersion\Installer\UserData\S-1-5-18\Products failed: %s`+err.Error())
		}

		var lastErr error

		for _, subkey := range subkeys {
			if err := processWindowsCurrentSubKey(subkey); err != nil {
				lastErr = err
			}
		}

		if lastErr != nil && len(result.WindowsCurrent) == 0 {
			result.Errors = append(result.Errors, `Can't find installer information in Windows\CurrentVersion, possibly due to: %s`+lastErr.Error())
		}

		_ = k.Close()
	}

	k, err = registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Classes\Installer\Products`, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		result.Errors = append(result.Errors, `OpenKey(SOFTWARE\Classes\Installer\Products) failed: %s`+err.Error())
	} else {
		subkeys, err := k.ReadSubKeyNames(-1)
		if err != nil {
			result.Errors = append(result.Errors, `ReadSubKeyNames on SOFTWARE\Classes\Installer\Products failed: %s`+err.Error())
		}

		var lastErr error

		for _, subkey := range subkeys {
			if err := processClassesInstallerSubKey(subkey); err != nil {
				lastErr = err
			}
		}

		if lastErr != nil && len(result.ClassesInstaller) == 0 {
			result.Errors = append(result.Errors, `Can't find installer information in Classes\Installer, possibly due to: %s`+lastErr.Error())
		}

		_ = k.Close()
	}

	file, err := archive.Create("auto-upgrade-troubleshooting/windows-registry.json")
	if err != nil {
		return err
	}

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")

	return enc.Encode(result)
}
