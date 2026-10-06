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
	// factAutoUpgradeStatus tells whether something prevents the Glouton auto-upgrade
	// from working.
	//
	// The value is autoUpgradeStatusOK or a sorted, comma-separated list of the problems
	// found. A check that can't answer keeps the answer it gave on the previous refresh.
	// The fact is absent when the checks don't apply (non-Linux, container, no APT,
	// Glouton not installed from a package), or when a check couldn't answer, nothing was
	// found and there is no previous refresh to take its answer from.
	factAutoUpgradeStatus = "auto_upgrade_status"

	// autoUpgradeStatusOK means no check found a problem. A check that couldn't answer
	// only counts as OK when it was OK on the previous refresh.
	autoUpgradeStatusOK = "ok"

	// autoUpgradeStatusRepositoryMissing means no configured repository provides Glouton,
	// e.g. after do-release-upgrade commented out third-party sources.
	autoUpgradeStatusRepositoryMissing = "repository_missing"

	// autoUpgradeStatusLocked means an APT/dpkg lock is held by a package process that
	// started more than lockHeldMinDuration ago.
	autoUpgradeStatusLocked = "package_manager_locked"

	// autoUpgradeStatusDpkgInterrupted means dpkg needs "dpkg --configure -a" before APT
	// can install anything.
	autoUpgradeStatusDpkgInterrupted = "dpkg_interrupted"

	// autoUpgradeStatusRunNotFinished means the glouton-auto-upgrade service started more
	// than autoUpgradeRunMaxDuration ago and hasn't finished. Until it does, the timer
	// starts no other run.
	autoUpgradeStatusRunNotFinished = "run_not_finished"

	// factAutoUpgradeEnabled is set wherever the glouton-auto-upgrade timer unit is
	// installed, which is also what gates factAutoUpgradeStatus. A manual install has no
	// auto-upgrade to enable, reporting false would ask the user to enable it. Both are
	// slow facts: reading them runs commands.
	factAutoUpgradeEnabled = "auto_upgrade_enabled"

	// autoUpgradeServiceUnit is the unit the glouton-auto-upgrade timer starts.
	autoUpgradeServiceUnit = "glouton-auto-upgrade.service"
)

const (
	// lockHeldMinDuration is how old the process holding a lock must be before the lock
	// is reported. Only processes that live for a single package operation are timed, so
	// their age is an upper bound on how long they have held it. It is well above the
	// duration of a large upgrade.
	lockHeldMinDuration = 4 * time.Hour

	// autoUpgradeRunMaxDuration is how long after its start an auto-upgrade run is
	// considered stuck.
	autoUpgradeRunMaxDuration = 2 * time.Hour

	// autoUpgradeCheckTimeout bounds the commands and process lookups of each of
	// autoUpgradeIsEnabled and autoUpgradeStatus. File reads are not bounded.
	autoUpgradeCheckTimeout = 30 * time.Second

	// dpkgJournalDir and dpkgStatusPath are relative to the host root.
	dpkgJournalDir = "var/lib/dpkg/updates"
	dpkgStatusPath = "var/lib/dpkg/status"

	// dpkgStatusSource is how apt-cache policy lists the local dpkg database.
	dpkgStatusSource = "/" + dpkgStatusPath

	procLocksPath = "/proc/locks"
)

var errUnexpectedProcLocks = errors.New("unexpected /proc/locks format")

var errUnexpectedSystemctlShow = errors.New("unexpected systemctl show output")

var errAPTHasNoIndex = errors.New("APT has no repository index, run apt-get update")

// errAutoUpgradeCheckFailed is returned when a check couldn't give an answer, no other
// check found a problem and there is no previous answer to fall back on.
var errAutoUpgradeCheckFailed = errors.New("an auto-upgrade check couldn't give an answer")

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
	HostRootPath string

	// How the checker reaches the system, replaced in tests. See newAutoUpgradeChecker.
	run           runFunc
	lookPath      func(file string, option gloutonexec.Option) (string, error)
	procLocksPath string
	monotonicNow  func() (time.Duration, error)
}

