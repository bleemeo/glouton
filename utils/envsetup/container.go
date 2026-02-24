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

package envsetup

import (
	"os"
	"path/filepath"

	"github.com/bleemeo/glouton/logger"
)

// SetupContainer will tune container to improve information gathered.
// Mostly it make that access to file pass though hostroot by altering process's environment.
func SetupContainer(hostRootPath string) {
	if hostRootPath == "" {
		logger.Printf("The agent is running in a container but GLOUTON_DF_HOST_MOUNT_POINT is unset. Some information will be missing")

		return
	}

	if _, err := os.Stat(hostRootPath); os.IsNotExist(err) {
		logger.Printf("The agent is running in a container but host / partition is not mounted on %#v. Some information will be missing", hostRootPath)
		logger.Printf("Hint: to fix this issue when using Docker, add \"-v /:%v:ro\" when running the agent", hostRootPath)

		return
	}

	if hostRootPath != "" && hostRootPath != "/" {
		if os.Getenv("HOST_VAR") == "" {
			// gopsutil will use HOST_VAR as prefix to host /var
			// It's used at least for reading the number of connected user from /var/run/utmp
			_ = os.Setenv("HOST_VAR", filepath.Join(hostRootPath, "var"))

			// ... but /var/run is usually a symlink to /run.
			varRun := filepath.Join(hostRootPath, "var/run")

			target, err := os.Readlink(varRun)
			if err == nil && target == "/run" {
				_ = os.Setenv("HOST_VAR", hostRootPath)
			}
		}

		if os.Getenv("HOST_ETC") == "" {
			_ = os.Setenv("HOST_ETC", filepath.Join(hostRootPath, "etc"))
		}

		if os.Getenv("HOST_PROC") == "" {
			_ = os.Setenv("HOST_PROC", filepath.Join(hostRootPath, "proc"))
		}

		if os.Getenv("HOST_SYS") == "" {
			_ = os.Setenv("HOST_SYS", filepath.Join(hostRootPath, "sys"))
		}

		if os.Getenv("HOST_RUN") == "" {
			_ = os.Setenv("HOST_RUN", filepath.Join(hostRootPath, "run"))
		}

		if os.Getenv("HOST_DEV") == "" {
			_ = os.Setenv("HOST_DEV", filepath.Join(hostRootPath, "dev"))
		}

		if os.Getenv("HOST_MOUNT_PREFIX") == "" {
			_ = os.Setenv("HOST_MOUNT_PREFIX", hostRootPath)
		}
	}
}
