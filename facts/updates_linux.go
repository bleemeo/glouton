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

package facts

import (
	"context"
	"glouton/logger"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

func decodeUpdateNotifierFile(content []byte) (pendingUpdates int, pendingSecurityUpdates int) {
	pendingUpdates = -1
	pendingSecurityUpdates = -1
	re := regexp.MustCompile(`^(\d+) [\pL\s]+.$`)
	firstMatch := true

	for _, line := range strings.Split(string(content), "\n") {
		match := re.FindStringSubmatch(line)
		if len(match) > 0 && firstMatch {
			tmp, _ := strconv.ParseInt(match[1], 10, 0)
			pendingUpdates = int(tmp)
			firstMatch = false
		} else if len(match) > 0 {
			tmp, _ := strconv.ParseInt(match[1], 10, 0)
			pendingSecurityUpdates = int(tmp)
		}
	}

	return pendingUpdates, pendingSecurityUpdates
}

func decodeAPTCheck(content []byte) (pendingUpdates int, pendingSecurityUpdates int) {
	pendingUpdates = -1
	pendingSecurityUpdates = -1

	part := strings.Split(string(content), ";")
	if len(part) == 2 {
		tmp, err := strconv.ParseInt(part[0], 10, 0)
		if err == nil {
			pendingUpdates = int(tmp)
		}

		tmp, err = strconv.ParseInt(part[1], 10, 0)
		if err == nil {
			pendingSecurityUpdates = int(tmp)
		}
	}

	return pendingUpdates, pendingSecurityUpdates
}

func decodeAPTGet(content []byte) (pendingUpdates int, pendingSecurityUpdates int) {
	re := regexp.MustCompile(`[^\(]*\(.* (Debian-Security|Ubuntu:[^/]*/[^-]*-security)`)

	for _, line := range strings.Split(string(content), "\n") {
		if !strings.HasPrefix(line, "Inst") {
			continue
		}

		pendingUpdates++

		if re.MatchString(line) {
			pendingSecurityUpdates++
		}
	}

	return pendingUpdates, pendingSecurityUpdates
}

func decodeDNF(content []byte) (pendingUpdates int, pendingSecurityUpdates int) {
	for _, line := range strings.Split(string(content), "\n") {
		if line == "" {
			continue
		}

		if strings.Contains(line, "/Sec.") {
			pendingSecurityUpdates++
		}

		pendingUpdates++
	}

	return
}

func decodeYUMOne(content []byte) int {
	result := 0

	for _, line := range strings.Split(string(content), "\n") {
		if line == "Updated Packages" {
			continue
		}

		if strings.HasPrefix(line, "Repo ") {
			continue
		}

		if len(line) == 0 || line[0] == ' ' {
			continue
		}

		result++
	}

	return result
}

func decodeYUM(content []byte, contentSecurity []byte) (pendingUpdates int, pendingSecurityUpdates int) {
	pendingUpdates = decodeYUMOne(content)
	pendingSecurityUpdates = decodeYUMOne(contentSecurity)

	return
}

func (uf updateFacter) fromUpdateNotifierFile(context.Context) (pendingUpdates int, pendingSecurityUpdates int) {
	updateFile := filepath.Join(uf.HostRootPath, "var/lib/update-notifier/updates-available")

	content, err := ioutil.ReadFile(updateFile)
	if err != nil {
		logger.V(2).Printf("Unable to read file %#v: %v", updateFile, err)

		return -1, -1
	}

	return decodeUpdateNotifierFile(content)
}

func (uf updateFacter) fromAPTCheck(ctx context.Context) (pendingUpdates int, pendingSecurityUpdates int) {
	cmd := exec.CommandContext(ctx, "/usr/lib/update-notifier/apt-check")
	cmd.Env = uf.Environ

	content, err := cmd.CombinedOutput()
	if err != nil {
		logger.V(2).Printf("Unable to execute apt-check: %v", err)

		return -1, -1
	}

	return decodeAPTCheck(content)
}

func (uf updateFacter) fromAPTGet(ctx context.Context) (pendingUpdates int, pendingSecurityUpdates int) {
	cmd := exec.CommandContext(ctx, "apt-get", "--simulate", "-o", "Debug::NoLocking=true", "--quiet", "--quiet", "dist-upgrade")
	cmd.Env = uf.Environ

	content, err := cmd.CombinedOutput()
	if err != nil {
		logger.V(2).Printf("Unable to execute apt-get: %v", err)

		return -1, -1
	}

	return decodeAPTGet(content)
}

func (uf updateFacter) fromDNF(ctx context.Context) (pendingUpdates int, pendingSecurityUpdates int) {
	cmd := exec.CommandContext(ctx, "dnf", "--cacheonly", "--quiet", "updateinfo", "--list")
	cmd.Env = uf.Environ

	content, err := cmd.CombinedOutput()
	if err != nil {
		logger.V(2).Printf("Unable to execute dnf %v", err)

		return -1, -1
	}

	return decodeDNF(content)
}

func (uf updateFacter) fromYUM(ctx context.Context) (pendingUpdates int, pendingSecurityUpdates int) {
	cmd := exec.CommandContext(ctx, "yum", "--cacheonly", "--quiet", "list", "updates")
	cmd.Env = uf.Environ

	content, err := cmd.CombinedOutput()
	if err != nil {
		logger.V(2).Printf("Unable to execute yum: %v", err)

		return -1, -1
	}

	cmd = exec.CommandContext(ctx, "yum", "--cacheonly", "--quiet", "--security", "list", "updates")
	cmd.Env = uf.Environ

	contentSecurity, err := cmd.CombinedOutput()
	if err != nil {
		logger.V(2).Printf("Unable to execute yum: %v", err)

		return -1, -1
	}

	return decodeYUM(content, contentSecurity)
}

func (uf updateFacter) pendingUpdates(ctx context.Context) (pendingUpdates int, pendingSecurityUpdates int) {
	pendingUpdates = -1
	pendingSecurityUpdates = -1

	methods := []func(context.Context) (int, int){
		uf.fromUpdateNotifierFile,
	}

	if !uf.InContainer {
		environ := make([]string, 0, len(os.Environ()))

		for _, s := range os.Environ() {
			if strings.HasPrefix(strings.ToLower(s), "LANG=") {
				continue
			}

			environ = append(environ, s)
		}

		uf.Environ = environ
		methods = append(
			methods,
			uf.fromAPTCheck,
			uf.fromAPTGet,
			uf.fromDNF,
			uf.fromYUM,
		)
	}

	for i, m := range methods {
		pendingUpdates, pendingSecurityUpdates = m(ctx)
		if pendingUpdates != -1 || pendingSecurityUpdates != -1 {
			logger.V(4).Printf("Pending updates calculated with method %d: %d, %d", i, pendingUpdates, pendingSecurityUpdates)

			break
		}
	}

	return pendingUpdates, pendingSecurityUpdates
}
