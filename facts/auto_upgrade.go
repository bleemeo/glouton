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
	// The value is either AutoUpgradeStatusOK or a comma-separated sorted list of the
	// problems found. It is absent when the checks don't apply - non-Linux, in a
	// container, no APT, Glouton not installed from a package - and when nothing was
	// found but a check couldn't run. Absent never means "everything is fine", only
	// AutoUpgradeStatusOK does, and a list is what was found rather than necessarily
	// everything there is.
	FactAutoUpgradeStatus = "auto_upgrade_status"

	// AutoUpgradeStatusOK means every check ran and none found a problem. A check that
	// couldn't run, or whose answer can't be trusted, is never reported as OK.
	AutoUpgradeStatusOK = "ok"

	// AutoUpgradeStatusRepositoryMissing means no repository provides Glouton anymore.
	// This is what an Ubuntu do-release-upgrade does: it comments out third-party
	// sources, and "apt-get install glouton" then succeeds without upgrading anything.
	// Because that failure is silent, it can only be seen by looking at the sources.
	AutoUpgradeStatusRepositoryMissing = "repository_missing"

	// AutoUpgradeStatusLocked means another process holds one of the APT/dpkg lock files
	// and isn't going to release it: held for longer than lockHeldMinDuration, or held
	// by a stopped process. APT doesn't wait for the lock by default, it fails right
	// away with "Could not get lock /var/lib/dpkg/lock-frontend".
	AutoUpgradeStatusLocked = "package_manager_locked"

	// AutoUpgradeStatusDpkgInterrupted means dpkg was interrupted in the middle of a
	// transaction and needs "dpkg --configure -a" before APT accepts to install anything.
	AutoUpgradeStatusDpkgInterrupted = "dpkg_interrupted"

	// AutoUpgradeStatusRunNotFinished means an auto-upgrade run started long ago and
	// never finished.
	AutoUpgradeStatusRunNotFinished = "run_not_finished"

	// factAutoUpgradeEnabled is set on every system where an auto-upgrade exists at all.
	// Its presence is what gates FactAutoUpgradeStatus.
	factAutoUpgradeEnabled = "auto_upgrade_enabled"
)

const (
	// lockHeldMinDuration is how long one of the APT/dpkg locks must have been held
	// before we call it a problem, wherever that duration is knowable at all, see
	// describeHolder. It only has to catch a holder that is running but wedged, since a
	// stopped one is reported whatever its age, so it is set well past what a large
	// upgrade over a slow mirror legitimately takes.
	lockHeldMinDuration = 4 * time.Hour

	// autoUpgradeMinInterval is how long an answer is kept before the checks run again,
	// whatever the pace at which the facts themselves are refreshed.
	autoUpgradeMinInterval = time.Hour

	// autoUpgradeRetryInterval is the same for an answer held back by a package
	// operation running while we looked. That ends on its own, unlike every other reason
	// a check fails, which keep the full interval rather than forking apt-cache again
	// every few minutes for an answer that won't have changed.
	autoUpgradeRetryInterval = 5 * time.Minute

	// autoUpgradeRunMaxDuration is how long after its start an auto-upgrade run is
	// taken for stuck. systemd doesn't time out Type=oneshot units by default, so a
	// glouton-auto-upgrade waiting on a dpkg prompt or a stalled mirror stays there
	// forever, while "apt-get update" plus one package install never takes that long.
	autoUpgradeRunMaxDuration = 2 * time.Hour

	// dpkgJournalDir and dpkgStatusPath are relative to the host root.
	dpkgJournalDir = "var/lib/dpkg/updates"
	dpkgStatusPath = "var/lib/dpkg/status"

	// dpkgStatusSource is the pseudo-source apt-cache uses for the local dpkg database,
	// the only entry of a version table that isn't a repository.
	dpkgStatusSource = "/" + dpkgStatusPath

	procLocksPath = "/proc/locks"
)

var errUnexpectedProcLocks = errors.New("unexpected /proc/locks format")

// gloutonPackageNames are the package names that may provide Glouton, current name first.
var gloutonPackageNames = []string{"glouton", "bleemeo-agent"} //nolint:gochecknoglobals

