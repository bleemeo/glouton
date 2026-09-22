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
	// problems found. The fact is absent when Glouton can't run the check: non-Linux,
	// running in a container, no APT, or Glouton not installed from a package. Absent
	// therefore never means "everything is fine", only AutoUpgradeStatusOK does.
	FactAutoUpgradeStatus = "auto_upgrade_status"

	// AutoUpgradeStatusOK means every check ran and none found a problem. A check that
	// couldn't run is never reported as OK, see status().
	AutoUpgradeStatusOK = "ok"

	// AutoUpgradeStatusRepositoryMissing means no repository provides Glouton anymore.
	// This is what an Ubuntu do-release-upgrade does: it comments out third-party
	// sources, and "apt-get install glouton" then succeeds without upgrading anything.
	// Because that failure is silent, it can only be seen by looking at the sources.
	AutoUpgradeStatusRepositoryMissing = "repository_missing"

	// AutoUpgradeStatusLocked means one of the APT/dpkg lock files has been held for a
	// long time by another process. APT doesn't wait for the lock by default, it fails
	// right away with "Could not get lock /var/lib/dpkg/lock-frontend".
	AutoUpgradeStatusLocked = "package_manager_locked"

	// AutoUpgradeStatusDpkgInterrupted means dpkg was interrupted in the middle of a
	// transaction and needs "dpkg --configure -a" before APT accepts to install anything.
	AutoUpgradeStatusDpkgInterrupted = "dpkg_interrupted"

	// AutoUpgradeStatusRunNotFinished means an auto-upgrade run started long ago and
	// never finished.
	AutoUpgradeStatusRunNotFinished = "run_not_finished"

	// factAutoUpgradeEnabled is set by fastUpdateFacts on every system where an
	// auto-upgrade exists at all. Its presence is what gates FactAutoUpgradeStatus,
	// which spares us running the same systemctl call twice.
	factAutoUpgradeEnabled = "auto_upgrade_enabled"
)

const (
	// lockHeldMinDuration is how long one of the APT/dpkg locks must have been held
	// before we call it a problem. Holding it for a few minutes is perfectly normal
	// (apt-daily, unattended-upgrades); holding it for an hour is not.
	lockHeldMinDuration = time.Hour

	// autoUpgradeMinInterval is how long an answer is kept before the checks run again,
	// whatever the pace at which the facts themselves are refreshed.
	autoUpgradeMinInterval = time.Hour

	// autoUpgradeRunMaxDuration is how long after its start an auto-upgrade run is
	// taken for stuck. systemd doesn't time out Type=oneshot units by default, so a
	// glouton-auto-upgrade waiting on a dpkg prompt or a stalled mirror stays there
	// forever, while "apt-get update" plus one package install never takes that long.
	autoUpgradeRunMaxDuration = 2 * time.Hour

	// dpkgStatusSource is the pseudo-source apt-cache uses for the local dpkg database.
	// It's the only entry of a version table that isn't a repository.
	dpkgStatusSource = "/var/lib/dpkg/status"

	// dpkgJournalDir and dpkgStatusPath are relative to the host root.
	dpkgJournalDir = "var/lib/dpkg/updates"
	dpkgStatusPath = "var/lib/dpkg/status"

	procLocksPath     = "/proc/locks"
	procMountInfoPath = "/proc/self/mountinfo"
)

var errUnexpectedProcLocks = errors.New("unexpected /proc/locks format")

// gloutonPackageNames are the package names that may provide Glouton, current name first.
var gloutonPackageNames = []string{"glouton", "bleemeo-agent"} //nolint:gochecknoglobals

// packageManagerLockPaths are the lock files APT and dpkg take, relative to the host root
// and in the order APT takes them.
var packageManagerLockPaths = []string{ //nolint:gochecknoglobals
	"var/lib/dpkg/lock-frontend",
	"var/lib/dpkg/lock",
	"var/lib/apt/lists/lock",
	"var/cache/apt/archives/lock",
}

