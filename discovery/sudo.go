// Copyright 2015-2026 Bleemeo
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
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/utils/gloutonexec"
)

// SudoFileReader read file using sudo cat (or direct read if running as root).
type SudoFileReader struct {
	HostRootPath string
	Runner       *gloutonexec.Runner
}

// ReadFile does the same as os.ReadFile but use sudo cat.
func (s SudoFileReader) ReadFile(ctx context.Context, path string) ([]byte, error) {
	path = filepath.Join(s.HostRootPath, path)

	if s.HostRootPath == "" {
		return nil, os.ErrNotExist
	}

	if os.Getuid() == 0 {
		return os.ReadFile(path)
	}

	logger.V(1).Printf("Running sudo -n cat %#v", path)

	return s.Runner.Run(ctx, gloutonexec.Option{RunAsRoot: true}, "cat", path)
}

// ReadDir does the same as os.ReadDir but use sudo ls, and returns the entry names only.
func (s SudoFileReader) ReadDir(ctx context.Context, path string) ([]string, error) {
	if s.HostRootPath == "" {
		return nil, os.ErrNotExist
	}

	path = filepath.Join(s.HostRootPath, path)

	if os.Getuid() == 0 {
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil, err
		}

		names := make([]string, 0, len(entries))

		for _, entry := range entries {
			names = append(names, entry.Name())
		}

		return names, nil
	}

	logger.V(1).Printf("Running sudo -n ls -1 %#v", path)

	// "ls -1" rather than "find -printf": one name per line is all that is needed, and
	// -printf is a GNU extension this can't count on wherever Glouton runs.
	output, err := s.Runner.Run(ctx, gloutonexec.Option{RunAsRoot: true}, "ls", "-1", path) //nolint:exhaustruct
	if err != nil {
		return nil, err
	}

	return lsNames(output), nil
}

// lsNames splits the output of "ls -1" into entry names. A name containing a newline
// would come back as two entries, which is a limitation of reading a directory through a
// command at all -- the caller is expected to check what it found rather than trust it.
func lsNames(output []byte) []string {
	lines := strings.Split(string(output), "\n")
	names := make([]string, 0, len(lines))

	for _, line := range lines {
		if name := strings.TrimSpace(line); name != "" {
			names = append(names, name)
		}
	}

	return names
}
