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

package facts

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/utils/gloutonexec"
	"github.com/bleemeo/glouton/version"

	"github.com/shirou/gopsutil/v4/process"
)

const (
	// FactAutoUpgradeStatus tells whether something prevents the Glouton auto-upgrade
	// from working.
	//
	// The value is AutoUpgradeStatusOK or a sorted, comma-separated list of the problems
	// found. The fact is absent when the checks don't apply (non-Linux, container, no APT,
	// Glouton not installed from a package) or when a check failed and nothing was found.
	FactAutoUpgradeStatus = "auto_upgrade_status"

	// AutoUpgradeStatusOK means every check ran and none found a problem. A check that
	// couldn't run, or whose answer can't be trusted, is never reported as OK.
	AutoUpgradeStatusOK = "ok"

	// AutoUpgradeStatusRepositoryMissing means no configured repository provides Glouton,
	// e.g. after do-release-upgrade commented out third-party sources.
	AutoUpgradeStatusRepositoryMissing = "repository_missing"

	// AutoUpgradeStatusLocked means an APT/dpkg lock is held by a package process that
	// started more than lockHeldMinDuration ago.
	AutoUpgradeStatusLocked = "package_manager_locked"

	// AutoUpgradeStatusDpkgInterrupted means dpkg needs "dpkg --configure -a" before APT
	// can install anything.
	AutoUpgradeStatusDpkgInterrupted = "dpkg_interrupted"

	// AutoUpgradeStatusRunNotFinished means an auto-upgrade run started more than
	// autoUpgradeRunMaxDuration ago and hasn't finished.
	AutoUpgradeStatusRunNotFinished = "run_not_finished"

	// factAutoUpgradeEnabled is set wherever an auto-upgrade exists, which is also what
	// gates FactAutoUpgradeStatus. Both are slow facts: reading it runs a command.
	factAutoUpgradeEnabled = "auto_upgrade_enabled"
)

const (
	// lockHeldMinDuration is how old the process holding a lock must be before the lock
	// is reported. Only processes that live for a single package operation are timed, so
	// their age is an upper bound on how long they have held it. It is well above the
	// duration of a large upgrade.
	lockHeldMinDuration = 4 * time.Hour

	// autoUpgradeMinInterval is how long a status is cached.
	autoUpgradeMinInterval = time.Hour

	// autoUpgradeRetryInterval is how long a status is cached when the check was cut
	// short by autoUpgradeCheckTimeout, which may well not happen again.
	autoUpgradeRetryInterval = 5 * time.Minute

	// dpkgInterruptedMinAge is how long the dpkg database must have been untouched
	// before a broken state is called an interruption. dpkg rewrites it at every state
	// change, so this only has to outlast the gap between two writes - but a maintainer
	// script can block for a long time between them, so it matches lockHeldMinDuration.
	dpkgInterruptedMinAge = 4 * time.Hour

	// autoUpgradeRunMaxDuration is how long after its start an auto-upgrade run is
	// considered stuck.
	autoUpgradeRunMaxDuration = 2 * time.Hour

	// autoUpgradeCheckTimeout bounds the commands and process lookups. File reads are
	// not bounded.
	autoUpgradeCheckTimeout = 30 * time.Second

	// dpkgJournalDir and dpkgStatusPath are relative to the host root.
	dpkgJournalDir = "var/lib/dpkg/updates"
	dpkgStatusPath = "var/lib/dpkg/status"

	// dpkgStatusSource is how apt-cache policy lists the local dpkg database.
	dpkgStatusSource = "/" + dpkgStatusPath

	procLocksPath = "/proc/locks"
)

var errUnexpectedProcLocks = errors.New("unexpected /proc/locks format")

var errAPTHasNoIndex = errors.New("APT has no repository index, run apt-get update")

// gloutonPackageName is the package that provides Glouton. bleemeo-agent only depends
// on it.
const gloutonPackageName = "glouton"

// packageManagerLockPaths are the APT and dpkg lock files, relative to the host root.
var packageManagerLockPaths = []string{ //nolint:gochecknoglobals
	"var/lib/dpkg/lock-frontend",
	"var/lib/dpkg/lock",
	"var/lib/apt/lists/lock",
	"var/cache/apt/archives/lock",
}

// shortLivedPackageProcesses are processes that live for a single package operation, so
// their start time is when the lock was taken. Names are capped at 15 characters by the
// kernel (TASK_COMM_LEN).
var shortLivedPackageProcesses = map[string]bool{ //nolint:gochecknoglobals
	"apt":      true,
	"apt-get":  true,
	"aptitude": true,
	"dpkg":     true,
}