// shortLivedPackageProcesses are processes whose whole lifetime is one package operation.
// For them the process start time is a good approximation of when the lock was taken, so
// we don't have to wait to observe the lock ourselves for lockHeldMinDuration.
// Daemons that take the lock from time to time (packagekitd, aptd) are deliberately absent.
//
// These are matched against the process name, which comes from /proc/<pid>/comm and is
// capped by the kernel at 15 characters (TASK_COMM_LEN): an entry longer than that could
// never match, and goes to shortLivedPackageScripts instead.
var shortLivedPackageProcesses = map[string]bool{ //nolint:gochecknoglobals
	"apt":      true,
	"apt-get":  true,
	"aptitude": true,
	"dpkg":     true,
}

// shortLivedPackageScripts are the same, for the ones whose name doesn't survive the
// TASK_COMM_LEN cap. Verified on Ubuntu 24.04: /usr/bin/unattended-upgrade is a script
// with a "#!/usr/bin/python3" shebang, and the kernel names such a process after the
// script, truncated: "unattended-upgr". Only its command line carries the full name.
// It's worth the extra look: python-apt takes /var/lib/dpkg/lock-frontend in that very
// process, which makes it the lock holder we are the most likely to find on Ubuntu.
var shortLivedPackageScripts = map[string]bool{ //nolint:gochecknoglobals
	"unattended-upgrade":  true,
	"unattended-upgrades": true,
}

// dpkgBrokenStates are the package states an interrupted dpkg leaves behind. Installing
// anything, Glouton included, then fails until they're repaired. triggers-awaited and
// triggers-pending are deliberately absent: they're normal and APT resolves them itself.
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
	// isn't the host one and we have no apt-cache to run. An empty path is a container
	// whose host root isn't mounted, which is the same answer. Saying nothing beats
	// saying "ok".
	if f.options.HostRootPath != "/" {
		return ""
	}

	// Computing this forks apt-cache and reads the whole dpkg database, about half a
	// second. Some callers refresh the facts far more often than the fact is worth
	// recomputing - mqtt.sendConnectMessage does it on every connect, and the duplicate
	// check with no max age at all - so the answer is kept for a while.
	if !f.autoUpgradeStatusAt.IsZero() && time.Since(f.autoUpgradeStatusAt) < autoUpgradeMinInterval {
		return f.autoUpgradeStatusValue
	}

	checker := autoUpgradeChecker{
		Runner:          f.options.Runner,
		AutoUpgradeFile: f.options.AutoUpgradeFile,
		HostRootPath:    f.options.HostRootPath,
	}

	f.autoUpgradeStatusValue = checker.status(ctx)
	f.autoUpgradeStatusAt = time.Now()

	return f.autoUpgradeStatusValue
}