// packageManagerLockPaths are the lock files APT and dpkg take, relative to the host root
// and in the order APT takes them. The first dpkgLockPathCount are dpkg's own; the others
// only guard APT's downloads.
var packageManagerLockPaths = []string{ //nolint:gochecknoglobals
	"var/lib/dpkg/lock-frontend",
	"var/lib/dpkg/lock",
	"var/lib/apt/lists/lock",
	"var/cache/apt/archives/lock",
}

// dpkgLockPathCount is how many of packageManagerLockPaths are dpkg's own, and so say
// whether an operation touching the dpkg database is running. That is what the dpkg
// checks depend on; the APT download locks say nothing about it.
const dpkgLockPathCount = 2

// shortLivedPackageProcesses are processes whose whole lifetime is one package operation,
// which makes their start time the time the lock was taken. Daemons that take the lock
// from time to time (packagekitd, aptd) must stay out: their age says nothing about it.
//
// Matching is on the process name, which comes from /proc/<pid>/comm and is capped by the
// kernel at 15 characters (TASK_COMM_LEN). Longer names go to shortLivedPackageScripts.
var shortLivedPackageProcesses = map[string]bool{ //nolint:gochecknoglobals
	"apt":      true,
	"apt-get":  true,
	"aptitude": true,
	"dpkg":     true,
}

// shortLivedPackageScripts are the same, for the ones whose name doesn't survive the
// TASK_COMM_LEN cap. A script started through a shebang is named after the script,
// truncated: /usr/bin/unattended-upgrade becomes "unattended-upgr", so the full name is
// only in the command line. python-apt takes /var/lib/dpkg/lock-frontend in that very
// process, which is what makes the extra lookup worth it.
var shortLivedPackageScripts = map[string]bool{ //nolint:gochecknoglobals
	"unattended-upgrade":  true,
	"unattended-upgrades": true,
}

// dpkgBrokenStates are the package states an interrupted dpkg leaves behind. Installing
// anything, Glouton included, then fails until they're repaired. triggers-awaited and
// triggers-pending are deliberately absent: they're normal and APT resolves them itself.
// The error flag of the same Status line is handled separately, see dpkgHasBrokenPackage.
var dpkgBrokenStates = map[string]bool{ //nolint:gochecknoglobals
	"half-installed":  true,
	"half-configured": true,
	"unpacked":        true,
}

// autoUpgradeChecker looks for the known reasons that make the Glouton auto-upgrade
// silently stop working.
type autoUpgradeChecker struct {
	Runner *gloutonexec.Runner
	// AutoUpgradeFile is the marker the glouton-auto-upgrade script maintains while it
	// runs. It's already an absolute path, HostRootPath doesn't apply to it.
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

	// Anything but "/" means APT and dpkg belong to the host, not to us: /proc/locks
	// isn't the host one and there is no apt-cache to run. An empty path is a container
	// whose host root isn't mounted, which is the same case.
	if f.options.HostRootPath != "/" {
		return ""
	}

	// Computing this forks apt-cache and reads the whole dpkg database. Callers refresh
	// the facts far more often than that is worth redoing: mqtt.sendConnectMessage on
	// every MQTT connect, and the duplicate check with no max age at all.
	if time.Now().Before(f.autoUpgradeStatusExpiresAt) {
		return f.autoUpgradeStatusValue
	}

	checker := autoUpgradeChecker{
		Runner:          f.options.Runner,
		AutoUpgradeFile: f.options.AutoUpgradeFile,
		HostRootPath:    f.options.HostRootPath,
	}

	value, retrySoon := checker.status(ctx)

	if ctx.Err() != nil {
		// updateFacts discards the whole refresh. Caching what a cancelled run managed
		// to compute would hide the fact for the interval that follows.
		return ""
	}

	keepFor := autoUpgradeMinInterval
	if retrySoon {
		keepFor = autoUpgradeRetryInterval
	}

	f.autoUpgradeStatusValue = value
	f.autoUpgradeStatusExpiresAt = time.Now().Add(keepFor)

	return f.autoUpgradeStatusValue
}