// shortLivedPackageScripts are the same for interpreted scripts, whose process name is
// truncated and which are matched on the command line instead.
var shortLivedPackageScripts = map[string]bool{ //nolint:gochecknoglobals
	"unattended-upgrade":  true,
	"unattended-upgrades": true,
}

// dpkgBrokenStates are the package states left by an interrupted dpkg.
var dpkgBrokenStates = map[string]bool{ //nolint:gochecknoglobals
	"half-installed":  true,
	"half-configured": true,
	"unpacked":        true,
}

// autoUpgradeChecker looks for the known reasons that prevent the Glouton auto-upgrade
// from working.
type autoUpgradeChecker struct {
	Runner *gloutonexec.Runner
	// AutoUpgradeFile is an absolute path, HostRootPath doesn't apply to it.
	AutoUpgradeFile string
	HostRootPath    string
}

// autoUpgradeStatus returns the value of the FactAutoUpgradeStatus fact,
// or "" when the check doesn't apply to this system.
//
// Caller must hold f.l.
func (f *FactProvider) autoUpgradeStatus(ctx context.Context) string {
	if !version.IsLinux() || f.options.Runner == nil {
		return ""
	}

	// APT and dpkg are only checked when Glouton runs on the host itself.
	if f.options.HostRootPath != "/" {
		return ""
	}

	if time.Now().Before(f.autoUpgradeStatusExpiresAt) {
		return f.autoUpgradeStatusValue
	}

	checker := autoUpgradeChecker{
		Runner:          f.options.Runner,
		AutoUpgradeFile: f.options.AutoUpgradeFile,
		HostRootPath:    f.options.HostRootPath,
	}

	checkCtx, cancel := context.WithTimeout(ctx, autoUpgradeCheckTimeout)
	defer cancel()

	value := checker.status(checkCtx)

	// Don't cache the result of a check cancelled by the caller.
	if ctx.Err() != nil {
		return ""
	}

	keepFor := autoUpgradeMinInterval

	// A check the timeout cut short may well answer next time.
	if checkCtx.Err() != nil && value == "" {
		keepFor = autoUpgradeRetryInterval
	}

	f.autoUpgradeStatusValue = value
	f.autoUpgradeStatusExpiresAt = time.Now().Add(keepFor)

	return f.autoUpgradeStatusValue
}

// status returns the value of the fact.
func (c *autoUpgradeChecker) status(ctx context.Context) string {
	// Only APT-based systems are supported.
	if _, err := c.Runner.LookPath("apt-cache", gloutonexec.Option{SkipInContainer: true}); err != nil {
		return ""
	}

	var (
		problems []string
		// failed is set when a check couldn't give an answer.
		failed bool
	)

	repository, err := c.repositoryState(ctx)

	switch {
	case err != nil:
		logger.V(1).Printf("auto-upgrade: unable to check the Glouton repository: %v", err)

		failed = true
	case repository == repositoryUnknownPackage:
		// Glouton wasn't installed from a package.
		return ""
	case repository == repositoryMissing:
		problems = append(problems, AutoUpgradeStatusRepositoryMissing)
	}

	record := func(problem string, found bool, err error) {
		switch {
		case err != nil:
			logger.V(1).Printf("auto-upgrade: unable to check %s: %v", problem, err)

			failed = true
		case found:
			problems = append(problems, problem)
		}
	}

	locks, lockErr := c.lockState(ctx)

	record(AutoUpgradeStatusLocked, holderIsStuck(locks.holder), lockErr)

	if locks.unknown {
		failed = true
	}

	interrupted, dpkgErr := c.dpkgIsInterrupted()
	record(AutoUpgradeStatusDpkgInterrupted, interrupted, dpkgErr)

	stuck, runErr := c.runIsStuck()
	record(AutoUpgradeStatusRunNotFinished, stuck, runErr)

	if len(problems) == 0 {
		if failed {
			return ""
		}

		return AutoUpgradeStatusOK
	}

	slices.Sort(problems)

	return strings.Join(problems, ",")
}

type repositoryState int

const (
	repositoryOK repositoryState = iota
	repositoryMissing
	repositoryUnknownPackage
)

