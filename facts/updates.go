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

package facts

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/utils/gloutonexec"
	"github.com/bleemeo/glouton/version"
)

var errOutDated = errors.New("cache is too old")

type updateFacter struct {
	Runner       *gloutonexec.Runner
	HostRootPath string
	Environ      []string
	InContainer  bool
}

// PendingSystemUpdateFreshness return the indicative value for last update of the preferred method.
// It could be zero if the preferred method don't have any cache or we can't determine the freshness value.
func PendingSystemUpdateFreshness(_ context.Context, runner *gloutonexec.Runner, inContainer bool, hostRootPath string) time.Time {
	if hostRootPath == "" && inContainer {
		return time.Time{}
	}

	uf := updateFacter{
		Runner:       runner,
		HostRootPath: hostRootPath,
		InContainer:  inContainer,
	}

	if version.IsLinux() {
		return uf.freshnessLinux()
	}

	return time.Time{}
}

// PendingSystemUpdate return the number of pending update & pending security update for the system.
// If the value of a field is -1, it means that value is unknown.
func PendingSystemUpdate(ctx context.Context, runner *gloutonexec.Runner, inContainer bool, hostRootPath string) (pendingUpdates int, pendingSecurityUpdates int) {
	if hostRootPath == "" && inContainer {
		return -1, -1
	}

	uf := updateFacter{
		Runner:       runner,
		HostRootPath: hostRootPath,
		InContainer:  inContainer,
	}

	if version.IsLinux() {
		return uf.pendingUpdatesLinux(ctx)
	}

	if version.IsWindows() {
		return uf.pendingUpdatesWindows()
	}

	return -1, -1
}

func (uf updateFacter) pendingUpdatesLinux(ctx context.Context) (pendingUpdates int, pendingSecurityUpdates int) {
	pendingUpdates = -1
	pendingSecurityUpdates = -1

	methods := []func(context.Context) (int, int, error){
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
		a, b, err := m(ctx)
		if err != nil && !errors.Is(err, errOutDated) {
			logger.V(4).Printf("Pending updates method %d failed: %v", i, err)

			continue
		}

		logger.V(4).Printf("Pending updates calculated with method %d: %d, %d", i, a, b)

		if errors.Is(err, errOutDated) {
			logger.V(4).Printf("Pending updates method %d is outdated, continuing with next method", i)

			if pendingUpdates == -1 {
				pendingUpdates = a
			}

			if pendingSecurityUpdates == -1 {
				pendingSecurityUpdates = b
			}

			continue
		}

		if a != -1 {
			pendingUpdates = a
		}

		if b != -1 {
			pendingSecurityUpdates = b
		}

		if pendingUpdates != -1 || pendingSecurityUpdates != -1 {
			break
		}
	}

	logger.V(4).Printf("Pending updates final result: %d, %d", pendingUpdates, pendingSecurityUpdates)

	return pendingUpdates, pendingSecurityUpdates
}

func (uf updateFacter) pendingUpdatesWindows() (pendingUpdates int, pendingSecurityUpdates int) {
	pendingUpdates = -1
	pendingSecurityUpdates = -1

	// Glouton runs under the LocalService account, so we do not have enough permissions to call into
	// the COM object, instead we read a file that a scheduled task is updating periodically.
	content, err := os.ReadFile(`C:\ProgramData\glouton\windowsupdate.txt`)
	if err != nil {
		logger.V(2).Printf("Couldn't retrieve Windows Update information: %v", err)
	}

	splits := strings.Split(string(content), "\r\n")

	// get rid of the trailing newline, if any
	if splits[len(splits)-1] == "" {
		splits = splits[:len(splits)-1]
	}

	if len(splits) != 2 {
		logger.V(2).Printf("Couldn't retrieve Windows Update information: got %d values, expected 2 in %v", len(splits), splits)

		return pendingUpdates, pendingSecurityUpdates
	}

	parsedPendingUpdates, err1 := strconv.Atoi(splits[0])

	parsedPendingSecurityUpdates, err2 := strconv.Atoi(splits[1])
	if err1 != nil || err2 != nil {
		logger.V(2).Printf("Couldn't retrieve Windows Update information: cannot parse the file with content %v", splits)

		return pendingUpdates, pendingSecurityUpdates
	}

	return parsedPendingUpdates, parsedPendingSecurityUpdates
}

func (uf updateFacter) freshnessLinux() time.Time {
	updateFile := filepath.Join(uf.HostRootPath, "var/lib/update-notifier/updates-available")

	stat, err := os.Stat(updateFile)
	if err != nil {
		return time.Time{}
	}

	return stat.ModTime()
}