// status returns the value of the fact, and whether the answer is worth recomputing
// before autoUpgradeMinInterval. Only one thing makes it so: a package operation still
// running while the dpkg state was read, which clears by itself. A check that failed for
// any other reason may well keep failing, and retrying it every few minutes would fork
// apt-cache for nothing.
func (c *autoUpgradeChecker) status(ctx context.Context) (value string, retrySoon bool) {
	// Only Debian-like systems are supported: dnf/yum would need their own repository
	// check.
	if _, err := c.Runner.LookPath("apt-cache", gloutonexec.Option{SkipInContainer: true}); err != nil {
		return "", false
	}

	var (
		problems []string
		// failed records that a check couldn't run, or answered something that can't be
		// trusted. AutoUpgradeStatusOK is then never answered: such a check vouches for
		// nothing.
		failed bool
		// transient records the one cause of that which goes away on its own.
		transient bool
	)

	repository, err := c.repositoryState(ctx)

	switch {
	case err != nil:
		logger.V(1).Printf("auto-upgrade: unable to check the Glouton repository: %v", err)

		failed = true
	case repository == repositoryUnknownPackage:
		// APT doesn't know Glouton at all: it was installed from a tarball or by the
		// installation script without the repository. There is no auto-upgrade to watch.
		return "", false
	case repository == repositoryMissing:
		problems = append(problems, AutoUpgradeStatusRepositoryMissing)
	}

	// These are separate repairs, so each is reported on its own rather than stopping at
	// the first. The dpkg one is conditional, see below: it never appears next to a
	// stuck lock.
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

	// Only a positive answer needs dpkg to be idle. dpkg fills its journal and leaves
	// packages unpacked as it works, tidying up when the transaction ends, so
	// "interrupted" while one of its locks is held can't be told from a healthy run in
	// progress - APT has the same rule, it checks the journal before taking the lock. A
	// negative answer needs nothing: a dirty journal and a half-configured package both
	// survive until they are repaired, so no operation in flight can hide one.
	interrupted, dpkgErr := c.dpkgIsInterrupted()

	// An operation could have started between the lock read and the dpkg one, which
	// would make a positive answer mean nothing again. Reading the locks once more
	// closes that window, and has to happen before the holder is judged so that one
	// appearing in it is still reported.
	if interrupted && lockErr == nil && !locks.dpkgHeld {
		locks, lockErr = c.lockState(ctx)
	}

	record(AutoUpgradeStatusLocked, holderIsStuck(locks.holder), lockErr)

	// A lock file that couldn't be looked at leaves the check without an answer: the
	// paths that were readable being free says nothing about the one that wasn't.
	if locks.unknown {
		failed = true
	}

	switch {
	case dpkgErr != nil:
		record(AutoUpgradeStatusDpkgInterrupted, false, dpkgErr)
	case interrupted && locks.dpkgHeld:
		// An operation holding a dpkg lock is running. It will end, and the journal
		// entries and unpacked packages it is leaving behind will go with it - unless
		// it is the stuck one, which is going to hold the answer back for good.
		failed = true
		transient = !locks.dpkgStuck
	case interrupted && (lockErr != nil || locks.dpkgUnknown):
		// We couldn't tell whether one is.
		failed = true
	default:
		record(AutoUpgradeStatusDpkgInterrupted, interrupted, nil)
	}

	stuck, runErr := c.runIsStuck()
	record(AutoUpgradeStatusRunNotFinished, stuck, runErr)

	if len(problems) == 0 {
		if failed {
			return "", transient
		}

		return AutoUpgradeStatusOK, false
	}

	slices.Sort(problems)

	// A withheld dpkg answer leaves the list short of what it could have held, so it is
	// worth completing sooner than a full answer would be recomputed.
	return strings.Join(problems, ","), transient
}

type repositoryState int

const (
	repositoryOK repositoryState = iota
	repositoryMissing
	repositoryUnknownPackage
)