// repositoryState tells whether APT has a repository to upgrade Glouton from.
//
// APT is queried rather than /etc/apt/sources.list*, so any way of disabling the
// repository is detected.
func (c *autoUpgradeChecker) repositoryState(ctx context.Context) (repositoryState, error) {
	// apt-cache doesn't take any lock.
	output, err := c.Runner.Run(
		ctx, gloutonexec.Option{SkipInContainer: true}, "apt-cache", "policy", gloutonPackageName,
	)
	if err != nil {
		return repositoryOK, fmt.Errorf("apt-cache policy %s: %w", gloutonPackageName, err)
	}

	installed, hasRepository := decodeAPTCachePolicy(output)
	if !installed {
		return repositoryUnknownPackage, nil
	}

	if hasRepository {
		return repositoryOK, nil
	}

	// Without any index in /var/lib/apt/lists, no package shows a repository.
	indexed, err := c.aptHasIndexedRepository(ctx)
	if err != nil {
		return repositoryOK, err
	}

	if !indexed {
		return repositoryOK, errAPTHasNoIndex
	}

	return repositoryMissing, nil
}

// aptHasIndexedRepository tells whether APT has at least one repository index.
func (c *autoUpgradeChecker) aptHasIndexedRepository(ctx context.Context) (bool, error) {
	output, err := c.Runner.Run(ctx, gloutonexec.Option{SkipInContainer: true}, "apt-cache", "policy")
	if err != nil {
		return false, fmt.Errorf("apt-cache policy: %w", err)
	}

	_, hasRepository := decodeAPTCachePolicy(output)

	return hasRepository, nil
}

// aptPolicySourceRe matches the source lines of an "apt-cache policy" output, e.g.
// "        500 https://packages.bleemeo.com/... noble/main amd64 Packages". Unlike the
// labels, these lines aren't translated.
var aptPolicySourceRe = regexp.MustCompile(`^\s+(\d+)\s+(\S+)`)

// decodeAPTCachePolicy parses the output of "apt-cache policy [package]".
//
// installed tells whether a version is marked "***". hasRepository tells whether a
// source other than the local dpkg database is listed. Both are false for an unknown
// package, which produces no output.
func decodeAPTCachePolicy(content []byte) (installed bool, hasRepository bool) {
	for line := range strings.SplitSeq(string(content), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "***") {
			installed = true

			continue
		}

		match := aptPolicySourceRe.FindStringSubmatch(line)
		if match == nil {
			continue
		}

		if isAPTRepositorySource(match[2]) {
			hasRepository = true
		}
	}

	return installed, hasRepository
}

// isAPTRepositorySource tells whether a source of "apt-cache policy" is a repository: a
// URI or an absolute path, except the local dpkg database.
func isAPTRepositorySource(source string) bool {
	if source == dpkgStatusSource {
		return false
	}

	return strings.Contains(source, ":") || strings.HasPrefix(source, "/")
}

// holderIsStuck tells whether a lock holder blocks the upgrade. nil means no lock is held.
func holderIsStuck(holder *lockHolder) bool {
	if holder == nil {
		return false
	}

	// A zero start time means the holder isn't a process that lives for a single package
	// operation, so its age says nothing about how long the lock has been held.
	if holder.holderStartTime.IsZero() {
		return false
	}

	return time.Since(holder.holderStartTime) >= lockHeldMinDuration
}

// dpkgIsInterrupted tells whether dpkg needs "dpkg --configure -a" before anything can
// be installed.
//
// An operation in progress leaves the same traces as an interrupted one, so the answer
// only counts once the dpkg database has been untouched for dpkgInterruptedMinAge: dpkg
// rewrites it at every state change, so a running operation keeps it fresh. That also
// spares the scan below on any machine that installed something recently.
func (c *autoUpgradeChecker) dpkgIsInterrupted() (bool, error) {
	stat, err := os.Stat(filepath.Join(c.HostRootPath, dpkgStatusPath))
	if err != nil {
		return false, fmt.Errorf("unable to stat the dpkg database: %w", err)
	}

	if changedAgo := time.Since(stat.ModTime()); changedAgo < dpkgInterruptedMinAge {
		logger.V(2).Printf("auto-upgrade: the dpkg database changed %v ago, too recent to judge", changedAgo.Truncate(time.Second))

		return false, nil
	}

	dirty, err := c.dpkgJournalIsDirty()
	if err != nil || dirty {
		return dirty, err
	}

	return c.dpkgHasBrokenPackage()
}

