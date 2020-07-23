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

package version

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
)

//nolint:gochecknoglobals
var (
	// BuildHash is the git hash of the build. (local change ignored)
	BuildHash = "unset"

	// Version is the agent version
	Version = "0.1"
)

// UserAgent returns the User-Agent for request performed by the agent.
func UserAgent() string {
	return fmt.Sprintf("Glouton %s", Version)
}

// IsWindows returns true when the current operating system is windows.
func IsWindows() bool {
	return runtime.GOOS == "windows"
}

// we do not store or compare git revisions, as we have no easy way to compare them without embedding the git
// history in the binaries, and we very much not want to do that.
type version struct {
	day   int
	month int
	year  int
}

func parse(s string) *version {
	splits := strings.SplitN(s, ".", 4)
	if len(splits) != 4 {
		return nil
	}

	year, err1 := strconv.Atoi(splits[0])
	month, err2 := strconv.Atoi(splits[1])
	day, err3 := strconv.Atoi(splits[2])

	if err1 != nil || err2 != nil || err3 != nil {
		return nil
	}

	if year < 100 {
		year += 2000
	}

	return &version{day: day, month: month, year: year}
}

// Compare returns true when v >= base.
// Comparing an invalid version (or a dev version) with any version will return true, as we may lack version
// numbers when testing, but that doesn't men we don't want to use the bleemeo mode.
// Comparing any valid version with an invalid (or dev) version will return false.
func Compare(v string, base string) bool {
	parsedV := parse(v)
	parsedBase := parse(base)

	if parsedBase == nil {
		return parsedV == nil
	}

	if parsedV == nil {
		return true
	}

	return parsedV.year > parsedBase.year ||
		(parsedV.year == parsedBase.year && parsedV.month > parsedBase.month) ||
		(parsedV.year == parsedBase.year && parsedV.month == parsedBase.month && parsedV.day >= parsedBase.day)
}