// repositoryState tells whether APT still has a repository to upgrade Glouton from.
//
// We ask APT rather than parsing /etc/apt/sources.list*: the answer is then the same
// whatever made the repository disappear (lines commented out by do-release-upgrade,
// the file renamed to .distUpgrade, "Enabled: no" in a deb822 .sources, or a suite
// that no longer exists on the mirror after a release upgrade).
func (c *autoUpgradeChecker) repositoryState(ctx context.Context) (repositoryState, error) {
	var lastErr error

	for _, pkg := range gloutonPackageNames {
		// apt-cache reads the APT database read-only and never takes the dpkg lock, so
		// this stays safe to run even while a package operation is in progress.
		output, err := c.Runner.Run(ctx, gloutonexec.Option{SkipInContainer: true}, "apt-cache", "policy", pkg)
		if err != nil {
			lastErr = fmt.Errorf("apt-cache policy %s: %w", pkg, err)

			continue
		}

		installed, hasRepository := decodeAPTCachePolicy(output)
		if !installed {
			continue
		}

		if hasRepository {
			return repositoryOK, nil
		}

		return repositoryMissing, nil
	}

	if lastErr != nil {
		return repositoryOK, lastErr
	}

	return repositoryUnknownPackage, nil
}

// aptPolicySourceRe matches the source lines of an "apt-cache policy" version table,
// e.g. "        500 https://packages.bleemeo.com/... noble/main amd64 Packages" or
// "        100 /var/lib/dpkg/status". Those lines are the only part of the output that
// isn't translated, unlike the "Installed:"/"Candidate:"/"Version table:" labels.
var aptPolicySourceRe = regexp.MustCompile(`^\s+(\d+)\s+(\S+)`)

// decodeAPTCachePolicy parses the output of "apt-cache policy <package>".
//
// installed tells whether the package is installed, from the "***" marker APT puts in
// front of the installed version. hasRepository tells whether at least one version comes
// from a configured repository instead of only from the local dpkg database: when it is
// false, APT has nowhere to upgrade the package from.
//
// An unknown package produces no output at all, so both values are false.
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

// isAPTRepositorySource tells whether a source of an "apt-cache policy" version table is
// a repository. Every source is, except the local dpkg database.
func isAPTRepositorySource(source string) bool {
	if source == dpkgStatusSource {
		return false
	}

	// A repository is either an URI with a scheme (http:, https:, file:, cdrom:, ...)
	// or an absolute path for a local mirror.
	return strings.Contains(source, ":") || strings.HasPrefix(source, "/")
}

// holderIsStuck tells whether the process holding one of the APT/dpkg locks is blocking
// the upgrade rather than doing ordinary work. A nil holder means no lock is held.
func holderIsStuck(holder *lockHolder) bool {
	if holder == nil {
		return false
	}

	// A stopped process won't release the lock on its own, so lockHeldMinDuration
	// doesn't apply to it.
	return holder.stopped || holder.heldFor >= lockHeldMinDuration
}

// dpkgIsInterrupted tells whether dpkg is left in a state that needs a manual
// "dpkg --configure -a" before anything can be installed again.
func (c *autoUpgradeChecker) dpkgIsInterrupted() (bool, error) {
	dirty, err := c.dpkgJournalIsDirty()
	if err != nil || dirty {
		return dirty, err
	}

	return c.dpkgHasBrokenPackage()
}

// dpkgJournalIsDirty looks for the same thing APT checks before taking its lock: entries
// left in the dpkg journal, meaning dpkg was interrupted mid-transaction. APT then refuses
// to do anything with "dpkg was interrupted, you must manually run 'dpkg --configure -a'".
//
// We mirror debSystem::CheckUpdates(): a journal entry is any file whose name is made of
// digits only, an empty one counting as much as a filled one, and any other name ignored.
// A missing journal directory means no journal, hence no interrupted transaction, which
// is also what APT concludes when its opendir() fails.
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