// dpkgJournalIsDirty tells whether the dpkg journal holds entries, like APT's
// debSystem::CheckUpdates(): any file whose name is only digits. A missing journal
// directory means no entry.
func (c *autoUpgradeChecker) dpkgJournalIsDirty() (bool, error) {
	entries, err := os.ReadDir(filepath.Join(c.HostRootPath, dpkgJournalDir))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}

		return false, fmt.Errorf("unable to read the dpkg journal: %w", err)
	}

	for _, entry := range entries {
		if isAllDigits(entry.Name()) {
			logger.V(1).Printf("auto-upgrade: the dpkg journal still holds %s, dpkg was interrupted", entry.Name())

			return true, nil
		}
	}

	return false, nil
}

// dpkgHasBrokenPackage tells whether a package is in one of dpkgBrokenStates or has an
// error flag (e.g. reinstreq).
func (c *autoUpgradeChecker) dpkgHasBrokenPackage() (bool, error) {
	file, err := os.Open(filepath.Join(c.HostRootPath, dpkgStatusPath))
	if err != nil {
		return false, fmt.Errorf("unable to read the dpkg database: %w", err)
	}

	defer file.Close()

	scanner := bufio.NewScanner(file)
	// Allow lines longer than the default 64 kB.
	scanner.Buffer(nil, 1024*1024)

	for scanner.Scan() {
		// "Status: <want> <flag> <state>", e.g. "Status: install ok half-configured".
		status, found := strings.CutPrefix(scanner.Text(), "Status: ")
		if !found {
			continue
		}

		fields := strings.Fields(status)
		if len(fields) != 3 {
			continue
		}

		if fields[1] != "ok" {
			logger.V(1).Printf("auto-upgrade: at least one package is flagged %q", fields[1])

			return true, nil
		}

		if dpkgBrokenStates[fields[2]] {
			logger.V(1).Printf("auto-upgrade: at least one package is in state %q", fields[2])

			return true, nil
		}
	}

	if err := scanner.Err(); err != nil {
		return false, fmt.Errorf("unable to read the dpkg database: %w", err)
	}

	return false, nil
}

// runIsStuck tells whether an auto-upgrade run started more than
// autoUpgradeRunMaxDuration ago and didn't finish.
//
// glouton-auto-upgrade creates the marker when it starts and removes it when it exits.
// Glouton removes it at startup, so a run killed before its EXIT trap doesn't leave one
// behind forever.
func (c *autoUpgradeChecker) runIsStuck() (bool, error) {
	stat, err := os.Stat(c.AutoUpgradeFile)
	if err != nil {
		// No marker, no run in progress.
		if os.IsNotExist(err) {
			return false, nil
		}

		return false, fmt.Errorf("unable to stat %s: %w", c.AutoUpgradeFile, err)
	}

	// An empty agent.auto_upgrade_file resolves to the state directory.
	if !stat.Mode().IsRegular() {
		return false, nil
	}

	startedAt := stat.ModTime()
	if time.Since(startedAt) < autoUpgradeRunMaxDuration {
		return false, nil
	}

	logger.V(1).Printf("auto-upgrade: the run started at %s never finished", startedAt.Format(time.RFC3339))

	return true, nil
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}

	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}

	return true
}

// lockHolder describes the process holding an APT/dpkg lock file.
type lockHolder struct {
	path string
	pid  int
	name string
	// holderStartTime is when the process started. It is only set for
	// shortLivedPackageProcesses and shortLivedPackageScripts, whose start time is when
	// the lock was taken, and is zero otherwise.
	holderStartTime time.Time
}

// lockState is what /proc/locks says about the APT and dpkg lock files.
type lockState struct {
	// holder is the holder to report among all held locks, nil when none is held.
	holder *lockHolder
	// unknown is set when a lock file couldn't be checked.
	unknown bool
}

// preferStuck returns candidate if it is stuck and current isn't, or if current is nil.
func preferStuck(current, candidate *lockHolder) *lockHolder {
	if current == nil || (holderIsStuck(candidate) && !holderIsStuck(current)) {
		return candidate
	}

	return current
}

