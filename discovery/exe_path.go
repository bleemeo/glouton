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
	"io/fs"
	"strings"
)

// guessExecutable returns the executable a process most likely runs, read from its
// command line, for when the process lister couldn't read /proc/PID/exe: an unprivileged
// Glouton, as the packaged one is, can't read it for processes of other users.
//
// Like psutil (which the Python agent used) does, the command line is only trusted when
// its first argument is an absolute path to an executable file. Some processes rewrite
// their command line into a single string ("/usr/bin/redis-server 127.0.0.1:6379",
// "nginx: master process /usr/sbin/nginx"), so the first word of it that is such a
// path is used otherwise.
//
// hostFS is the host's filesystem, with paths relative to its root. The guess is
// disabled when it is nil.
func guessExecutable(hostFS fs.FS, cmdLine []string) string {
	if hostFS == nil || len(cmdLine) == 0 {
		return ""
	}

	candidates := []string{cmdLine[0]}

	if strings.Contains(cmdLine[0], " ") {
		candidates = append(candidates, strings.Fields(cmdLine[0])...)
	}

	for _, candidate := range candidates {
		if isExecutableFile(hostFS, candidate) {
			return candidate
		}
	}

	return ""
}

func isExecutableFile(hostFS fs.FS, path string) bool {
	name, ok := strings.CutPrefix(path, "/")
	if !ok {
		return false
	}

	info, err := fs.Stat(hostFS, name)

	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}
