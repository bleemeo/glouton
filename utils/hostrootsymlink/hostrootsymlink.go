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

package hostrootsymlink

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// EvalSymlinks does the same as Golang filepath.EvalSymlinks but
// take in account that hostroot could by something else than "/".
//
// Therefor with a symlink that use "/var/log/syslog" as target, it
// consider that real path is /hostroot/var/log/syslog. It will
// try to follow possible additional links in this path.
//
// Note: the result is the path *without* /hostroot prefix, this is
// such are result could be used in any of the function that always
// add /hostroot prefix.
//
// This function only works with absolute path. Relative path are not handled and passed
// to filepath.EvalSymlinks (which ignore hostroot).
//
// Unlike filepath.EvalSymlinks, if a path isn't found it stop the transformation and return with all
// change already performed, but no error.
func EvalSymlinks(hostroot string, path string) string {
	return evalSymlinks(hostroot, path, os.Lstat, os.Readlink)
}

func evalSymlinks(hostroot string, path string, statImpl func(string) (os.FileInfo, error), readLinkImpl func(string) (string, error)) string {
	if !filepath.IsAbs(path) {
		result, err := filepath.EvalSymlinks(path)
		if err != nil {
			return path
		}

		return result
	}

	pathComponents := strings.Split(path, "/")

	finalComponents := expandSymlinks(hostroot, nil, pathComponents, statImpl, readLinkImpl, 0)

	return strings.Join(finalComponents, "/")
}

func expandSymlinks(hostroot string, prefix []string, pathComponents []string, statImpl func(string) (os.FileInfo, error), readLinkImpl func(string) (string, error), depth int) []string {
	const maxDepth = 20

	if depth >= maxDepth {
		return append(prefix, pathComponents...)
	}

	for idx, component := range pathComponents {
		if component == "" {
			prefix = append(prefix, component)

			continue
		}

		directoryInHostroot := strings.Join(prefix, "/")
		pathWithHostRoot := filepath.Join(hostroot, directoryInHostroot, component)

		fi, err := statImpl(pathWithHostRoot)
		if err != nil {
			prefix = append(prefix, pathComponents[idx:]...)

			return prefix
		}

		if fi.Mode()&fs.ModeSymlink == 0 {
			prefix = append(prefix, component)

			continue
		}

		target, err := readLinkImpl(pathWithHostRoot)
		if err != nil {
			prefix = append(prefix, pathComponents[idx:]...)

			return prefix
		}

		if filepath.IsAbs(target) {
			targetComponents := strings.Split(target, "/")

			return expandSymlinks(hostroot, nil, append(targetComponents, pathComponents[idx+1:]...), statImpl, readLinkImpl, depth+1)
		}

		targetComponents := strings.Split(filepath.Join(directoryInHostroot, target), "/")

		return expandSymlinks(hostroot, nil, append(targetComponents, pathComponents[idx+1:]...), statImpl, readLinkImpl, depth+1)
	}

	return prefix
}