// lockState reads who holds the APT/dpkg locks, without taking them.
//
// The lock files are stat'ed, not opened: they are 0640 root:root and Glouton runs as
// the "glouton" user.
func (c *autoUpgradeChecker) lockState(ctx context.Context) (lockState, error) {
	content, err := os.ReadFile(procLocksPath)
	if err != nil {
		return lockState{}, fmt.Errorf("unable to read %s: %w", procLocksPath, err)
	}

	locks := decodeProcLocks(content)

	var state lockState

	for _, lockPath := range packageManagerLockPaths {
		path := filepath.Join(c.HostRootPath, lockPath)

		inode, err := statLockInode(path)
		if err != nil {
			// A missing lock file isn't held.
			if !errors.Is(err, fs.ErrNotExist) {
				logger.V(1).Printf("auto-upgrade: unable to stat %s: %v", path, err)

				state.unknown = true
			}

			continue
		}

		pid, ok := locks[inode]
		if !ok {
			continue
		}

		holder := &lockHolder{path: path, pid: pid}
		describeHolder(ctx, holder)

		state.holder = preferStuck(state.holder, holder)
	}

	if holderIsStuck(state.holder) {
		logger.V(1).Printf(
			"auto-upgrade: %s is locked by PID %d (%s), holder started at %s",
			state.holder.path, state.holder.pid, state.holder.name,
			state.holder.holderStartTime.Format(time.RFC3339),
		)
	}

	return state, nil
}

// describeHolder fills in the holder's name and, for a short-lived package process, the
// start time that says how long it has held the lock.
func describeHolder(ctx context.Context, holder *lockHolder) {
	proc, err := process.NewProcessWithContext(ctx, int32(holder.pid)) //nolint:gosec
	if err != nil {
		return
	}

	holder.name, err = proc.NameWithContext(ctx)
	if err != nil {
		return
	}

	if !isShortLivedPackageProcess(ctx, proc, holder.name) {
		return
	}

	createdAtMS, err := proc.CreateTimeWithContext(ctx)
	if err != nil {
		return
	}

	holder.holderStartTime = time.UnixMilli(createdAtMS)
}

// isShortLivedPackageProcess tells whether a process lives for a single package operation.
func isShortLivedPackageProcess(ctx context.Context, proc *process.Process, name string) bool {
	if shortLivedPackageProcesses[name] {
		return true
	}

	args, err := proc.CmdlineSliceWithContext(ctx)
	if err != nil {
		return false
	}

	// The script can be at any position, e.g. "/usr/bin/python3 /usr/bin/unattended-upgrade".
	// It must be an executable file: /var/log/unattended-upgrades is a directory. Only an
	// absolute path is checked, a relative one is read against the holder's working
	// directory rather than ours.
	for _, arg := range args {
		if shortLivedPackageScripts[filepath.Base(arg)] && filepath.IsAbs(arg) && isExecutableFile(arg) {
			return true
		}
	}

	return false
}

// isExecutableFile tells whether a path is an executable regular file.
func isExecutableFile(path string) bool {
	stat, err := os.Stat(path)

	return err == nil && stat.Mode().IsRegular() && stat.Mode().Perm()&0o111 != 0
}

// decodeProcLocks parses /proc/locks and returns the PID holding each locked file, keyed
// by inode.
//
// A line looks like:
//
//	25: POSIX  ADVISORY  WRITE 7 00:47:1425030 0 EOF
//
// that is: index, type, mandatory/advisory, access, PID, MAJOR:MINOR:INODE, start, end.
// Waiting processes have "->" after the index and are skipped.
//
// The key is the inode alone because on btrfs the device in /proc/locks differs from
// the one stat(2) returns. Only whole-file POSIX write locks are kept, which is what APT
// and dpkg take.
func decodeProcLocks(content []byte) map[uint64]int {
	result := make(map[uint64]int)

	for line := range strings.SplitSeq(string(content), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 8 {
			continue
		}

		fields = fields[1:]
		if fields[0] == "->" {
			continue
		}

		if fields[0] != "POSIX" || fields[2] != "WRITE" || fields[5] != "0" || fields[6] != "EOF" {
			continue
		}

		pid, err := strconv.Atoi(fields[3])
		if err != nil {
			continue
		}

		inode, err := decodeProcLocksInode(fields[4])
		if err != nil {
			continue
		}

		// On an inode collision across filesystems, the first holder is kept.
		if _, ok := result[inode]; !ok {
			result[inode] = pid
		}
	}

	return result
}

func decodeProcLocksInode(field string) (uint64, error) {
	parts := strings.Split(field, ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("%w: %q isn't a MAJOR:MINOR:INODE triplet", errUnexpectedProcLocks, field)
	}

	inode, err := strconv.ParseUint(parts[2], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %q: %w", errUnexpectedProcLocks, field, err)
	}

	return inode, nil
}
