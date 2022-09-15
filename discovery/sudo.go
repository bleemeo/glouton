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

package discovery

import (
	"glouton/logger"
	"os"
	"os/exec"
	"path/filepath"
)

// SudoFileReader read file using sudo cat (or direct read if running as root).
type SudoFileReader struct {
	HostRootPath string
}

// ReadFile does the same as os.ReadFile but use sudo cat.
func (s SudoFileReader) ReadFile(path string) ([]byte, error) {
	path = filepath.Join(s.HostRootPath, path)

	if s.HostRootPath == "" {
		return nil, os.ErrNotExist
	}

	if os.Getuid() == 0 {
		return os.ReadFile(path)
	}

	logger.V(1).Printf("Running sudo -n cat %#v", path)

	cmd := exec.Command(
		"sudo", "-n", "cat", path,
	)

	return cmd.Output()
}