func newAutoUpgradeChecker(runner *gloutonexec.Runner, hostRootPath string) *autoUpgradeChecker {
	return &autoUpgradeChecker{
		HostRootPath:  hostRootPath,
		run:           runner.Run,
		lookPath:      runner.LookPath,
		procLocksPath: procLocksPath,
		monotonicNow:  monotonicNow,
	}
}

// autoUpgradeStatus returns the value of the factAutoUpgradeStatus fact, or "" when the
// check doesn't apply to this system. previous is the value of the previous refresh, ""
// when there is none. It must only be called when autoUpgradeIsEnabled found the
// auto-upgrade timer unit: that excludes a container and a nil Runner.
func (f *FactProvider) autoUpgradeStatus(ctx context.Context, previous string) (string, error) {
	if !version.IsLinux() {
		return "", nil
	}

	checker := newAutoUpgradeChecker(f.options.Runner, f.options.HostRootPath)

	ctx, cancel := context.WithTimeout(ctx, autoUpgradeCheckTimeout)
	defer cancel()

	return checker.status(ctx, previous)
}

// checkVerdict is the answer of one check.
type checkVerdict int

const (
	// verdictUnknown means the check couldn't give an answer.
	verdictUnknown checkVerdict = iota
	verdictOK
	verdictProblem
)

// status returns the value of the fact, or errAutoUpgradeCheckFailed when no answer can
// be given, see combineVerdicts.
func (c *autoUpgradeChecker) status(ctx context.Context, previous string) (string, error) {
	// Only APT-based systems are supported.
	if _, err := c.lookPath("apt-cache", gloutonexec.Option{SkipInContainer: true}); err != nil {
		return "", nil //nolint:nilerr // Not an APT system, the check doesn't apply.
	}

	verdicts := make(map[string]checkVerdict, 4)

	record := func(problem string, found bool, err error) {
		switch {
		case err != nil:
			logger.V(1).Printf("auto-upgrade: unable to check %s: %v", problem, err)

			verdicts[problem] = verdictUnknown
		case found:
			verdicts[problem] = verdictProblem
		default:
			verdicts[problem] = verdictOK
		}
	}

	repository, err := c.repositoryState(ctx)
	if err == nil && repository == repositoryUnknownPackage {
		// Glouton wasn't installed from a package.
		return "", nil
	}

	record(autoUpgradeStatusRepositoryMissing, repository == repositoryMissing, err)

	locks, err := c.lockState(ctx)

	if err == nil && locks.unknown && !holderIsStuck(locks.holder) {
		// lockState already logged which lock file it couldn't check.
		verdicts[autoUpgradeStatusLocked] = verdictUnknown
	} else {
		record(autoUpgradeStatusLocked, holderIsStuck(locks.holder), err)
	}

	// Only when no lock is known to be held: a running dpkg writes each state change to its
	// journal and only folds it back into the status file once the operation ends, so it
	// looks exactly like an interrupted one until then. A successful "apt-get install" on
	// Ubuntu 24.04 shows up to 34 journal entries while it runs. The Glouton auto-upgrade
	// is the worst case: its postinst restarts Glouton while dpkg still runs.
	if err == nil && !locks.unknown && locks.holder == nil {
		interrupted, err := c.dpkgIsInterrupted()
		record(autoUpgradeStatusDpkgInterrupted, interrupted, err)
	} else {
		// Routine, e.g. while unattended-upgrades runs: not worth more than a debug line.
		logger.V(2).Printf("auto-upgrade: dpkg check skipped, an APT/dpkg lock is held or couldn't be checked")

		verdicts[autoUpgradeStatusDpkgInterrupted] = verdictUnknown
	}

	stuck, err := c.runIsStuck(ctx)
	record(autoUpgradeStatusRunNotFinished, stuck, err)

	return combineVerdicts(verdicts, previous)
}