func (c *autoUpgradeChecker) status(ctx context.Context) string {
	// Only Debian-like systems are supported for now. dnf/yum would need their own
	// repository check; until then we prefer no fact over a wrong "ok".
	if _, err := c.Runner.LookPath("apt-cache", gloutonexec.Option{SkipInContainer: true}); err != nil {
		return ""
	}

	var (
		problems []string
		// failed records that one of the checks couldn't run. We then never answer
		// AutoUpgradeStatusOK: a check that didn't run tells nothing, and the repository
		// one is precisely the case this fact exists for.
		failed bool
	)

	repository, err := c.repositoryState(ctx)

	switch {
	case err != nil:
		logger.V(1).Printf("auto-upgrade: unable to check the Glouton repository: %v", err)

		failed = true
	case repository == repositoryUnknownPackage:
		// APT doesn't know Glouton at all: it was installed from a tarball or by the
		// installation script without the repository. There is no auto-upgrade to watch.
		return ""
	case repository == repositoryMissing:
		problems = append(problems, AutoUpgradeStatusRepositoryMissing)
	}

	// The remaining reasons are independent from one another: a stuck lock and a dpkg
	// left half-way are two different repairs, so we report all of them rather than
	// stopping at the first.
	record := func(problem string, found bool, err error) {
		switch {
		case err != nil:
			logger.V(1).Printf("auto-upgrade: unable to check %s: %v", problem, err)

			failed = true
		case found:
			problems = append(problems, problem)
		}
	}

	holder, err := c.lockHolder(ctx)
	record(AutoUpgradeStatusLocked, holderIsStuck(holder), err)

	// Only when nothing holds the lock: dpkg fills its journal and leaves packages
	// unpacked as it works, and tidies up only once the transaction ends. Measured on
	// Ubuntu 24.04, a plain "apt-get install" that succeeds shows up to 34 journal
	// entries and 6 half-configured packages while it runs, so looking mid-transaction
	// would report a healthy upgrade as an interrupted one - the Glouton auto-upgrade
	// included, since it runs apt-get itself. APT has the same rule: it checks the
	// journal before taking the lock, never while holding it.
	switch {
	case err != nil:
		// The lock is unknown, so the dpkg state can't be read either.
	case holder == nil:
		interrupted, dpkgErr := c.dpkgIsInterrupted()
		record(AutoUpgradeStatusDpkgInterrupted, interrupted, dpkgErr)
	default:
		// A package operation is running, so this check didn't run. Saying OK on its
		// behalf would hide a dpkg that really does need repairing.
		failed = true
	}

	stuck, err := c.runIsStuck()
	record(AutoUpgradeStatusRunNotFinished, stuck, err)

	if len(problems) == 0 {
		if failed {
			// Something we couldn't look at may well be broken. Reporting nothing beats
			// reporting a health the checks that did run can't vouch for.
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

	switch {
	case holder.stopped:
		// A stopped process won't release the lock on its own, so there is nothing to
		// wait for and lockHeldMinDuration doesn't apply. This is the state a
		// "kill -STOP <pid>" on apt leaves behind, our closest reproduction of the
		// stuck lock reported from the field.
		logger.V(1).Printf(
			"auto-upgrade: %s is locked by PID %d (%s), which is stopped",
			holder.path, holder.pid, holder.name,
		)
	case holder.heldFor >= lockHeldMinDuration:
		logger.V(1).Printf(
			"auto-upgrade: %s has been locked for at least %v by PID %d (%s)",
			holder.path, holder.heldFor.Truncate(time.Minute), holder.pid, holder.name,
		)
	default:
		return false
	}

	return true
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
// digits only. Verified on Ubuntu 24.04, an empty one counts just as much as a filled one,
// while a file with any other name is ignored. A missing journal directory means no
// journal, so no interrupted transaction: APT itself returns false when its opendir()
// fails, and we must not turn that into an error that would hold back the whole fact.
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

// dpkgHasBrokenPackage looks for packages an interrupted dpkg left half-way. APT reports
// those separately from the journal, as unmet dependencies to repair with --fix-broken,
// and they make "apt-get install glouton" fail just the same.
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
		if len(fields) == 3 && dpkgBrokenStates[fields[2]] {
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
		// Nothing to watch, which is not the same as a failure: an unset marker path
		// must not suppress the checks that did run.
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
		// An empty agent.auto_upgrade_file resolves to the state directory itself, see
		// config.Load(). Its mtime is days old, which would report a run that never
		// ended, for ever and with nothing the user could delete to clear it.
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

// lockHolder returns the process holding one of the APT/dpkg lock files, or nil when none
// of them is held.
//
// We never take the lock ourselves: we only stat() the lock files and read /proc/locks, so
// this check can't become the blocker it's looking for. It also matters that both work
// without root privilege, because Glouton runs as the "glouton" user while the lock files
// are 0640 root:root (/var/lib/dpkg is 0755, so stat() is enough).
func (c *autoUpgradeChecker) lockHolder(ctx context.Context) (*lockHolder, error) {
	content, err := os.ReadFile(procLocksPath)
	if err != nil {
		return nil, fmt.Errorf("unable to read %s: %w", procLocksPath, err)
	}

	locks := decodeProcLocks(content)

	var mounts []mountEntry

	if mountInfo, err := os.ReadFile(procMountInfoPath); err != nil {
		// Not fatal: statLockID still answers with the device stat(2) reports, which is
		// the right one on every filesystem but btrfs.
		logger.V(2).Printf("auto-upgrade: unable to read %s: %v", procMountInfoPath, err)
	} else {
		mounts = decodeMountInfo(mountInfo)
	}

	for _, relativePath := range packageManagerLockPaths {
		path := filepath.Join(c.HostRootPath, relativePath)

		id, err := lockFileID(path, mounts)
		if err != nil {
			continue // that lock file doesn't exist on this system
		}

		pid, ok := locks[id]
		if !ok || pid <= 0 {
			// pid is -1 for open file description locks, whose holder can't be named.
			// APT and dpkg use classic POSIX locks, so this isn't one of them.
			continue
		}

		holder := &lockHolder{path: path, pid: pid}
		describeHolder(ctx, holder)

		return holder, nil
	}

	return nil, nil //nolint:nilnil
}

// describeHolder fills in what we know about the process holding the lock: its name,
// whether it is stopped, and for how long it has been holding.
//
// /proc/locks says who holds a lock but not since when, and Glouton only looks when it
// refreshes its facts, hours apart. Seeing the same process hold the lock on two
// refreshes therefore proves nothing: a daemon that takes it for a second on each of
// them would be indistinguishable from one that never let go. So heldFor is only filled
// where it can be known, from the start time of a process whose whole life is one
// package operation; for anything else it stays zero and only a stopped holder counts.
//
// Everything we can't read is left at its zero value: an unreadable /proc entry costs us
// the checks below, never a wrong answer.
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

// lockFileID identifies a lock file the way /proc/locks does.
//
// The inode comes from stat(2), but the device can't: /proc/locks names a file by the
// device of its superblock, and btrfs gives every subvolume its own anonymous device, so
// stat(2) and /proc/locks disagree on every btrfs filesystem, the default subvolume
// included. Measured on Ubuntu 24.04 with a btrfs root, stat(2) answered 0:54 where
// /proc/locks said 0:53, which silently made every lock unmatchable. The mount table
// carries the superblock device, so it is the one to believe when the file is under a
// known mount.
func lockFileID(path string, mounts []mountEntry) (procLockID, error) {
	id, err := statLockID(path)
	if err != nil {
		return procLockID{}, err
	}

	if major, minor, ok := mountDeviceForPath(mounts, path); ok {
		id.major, id.minor = major, minor
	}

	return id, nil
}

// mountEntry is what one line of /proc/self/mountinfo tells us: where a filesystem is
// mounted, and the device the kernel names it by.
type mountEntry struct {
	point string
	major uint32
	minor uint32
}

// decodeMountInfo parses /proc/self/mountinfo.
//
// A line looks like:
//
//	36 35 98:0 /mnt1 /mnt2 rw,noatime master:1 - ext3 /dev/root rw
//
// that is: mount ID, parent ID, MAJOR:MINOR, root within the filesystem, mount point,
// then options. Unlike /proc/locks, MAJOR and MINOR are in decimal here. A mount point
// holding a space, a tab, a newline or a backslash has it octal-escaped.
func decodeMountInfo(content []byte) []mountEntry {
	var entries []mountEntry

	for line := range strings.SplitSeq(string(content), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}

		major, minor, ok := decodeDecimalDeviceID(fields[2])
		if !ok {
			continue
		}

		entries = append(entries, mountEntry{
			point: unescapeMountPoint(fields[4]),
			major: major,
			minor: minor,
		})
	}

	return entries
}

func decodeDecimalDeviceID(field string) (major uint32, minor uint32, ok bool) {
	rawMajor, rawMinor, found := strings.Cut(field, ":")
	if !found {
		return 0, 0, false
	}

	parsedMajor, err := strconv.ParseUint(rawMajor, 10, 32)
	if err != nil {
		return 0, 0, false
	}

	parsedMinor, err := strconv.ParseUint(rawMinor, 10, 32)
	if err != nil {
		return 0, 0, false
	}

	return uint32(parsedMajor), uint32(parsedMinor), true
}

// unescapeMountPoint undoes the octal escaping the kernel applies to the few characters
// that would otherwise split a mountinfo field.
func unescapeMountPoint(point string) string {
	if !strings.Contains(point, `\`) {
		return point
	}

	var out strings.Builder

	for i := 0; i < len(point); i++ {
		if point[i] == '\\' && i+3 < len(point) {
			if value, err := strconv.ParseUint(point[i+1:i+4], 8, 8); err == nil {
				out.WriteByte(byte(value))

				i += 3

				continue
			}
		}

		out.WriteByte(point[i])
	}

	return out.String()
}

// mountDeviceForPath returns the device of the filesystem path lives on.
func mountDeviceForPath(mounts []mountEntry, path string) (major uint32, minor uint32, ok bool) {
	best := -1

	for i, entry := range mounts {
		if !pathIsUnder(path, entry.point) {
			continue
		}

		// The longest mount point wins, and on equal length the last line does: that's
		// the filesystem mounted on top of the other.
		if best == -1 || len(entry.point) >= len(mounts[best].point) {
			best = i
		}
	}

	if best == -1 {
		return 0, 0, false
	}

	return mounts[best].major, mounts[best].minor, true
}

// pathIsUnder tells whether path is a mount point itself or lives under it.
func pathIsUnder(path string, mountPoint string) bool {
	if path == mountPoint {
		return true
	}

	if mountPoint == "/" {
		return strings.HasPrefix(path, "/")
	}

	return strings.HasPrefix(path, mountPoint+"/")
}

// procLockID identifies the file a lock is held on. /proc/locks doesn't name that file,
// it only gives the device and inode it lives on.
type procLockID struct {
	major uint32
	minor uint32
	inode uint64
}

// decodeProcLocks parses /proc/locks and returns the PID holding each locked file.
//
// A line looks like:
//
//	25: POSIX  ADVISORY  WRITE 7 00:47:1425030 0 EOF
//
// that is: index, type, mandatory/advisory, access, PID, MAJOR:MINOR:INODE, start, end,
// with MAJOR and MINOR in hexadecimal. Processes waiting for a lock are listed too, with
// an extra "->" after the index; only the holder interests us.
func decodeProcLocks(content []byte) map[procLockID]int {
	result := make(map[procLockID]int)

	for line := range strings.SplitSeq(string(content), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}

		// Drop the index, then the "->" of the processes blocked on the lock.
		fields = fields[1:]
		if fields[0] == "->" {
			continue
		}

		if len(fields) < 5 || fields[0] != "POSIX" {
			continue
		}

		pid, err := strconv.Atoi(fields[3])
		if err != nil {
			continue
		}

		id, err := decodeProcLocksFileID(fields[4])
		if err != nil {
			continue
		}

		// The first holder wins: a file can't have two conflicting POSIX locks, and a
		// later line for the same file would be a waiter.
		if _, ok := result[id]; !ok {
			result[id] = pid
		}
	}

	return result
}

func decodeProcLocksFileID(field string) (procLockID, error) {
	parts := strings.Split(field, ":")
	if len(parts) != 3 {
		return procLockID{}, fmt.Errorf("%w: %q isn't a MAJOR:MINOR:INODE triplet", errUnexpectedProcLocks, field)
	}

	major, err := strconv.ParseUint(parts[0], 16, 32)
	if err != nil {
		return procLockID{}, fmt.Errorf("%w: %q: %w", errUnexpectedProcLocks, field, err)
	}

	minor, err := strconv.ParseUint(parts[1], 16, 32)
	if err != nil {
		return procLockID{}, fmt.Errorf("%w: %q: %w", errUnexpectedProcLocks, field, err)
	}

	inode, err := strconv.ParseUint(parts[2], 10, 64)
	if err != nil {
		return procLockID{}, fmt.Errorf("%w: %q: %w", errUnexpectedProcLocks, field, err)
	}

	return procLockID{major: uint32(major), minor: uint32(minor), inode: inode}, nil
}
