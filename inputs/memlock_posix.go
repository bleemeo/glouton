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

//go:build !windows

package inputs

import (
	"github.com/bleemeo/glouton/logger"

	"golang.org/x/sys/unix"
)

func getLockedMemoryLimit() uint64 {
	var limit unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_MEMLOCK, &limit); err != nil {
		logger.V(0).Printf("Cannot get locked memory limit: %v", err)

		return 0
	}

	return uint64(limit.Max) //nolint:unconvert, nolintlint // required for e.g., FreeBSD that has the field as int64
}