// combineVerdicts returns the value of the fact from the verdict of each check, keyed by
// the problem it looks for.
//
// A check that couldn't answer keeps the answer it gave in previous, the value of the
// previous refresh: dropping a problem would tell the API it's fixed, and reporting one
// would be a guess. A check is routinely unable to answer, e.g. the dpkg check while
// unattended-upgrades holds a lock. With no previous value, a check that couldn't answer
// is never reported as OK: errAutoUpgradeCheckFailed is returned when no other check
// found a problem.
func combineVerdicts(verdicts map[string]checkVerdict, previous string) (string, error) {
	var previousProblems []string

	if previous != "" && previous != autoUpgradeStatusOK {
		previousProblems = strings.Split(previous, ",")
	}

	var (
		problems   []string
		unanswered bool
	)

	for problem, verdict := range verdicts {
		switch verdict {
		case verdictProblem:
			problems = append(problems, problem)
		case verdictUnknown:
			if previous == "" {
				unanswered = true
			} else if slices.Contains(previousProblems, problem) {
				problems = append(problems, problem)
			}
		case verdictOK:
		}
	}

	if len(problems) == 0 {
		if unanswered {
			return "", errAutoUpgradeCheckFailed
		}

		return autoUpgradeStatusOK, nil
	}

	slices.Sort(problems)

	return strings.Join(problems, ","), nil
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
	output, err := c.run(
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
	output, err := c.run(ctx, gloutonexec.Option{SkipInContainer: true}, "apt-cache", "policy")
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
// An operation in progress leaves the same traces as an interrupted one, so this must
// only be called when no APT/dpkg lock is held. The age of the status file can't tell
// them apart: dpkg only rewrites it once the operation ends, so it keeps the date of the
// previous one meanwhile.
func (c *autoUpgradeChecker) dpkgIsInterrupted() (bool, error) {
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

// runIsStuck tells whether the glouton-auto-upgrade service started more than
// autoUpgradeRunMaxDuration ago and didn't finish.
//
// systemd is asked rather than relying on the marker file the script writes. Glouton
// removes that marker at startup, which the upgrade itself causes mid-run. dash skips the
// script's EXIT trap when a signal kills it, which leaves the marker behind. And the script
// hardcodes its path while Glouton's is configurable. The service is a oneshot without a
// start timeout, so a run that hangs keeps it "activating" for as long as it hangs.
func (c *autoUpgradeChecker) runIsStuck(ctx context.Context) (bool, error) {
	output, err := c.run(
		ctx,
		gloutonexec.Option{SkipInContainer: true},
		"systemctl", "show", "--property=ActiveState,InactiveExitTimestampMonotonic", autoUpgradeServiceUnit,
	)
	if err != nil {
		return false, fmt.Errorf("systemctl show %s: %w", autoUpgradeServiceUnit, err)
	}

	now, err := c.monotonicNow()
	if err != nil {
		return false, err
	}

	runningFor, err := decodeRunDuration(output, now)
	if err != nil {
		return false, err
	}

	if runningFor < autoUpgradeRunMaxDuration {
		return false, nil
	}

	logger.V(1).Printf("auto-upgrade: %s has been running for %s", autoUpgradeServiceUnit, runningFor.Truncate(time.Second))

	return true, nil
}

// decodeRunDuration parses the output of "systemctl show
// --property=ActiveState,InactiveExitTimestampMonotonic" and returns how long the unit has
// been starting, 0 when it isn't. now is the CLOCK_MONOTONIC time, which is the clock of
// the *Monotonic timestamps.
//
// A oneshot service stays "activating" while its command runs, and InactiveExitTimestamp
// is when it left "inactive". An unknown unit is "inactive".
func decodeRunDuration(output []byte, now time.Duration) (time.Duration, error) {
	properties := make(map[string]string, 2)

	for line := range strings.SplitSeq(string(output), "\n") {
		if key, value, found := strings.Cut(line, "="); found {
			properties[key] = strings.TrimSpace(value)
		}
	}

	activeState, ok := properties["ActiveState"]
	if !ok {
		return 0, fmt.Errorf("%w: no ActiveState in %q", errUnexpectedSystemctlShow, output)
	}

	if activeState != "activating" {
		return 0, nil
	}

	startedAtUS, err := strconv.ParseUint(properties["InactiveExitTimestampMonotonic"], 10, 63)
	if err != nil || startedAtUS == 0 {
		return 0, fmt.Errorf("%w: no start time in %q", errUnexpectedSystemctlShow, output)
	}

	return max(now-time.Duration(startedAtUS)*time.Microsecond, 0), nil
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
	content, err := os.ReadFile(c.procLocksPath)
	if err != nil {
		return lockState{}, fmt.Errorf("unable to read %s: %w", c.procLocksPath, err)
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