func (uf updateFacter) fromUpdateNotifierFile(context.Context) (pendingUpdates int, pendingSecurityUpdates int, err error) {
	const maxAge = 2 * 24 * time.Hour

	updateFile := filepath.Join(uf.HostRootPath, "var/lib/update-notifier/updates-available")

	stat, err := os.Stat(updateFile)
	if err != nil {
		return -1, -1, fmt.Errorf("unable to stat file %v: %w", updateFile, err)
	}

	content, err := os.ReadFile(updateFile)
	if err != nil {
		return -1, -1, fmt.Errorf("unable to read file %v: %w", updateFile, err)
	}

	if time.Since(stat.ModTime()) > maxAge {
		err = fmt.Errorf("update-notifier/updates-available: %w", errOutDated)
	}

	pendingUpdates, pendingSecurityUpdates = decodeUpdateNotifierFile(content)

	return pendingUpdates, pendingSecurityUpdates, err
}

func (uf updateFacter) fromAPTCheck(ctx context.Context) (pendingUpdates int, pendingSecurityUpdates int, err error) {
	content, err := uf.Runner.Run(ctx, gloutonexec.Option{Environ: uf.Environ, SkipInContainer: true, CombinedOutput: true}, "/usr/lib/update-notifier/apt-check")
	if err != nil {
		return -1, -1, fmt.Errorf("unable to execute apt-check: %w", err)
	}

	a, b := decodeAPTCheck(content)

	return a, b, nil
}

func (uf updateFacter) fromAPTGet(ctx context.Context) (pendingUpdates int, pendingSecurityUpdates int, err error) {
	content, err := uf.Runner.Run(ctx, gloutonexec.Option{Environ: uf.Environ, SkipInContainer: true, CombinedOutput: true}, "apt-get", "--simulate", "-o", "Debug::NoLocking=true", "--quiet", "--quiet", "dist-upgrade")
	if err != nil {
		logger.V(2).Printf("Unable to execute apt-get: %v", err)

		return -1, -1, fmt.Errorf("unable to execute apt-get: %w", err)
	}

	a, b := decodeAPTGet(content)

	return a, b, nil
}

func (uf updateFacter) fromDNF(ctx context.Context) (pendingUpdates int, pendingSecurityUpdates int, err error) {
	content, err := uf.Runner.Run(ctx, gloutonexec.Option{Environ: uf.Environ, SkipInContainer: true, CombinedOutput: true}, "dnf", "--cacheonly", "--quiet", "updateinfo", "--list")
	if err != nil {
		return -1, -1, fmt.Errorf("unable to execute dnf: %w", err)
	}

	a, b := decodeDNF(content)

	return a, b, nil
}

func (uf updateFacter) fromYUM(ctx context.Context) (pendingUpdates int, pendingSecurityUpdates int, err error) {
	content, err := uf.Runner.Run(ctx, gloutonexec.Option{Environ: uf.Environ, SkipInContainer: true, CombinedOutput: true}, "yum", "--cacheonly", "--quiet", "list", "updates")
	if err != nil {
		return -1, -1, fmt.Errorf("unable to execute yum: %w", err)
	}

	contentSecurity, err := uf.Runner.Run(ctx, gloutonexec.Option{Environ: uf.Environ, SkipInContainer: true, CombinedOutput: true}, "yum", "--cacheonly", "--quiet", "--security", "list", "updates")
	if err != nil {
		return -1, -1, fmt.Errorf("unable to execute yum: %w", err)
	}

	a, b := decodeYUM(content, contentSecurity)

	return a, b, nil
}

func decodeUpdateNotifierFile(content []byte) (pendingUpdates int, pendingSecurityUpdates int) {
	pendingUpdates = -1
	pendingSecurityUpdates = -1
	re := regexp.MustCompile(`^[^\d]*(\d+) .+.$`)
	firstMatch := true

	// The output from update-notifier seems to always have the following:
	// * the number of updates (including security updates)
	// * (optional) the number of security update. This is absent if no security update exists
	//   and if security update exists, that number of updates is > 0
	// * (optional) the number of ESM (paid support) security update on Ubuntu. This
	//   number could exists with number of updates == 0.
	for line := range strings.SplitSeq(string(content), "\n") {
		match := re.FindStringSubmatch(line)
		if len(match) > 0 && firstMatch {
			tmp, _ := strconv.ParseInt(match[1], 10, 0)
			pendingUpdates = int(tmp)
			firstMatch = false
		} else if len(match) > 0 {
			tmp, _ := strconv.ParseInt(match[1], 10, 0)

			// Exclude ESM update.
			if strings.Contains(line, "ESM") {
				continue
			}

			// If number of update is 0, we shouldn't have any security update. It's likely a
			// false detection of ESM-like feature.
			if pendingUpdates == 0 {
				continue
			}

			pendingSecurityUpdates = int(tmp)
		}
	}

	if pendingUpdates != -1 && pendingSecurityUpdates == -1 {
		pendingSecurityUpdates = 0
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

	for line := range strings.SplitSeq(string(content), "\n") {
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
	for line := range strings.SplitSeq(string(content), "\n") {
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

	for line := range strings.SplitSeq(string(content), "\n") {
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