// dpkgHasBrokenPackage looks for packages dpkg left needing repair, either half-way
// through an operation or flagged for reinstallation. APT reports those separately from
// the journal, as unmet dependencies to repair with --fix-broken, and they make
// "apt-get install glouton" fail just the same.
func (c *autoUpgradeChecker) dpkgHasBrokenPackage() (bool, error) {
	file, err := os.Open(filepath.Join(c.HostRootPath, dpkgStatusPath))
	if err != nil {
		return false, fmt.Errorf("unable to read the dpkg database: %w", err)
	}

	defer file.Close()

	scanner := bufio.NewScanner(file)
	// The dpkg database holds a few MB of long Depends and Description lines, give the
	// scanner more room than its 64 kB default.
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

		// The flag is "ok" unless dpkg wants the package reinstalled before anything
		// else happens to it. "apt-get install glouton" then exits 100 with "needs to be
		// reinstalled, but I can't find an archive for it", whatever the state says.
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

// runIsStuck tells whether an auto-upgrade run started and never finished.
//
// glouton-auto-upgrade touches AutoUpgradeFile when it starts and removes it from an EXIT
// trap, and Glouton removes it at startup. So the file still being there long after it was
// created means the run neither completed nor got as far as restarting Glouton, which is
// what a package operation waiting forever on a prompt or a stalled mirror looks like.
func (c *autoUpgradeChecker) runIsStuck() (bool, error) {
	if c.AutoUpgradeFile == "" {
		// The check doesn't apply, which isn't the same as having failed.
		return false, nil
	}

	stat, err := os.Stat(c.AutoUpgradeFile)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil // no run in progress, the usual case
		}

		return false, fmt.Errorf("unable to stat %s: %w", c.AutoUpgradeFile, err)
	}

	if !stat.Mode().IsRegular() {
		// An empty agent.auto_upgrade_file resolves to the state directory itself (see
		// config.Load()), whose mtime is old enough to look like a run that never ended.
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

// lockHolder describes the process holding one of the APT/dpkg lock files.
type lockHolder struct {
	path string
	pid  int
	name string
	// heldFor is how long the lock has been held, which is only knowable for the
	// processes of shortLivedPackageProcesses and shortLivedPackageScripts. It stays
	// zero for every other holder, see describeHolder.
	heldFor time.Duration
	// stopped is set when the holder is in the stopped state (SIGSTOP, or held by a
	// debugger). Such a holder is stuck whatever heldFor says.
	stopped bool
}

// lockState is what /proc/locks says about the APT and dpkg lock files.
type lockState struct {
	// holder is the one worth reporting: the process that looks stuck if any does,
	// otherwise the first lock found held. nil when none of them is.
	holder *lockHolder
	// dpkgHeld says one of dpkg's own locks is held, so an operation that touches the
	// dpkg database is running. APT's download locks don't say that: "apt-get update"
	// takes them without going near the dpkg database.
	dpkgHeld bool
	// dpkgStuck says the holder of one of dpkg's own locks is the stuck one. Such a
	// holder is not going to finish, so what it makes ambiguous stays ambiguous.
	dpkgStuck bool
	// dpkgUnknown says one of dpkg's locks couldn't be looked at, leaving dpkgHeld
	// unanswered either way.
	dpkgUnknown bool
	// unknown says any of the lock files couldn't be looked at, so the check as a whole
	// has no answer: a lock may well be held on a path that was skipped.
	unknown bool
}

// lockState reads who holds the APT/dpkg locks.
//
// The locks are never taken here: this only stats the lock files and reads /proc/locks,
// so the check can't become the blocker it looks for. Both work unprivileged, which
// matters because Glouton runs as the "glouton" user while the lock files are 0640
// root:root (/var/lib/dpkg is 0755, so stat() is enough).
func (c *autoUpgradeChecker) lockState(ctx context.Context) (lockState, error) {
	content, err := os.ReadFile(procLocksPath)
	if err != nil {
		return lockState{}, fmt.Errorf("unable to read %s: %w", procLocksPath, err)
	}

	locks := decodeProcLocks(content)

	var state lockState

	for i, relativePath := range packageManagerLockPaths {
		path := filepath.Join(c.HostRootPath, relativePath)
		isDpkgLock := i < dpkgLockPathCount

		inode, err := statLockInode(path)
		if err != nil {
			// A missing lock file can't be held, so that path is answered. Any other
			// error leaves it unknown: keep going, since the rest still have something
			// to say.
			if !errors.Is(err, fs.ErrNotExist) {
				logger.V(1).Printf("auto-upgrade: unable to stat %s: %v", path, err)

				state.unknown = true
				state.dpkgUnknown = state.dpkgUnknown || isDpkgLock
			}

			continue
		}

		pid, ok := locks[inode]
		if !ok || pid <= 0 {
			// Only open file description locks report -1, and decodeProcLocks keeps
			// POSIX lines alone, so the pid guard is just a safety net.
			continue
		}

		state.dpkgHeld = state.dpkgHeld || isDpkgLock

		holder := &lockHolder{path: path, pid: pid}
		describeHolder(ctx, holder)

		state.dpkgStuck = state.dpkgStuck || (isDpkgLock && holderIsStuck(holder))

		// Several of these are held at once during a transaction, by different
		// processes: APT takes lock-frontend while dpkg takes lock. A stuck holder on
		// any of them is the answer, so a busy but healthy one must not stand in for it.
		if state.holder == nil || (holderIsStuck(holder) && !holderIsStuck(state.holder)) {
			state.holder = holder
		}
	}

	if holderIsStuck(state.holder) {
		logger.V(1).Printf(
			"auto-upgrade: %s is locked by PID %d (%s), stopped=%t, held for %v",
			state.holder.path, state.holder.pid, state.holder.name, state.holder.stopped,
			state.holder.heldFor.Truncate(time.Second),
		)
	}

	return state, nil
}

// describeHolder fills in the holder's name, whether it is stopped, and how long it has
// held the lock.
//
// /proc/locks says who holds a lock but not since when, and these checks run hours apart,
// so seeing the same holder twice proves nothing: a daemon taking the lock for a second
// on each of two runs is indistinguishable from one that never let go. heldFor is
// therefore filled only from the start time of a process whose whole life is one package
// operation, and stays zero otherwise, where only the stopped state counts.
func describeHolder(ctx context.Context, holder *lockHolder) {
	proc, err := process.NewProcessWithContext(ctx, int32(holder.pid)) //nolint:gosec
	if err != nil {
		return
	}

	if status, err := proc.StatusWithContext(ctx); err == nil {
		holder.stopped = slices.Contains(status, process.Stop)
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

	holder.heldFor = time.Since(time.UnixMilli(createdAtMS))
}

// isShortLivedPackageProcess tells whether the whole lifetime of a process is one package
// operation, which is what makes its start time usable as the time the lock was taken.
func isShortLivedPackageProcess(ctx context.Context, proc *process.Process, name string) bool {
	if shortLivedPackageProcesses[name] {
		return true
	}

	args, err := proc.CmdlineSliceWithContext(ctx)
	if err != nil {
		return false
	}

	// "/usr/bin/python3 /usr/bin/unattended-upgrade --download-and-install": running a
	// script through a shebang puts the interpreter first, so the name we want is an
	// argument, at no fixed position.
	for _, arg := range args {
		if shortLivedPackageScripts[filepath.Base(arg)] {
			return true
		}
	}

	return false
}

// decodeProcLocks parses /proc/locks and returns the PID holding each locked file, keyed
// by inode.
//
// A line looks like:
//
//	25: POSIX  ADVISORY  WRITE 7 00:47:1425030 0 EOF
//
// that is: index, type, mandatory/advisory, access, PID, MAJOR:MINOR:INODE, start, end.
// Processes waiting for a lock are listed too, with an extra "->" after the index; only
// the holder interests us.
//
// The device is left out of the key on purpose: /proc/locks prints the device of the
// file's superblock, while stat(2) on btrfs reports a per-subvolume anonymous device, so
// the two never agree there and no lock would ever match. The inode agrees everywhere.
// What that gives up is telling apart two locked files sharing an inode number on
// different filesystems, so only the lock APT and dpkg actually take is kept: a write
// lock over the whole file, never a read or byte-range one of the sort SQLite uses.
func decodeProcLocks(content []byte) map[uint64]int {
	result := make(map[uint64]int)

	for line := range strings.SplitSeq(string(content), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 8 {
			continue
		}

		// Drop the index, then the "->" of the processes blocked on the lock.
		fields = fields[1:]
		if fields[0] == "->" {
			continue
		}

		// type, access, then start and end of the range.
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

		// The first holder wins. A file can't hold two conflicting POSIX locks and
		// waiters are already filtered out, so a second line for the same inode is a
		// different file on another filesystem: see TestDecodeProcLocksSharedInode.
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
