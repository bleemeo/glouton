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

package version

import (
	"runtime"
	"time"
)

//nolint:gochecknoglobals
var (
	// BuildHash is the git hash of the build. (local change ignored).
	BuildHash = "unset"

	// Version is the agent version.
	Version = "0.1"
)

// UserAgent returns the User-Agent for request performed by the agent.
func UserAgent() string {
	return "Glouton " + Version
}

// IsWindows returns true when the current operating system is Windows.
func IsWindows() bool {
	return runtime.GOOS == "windows"
}

// IsLinux returns true when the current operating system is Linux.
func IsLinux() bool {
	return runtime.GOOS == "linux"
}

// IsMacOS returns true when the current operating system is MacOS.
func IsMacOS() bool {
	return runtime.GOOS == "darwin"
}

// IsFreeBSD returns true when the current operating system is FreeBSD.
func IsFreeBSD() bool {
	return runtime.GOOS == "freebsd"
}

type version struct {
	valid bool
	date  time.Time
}

func parse(s string) version {
	for _, pattern := range []string{"06.01.02.150405", "2006.01.02.150405", "2006.01.02", "06.01.02"} {
		t, err := time.Parse(pattern, s)
		if err == nil {
			return version{valid: true, date: t}
		}
	}

	// this is the default value of 'version', but this is the kind of cases where 'explicit is better'
	return version{valid: false}
}

// Compare returns true when v >= base.
// Comparing an invalid version (or a dev version) with any version will return true, as we may lack version
// numbers when testing, but that doesn't mean we don't want to use the bleemeo mode.
func Compare(v string, base string) bool {
	parsedV := parse(v)
	parsedBase := parse(base)

	// when the base version (retrieved from the API) is invalid, we assume we cannot parse it, and the
	// base version is newer than 'v', unless the two versions are identical.
	if !parsedBase.valid {
		return v == base
	}

	if !parsedV.valid {
		return true
	}

	return parsedV.date.After(parsedBase.date) || parsedV.date.Equal(parsedBase.date)
}
